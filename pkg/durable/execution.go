package durable

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/state"
)

// HandlerFunc is the signature for durable Lambda handlers.
type HandlerFunc func(context.Context) (interface{}, error)

// DurableExecutionOption is a functional option for configuring WithDurableExecution.
type DurableExecutionOption func(*durableExecutionConfig)

// durableExecutionConfig holds the configuration for durable execution.
type durableExecutionConfig struct {
	client client.DurableClient
}

// WithClient sets a custom DurableClient for durable execution.
// If not provided, a default client will be created using NewDurableClient.
func WithClient(durableClient client.DurableClient) DurableExecutionOption {
	return func(cfg *durableExecutionConfig) {
		cfg.client = durableClient
	}
}

// WithDurableExecution wraps a handler function to enable durable execution.
// This is the main entry point for creating durable Lambda functions.
//
// The event parameter should be the raw Lambda event payload which contains:
//   - CheckpointToken: Token for maintaining checkpoint ordering
//   - DurableExecutionArn: ARN of the durable execution
//   - InitialExecutionState: Initial state with operations and marker
//
// Options:
//   - WithClient: Use a custom DurableClient instead of the default
//
// Example:
//
//	func handler(ctx context.Context, event durable.DurableExecutionEvent) (interface{}, error) {
//	    return durable.WithDurableExecution(ctx, event, func(durableCtx *durable.Context) (interface{}, error) {
//	        // Your durable workflow logic
//	        return result, nil
//	    })
//	}
func WithDurableExecution(ctx context.Context, event client.DurableExecutionEvent, handler HandlerFunc, opts ...DurableExecutionOption) (interface{}, error) {
	// Apply options
	cfg := &durableExecutionConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Validate event payload
	if event.DurableExecutionArn == "" {
		return nil, &ValidationError{
			Message: "DurableExecutionArn is required in event payload",
		}
	}

	if event.CheckpointToken == "" {
		return nil, &ValidationError{
			Message: "CheckpointToken is required in event payload",
		}
	}

	// Create or use provided client
	var durableClient client.DurableClient
	var err error
	if cfg.client != nil {
		durableClient = cfg.client
	} else {
		durableClient, err = client.NewDurableClient(ctx)
		if err != nil {
			return nil, &ExecutionError{
				Message: "failed to create durable client",
				Cause:   err,
			}
		}
	}

	// Create Lambda service client
	lambdaClient := client.NewLambdaServiceClient(durableClient, event.DurableExecutionArn, event.CheckpointToken)

	// Load initial execution state with event data
	initialState, err := loadInitialState(ctx, lambdaClient, event.InitialExecutionState)
	if err != nil {
		return nil, &ExecutionError{
			Message: "failed to load initial execution state",
			Cause:   err,
		}
	}

	// Create execution state
	executionState := state.NewExecutionState(ctx, lambdaClient, *initialState)
	defer executionState.Close()

	// Create execution context
	executionContext := durablecontext.ExecutionContext{
		DurableExecutionArn: event.DurableExecutionArn,
	}

	// Create durable context
	logger := durablecontext.NewSimpleLogger()
	durableCtx := durablecontext.NewDurableContext(ctx, executionState, logger, executionContext, config.DefaultSerDes())

	// Execute the handler
	result, err := executeHandler(durableCtx, handler)

	if err != nil {
		// Check if it's a suspension
		if IsSuspendExecution(err) {
			// This is expected - execution will resume later
			return map[string]interface{}{
				"status":  "suspended",
				"message": err.Error(),
			}, nil
		}

		// Actual error
		return nil, err
	}

	return result, nil
}

// loadInitialState loads the initial execution state from the event payload,
// and fetches additional pages if NextMarker is present.
func loadInitialState(ctx context.Context, lambdaClient client.LambdaServiceClient, eventState client.InitialExecutionState) (*client.InitialExecutionState, error) {
	// Start with operations from the event payload
	initialState := &client.InitialExecutionState{
		Operations: eventState.Operations,
		NextMarker: eventState.NextMarker,
	}

	// Load all remaining pages if NextMarker is present
	marker := eventState.NextMarker
	for marker != "" {
		output, err := lambdaClient.GetDurableExecutionState(ctx, marker)
		if err != nil {
			return nil, fmt.Errorf("failed to get durable execution state page: %w", err)
		}

		initialState.Operations = append(initialState.Operations, output.Operations...)
		marker = output.NextMarker
	}

	return initialState, nil
}

// executeHandler executes the user's handler with error handling.
func executeHandler(ctx context.Context, handler HandlerFunc) (interface{}, error) {
	// Use defer to catch panics
	defer func() {
		if r := recover(); r != nil {
			durablecontext.Logger(ctx).Error("Handler panicked: %v", r)
		}
	}()

	// Execute the handler
	return handler(ctx)
}

// MarshalPayload marshals a Go value to JSON for use as Lambda payload.
func MarshalPayload(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}
	return data, nil
}

// UnmarshalPayload unmarshals JSON payload to a Go value.
func UnmarshalPayload(data []byte, v interface{}) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal payload: %w", err)
	}
	return nil
}

// ExtractInput is a generic helper function to extract and parse user input from the durable execution event.
// It retrieves the input payload from the event's InitialExecutionState and unmarshals it into the target type.
//
// Example:
//
//	var orderEvent OrderEvent
//	if err := durable.ExtractInput(event, &orderEvent); err != nil {
//	    return nil, fmt.Errorf("failed to parse input: %w", err)
//	}
func ExtractInput[T any](event client.DurableExecutionEvent, target *T) error {
	payload, err := event.InitialExecutionState.GetInputPayload()
	if err != nil {
		return fmt.Errorf("failed to get input payload: %w", err)
	}
	if payload == nil || *payload == "" {
		return fmt.Errorf("input payload is empty")
	}
	return UnmarshalPayload([]byte(*payload), target)
}
