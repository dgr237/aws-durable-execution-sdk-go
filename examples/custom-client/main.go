package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/operations"
)

// OrderEvent represents an order to process.
type OrderEvent struct {
	OrderID string  `json:"orderId"`
	Amount  float64 `json:"amount"`
}

// handler demonstrates using a custom Lambda client with durable execution.
func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	// Create a custom AWS Lambda client with specific configuration
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-west-2"),
		// Add custom retry configuration
		awsconfig.WithRetryMaxAttempts(5),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create custom Lambda client with additional options
	customClient := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
		// Add custom endpoint for testing or regional deployment
		// o.BaseEndpoint = aws.String("https://lambda.us-west-2.amazonaws.com")

		// Add custom user agent
		// o.APIOptions = append(o.APIOptions, addUserAgent("CustomApp/1.0"))
	})

	// Use WithDurableExecution with the custom client
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var orderEvent OrderEvent
		if err := durable.ExtractInput(event, &orderEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		durablecontext.Logger(durableCtx).Info("Processing order with custom client: %s", orderEvent.OrderID)

		// Validate order
		validationResult, err := operations.Step(durableCtx, "validate-order", func(stepCtx context.Context) (bool, error) {
			durablecontext.Logger(stepCtx).Info("Validating order %s", orderEvent.OrderID)

			// Validation logic
			if orderEvent.Amount <= 0 {
				return false, fmt.Errorf("invalid amount: %f", orderEvent.Amount)
			}

			return true, nil
		})
		if err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}

		if !validationResult {
			return nil, fmt.Errorf("order validation failed")
		}

		// Process payment
		paymentID, err := operations.Step(durableCtx, "process-payment", func(stepCtx context.Context) (string, error) {
			durablecontext.Logger(stepCtx).Info("Processing payment for order %s", orderEvent.OrderID)

			// Simulate payment processing
			return fmt.Sprintf("payment-%s", orderEvent.OrderID), nil
		})
		if err != nil {
			return nil, fmt.Errorf("payment failed: %w", err)
		}

		// Ship order
		trackingID, err := operations.Step(durableCtx, "ship-order", func(stepCtx context.Context) (string, error) {
			durablecontext.Logger(stepCtx).Info("Shipping order %s", orderEvent.OrderID)

			// Simulate shipping
			return fmt.Sprintf("tracking-%s", orderEvent.OrderID), nil
		})
		if err != nil {
			return nil, fmt.Errorf("shipping failed: %w", err)
		}

		return map[string]interface{}{
			"orderId":    orderEvent.OrderID,
			"paymentId":  paymentID,
			"trackingId": trackingID,
			"status":     "completed",
		}, nil
	}, durable.WithClient(customClient)) // ⬅️ Pass custom client as option
}

func main() {
	lambda.Start(handler)
}
