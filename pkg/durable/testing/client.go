package durabletest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

type callbackResult struct {
	value *string
	err   *types.ErrorObject
}

// testClient is an in-process implementation of checkpoint.Client for testing.
// It accumulates operation state across re-invocations, simulating the real
// checkpoint backend.
type testClient struct {
	mu  sync.Mutex
	arn string

	// All stored operations, keyed by hashed ID, in insertion order.
	opsMap   map[string]*types.Operation
	opsOrder []string // hashed IDs in insertion order

	// Flattened updates from the current invocation; reset after each flush.
	invocationUpdates []types.OperationUpdate

	// Per-invocation token.
	token string

	// Pending callbacks: hashedOpID → channel awaited by the orchestrator.
	callbackChs map[string]chan callbackResult
}

func newTestClient(arn string) *testClient {
	return &testClient{
		arn:         arn,
		token:       "tok-0",
		opsMap:      make(map[string]*types.Operation),
		callbackChs: make(map[string]chan callbackResult),
	}
}

func (c *testClient) Checkpoint(_ context.Context, req types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range req.Operations {
		u := req.Operations[i]
		c.applyUpdate(&u)
		c.invocationUpdates = append(c.invocationUpdates, u)
	}
	c.token = fmt.Sprintf("tok-%d", len(c.opsOrder))
	tok := c.token
	return &types.CheckpointDurableExecutionResponse{NextCheckpointToken: &tok}, nil
}

func (c *testClient) GetExecutionState(_ context.Context, _ types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error) {
	// No pagination in tests — all state is passed via InitialExecutionState.
	return &types.GetDurableExecutionStateResponse{}, nil
}

// applyUpdate merges a single OperationUpdate into the stored operation map.
// Must be called with c.mu held.
// NOTE: The checkpoint Manager pre-hashes all IDs before calling client.Checkpoint,
// so u.Id and u.ParentId are already in hashed (MD5 16-char) form here.
func (c *testClient) applyUpdate(u *types.OperationUpdate) {
	id := u.Id // already hashed by the checkpoint Manager

	existing, ok := c.opsMap[id]
	if !ok {
		op := &types.Operation{
			Id:      id,
			Type:    u.Type,
			SubType: u.SubType,
			Name:    u.Name,
		}
		if u.ParentId != nil {
			pid := *u.ParentId // also already hashed
			op.ParentId = &pid
		}
		c.opsMap[id] = op
		c.opsOrder = append(c.opsOrder, id)
		existing = op
	}

	// Status from action.
	switch u.Action {
	case types.OperationActionStart:
		existing.Status = types.OperationStatusStarted
	case types.OperationActionSucceed:
		existing.Status = types.OperationStatusSucceeded
	case types.OperationActionFail:
		existing.Status = types.OperationStatusFailed
		existing.Error = u.Error
	case types.OperationActionRetry:
		existing.Status = types.OperationStatusInProgress
	case types.OperationActionCancel:
		existing.Status = types.OperationStatusCancelled
	}

	// Payload → type-specific details.
	if u.Payload != nil {
		switch u.Type {
		case types.OperationTypeStep:
			if existing.StepDetails == nil {
				existing.StepDetails = &types.StepDetails{}
			}
			existing.StepDetails.Result = u.Payload
		case types.OperationTypeContext:
			if existing.ContextDetails == nil {
				existing.ContextDetails = &types.ContextDetails{}
			}
			existing.ContextDetails.Result = u.Payload
		case types.OperationTypeCallback:
			if existing.CallbackDetails == nil {
				existing.CallbackDetails = &types.CallbackDetails{}
			}
			existing.CallbackDetails.Result = u.Payload
		case types.OperationTypeChainedInvoke:
			if existing.ChainedInvokeDetails == nil {
				existing.ChainedInvokeDetails = &types.ChainedInvokeDetails{}
			}
			existing.ChainedInvokeDetails.Result = u.Payload
		}
	}

	// Error → type-specific details.
	if u.Error != nil {
		switch u.Type {
		case types.OperationTypeCallback:
			if existing.CallbackDetails == nil {
				existing.CallbackDetails = &types.CallbackDetails{}
			}
			existing.CallbackDetails.Error = u.Error
		case types.OperationTypeContext:
			if existing.ContextDetails == nil {
				existing.ContextDetails = &types.ContextDetails{}
			}
			existing.ContextDetails.Error = u.Error
		case types.OperationTypeChainedInvoke:
			if existing.ChainedInvokeDetails == nil {
				existing.ChainedInvokeDetails = &types.ChainedInvokeDetails{}
			}
			existing.ChainedInvokeDetails.Error = u.Error
		}
	}

	// Wait details — compute scheduled end time.
	if u.WaitOptions != nil && u.WaitOptions.WaitSeconds != nil {
		end := types.NewFlexibleTime(time.Now().Add(time.Duration(*u.WaitOptions.WaitSeconds) * time.Second))
		existing.WaitDetails = &types.WaitDetails{ScheduledEndTimestamp: &end}
	}
}

func (c *testClient) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// allOperations returns a snapshot of all stored operations in insertion order.
func (c *testClient) allOperations() []types.Operation {
	c.mu.Lock()
	defer c.mu.Unlock()
	ops := make([]types.Operation, 0, len(c.opsOrder))
	for _, id := range c.opsOrder {
		ops = append(ops, *c.opsMap[id])
	}
	return ops
}

// flushInvocationUpdates returns and resets the updates from the current invocation.
func (c *testClient) flushInvocationUpdates() []types.OperationUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	updates := c.invocationUpdates
	c.invocationUpdates = nil
	return updates
}

// registerCallbackCh creates and stores a result channel for the given operation ID.
// The ID is already in hashed form (pre-hashed by the checkpoint Manager).
// The orchestrator reads from this channel when waiting for callback completion.
func (c *testClient) registerCallbackCh(id string) chan callbackResult {
	ch := make(chan callbackResult, 1)
	c.mu.Lock()
	c.callbackChs[id] = ch
	c.mu.Unlock()
	return ch
}

// completeCallback signals callback success or failure for the given hashed operation ID.
// It updates the stored operation state and unblocks the orchestrator.
func (c *testClient) completeCallback(hashedID string, value *string, errObj *types.ErrorObject) {
	c.mu.Lock()
	if op, ok := c.opsMap[hashedID]; ok {
		if errObj != nil {
			op.Status = types.OperationStatusFailed
			if op.CallbackDetails == nil {
				op.CallbackDetails = &types.CallbackDetails{}
			}
			op.CallbackDetails.Error = errObj
		} else {
			op.Status = types.OperationStatusSucceeded
			if op.CallbackDetails == nil {
				op.CallbackDetails = &types.CallbackDetails{}
			}
			op.CallbackDetails.Result = value
		}
	}
	ch := c.callbackChs[hashedID]
	delete(c.callbackChs, hashedID)
	c.mu.Unlock()

	if ch != nil {
		ch <- callbackResult{value: value, err: errObj}
	}
}

// updateOperationStatus directly sets the status for a hashed operation ID.
// Used by the orchestrator to mark Wait operations as completed.
func (c *testClient) updateOperationStatus(hashedID string, status types.OperationStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if op, ok := c.opsMap[hashedID]; ok {
		op.Status = status
	}
}
