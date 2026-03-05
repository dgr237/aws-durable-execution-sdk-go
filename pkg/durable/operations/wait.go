package operations

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
)

// Wait suspends execution for the specified duration.
// The execution will resume after the duration has elapsed.
func Wait(ctx context.Context, name string, duration config.Duration) error {
	return WaitWithConfig(ctx, name, duration, false)
}

// WaitWithConfig suspends execution with custom configuration.
func WaitWithConfig(ctx context.Context, name string, duration config.Duration, checkpointAsync bool) error {
	if err := duration.Validate(); err != nil {
		return err
	}

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if wait already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Wait already completed
		durablecontext.Logger(ctx).Debug("Wait already completed, skipping: %s", name)
		return nil
	}

	if result.IsPending() {
		// Wait is still pending
		return &durable.SuspendExecutionError{
			Message: fmt.Sprintf("wait %s is pending", name),
		}
	}

	// Create wait checkpoint
	durablecontext.Logger(ctx).Info("Creating wait: %s for %d seconds", name, duration.Seconds)

	subType := client.OperationSubTypeWait

	startUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeWait,
		Action:  client.OperationActionStart,
		SubType: &subType,
		WaitOptions: &client.WaitOptions{
			WaitSeconds: aws.Int32(int32(duration.Seconds)),
		},
	}

	if checkpointAsync {
		if err := durablecontext.State(ctx).Checkpoint(startUpdate); err != nil {
			return err
		}
	} else {
		if err := durablecontext.State(ctx).CheckpointSync(ctx, startUpdate); err != nil {
			return err
		}
	}

	// Suspend execution
	return &durable.SuspendExecutionError{
		Message: fmt.Sprintf("waiting for %d seconds", duration.Seconds),
	}
}

// Invoke calls another durable Lambda function and waits for the result.
// The functionArn MUST be a qualified ARN (with version or alias).
func Invoke[T any](ctx context.Context, name string, functionArn string, payload interface{}) (T, error) {
	return InvokeWithConfig[T](ctx, name, functionArn, payload, config.DefaultInvokeConfig())
}

// InvokeWithConfig invokes a function with custom configuration.
func InvokeWithConfig[T any](ctx context.Context, name string, functionArn string, payload interface{}, cfg config.InvokeConfig) (T, error) {
	var zero T

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if invoke already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Invoke already succeeded
		durablecontext.Logger(ctx).Debug("Invoke already completed, returning result: %s", name)
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize invoke result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	if result.IsFailed() {
		// Invoke failed
		if result.Error != nil {
			return zero, &durable.InvocationError{
				Message:      "invoke failed previously",
				FunctionArn:  functionArn,
				ErrorType:    aws.ToString(result.Error.ErrorType),
				ErrorMessage: aws.ToString(result.Error.ErrorMessage),
			}
		}
		return zero, &durable.InvocationError{
			Message:     "invoke failed previously",
			FunctionArn: functionArn,
		}
	}

	if result.IsPending() || result.IsStarted() {
		// Invoke is still in progress
		return zero, &durable.SuspendExecutionError{
			Message: fmt.Sprintf("invoke %s is pending", name),
		}
	}

	// Create invoke checkpoint
	durablecontext.Logger(ctx).Info("Invoking function: %s -> %s", name, functionArn)

	// Serialize payload
	serializedPayload, err := durablecontext.SerDes(ctx).Serialize(payload)
	if err != nil {
		return zero, fmt.Errorf("failed to serialize invoke payload: %w", err)
	}

	subType := client.OperationSubTypeChainedInvoke

	startUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeChainedInvoke,
		Action:  client.OperationActionStart,
		SubType: &subType,
		ChainedInvokeOptions: &client.ChainedInvokeOptions{
			FunctionName: aws.String(functionArn),
		},
		Payload: &serializedPayload,
	}

	if cfg.CheckpointAsync {
		if err := durablecontext.State(ctx).Checkpoint(startUpdate); err != nil {
			return zero, err
		}
	} else {
		if err := durablecontext.State(ctx).CheckpointSync(ctx, startUpdate); err != nil {
			return zero, err
		}
	}

	// Suspend - Lambda service will invoke the function and resume us when complete
	return zero, &durable.SuspendExecutionError{
		Message: fmt.Sprintf("invoking function %s", functionArn),
	}
}

// RunInChildContext executes a function in a child context, grouping operations together.
func RunInChildContext[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error) {
	return RunInChildContextWithConfig[T](ctx, name, fn, config.DefaultChildConfig())
}

