package operations

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
)

// Step executes a function with automatic checkpointing and retry logic.
// The function is checkpointed before execution, and results are stored on success.
// On replay, if the step has already completed successfully, the stored result is returned.
func Step[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error) {
	return StepWithConfig(ctx, name, fn, config.DefaultStepConfig())
}

// StepWithConfig executes a step with custom configuration.
func StepWithConfig[T any](ctx context.Context, name string, fn func(context.Context) (T, error), cfg config.StepConfig) (T, error) {
	var zero T

	// Generate operation ID
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if step already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Step already succeeded, deserialize and return result
		durablecontext.Logger(ctx).Debug("Step already completed, skipping execution: %s", name)
		if result.Result != nil {
			var value T
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &value); err != nil {
				return zero, fmt.Errorf("failed to deserialize step result: %w", err)
			}
			return value, nil
		}
		return zero, nil
	}

	if result.IsFailed() {
		// Step failed previously
		if result.Error != nil {
			return zero, &durable.CallableRuntimeError{
				Message:       "step failed previously",
				OperationName: name,
				Cause:         fmt.Errorf("%s: %s", aws.ToString(result.Error.ErrorType), aws.ToString(result.Error.ErrorMessage)),
			}
		}
		return zero, &durable.CallableRuntimeError{
			Message:       "step failed previously",
			OperationName: name,
		}
	}

	if result.IsPending() || result.IsReady() {
		// Step is pending retry
		return zero, &durable.SuspendExecutionError{
			Message: fmt.Sprintf("step %s is pending retry", name),
		}
	}

	// Check if we need to handle AT_MOST_ONCE semantics for interrupted steps
	if cfg.Semantics == config.StepSemanticsAtMostOnce && result.IsStarted() {
		// Step was started but didn't complete - this is an interruption
		return zero, &durable.StepInterruptedError{
			Message:       "AT_MOST_ONCE step was interrupted",
			OperationName: name,
		}
	}

	// Step hasn't completed, execute it
	durablecontext.Logger(ctx).Info("Executing step: %s", name)

	// Create START checkpoint
	subType := client.OperationSubTypeStep
	startUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    types.OperationTypeStep,
		Action:  types.OperationActionStart,
		SubType: &subType,
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

	// Execute the step function
	stepCtx := durablecontext.NewStepContext(ctx, durablecontext.NewDurableLogger(opID.OperationID, name, durablecontext.State(ctx)))
	value, err := fn(stepCtx)

	if err != nil {
		// Step failed, checkpoint failure
		durablecontext.Logger(ctx).Error("Step failed: %s - %v", name, err)

		errorObj := &types.ErrorObject{
			ErrorType:    aws.String("StepError"),
			ErrorMessage: aws.String(err.Error()),
		}

		failUpdate := types.OperationUpdate{
			Id:     aws.String(opID.OperationID),
			Name:   &name,
			Type:   types.OperationTypeStep,
			Action: types.OperationActionFail,
			Error:  errorObj,
		}

		if cfg.CheckpointAsync {
			_ = durablecontext.State(ctx).Checkpoint(failUpdate)
		} else {
			_ = durablecontext.State(ctx).CheckpointSync(ctx, failUpdate)
		}

		// Apply retry strategy
		if cfg.RetryStrategy != nil {
			decision := cfg.RetryStrategy(err, 1)
			if decision.ShouldRetry {
				// Schedule retry
				retryUpdate := types.OperationUpdate{
					Id:     aws.String(opID.OperationID),
					Type:   types.OperationTypeStep,
					Action: types.OperationActionRetry,
					StepOptions: &types.StepOptions{
						NextAttemptDelaySeconds: aws.Int32(int32(decision.Delay.Seconds)),
					},
				}
				_ = durablecontext.State(ctx).Checkpoint(retryUpdate)

				// Suspend execution to retry later
				return zero, &durable.SuspendExecutionError{
					Message: fmt.Sprintf("step %s failed, retrying", name),
				}
			}
		}

		return zero, &durable.CallableRuntimeError{
			Message:       "step execution failed",
			OperationName: name,
			Cause:         err,
		}
	}

	// Step succeeded, serialize result
	durablecontext.Logger(ctx).Info("Step succeeded: %s", name)

	serialized, err := durablecontext.SerDes(ctx).Serialize(value)
	if err != nil {
		return zero, fmt.Errorf("failed to serialize step result: %w", err)
	}

	successUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    types.OperationTypeStep,
		Action:  types.OperationActionSucceed,
		Payload: &serialized,
	}

	if cfg.CheckpointAsync {
		if err := durablecontext.State(ctx).Checkpoint(successUpdate); err != nil {
			return zero, err
		}
	} else {
		if err := durablecontext.State(ctx).CheckpointSync(ctx, successUpdate); err != nil {
			return zero, err
		}
	}

	return value, nil
}
