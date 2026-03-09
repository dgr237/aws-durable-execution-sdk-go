// Package main demonstrates how to use the AWS Durable Execution SDK for Go.
package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/durable-execution-sdk-go/pkg/durable"
	durablecontext "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
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
var durableHandler = func(ctx context.Context, event OrderEvent) (OrderResult, error) {
	dc := durablecontext.GetDurableContext(ctx)
	dc.Logger().Info("Starting order processing", "userId", event.UserID)

	// Step 1: Validate the order (retried up to 3 times on failure)
	validatedRaw, err := operations.Step(ctx, "validate-order", func(stepCtx context.Context) (any, error) {
		return validateOrder(event)
	}, operations.WithStepRetryStrategy[any](utils.Presets.ExponentialBackoff()),
	)
	if err != nil {
		return OrderResult{}, fmt.Errorf("validation failed: %w", err)
	}
	validatedOrderID := validatedRaw.(string)

	// Step 2: Process all line items in parallel (max 3 concurrent)
	anyItems := make([]any, len(event.Items))
	for i, item := range event.Items {
		anyItems[i] = item
	}

	batchResults, err := operations.Map(ctx,
		"process-items",
		anyItems,
		func(child context.Context, itemRaw any, index int, _ []any) (any, error) {
			item := itemRaw.(Item)
			return operations.Step(ctx, fmt.Sprintf("process-item-%d", index), func(stepCtx context.Context) (any, error) {
				return processItem(item)
			}, nil)
		}, operations.WithMapMaxConcurrency[any, any](3),
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
	_, err = operations.WaitForCallback[any](ctx,
		"payment-approval",
		func(sc context.Context, callbackID string) error {
			return sendPaymentRequest(event.UserID, event.Amount, callbackID)
		}, operations.WithWaitForCallbackTimeout[any](types.Duration{Hours: 1}))
	if err != nil {
		return OrderResult{}, fmt.Errorf("payment approval failed: %w", err)
	}

	// Step 4: Charge payment (at-most-once semantics — do not double-charge!)
	paymentIDRaw, err := operations.Step(ctx, "charge-payment", func(sc context.Context) (any, error) {
		return chargePayment(validatedOrderID, event.Amount)
	}, operations.WithStepSemantics[any](types.StepSemanticsAtMostOncePerRetry), operations.WithStepRetryStrategy[any](utils.Presets.NoRetry()))
	if err != nil {
		return OrderResult{}, fmt.Errorf("payment failed: %w", err)
	}

	// Step 5: Wait 5 seconds before sending confirmation (demonstrating Wait)
	_ = operations.Wait(ctx, "pre-email-delay", types.Duration{Seconds: 5})

	// Step 6: Send confirmation email (inside a step so it doesn't replay)
	_, err = operations.Step(ctx, "send-confirmation", func(sc context.Context) (any, error) {
		return nil, sendConfirmationEmail(paymentIDRaw.(string), event.UserID)
	}, nil)
	if err != nil {
		// Non-critical — log but don't fail the order
		dc.Logger().Error("Failed to send confirmation email", err)
	}

	dc.Logger().Info("Order completed", "orderId", paymentIDRaw.(string))

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
