// Package durable provides the AWS Durable Execution SDK for Go.
//
// The SDK enables developers to write multi-step, fault-tolerant Lambda functions
// that automatically checkpoint their state. If a function times out or fails, the
// durable execution service will replay it, skipping already-completed operations.
//
// # Basic Usage
//
//	type MyEvent struct { UserID string }
//	type MyResult struct { Status string }
//
//	handler := durable.WithDurableExecution(func(event MyEvent, ctx types.DurableContext) (MyResult, error) {
//	    data, err := ctx.Step("fetch-user", func(sc types.StepContext) (any, error) {
//	        return fetchUser(event.UserID)
//	    }, nil)
//	    if err != nil {
//	        return MyResult{}, err
//	    }
//	    return MyResult{Status: "ok"}, nil
//	})
//
//	func main() { lambda.Start(handler) }
package durable

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/client"
	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

const lambdaResponseSizeLimit = 6*1024*1024 - 50 // 6MB minus small envelope overhead

// Config holds optional configuration for the durable execution runtime.
type Config struct {
	// Client is an optional custom backend client (useful for testing).
	// If nil, a default AWS Lambda client is created automatically.
	Client checkpoint.Client
}

// HandlerFunc is the type signature for a durable execution handler function.
//
//	TEvent  – type of the deserialized Lambda input event
//	TResult – type of the value returned by the handler
type HandlerFunc[TEvent any, TResult any] func(event TEvent, ctx types.DurableContext) (TResult, error)

// LambdaHandler is the AWS Lambda-compatible handler type returned by WithDurableExecution.
// Register it with the Lambda runtime:
//
//	lambda.Start(handler)
type LambdaHandler func(ctx context.Context, event types.DurableExecutionInvocationInput) (types.DurableExecutionInvocationOutput, error)

// WithDurableExecution wraps a user handler to create a Lambda handler that automatically
// manages durable execution state, checkpointing, and replay.
//
// Example:
//
//	handler := durable.WithDurableExecution(func(event OrderEvent, ctx types.DurableContext) (OrderResult, error) {
//	    order, err := ctx.Step("validate-order", func(sc types.StepContext) (any, error) {
//	        return validateOrder(event)
//	    }, nil)
//	    if err != nil {
//	        return OrderResult{}, err
//	    }
//	    return OrderResult{OrderID: order.(string)}, nil
//	}, nil)
//
//	func main() { lambda.Start(handler) }
func WithDurableExecution[TEvent any, TResult any](
	handler HandlerFunc[TEvent, TResult],
	cfg *Config,
) LambdaHandler {
	return func(goCtx context.Context, event types.DurableExecutionInvocationInput) (types.DurableExecutionInvocationOutput, error) {
		// Validate the durable execution input
		if err := checkpoint.ValidateDurableExecutionEvent(event); err != nil {
			return failedOutput(err), nil
		}

		// Resolve the backend client
		var backendClient checkpoint.Client
		if cfg != nil && cfg.Client != nil {
			backendClient = cfg.Client
		} else {
			c, err := client.NewDefaultLambdaClient(goCtx)
			if err != nil {
				return types.DurableExecutionInvocationOutput{
					Status: types.InvocationStatusFailed,
					Error: &types.ErrorObject{
						ErrorType:    "*errors.errorString",
						ErrorMessage: fmt.Sprintf("failed to initialize Lambda client: %v", err),
					},
				}, nil
			}
			backendClient = c
		}

		// Build a minimal LambdaContext
		lambdaCtx := extractLambdaContext(goCtx)

		// Initialize the execution context (loads full operation history, handles pagination)
		execCtx, mode, checkpointToken, err := durableCtx.InitializeExecutionContext(event, lambdaCtx, backendClient)
		if err != nil {
			return failedOutput(err), nil
		}

		return runHandler(execCtx, lambdaCtx, mode, checkpointToken, handler)
	}
}

