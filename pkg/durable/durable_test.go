package durable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/aws/durable-execution-sdk-go/pkg/durable"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ---------------------------------------------------------------------------
// Mock client
// ---------------------------------------------------------------------------

type mockClient struct {
	mu           sync.Mutex
	checkpoints  []types.CheckpointDurableExecutionRequest
	statePages   [][]types.Operation
	statePageIdx int
	tokenCounter int
}

func (m *mockClient) Checkpoint(ctx context.Context, params types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = append(m.checkpoints, params)
	m.tokenCounter++
	token := fmt.Sprintf("token-%d", m.tokenCounter)
	return &types.CheckpointDurableExecutionResponse{NextCheckpointToken: &token}, nil
}

func (m *mockClient) GetExecutionState(r types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.statePageIdx >= len(m.statePages) {
		return &types.GetDurableExecutionStateResponse{}, nil
	}
	ops := m.statePages[m.statePageIdx]
	m.statePageIdx++
	return &types.GetDurableExecutionStateResponse{Operations: ops}, nil
}

func newMockClient() *mockClient { return &mockClient{} }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeInput(arn, token string, ops []types.Operation, inputPayload string) types.DurableExecutionInvocationInput {
	payload := inputPayload
	return types.DurableExecutionInvocationInput{
		DurableExecutionArn: arn,
		CheckpointToken:     token,
		InitialExecutionState: types.InitialExecutionState{
			Operations: append([]types.Operation{
				{
					Id:   "0",
					Type: types.OperationTypeExecution,
					ExecutionDetails: &types.ExecutionDetails{
						InputPayload: &payload,
					},
				},
			}, ops...),
		},
	}
}

