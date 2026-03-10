// Package durabletest provides an in-process test runner for durable execution handlers.
//
// It simulates the AWS Lambda Durable Execution checkpoint backend locally, enabling
// fast, deterministic tests without deploying to AWS. Handlers are re-invoked
// automatically when Wait or WaitForCallback operations are encountered, and
// Wait durations can be skipped instantly via RunConfig.SkipTime.
//
// # Basic usage (no callbacks)
//
//	runner := durabletest.NewLocalDurableTestRunner(myHandler, durabletest.RunConfig{SkipTime: true})
//	result, err := runner.Run(t.Context(), myEvent)
//	if err != nil { t.Fatal(err) }
//	if result.Status() != durabletest.ExecutionStatusSucceeded { t.Fatalf("want SUCCEEDED, got %s: %v", result.Status(), result.GetError()) }
//	got, _ := result.GetResult()
//	// assert on got…
//
// # Usage with WaitForCallback
//
//	handle := runner.Start(t.Context(), myEvent)
//
//	// In a separate goroutine the orchestrator will block until SendCallbackSuccess is called.
//	callbackOp := handle.GetOperation(t.Context(), "payment-approval")
//	callbackOp.SendCallbackSuccess(`"approved"`)
//
//	result, err := handle.Await()
package durabletest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

const testARN = "arn:aws:lambda:us-east-1:123456789012:function:durable-test:1"

// RunConfig configures the behaviour of the test runner.
type RunConfig struct {
	// SkipTime causes Wait durations and step retry delays to complete immediately
	// without blocking. Defaults to true when using NewLocalDurableTestRunner.
	SkipTime bool
}

// runnerState is shared between the orchestrator goroutine and the test goroutine.
// Operations are added as they are checkpointed; GetOperation blocks until
// the requested operation appears or execution ends.
type runnerState struct {
	mu   sync.Mutex
	cond *sync.Cond

	ops       []*DurableOperation            // all ops in checkpoint order
	opsByName map[string][]*DurableOperation // name → ordered list

	invocations []Invocation
	done        bool
}

func newRunnerState() *runnerState {
	s := &runnerState{opsByName: make(map[string][]*DurableOperation)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *runnerState) addOp(op *DurableOperation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, op)
	if name := op.Name(); name != "" {
		s.opsByName[name] = append(s.opsByName[name], op)
	}
	s.cond.Broadcast()
}

func (s *runnerState) addInvocation(inv Invocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invocations = append(s.invocations, inv)
}

func (s *runnerState) markDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.cond.Broadcast()
}

// waitForOp blocks until an operation with the given name is registered, or
// execution ends / ctx is cancelled.
func (s *runnerState) waitForOp(ctx context.Context, name string) *DurableOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if ops := s.opsByName[name]; len(ops) > 0 {
			return ops[0]
		}
		if s.done || ctx.Err() != nil {
			return nil
		}
		// Wake on context cancellation without holding the lock.
		wake := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				s.cond.Broadcast()
			case <-wake:
			}
		}()
		s.cond.Wait()
		close(wake)
	}
}

func (s *runnerState) snapshot() (ops []*DurableOperation, invocations []Invocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ops = append([]*DurableOperation(nil), s.ops...)
	invocations = append([]Invocation(nil), s.invocations...)
	return
}

// RunHandle is returned by Start and provides concurrent access to operations
// and the final result.
type RunHandle[TResult any] struct {
	state    *runnerState
	resultCh <-chan runOutcome[TResult]
}

type runOutcome[TResult any] struct {
	result *TestResult[TResult]
	err    error
}

// GetOperation blocks until an operation with the given name is checkpointed,
// then returns it. Returns nil if execution ends without registering the operation
// or if ctx is cancelled.
// Intended for callback interaction: call this from the test goroutine while the
// orchestrator goroutine is running.
func (h *RunHandle[TResult]) GetOperation(ctx context.Context, name string) *DurableOperation {
	return h.state.waitForOp(ctx, name)
}

// Await blocks until execution completes and returns the TestResult.
func (h *RunHandle[TResult]) Await() (*TestResult[TResult], error) {
	o := <-h.resultCh
	return o.result, o.err
}

// LocalDurableTestRunner runs a durable handler in-process with a simulated
// checkpoint backend. Create one per test; it is not safe for concurrent use
// across multiple Run/Start calls.
type LocalDurableTestRunner[TEvent, TResult any] struct {
	fn  durable.HandlerFunc[TEvent, TResult]
	cfg RunConfig
}

// NewLocalDurableTestRunner creates a test runner for the given handler function.
// Pass the raw HandlerFunc — do not wrap it with WithDurableExecution first.
// SkipTime defaults to true.
func NewLocalDurableTestRunner[TEvent, TResult any](
	fn durable.HandlerFunc[TEvent, TResult],
	cfg RunConfig,
) *LocalDurableTestRunner[TEvent, TResult] {
	return &LocalDurableTestRunner[TEvent, TResult]{fn: fn, cfg: cfg}
}