// RunInChildContextWithConfig executes in a child context with custom configuration.
func RunInChildContextWithConfig[T any](ctx context.Context, name string, fn func(context.Context) (T, error), cfg config.ChildConfig) (T, error) {
	var zero T

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if child context already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Child context already succeeded
		durablecontext.Logger(ctx).Debug("Child context already completed: %s", name)
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize child context result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	if result.IsFailed() {
		// Child context failed
		if result.Error != nil {
			return zero, &durable.CallableRuntimeError{
				Message:       "child context failed previously",
				OperationName: name,
				Cause:         fmt.Errorf("%s: %s", aws.ToString(result.Error.ErrorType), aws.ToString(result.Error.ErrorMessage)),
			}
		}
		return zero, &durable.CallableRuntimeError{
			Message:       "child context failed previously",
			OperationName: name,
		}
	}

	// Execute child context
	durablecontext.Logger(ctx).Info("Executing child context: %s", name)

	subType := client.OperationSubTypeRunInChildContext

	startUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeContext,
		Action:  client.OperationActionStart,
		SubType: &subType,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(startUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, startUpdate)
	}

	// Create child context (reuses parent's state and logger)
	childCtx := durablecontext.NewDurableContext(
		ctx,
		durablecontext.State(ctx),
		durablecontext.Logger(ctx),
		durablecontext.ExecutionContextFrom(ctx),
		durablecontext.SerDes(ctx),
	)

	// Execute function in child context
	value, err := fn(childCtx)

	if err != nil {
		// Check if it's a suspension
		if durable.IsSuspendExecution(err) {
			return zero, err
		}

		// Child context failed
		durablecontext.Logger(ctx).Error("Child context failed: %s - %v", name, err)

		errorObj := &client.ErrorObject{
			ErrorType:    aws.String("ChildContextError"),
			ErrorMessage: aws.String(err.Error()),
		}

		failUpdate := client.OperationUpdate{
			Id:     aws.String(opID.OperationID),
			Type:   client.OperationTypeContext,
			Action: client.OperationActionFail,
			Error:  errorObj,
		}

		_ = durablecontext.State(ctx).Checkpoint(failUpdate)

		return zero, &durable.CallableRuntimeError{
			Message:       "child context execution failed",
			OperationName: name,
			Cause:         err,
		}
	}

	// Child context succeeded
	durablecontext.Logger(ctx).Info("Child context succeeded: %s", name)

	serialized, err := durablecontext.SerDes(ctx).Serialize(value)
	if err != nil {
		return zero, fmt.Errorf("failed to serialize child context result: %w", err)
	}

	successUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Type:    client.OperationTypeContext,
		Action:  client.OperationActionSucceed,
		Payload: &serialized,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(successUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, successUpdate)
	}

	return value, nil
}

// CallbackResult represents a result that will be provided by a callback.
type CallbackResult[T any] struct {
	ctx         context.Context
	operationID string
	name        string
}

// Wait waits for the callback to complete and returns the result.
func (cr *CallbackResult[T]) Wait() (T, error) {
	var zero T

	// Check if callback already completed
	result := durablecontext.State(cr.ctx).GetCheckpointResult(cr.operationID)

	if result.IsSucceeded() {
		// Callback already received
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(cr.ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize callback result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	if result.IsFailed() {
		// Callback failed (timeout, etc.)
		if result.Error != nil {
			return zero, &durable.CallbackError{
				Message: fmt.Sprintf("callback failed: %s", aws.ToString(result.Error.ErrorMessage)),
				Cause:   fmt.Errorf("%s", aws.ToString(result.Error.ErrorType)),
			}
		}
		return zero, &durable.CallbackError{Message: "callback failed"}
	}

	// Still waiting for callback
	return zero, &durable.SuspendExecutionError{
		Message: fmt.Sprintf("waiting for callback %s", cr.name),
	}
}

// CreateCallback creates a callback and returns the callback ID and a result object.
// The callback ID should be sent to an external system, which will later provide the result.
// Use the returned CallbackResult.Wait() to wait for the callback to complete.
//
// Example:
//
//	result, callbackID, err := durable.CreateCallback[string](ctx, "approval-request", config)
//	if err != nil {
//	    return nil, err
//	}
//	// Send callbackID to external approval system
//	sendApprovalRequest(callbackID)
//	// Wait for approval
//	approval, err := result.Wait()
func CreateCallback[T any](ctx context.Context, name string, cfg config.WaitForCallbackConfig) (*CallbackResult[T], string, error) {
	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if callback already completed or started
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() || result.IsFailed() || result.IsPending() || result.IsStarted() {
		// Callback already exists, return the existing callback ID
		callbackID := fmt.Sprintf("callback-%s", opID.OperationID)
		callbackResult := &CallbackResult[T]{
			ctx:         ctx,
			operationID: opID.OperationID,
			name:        name,
		}
		return callbackResult, callbackID, nil
	}

	// Create callback checkpoint
	durablecontext.Logger(ctx).Info("Creating callback: %s", name)

	callbackID := fmt.Sprintf("callback-%s", opID.OperationID)
	subType := client.OperationSubTypeWaitForCallback

	startUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeCallback,
		Action:  client.OperationActionStart,
		SubType: &subType,
		Payload: aws.String(callbackID),
	}

	if cfg.CheckpointAsync {
		if err := durablecontext.State(ctx).Checkpoint(startUpdate); err != nil {
			return nil, "", err
		}
	} else {
		if err := durablecontext.State(ctx).CheckpointSync(ctx, startUpdate); err != nil {
			return nil, "", err
		}
	}

	callbackResult := &CallbackResult[T]{
		ctx:         ctx,
		operationID: opID.OperationID,
		name:        name,
	}

	return callbackResult, callbackID, nil
}

// WaitForCallback waits for an external system to provide a callback.
// The submitter function is called with a callback ID that must be used to resume.
func WaitForCallback[T any](ctx context.Context, name string, submitter func(callbackID string) error, cfg config.WaitForCallbackConfig) (T, error) {
	var zero T

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if callback already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Callback already received
		durablecontext.Logger(ctx).Debug("Callback already completed: %s", name)
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize callback result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	if result.IsFailed() {
		// Callback failed (timeout, etc.)
		if result.Error != nil {
			return zero, &durable.CallbackError{
				Message: fmt.Sprintf("callback failed: %s", aws.ToString(result.Error.ErrorMessage)),
				Cause:   fmt.Errorf("%s", aws.ToString(result.Error.ErrorType)),
			}
		}
		return zero, &durable.CallbackError{Message: "callback failed"}
	}

	if result.IsPending() || result.IsStarted() {
		// Still waiting for callback
		return zero, &durable.SuspendExecutionError{
			Message: fmt.Sprintf("waiting for callback %s", name),
		}
	}

	// Create callback and call submitter
	durablecontext.Logger(ctx).Info("Creating callback: %s", name)

	callbackID := fmt.Sprintf("callback-%s", opID.OperationID)
	subType := client.OperationSubTypeWaitForCallback

	startUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeCallback,
		Action:  client.OperationActionStart,
		SubType: &subType,
		Payload: aws.String(callbackID),
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(startUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, startUpdate)
	}

	// Call submitter to send callback ID to external system
	if err := submitter(callbackID); err != nil {
		return zero, &durable.CallbackError{
			Message: "failed to submit callback",
			Cause:   err,
		}
	}

	// Suspend execution
	return zero, &durable.SuspendExecutionError{
		Message: fmt.Sprintf("waiting for callback %s", name),
	}
}

