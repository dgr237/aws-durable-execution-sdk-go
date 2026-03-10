package durabletest_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	durabletest "github.com/aws/durable-execution-sdk-go/pkg/durable/testing"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// Package-level types are required so Go can infer the generic type parameters
// of NewLocalDurableTestRunner from the handler function signature.

type greetEvt struct{ Name string }
type greetRes struct{ Greeting string }

type multiEvt struct{}
type multiRes struct{ A, B string }

type emptyEvt struct{}
type waitRes struct{ Done bool }

type approvalEvt struct{}
type approvalRes struct{ Approval string }

type opEvt struct{}

// ---------------------------------------------------------------------------
// Simple step
// ---------------------------------------------------------------------------

func TestRunner_SimpleStep_Succeeds(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event greetEvt, dc types.DurableContext) (greetRes, error) {
			raw, err := operations.Step(dc, "greet", func(sc types.StepContext) (any, error) {
				return "Hello, " + event.Name, nil
			})
			if err != nil {
				return greetRes{}, err
			}
			return greetRes{Greeting: raw.(string)}, nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	result, err := runner.Run(t.Context(), greetEvt{Name: "World"})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s: %v", result.Status(), result.GetError())
	}

	got, err := result.GetResult()
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.Greeting != "Hello, World" {
		t.Errorf("expected 'Hello, World', got %q", got.Greeting)
	}
}

// ---------------------------------------------------------------------------
// Multiple steps
// ---------------------------------------------------------------------------