// Run executes the handler to completion and returns the TestResult.
// Equivalent to Start followed immediately by Await.
// Use Start for handlers that require WaitForCallback interaction during the test.
func (r *LocalDurableTestRunner[TEvent, TResult]) Run(ctx context.Context, event TEvent) (*TestResult[TResult], error) {
	return r.Start(ctx, event).Await()
}

// Start launches the handler execution in a background goroutine and returns a
// RunHandle immediately. Use GetOperation on the handle to interact with
// WaitForCallback operations, then call Await to get the final result.
func (r *LocalDurableTestRunner[TEvent, TResult]) Start(ctx context.Context, event TEvent) *RunHandle[TResult] {
	state := newRunnerState()
	ch := make(chan runOutcome[TResult], 1)
	go func() {
		result, err := r.orchestrate(ctx, event, state)
		state.markDone()
		ch <- runOutcome[TResult]{result: result, err: err}
	}()
	return &RunHandle[TResult]{state: state, resultCh: ch}
}

// orchestrate is the core re-invocation loop. It runs the handler repeatedly
// until it returns SUCCEEDED or FAILED, handling Wait, WaitForCallback, and
// step retry between invocations.
func (r *LocalDurableTestRunner[TEvent, TResult]) orchestrate(
	ctx context.Context,
	event TEvent,
	state *runnerState,
) (*TestResult[TResult], error) {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("durabletest: marshal event: %w", err)
	}
	eventStr := string(eventJSON)

	client := newTestClient(testARN)
	lambdaHandler := durable.WithDurableExecution(r.fn, &durable.Config{Client: client})

	// The EXECUTION operation carries the user's event payload and must be first.
	execOp := types.Operation{
		Id:               "0",
		Type:             types.OperationTypeExecution,
		ExecutionDetails: &types.ExecutionDetails{InputPayload: &eventStr},
	}

	seenHashedIDs := make(map[string]bool)

	for invocationIdx := 0; ; invocationIdx++ {
		// Build the invocation input from all accumulated operations.
		storedOps := client.allOperations()
		inputOps := make([]types.Operation, 0, 1+len(storedOps))
		inputOps = append(inputOps, execOp)
		inputOps = append(inputOps, storedOps...)

		input := types.DurableExecutionInvocationInput{
			DurableExecutionArn: testARN,
			CheckpointToken:     client.currentToken(),
			InitialExecutionState: types.InitialExecutionState{
				Operations: inputOps,
			},
		}

		out, lambdaErr := lambdaHandler(ctx, input)
		state.addInvocation(Invocation{Index: invocationIdx, Error: lambdaErr})

		// Register newly checkpointed operations for test access.
		// IDs are already in hashed form (pre-hashed by the checkpoint Manager).
		updates := client.flushInvocationUpdates()
		for i := range updates {
			id := updates[i].Id
			if !seenHashedIDs[id] {
				seenHashedIDs[id] = true
				state.addOp(newDurableOperation(id, client))
			}
		}

		ops, invocations := state.snapshot()

		if lambdaErr != nil {
			return buildFailedResult[TResult](ops, invocations, nil, lambdaErr.Error()), nil
		}

		switch out.Status {
		case types.InvocationStatusSucceeded:
			return buildSucceededResult[TResult](ops, invocations, out.Result), nil
		case types.InvocationStatusFailed:
			return buildFailedResult[TResult](ops, invocations, out.Error, ""), nil
		case types.InvocationStatusPending:
			if err := r.processPending(ctx, client, updates); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("durabletest: unexpected invocation status: %q", out.Status)
		}
	}
}

// processPending inspects the updates from the last invocation and handles any
// pending operations (Wait, WaitForCallback, step retry) before the next invocation.
func (r *LocalDurableTestRunner[TEvent, TResult]) processPending(
	ctx context.Context,
	client *testClient,
	updates []types.OperationUpdate,
) error {
	for i := range updates {
		u := &updates[i]
		switch {

		case u.Type == types.OperationTypeWait && u.Action == types.OperationActionStart:
			if r.cfg.SkipTime {
				client.updateOperationStatus(u.Id, types.OperationStatusSucceeded)
			} else {
				var waitSecs int32
				if u.WaitOptions != nil && u.WaitOptions.WaitSeconds != nil {
					waitSecs = *u.WaitOptions.WaitSeconds
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(waitSecs) * time.Second):
				}
				client.updateOperationStatus(u.Id, types.OperationStatusSucceeded)
			}

		case u.Type == types.OperationTypeCallback && u.Action == types.OperationActionStart:
			// Block until the test calls SendCallbackSuccess/Failure on the operation.
			ch := client.registerCallbackCh(u.Id)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ch:
				// client.completeCallback already updated the stored operation.
			}

		case u.Type == types.OperationTypeStep && u.Action == types.OperationActionRetry:
			// Honour the retry delay unless SkipTime is set.
			if !r.cfg.SkipTime {
				var delaySecs int32
				if u.StepOptions != nil && u.StepOptions.NextAttemptDelaySeconds != nil {
					delaySecs = *u.StepOptions.NextAttemptDelaySeconds
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Duration(delaySecs) * time.Second):
				}
			}
			// Leave status as IN_PROGRESS — the step will re-execute on the next invocation.
		}
	}
	return nil
}