// WaitForCondition polls a condition until it's met.
func WaitForCondition[S any, T any](ctx context.Context, name string, check func(S, context.Context) (S, error), cfg config.WaitForConditionConfig[S]) (T, error) {
	var zero T

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if condition already met
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		durablecontext.Logger(ctx).Debug("Condition already met: %s", name)
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize condition result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	// Execute condition check
	durablecontext.Logger(ctx).Info("Checking condition: %s", name)

	checkCtx := durablecontext.NewConditionCheckContext(ctx, durablecontext.Logger(ctx))
	newState, err := check(cfg.InitialState, checkCtx)
	if err != nil {
		return zero, &durable.CallableRuntimeError{
			Message:       "condition check failed",
			OperationName: name,
			Cause:         err,
		}
	}

	// Check if condition is met
	if cfg.Condition(newState) {
		// Condition met, checkpoint success
		durablecontext.Logger(ctx).Info("Condition met: %s", name)

		serialized, err := durablecontext.SerDes(ctx).Serialize(newState)
		if err != nil {
			return zero, fmt.Errorf("failed to serialize condition result: %w", err)
		}

		subType := client.OperationSubTypeWaitForCondition

		successUpdate := client.OperationUpdate{
			Id:      aws.String(opID.OperationID),
			Name:    &name,
			Type:    client.OperationTypeStep,
			Action:  client.OperationActionSucceed,
			SubType: &subType,
			Payload: &serialized,
		}

		_ = durablecontext.State(ctx).CheckpointSync(ctx, successUpdate)

		// Return the state as result (type conversion)
		var resultVal T
		if err := durablecontext.SerDes(ctx).Deserialize(serialized, &resultVal); err != nil {
			return zero, fmt.Errorf("failed to convert condition result: %w", err)
		}
		return resultVal, nil
	}

	// Condition not met, schedule retry
	durablecontext.Logger(ctx).Debug("Condition not met, scheduling retry: %s", name)

	decision := cfg.WaitStrategy(newState, 1)
	if !decision.ShouldContinue {
		return zero, fmt.Errorf("wait condition exhausted retries")
	}

	subType := client.OperationSubTypeWaitForCondition

	retryUpdate := client.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    client.OperationTypeStep,
		Action:  client.OperationActionRetry,
		SubType: &subType,
		StepOptions: &client.StepOptions{
			NextAttemptDelaySeconds: aws.Int32(int32(decision.Delay.Seconds)),
		},
	}

	_ = durablecontext.State(ctx).Checkpoint(retryUpdate)
	return zero, &durable.SuspendExecutionError{
		Message: fmt.Sprintf("waiting for condition %s", name),
	}
}
