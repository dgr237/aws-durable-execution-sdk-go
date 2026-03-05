package main

import (
	"context"
	"testing"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	durableTesting "github.com/dgr237/aws-durable-execution-sdk-go/testing"
)

func TestOrderProcessing(t *testing.T) {
	// Create test runner
	runner := durableTesting.NewLocalDurableTestRunner(
		func(ctx context.Context) (interface{}, error) {
			// Create order event
			orderEvent := OrderEvent{
				OrderID:  "test-123",
				Amount:   100.0,
				Email:    "test@example.com",
				Currency: "USD",
			}

			return processOrder(ctx, orderEvent)
		},
		durableTesting.LocalDurableTestRunnerConfig{
			SkipTime: true, // Skip wait operations
		},
	)

	// Run the test
	result, err := runner.Run(durableTesting.RunConfig{
		Payload: map[string]interface{}{
			"orderId":  "test-123",
			"amount":   100.0,
			"email":    "test@example.com",
			"currency": "USD",
		},
	})

	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify status - execution should suspend at Wait operation
	if result.Status() != durableTesting.StatusSuspended {
		t.Errorf("Expected SUSPENDED status (due to Wait), got %v", result.Status())
	}

	// Verify operations executed up to the Wait
	validateOp, err := result.GetOperation("validate-order")
	if err != nil {
		t.Fatalf("validate-order operation not found: %v", err)
	}

	if validateOp.Status != client.OperationStatusSucceeded {
		t.Errorf("validate-order should succeed, got %v", validateOp.Status)
	}

	paymentOp, err := result.GetOperation("process-payment")
	if err != nil {
		t.Fatalf("process-payment operation not found: %v", err)
	}

	if paymentOp.Status != client.OperationStatusSucceeded {
		t.Errorf("process-payment should succeed, got %v", paymentOp.Status)
	}
}

func TestOrderProcessingWithInvalidAmount(t *testing.T) {
	runner := durableTesting.NewLocalDurableTestRunner(
		func(ctx context.Context) (interface{}, error) {
			// Create order event with invalid amount
			orderEvent := OrderEvent{
				OrderID:  "test-456",
				Amount:   -10.0, // Invalid amount
				Email:    "test@example.com",
				Currency: "USD",
			}

			return processOrder(ctx, orderEvent)
		},
		durableTesting.LocalDurableTestRunnerConfig{
			SkipTime: true,
		},
	)

	result, err := runner.Run(durableTesting.RunConfig{
		Payload: map[string]interface{}{
			"orderId":  "test-456",
			"amount":   -10.0,
			"email":    "test@example.com",
			"currency": "USD",
		},
	})

	// Should fail during validation
	if err == nil && result.Status() == durableTesting.StatusSucceeded {
		t.Error("Expected execution to fail with invalid amount")
	}
}
