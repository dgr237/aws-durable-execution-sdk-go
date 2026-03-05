package durablecontext

import (
	"context"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/state"
)

type durableDataKey struct{}
type stepDataKey struct{}
type conditionCheckDataKey struct{}

// DurableData holds durable execution data that flows through context.
type DurableData struct {
	State               *state.ExecutionState
	Logger              DurableLogger
	ExecutionContext    ExecutionContext
	OperationIDSequence *OperationIDSequence
	SerDes              config.SerDes
}

// StepData holds step execution data that flows through context.
type StepData struct {
	Logger DurableLogger
}

// ConditionCheckData holds condition check data that flows through context.
type ConditionCheckData struct {
	Logger DurableLogger
}

// ExecutionContext provides readonly metadata about the current durable execution.
type ExecutionContext struct {
	DurableExecutionArn string
}

// WithDurableData adds durable execution data to the context.
func WithDurableData(ctx context.Context, data *DurableData) context.Context {
	return context.WithValue(ctx, durableDataKey{}, data)
}

// GetDurableData retrieves durable execution data from the context.
func GetDurableData(ctx context.Context) *DurableData {
	if data, ok := ctx.Value(durableDataKey{}).(*DurableData); ok {
		return data
	}
	return nil
}

// WithStepData adds step execution data to the context.
func WithStepData(ctx context.Context, data *StepData) context.Context {
	return context.WithValue(ctx, stepDataKey{}, data)
}

// GetStepData retrieves step execution data from the context.
func GetStepData(ctx context.Context) *StepData {
	if data, ok := ctx.Value(stepDataKey{}).(*StepData); ok {
		return data
	}
	return nil
}

// WithConditionCheckData adds condition check data to the context.
func WithConditionCheckData(ctx context.Context, data *ConditionCheckData) context.Context {
	return context.WithValue(ctx, conditionCheckDataKey{}, data)
}

// GetConditionCheckData retrieves condition check data from the context.
func GetConditionCheckData(ctx context.Context) *ConditionCheckData {
	if data, ok := ctx.Value(conditionCheckDataKey{}).(*ConditionCheckData); ok {
		return data
	}
	return nil
}

// Helper functions for convenient access to durable data

// State retrieves the execution state from context.
func State(ctx context.Context) *state.ExecutionState {
	if data := GetDurableData(ctx); data != nil {
		return data.State
	}
	return nil
}

// Logger retrieves the logger from context (checks durable, step, and condition check data).
func Logger(ctx context.Context) DurableLogger {
	// Check step data first
	if stepData := GetStepData(ctx); stepData != nil {
		return stepData.Logger
	}
	// Check condition check data
	if checkData := GetConditionCheckData(ctx); checkData != nil {
		return checkData.Logger
	}
	// Check durable data
	if data := GetDurableData(ctx); data != nil {
		return data.Logger
	}
	return nil
}

// ExecutionContextFrom retrieves the execution context from context.
func ExecutionContextFrom(ctx context.Context) ExecutionContext {
	if data := GetDurableData(ctx); data != nil {
		return data.ExecutionContext
	}
	return ExecutionContext{}
}

// NextOperationID generates the next operation ID from context.
func NextOperationID(ctx context.Context, name string) OperationIdentifier {
	if data := GetDurableData(ctx); data != nil {
		return data.OperationIDSequence.Next(name)
	}
	return OperationIdentifier{}
}

// SerDes retrieves the serializer/deserializer from context.
func SerDes(ctx context.Context) config.SerDes {
	if data := GetDurableData(ctx); data != nil {
		return data.SerDes
	}
	return nil
}

// NewDurableContext creates a new context with durable execution data.
func NewDurableContext(
	ctx context.Context,
	st *state.ExecutionState,
	logger DurableLogger,
	executionContext ExecutionContext,
	serdes config.SerDes,
) context.Context {
	data := &DurableData{
		State:               st,
		Logger:              logger,
		ExecutionContext:    executionContext,
		OperationIDSequence: NewOperationIDSequence(),
		SerDes:              serdes,
	}
	return WithDurableData(ctx, data)
}

// NewStepContext creates a new context with step execution data.
func NewStepContext(ctx context.Context, logger DurableLogger) context.Context {
	data := &StepData{
		Logger: logger,
	}
	return WithStepData(ctx, data)
}

// NewConditionCheckContext creates a new context with condition check data.
func NewConditionCheckContext(ctx context.Context, logger DurableLogger) context.Context {
	data := &ConditionCheckData{
		Logger: logger,
	}
	return WithConditionCheckData(ctx, data)
}