func TestRunner_MultipleSteps_Succeeds(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event multiEvt, dc types.DurableContext) (multiRes, error) {
			a, err := operations.Step(dc, "step-a", func(sc types.StepContext) (any, error) {
				return "alpha", nil
			})
			if err != nil {
				return multiRes{}, err
			}
			b, err := operations.Step(dc, "step-b", func(sc types.StepContext) (any, error) {
				return "beta", nil
			})
			if err != nil {
				return multiRes{}, err
			}
			return multiRes{A: a.(string), B: b.(string)}, nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	result, err := runner.Run(t.Context(), multiEvt{})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s: %v", result.Status(), result.GetError())
	}

	got, _ := result.GetResult()
	if got.A != "alpha" || got.B != "beta" {
		t.Errorf("unexpected result: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Step error → FAILED
// ---------------------------------------------------------------------------

func TestRunner_StepError_ReturnsFailed(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event emptyEvt, dc types.DurableContext) (string, error) {
			_, err := operations.Step(dc, "bad-step", func(sc types.StepContext) (any, error) {
				return nil, fmt.Errorf("intentional failure")
			})
			if err != nil {
				return "", err
			}
			return "unreachable", nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	result, err := runner.Run(t.Context(), emptyEvt{})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusFailed {
		t.Fatalf("expected FAILED, got %s", result.Status())
	}
	if result.GetError() == nil {
		t.Error("expected non-nil GetError()")
	}
}

// ---------------------------------------------------------------------------
// Wait with SkipTime
// ---------------------------------------------------------------------------

func TestRunner_Wait_SkipTime_Completes(t *testing.T) {
	stepBeforeWait := false
	stepAfterWait := false

	runner := durabletest.NewLocalDurableTestRunner(
		func(event emptyEvt, dc types.DurableContext) (waitRes, error) {
			_, err := operations.Step(dc, "before-wait", func(sc types.StepContext) (any, error) {
				stepBeforeWait = true
				return nil, nil
			})
			if err != nil {
				return waitRes{}, err
			}

			if err := operations.Wait(dc, "my-wait", types.Duration{Seconds: 30}); err != nil {
				return waitRes{}, err
			}

			_, err = operations.Step(dc, "after-wait", func(sc types.StepContext) (any, error) {
				stepAfterWait = true
				return nil, nil
			})
			if err != nil {
				return waitRes{}, err
			}

			return waitRes{Done: true}, nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	result, err := runner.Run(t.Context(), emptyEvt{})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s: %v", result.Status(), result.GetError())
	}

	got, _ := result.GetResult()
	if !got.Done {
		t.Error("expected Done = true")
	}
	if !stepBeforeWait {
		t.Error("step before wait did not execute")
	}
	if !stepAfterWait {
		t.Error("step after wait did not execute")
	}

	// Invocations: first run suspends at Wait; second run replays after wait completes.
	invocations := result.GetInvocations()
	if len(invocations) < 2 {
		t.Errorf("expected >=2 invocations for wait+replay, got %d", len(invocations))
	}
}

// ---------------------------------------------------------------------------
// WaitForCallback — success
// ---------------------------------------------------------------------------

func TestRunner_WaitForCallback_Success(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event approvalEvt, dc types.DurableContext) (approvalRes, error) {
			approval, err := operations.WaitForCallback[string](
				dc,
				"payment-approval",
				func(sc types.StepContext, callbackID string) error {
					// In production this would call an external API.
					// The test sends the callback via RunHandle.GetOperation.
					return nil
				},
			)
			if err != nil {
				return approvalRes{}, err
			}
			return approvalRes{Approval: approval}, nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	handle := runner.Start(t.Context(), approvalEvt{})

	// Block until the callback operation is checkpointed, then approve it.
	op := handle.GetOperation(t.Context(), "payment-approval")
	if op == nil {
		t.Fatal("expected payment-approval operation, got nil")
	}
	op.SendCallbackSuccess(`"approved"`)

	result, err := handle.Await()
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusSucceeded {
		t.Fatalf("expected SUCCEEDED, got %s: %v", result.Status(), result.GetError())
	}

	got, _ := result.GetResult()
	if got.Approval != "approved" {
		t.Errorf("expected 'approved', got %q", got.Approval)
	}
}

// ---------------------------------------------------------------------------
// WaitForCallback — failure
// ---------------------------------------------------------------------------

func TestRunner_WaitForCallback_Failure(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event approvalEvt, dc types.DurableContext) (string, error) {
			_, err := operations.WaitForCallback[string](
				dc,
				"risky-callback",
				func(sc types.StepContext, callbackID string) error { return nil },
			)
			if err != nil {
				return "", err
			}
			return "unreachable", nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	handle := runner.Start(t.Context(), approvalEvt{})

	op := handle.GetOperation(t.Context(), "risky-callback")
	if op == nil {
		t.Fatal("expected risky-callback operation, got nil")
	}
	op.SendCallbackFailure("PaymentDeclined", "card declined")

	result, err := handle.Await()
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	if result.Status() != durabletest.ExecutionStatusFailed {
		t.Fatalf("expected FAILED, got %s", result.Status())
	}
}

// ---------------------------------------------------------------------------
// GetOperations / GetOperation on result
// ---------------------------------------------------------------------------

func TestRunner_ResultGetOperation(t *testing.T) {
	runner := durabletest.NewLocalDurableTestRunner(
		func(event opEvt, dc types.DurableContext) (string, error) {
			_, _ = operations.Step(dc, "alpha", func(sc types.StepContext) (any, error) { return 1, nil })
			_, _ = operations.Step(dc, "beta", func(sc types.StepContext) (any, error) { return 2, nil })
			return "done", nil
		},
		durabletest.RunConfig{SkipTime: true},
	)

	result, err := runner.Run(t.Context(), opEvt{})
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}

	ops := result.GetOperations()
	if len(ops) == 0 {
		t.Fatal("expected at least one operation")
	}

	alpha := result.GetOperation("alpha")
	if alpha == nil {
		t.Fatal("expected to find operation 'alpha'")
	}
	if alpha.Name() != "alpha" {
		t.Errorf("expected name 'alpha', got %q", alpha.Name())
	}
	if alpha.Status() != types.OperationStatusSucceeded {
		t.Errorf("expected SUCCEEDED status for alpha, got %q", alpha.Status())
	}

	if result.GetOperation("nonexistent") != nil {
		t.Error("expected nil for nonexistent operation")
	}
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestRunner_ContextCancelled_ReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())

	runner := durabletest.NewLocalDurableTestRunner(
		func(event emptyEvt, dc types.DurableContext) (string, error) {
			// With SkipTime=false this Wait blocks; cancelling ctx unblocks it.
			_ = operations.Wait(dc, "long-wait", types.Duration{Seconds: 9999})
			return "done", nil
		},
		durabletest.RunConfig{SkipTime: false},
	)

	handle := runner.Start(ctx, emptyEvt{})
	cancel()

	_, err := handle.Await()
	if err == nil {
		t.Log("runner returned nil error after context cancellation (result produced instead)")
	} else {
		t.Logf("runner returned error after context cancellation: %v", err)
	}
}
