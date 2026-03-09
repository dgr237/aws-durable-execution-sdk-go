// Package main demonstrates how to use the AWS Durable Execution SDK for Go.
package main

import (
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	durable "github.com/aws/durable-execution-sdk-go"
	"github.com/aws/durable-execution-sdk-go/context"
	"github.com/aws/durable-execution-sdk-go/types"
	"github.com/aws/durable-execution-sdk-go/utils"
)

// ---- Domain types ----------------------------------------------------------

type OrderEvent struct {
	UserID string  `json:"userId"`
	Items  []Item  `json:"items"`
	Amount float64 `json:"amount"`
}

type Item struct {
	ProductID string `json:"productId"`
	Quantity  int    `json:"quantity"`
}

type OrderResult struct {
	OrderID string   `json:"orderId"`
	Status  string   `json:"status"`
	Items   []string `json:"items"`
}

// ---- Simulated external services -------------------------------------------

func validateOrder(event OrderEvent) (string, error) {
	if event.Amount <= 0 {
		return "", fmt.Errorf("invalid order amount: %f", event.Amount)
	}
	return fmt.Sprintf("validated-%s", event.UserID), nil
}

func sendPaymentRequest(userID string, amount float64, callbackID string) error {
	fmt.Printf("💳 Sending payment request for user %s, amount %.2f, callbackID %s\n",
		userID, amount, callbackID)
	return nil
}

func chargePayment(validatedOrderID string, amount float64) (string, error) {
	return fmt.Sprintf("payment-%s", validatedOrderID), nil
}

func processItem(item Item) (string, error) {
	return fmt.Sprintf("processed-%s-x%d", item.ProductID, item.Quantity), nil
}

func sendConfirmationEmail(orderID string, userID string) error {
	fmt.Printf("📧 Sending confirmation for order %s to user %s\n", orderID, userID)
	return nil
}

// ---- Handler ----------------------------------------------------------------

// durableHandler is the business logic wrapped in a durable execution handler.
// The DurableContext provides all durable operations.
var durableHandler = func(event OrderEvent, durableCtx *context.DurableContext) (OrderResult, error) {
	durableCtx.Logger().Info("Starting order processing", "userId", event.UserID)

	// Step 1: Validate the order (retried up to 3 times on failure)
	validatedRaw, err := context.Step(durableCtx, "validate-order", func(stepCtx types.StepContext) (any, error) {
		return validateOrder(event)
	}, &types.StepConfig{
		RetryStrategy: utils.Presets.ExponentialBackoff(),
	})
	if err != nil {
		return OrderResult{}, fmt.Errorf("validation failed: %w", err)
	}
	validatedOrderID := validatedRaw.(string)

	// Step 2: Process all line items in parallel (max 3 concurrent)
	anyItems := make([]any, len(event.Items))
	for i, item := range event.Items {
		anyItems[i] = item
	}

	batchResults, err := context.Map(durableCtx,
		"process-items",
		anyItems,
		func(child *context.DurableContext, itemRaw any, index int, _ []any) (any, error) {
			item := itemRaw.(Item)
			return context.Step(durableCtx, fmt.Sprintf("process-item-%d", index), func(stepCtx types.StepContext) (any, error) {
				return processItem(item)
			}, nil)
		},
		&types.MapConfig{MaxConcurrency: 3},
	)
	if err != nil {
		return OrderResult{}, fmt.Errorf("item processing failed: %w", err)
	}

	processedItems := make([]string, 0, len(batchResults.Items))
	for _, r := range batchResults.Items {
		if r.Err != nil {
			return OrderResult{}, fmt.Errorf("item %d failed: %w", r.Index, r.Err)
		}
		processedItems = append(processedItems, r.Value.(string))
	}

	// Step 3: Wait for human approval via external callback (1 hour timeout)
	_, err = durableCtx.WaitForCallback(
		"payment-approval",
		func(sc types.StepContext, callbackID string) error {
			return sendPaymentRequest(event.UserID, event.Amount, callbackID)
		},
		&types.WaitForCallbackConfig{
			Timeout: &types.Duration{Hours: 1},
		},
	)
	if err != nil {
		return OrderResult{}, fmt.Errorf("payment approval failed: %w", err)
	}

	// Step 4: Charge payment (at-most-once semantics — do not double-charge!)
	paymentIDRaw, err := context.Step(durableCtx, "charge-payment", func(sc types.StepContext) (any, error) {
		return chargePayment(validatedOrderID, event.Amount)
	}, &types.StepConfig{
		Semantics:     types.StepSemanticsAtMostOncePerRetry,
		RetryStrategy: utils.Presets.NoRetry(),
	})
	if err != nil {
		return OrderResult{}, fmt.Errorf("payment failed: %w", err)
	}

	// Step 5: Wait 5 seconds before sending confirmation (demonstrating Wait)
	_ = context.Wait(durableCtx, "pre-email-delay", types.Duration{Seconds: 5})

	// Step 6: Send confirmation email (inside a step so it doesn't replay)
	_, err = context.Step(durableCtx, "send-confirmation", func(sc types.StepContext) (any, error) {
		return nil, sendConfirmationEmail(paymentIDRaw.(string), event.UserID)
	}, nil)
	if err != nil {
		// Non-critical — log but don't fail the order
		durableCtx.Logger().Error("Failed to send confirmation email", err)
	}

	durableCtx.Logger().Info("Order completed", "orderId", paymentIDRaw.(string))

	return OrderResult{
		OrderID: paymentIDRaw.(string),
		Status:  "confirmed",
		Items:   processedItems,
	}, nil
}

// Handler is the Lambda entrypoint, registered with the Lambda runtime.
var Handler = durable.WithDurableExecution(durableHandler, nil)

func main() {
	lambda.Start(Handler)
}