func invokeHandler(t *testing.T, h durable.LambdaHandler, event types.DurableExecutionInvocationInput) types.DurableExecutionInvocationOutput {
	t.Helper()
	out, err := h(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected Lambda error: %v", err)
	}
	return out
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestWithDurableExecution_SimpleStep_FirstInvocation(t *testing.T) {
	mock := newMockClient()

	type Evt struct{ Name string }
	type Res struct{ Greeting string }

	handler := durable.WithDurableExecution(func(event Evt, ctx types.DurableContext) (Res, error) {
		raw, err := operations.Step(ctx, "greet", func(sc types.StepContext) (any, error) {
			return "Hello, " + event.Name, nil
		})
		if err != nil {
			return Res{}, err
		}
		return Res{Greeting: raw.(string)}, nil
	}, &durable.Config{Client: mock})

	eventJSON, _ := json.Marshal(Evt{Name: "World"})
	out := invokeHandler(t, handler, makeInput(
		"arn:aws:lambda:::exec-1", "tok-1", nil, string(eventJSON),
	))

	if out.Status != types.InvocationStatusSucceeded {
		t.Errorf("expected SUCCEEDED, got %s — error: %+v", out.Status, out.Error)
	}
	if out.Result == nil {
		t.Fatal("expected non-nil Result")
	}

	var res Res
	if err := json.Unmarshal([]byte(*out.Result), &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.Greeting != "Hello, World" {
		t.Errorf("unexpected greeting: %q", res.Greeting)
	}

	// Verify a checkpoint was made
	if len(mock.checkpoints) == 0 {
		t.Error("expected at least one checkpoint")
	}
}

func TestWithDurableExecution_StepReplay(t *testing.T) {
	// Simulate a second invocation where step "1" already succeeded
	storedResult := `"replayed-value"`
	ops := []types.Operation{
		{
			Id:     "c4ca4238a0b92382",
			Type:   types.OperationTypeStep,
			Status: types.OperationStatusSucceeded,
			StepDetails: &types.StepDetails{
				Result: &storedResult,
			},
		},
	}

	mock := newMockClient()
	type Evt struct{}
	type Res struct{ Value string }

	stepExecuted := false
	handler := durable.WithDurableExecution(func(event Evt, ctx types.DurableContext) (Res, error) {
		raw, err := operations.Step(ctx, "", func(sc types.StepContext) (any, error) {
			stepExecuted = true
			return "fresh-value", nil
		})
		if err != nil {
			return Res{}, err
		}
		return Res{Value: raw.(string)}, nil
	}, &durable.Config{Client: mock})

	out := invokeHandler(t, handler, makeInput("arn:aws:lambda:::exec-2", "tok-2", ops, `{}`))

	if out.Status != types.InvocationStatusSucceeded {
		t.Errorf("expected SUCCEEDED, got %s — %+v", out.Status, out.Error)
	}
	if stepExecuted {
		t.Error("step function should NOT have executed during replay")
	}

	var res Res
	_ = json.Unmarshal([]byte(*out.Result), &res)
	if res.Value != "replayed-value" {
		t.Errorf("expected replayed-value, got %q", res.Value)
	}
}

func TestWithDurableExecution_InvalidEvent(t *testing.T) {
	handler := durable.WithDurableExecution(func(event any, ctx types.DurableContext) (any, error) {
		return nil, nil
	}, &durable.Config{Client: newMockClient()})

	// Empty ARN should produce FAILED
	out, err := handler(context.Background(), types.DurableExecutionInvocationInput{
		CheckpointToken: "tok",
	})
	if err != nil {
		t.Fatalf("unexpected Lambda error: %v", err)
	}
	if out.Status != types.InvocationStatusFailed {
		t.Errorf("expected FAILED for invalid event, got %s", out.Status)
	}
}

func TestWithDurableExecution_MultipleSteps(t *testing.T) {
	mock := newMockClient()
	type Evt struct{}
	type Res struct {
		Step1 string
		Step2 string
	}

	handler := durable.WithDurableExecution(func(event Evt, ctx types.DurableContext) (Res, error) {
		s1, err := operations.Step(ctx, "step-1", func(sc types.StepContext) (any, error) {
			return "result-1", nil
		})
		if err != nil {
			return Res{}, err
		}

		s2, err := operations.Step(ctx, "step-2", func(sc types.StepContext) (any, error) {
			return "result-2", nil
		})
		if err != nil {
			return Res{}, err
		}

		return Res{Step1: s1.(string), Step2: s2.(string)}, nil
	}, &durable.Config{Client: mock})

	out := invokeHandler(t, handler, makeInput("arn:exec-3", "tok-3", nil, `{}`))
	if out.Status != types.InvocationStatusSucceeded {
		t.Errorf("expected SUCCEEDED, got %s — %+v", out.Status, out.Error)
	}

	var res Res
	_ = json.Unmarshal([]byte(*out.Result), &res)
	if res.Step1 != "result-1" || res.Step2 != "result-2" {
		t.Errorf("unexpected result: %+v", res)
	}

	// Two steps = at least 2 checkpoints
	if len(mock.checkpoints) < 2 {
		t.Errorf("expected >=2 checkpoints, got %d", len(mock.checkpoints))
	}
}

func TestWithDurableExecution_StepError(t *testing.T) {
	mock := newMockClient()
	type Evt struct{}

	handler := durable.WithDurableExecution(func(event Evt, ctx types.DurableContext) (string, error) {
		_, err := operations.Step(ctx, "failing-step", func(sc types.StepContext) (any, error) {
			return nil, fmt.Errorf("intentional failure")
		}) // no retry
		if err != nil {
			return "", err
		}
		return "should not reach", nil
	}, &durable.Config{Client: mock})

	out := invokeHandler(t, handler, makeInput("arn:exec-4", "tok-4", nil, `{}`))
	if out.Status != types.InvocationStatusFailed {
		t.Errorf("expected FAILED, got %s", out.Status)
	}
	if out.Error == nil {
		t.Error("expected non-nil Error field")
	}
}

func TestWithDurableExecution_ChildContext(t *testing.T) {
	mock := newMockClient()
	type Evt struct{}
	type Res struct{ Combined string }

	handler := durable.WithDurableExecution(func(event Evt, ctx types.DurableContext) (Res, error) {
		raw, err := operations.RunInChildContext(ctx, "group", func(child types.DurableContext) (any, error) {
			s1, _ := operations.Step(child, "a", func(sc types.StepContext) (any, error) { return "A", nil })
			s2, _ := operations.Step(child, "b", func(sc types.StepContext) (any, error) { return "B", nil })
			return s1.(string) + s2.(string), nil
		})
		if err != nil {
			return Res{}, err
		}
		return Res{Combined: raw.(string)}, nil
	}, &durable.Config{Client: mock})

	out := invokeHandler(t, handler, makeInput("arn:exec-5", "tok-5", nil, `{}`))
	if out.Status != types.InvocationStatusSucceeded {
		t.Errorf("expected SUCCEEDED, got %s — %+v", out.Status, out.Error)
	}

	var res Res
	_ = json.Unmarshal([]byte(*out.Result), &res)
	if res.Combined != "AB" {
		t.Errorf("expected 'AB', got %q", res.Combined)
	}
}
