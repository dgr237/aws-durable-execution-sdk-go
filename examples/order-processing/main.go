// Package main demonstrates a simple order processing workflow using durable execution.
package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/operations"
)

// OrderEvent represents an incoming order.
type OrderEvent struct {
	OrderID  string  `json:"orderId"`
	Amount   float64 `json:"amount"`
	Email    string  `json:"email"`
	Currency string  `json:"currency"`
}

// Order represents a validated order.
type Order struct {
	OrderID     string  `json:"orderId"`
	Amount      float64 `json:"amount"`
	Email       string  `json:"email"`
	Currency    string  `json:"currency"`
	Validated   bool    `json:"validated"`
	TaxAmount   float64 `json:"taxAmount"`
	TotalAmount float64 `json:"totalAmount"`
}

// PaymentResult represents the result of a payment.
type PaymentResult struct {
	TransactionID string `json:"transactionId"`
	Status        string `json:"status"`
}

// ShipmentResult represents the result of shipping.
type ShipmentResult struct {
	TrackingNumber string `json:"trackingNumber"`
	Carrier        string `json:"carrier"`
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var orderEvent OrderEvent
		if err := durable.ExtractInput(event, &orderEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		return processOrder(durableCtx, orderEvent)
	})
}

// processOrder contains the business logic for order processing.
// This function is exported so it can be tested.
func processOrder(durableCtx context.Context, orderEvent OrderEvent) (interface{}, error) {
	durablecontext.Logger(durableCtx).Info("Starting order processing for order: %s", orderEvent.OrderID)

	// Step 1: Validate order
	order, err := operations.Step(durableCtx, "validate-order", func(stepCtx context.Context) (Order, error) {
		durablecontext.Logger(stepCtx).Info("Validating order: %s", orderEvent.OrderID)

		// Validate order details
		if orderEvent.Amount <= 0 {
			return Order{}, fmt.Errorf("invalid amount: %f", orderEvent.Amount)
		}

		// Calculate tax
		taxAmount := orderEvent.Amount * 0.1 // 10% tax
		totalAmount := orderEvent.Amount + taxAmount

		return Order{
			OrderID:     orderEvent.OrderID,
			Amount:      orderEvent.Amount,
			Email:       orderEvent.Email,
			Currency:    orderEvent.Currency,
			Validated:   true,
			TaxAmount:   taxAmount,
			TotalAmount: totalAmount,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	durablecontext.Logger(durableCtx).Info("Order validated. Total: %f %s", order.TotalAmount, order.Currency)

	// Step 2: Process payment
	payment, err := operations.Step(durableCtx, "process-payment", func(stepCtx context.Context) (PaymentResult, error) {
		durablecontext.Logger(stepCtx).Info("Processing payment for order: %s", order.OrderID)

		// Simulate payment processing
		// In real implementation, this would call a payment gateway
		return PaymentResult{
			TransactionID: fmt.Sprintf("txn-%s", order.OrderID),
			Status:        "completed",
		}, nil
	})
	if err != nil {
		return nil, err
	}

	durablecontext.Logger(durableCtx).Info("Payment processed. Transaction ID: %s", payment.TransactionID)

	// Step 3: Wait for inventory preparation (3 minutes)
	durablecontext.Logger(durableCtx).Info("Waiting for inventory preparation...")
	if err := operations.Wait(durableCtx, "inventory-prep", config.NewDurationFromMinutes(3)); err != nil {
		return nil, err
	}

	// Step 4: Ship order
	shipment, err := operations.Step(durableCtx, "ship-order", func(stepCtx context.Context) (ShipmentResult, error) {
		durablecontext.Logger(stepCtx).Info("Shipping order: %s", order.OrderID)

		// Simulate shipment creation
		return ShipmentResult{
			TrackingNumber: fmt.Sprintf("TRACK-%s", order.OrderID),
			Carrier:        "FastShip Express",
		}, nil
	})
	if err != nil {
		return nil, err
	}

	durablecontext.Logger(durableCtx).Info("Order shipped. Tracking: %s", shipment.TrackingNumber)

	// Step 5: Send confirmation email
	_, err = operations.Step(durableCtx, "send-confirmation", func(stepCtx context.Context) (bool, error) {
		durablecontext.Logger(stepCtx).Info("Sending confirmation email to: %s", order.Email)

		// Simulate email sending
		// In real implementation, this would call an email service
		return true, nil
	})
	if err != nil {
		return nil, err
	}

	// Return success response
	return map[string]interface{}{
		"success":        true,
		"orderId":        order.OrderID,
		"transactionId":  payment.TransactionID,
		"trackingNumber": shipment.TrackingNumber,
		"message":        "Order processed successfully",
	}, nil
}