// runHandler is the core execution engine.
//
// It:
//  1. Creates the checkpoint manager and root DurableContext.
//  2. Parses the user event from the first operation's InputPayload.
//  3. Runs the user handler in a goroutine.
//  4. Races the handler goroutine against the termination manager channel.
//  5. Returns the appropriate DurableExecutionInvocationOutput.
func runHandler[TEvent any, TResult any](
	execCtx *checkpoint.ExecutionContext,
	lambdaCtx *types.LambdaContext,
	mode types.DurableExecutionMode,
	checkpointToken string,
	handler HandlerFunc[TEvent, TResult],
) (types.DurableExecutionInvocationOutput, error) {
	logger := utils.NewDefaultLogger(execCtx.DurableExecutionArn, execCtx.RequestID, "")

	// Create the checkpoint manager
	mgr := checkpoint.NewManager(
		execCtx.DurableExecutionArn,
		checkpointToken,
		execCtx.Client,
		execCtx.TerminationManager,
		logger,
	)

	// Ensure the checkpoint manager is told to stop when execution terminates
	execCtx.TerminationManager.RegisterTerminationCallback(mgr.SetTerminating)

	// Create the root DurableContext
	dc := durableCtx.NewRootContext(execCtx, lambdaCtx, mgr, mode, logger)

	// Extract the customer's event from the first operation's InputPayload
	var userEvent TEvent
	if op := firstOperationWithInput(execCtx); op != nil {
		_ = json.Unmarshal([]byte(*op.ExecutionDetails.InputPayload), &userEvent)
	}

	// Run the handler and collect its result on a buffered channel
	type result struct {
		value TResult
		err   error
	}
	handlerCh := make(chan result, 1)
	go func() {
		v, err := handler(userEvent, dc)
		handlerCh <- result{value: v, err: err}
	}()

	// Race handler completion against termination
	select {
	case hr := <-handlerCh:
		// Flush any in-flight checkpoints before returning
		_ = mgr.WaitForQueueCompletion()

		if hr.err != nil {
			if durableErrors.IsUnrecoverableInvocationError(hr.err) {
				return types.DurableExecutionInvocationOutput{}, hr.err
			}
			return failedOutput(hr.err), nil
		}

		// Serialize the result
		resultBytes, err := json.Marshal(hr.value)
		if err != nil {
			return failedOutput(fmt.Errorf("failed to serialize handler result: %w", err)), nil
		}
		resultStr := string(resultBytes)

		// Check Lambda response size limit; checkpoint large results
		if len(resultBytes) > lambdaResponseSizeLimit {
			stepID := fmt.Sprintf("execution-result-%d", time.Now().UnixMilli())
			_ = mgr.Checkpoint(stepID, types.OperationUpdate{
				Id:      stepID,
				Action:  types.OperationActionSucceed,
				Type:    types.OperationTypeExecution,
				Payload: &resultStr,
			})
			_ = mgr.WaitForQueueCompletion()

			empty := ""
			return types.DurableExecutionInvocationOutput{
				Status: types.InvocationStatusSucceeded,
				Result: &empty,
			}, nil
		}

		return types.DurableExecutionInvocationOutput{
			Status: types.InvocationStatusSucceeded,
			Result: &resultStr,
		}, nil

	case term := <-execCtx.TerminationManager.TerminationChannel():
		_ = mgr.WaitForQueueCompletion()

		switch term.Reason {
		case types.TerminationReasonCheckpointFailed:
			if durableErrors.IsUnrecoverableInvocationError(term.Error) {
				return types.DurableExecutionInvocationOutput{}, term.Error
			}
			return failedOutput(term.Error), nil

		case types.TerminationReasonSerdesFailed:
			return types.DurableExecutionInvocationOutput{}, &durableErrors.SerdesFailedError{
				Message: term.Message,
			}

		case types.TerminationReasonContextValidationError:
			var errObj *types.ErrorObject
			if term.Error != nil {
				errObj = utils.SafeStringify(term.Error)
			} else {
				errObj = &types.ErrorObject{ErrorMessage: term.Message}
			}
			return types.DurableExecutionInvocationOutput{
				Status: types.InvocationStatusFailed,
				Error:  errObj,
			}, nil

		default:
			// Normal pending state – execution will be resumed by the durable execution service
			return types.DurableExecutionInvocationOutput{
				Status: types.InvocationStatusPending,
			}, nil
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func failedOutput(err error) types.DurableExecutionInvocationOutput {
	return types.DurableExecutionInvocationOutput{
		Status: types.InvocationStatusFailed,
		Error:  utils.SafeStringify(err),
	}
}

func firstOperationWithInput(execCtx *checkpoint.ExecutionContext) *types.Operation {
	for _, op := range execCtx.StepData {
		if op.ExecutionDetails != nil && op.ExecutionDetails.InputPayload != nil {
			return op
		}
	}
	return nil
}

func extractLambdaContext(ctx context.Context) *types.LambdaContext {
	// Best-effort extraction of Lambda invocation metadata from the context.
	// The aws-lambda-go runtime stores these under unexported keys; here we
	// fall back to a minimal context and let callers fill it in via Config if needed.
	lc := &types.LambdaContext{}
	_ = ctx // suppress unused warning
	return lc
}
