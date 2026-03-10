package operations

import (
	"context"
	"fmt"
	"time"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// StepRunner
// ---------------------------------------------------------------------------

type StepRunner[TOut any] struct {
	d             types.DurableContext
	name          string
	namePtr       *string
	fn            func(ctx context.Context) (TOut, error)
	serdes        types.Serdes
	semantics     types.StepSemantics
	retryStrategy func(err error, attempt int) types.RetryDecision
	subType       types.OperationSubType
	stepID        string
}

func newStepRunner[TOut any](
	d types.DurableContext,
	name string,
	fn func(ctx context.Context) (TOut, error),
	opts []StepOption[TOut],
) *StepRunner[TOut] {
	r := &StepRunner[TOut]{
		d:         d,
		name:      name,
		namePtr:   stringPtr(name),
		fn:        fn,
		serdes:    utils.DefaultSerdes,
		semantics: types.StepSemanticsAtLeastOncePerRetry,
		subType:   types.OperationSubTypeStep,
		stepID:    d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// Step — public entry point
// ---------------------------------------------------------------------------

func Step[TOut any](
	ctx context.Context,
	name string,
	fn func(ctx context.Context) (TOut, error),
	opts ...StepOption[TOut],
) (TOut, error) {
	d, err := durableCtx.GetDurableContext(ctx)
	if err != nil {
		panic("durable: no DurableContext found in ctx — pass the context.Context received by your HandlerFunc, not context.Background()")
	}
	r := newStepRunner[TOut](d, name, fn, opts)

	stored := r.d.GetStepData(r.stepID)

	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeStep, r.namePtr, &r.subType, stored); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		var zero TOut
		return zero, err
	}

	switch {
	case stored != nil && stored.Status == types.OperationStatusSucceeded:
		return r.replaySucceeded(stored)
	case stored != nil && stored.Status == types.OperationStatusFailed:
		return r.replayFailed(stored)
	default:
		return r.execute(ctx)
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *StepRunner[TOut]) replaySucceeded(stored *types.Operation) (TOut, error) {
	var zero TOut
	stepName := r.name
	if stepName == "" {
		stepName = "unnamed"
	}
	r.d.Logger().Info(fmt.Sprintf("Replaying step %s (ID: %s) as SUCCEEDED", stepName, r.stepID))
	r.markCompleted()

	var resultPtr *string
	if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	result, err := utils.SafeDeserialize[TOut](r.serdes, resultPtr, r.stepID, r.d.DurableExecutionArn())
	if err != nil {
		return zero, &durableErrors.SerdesError{
			Message:   "failed to deserialize stored step result",
			StepID:    r.stepID,
			StepName:  r.namePtr,
			Operation: "deserialize",
			Cause:     err,
		}
	}
	return result, nil
}

func (r *StepRunner[TOut]) replayFailed(stored *types.Operation) (TOut, error) {
	var zero TOut
	r.markCompleted()
	var cause error
	if stored.Error != nil {
		cause = utils.ErrorFromErrorObject(stored.Error)
	}
	return zero, durableErrors.NewStepError(r.stepID, r.namePtr, cause)
}

func (r *StepRunner[TOut]) execute(ctx context.Context) (TOut, error) {
	// Build a step context.Context from the parent durable context
	stepCtxGo := durableCtx.NewStepContextFrom(ctx)
	for attempt := 1; ; attempt++ {
		if err := r.maybeCheckpointStart(ctx); err != nil {
			var zero TOut
			return zero, err
		}

		result, stepErr := r.fn(stepCtxGo)

		if stepErr == nil {
			return r.handleSuccess(ctx, result)
		}

		if shouldStop, out, err := r.handleFailure(ctx, stepErr, attempt); shouldStop {
			return out, err
		}
	}
}

// ---------------------------------------------------------------------------
// Retry loop helpers
// ---------------------------------------------------------------------------

func (r *StepRunner[TOut]) maybeCheckpointStart(ctx context.Context) error {
	if r.semantics != types.StepSemanticsAtMostOncePerRetry {
		return nil
	}
	return r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
	})
}

func (r *StepRunner[TOut]) handleSuccess(ctx context.Context, result TOut) (TOut, error) {
	var zero TOut

	serialized, serErr := utils.SafeSerialize(r.serdes, result, r.stepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize step result: %v", serErr),
		})
		return zero, &durableErrors.SerdesFailedError{Message: serErr.Error()}
	}

	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}

	if err := r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionSucceed,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Payload: payloadPtr,
	}); err != nil {
		return zero, err
	}

	r.markCompleted()
	return result, nil
}

func (r *StepRunner[TOut]) handleFailure(ctx context.Context, stepErr error, attempt int) (stop bool, _ TOut, _ error) {
	var zero TOut

	if durableErrors.IsUnrecoverableError(stepErr) {
		return true, zero, stepErr
	}

	if r.retryStrategy != nil {
		decision := r.retryStrategy(stepErr, attempt)
		if decision.ShouldRetry {
			if decision.Delay != nil && decision.Delay.ToSeconds() > 0 {
				delaySeconds := int32(decision.Delay.ToSeconds())
				if cpErr := r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
					Id:      r.stepID,
					Action:  types.OperationActionRetry,
					Type:    types.OperationTypeStep,
					SubType: &r.subType,
					Name:    r.namePtr,
					Error:   utils.SafeStringify(stepErr),
					StepOptions: &types.StepOptions{
						NextAttemptDelaySeconds: &delaySeconds,
					},
				}); cpErr != nil {
					r.d.Terminate(types.TerminationResult{
						Reason:  types.TerminationReasonCheckpointFailed,
						Error:   cpErr,
						Message: fmt.Sprintf("failed to checkpoint RETRY for step %s: %v", r.stepID, cpErr),
					})
					return true, zero, cpErr
				}
				r.d.Terminate(types.TerminationResult{
					Reason:  types.TerminationReasonCheckpointTerminating,
					Message: fmt.Sprintf("step %s suspended for retry after %d seconds", r.stepID, delaySeconds),
				})
				return true, zero, &durableErrors.TerminatedError{Message: "step suspended for retry"}
			}
			// No delay — retry inline.
			time.Sleep(time.Second)
			return false, zero, nil
		}
	}

	if cpErr := r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Error:   utils.SafeStringify(stepErr),
	}); cpErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   cpErr,
			Message: fmt.Sprintf("failed to checkpoint FAIL for step %s: %v", r.stepID, cpErr),
		})
		return true, zero, cpErr
	}
	r.markCompleted()
	return true, zero, durableErrors.NewStepError(r.stepID, r.namePtr, stepErr)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *StepRunner[TOut]) markCompleted() {
	r.d.MarkOperationState(r.stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
		StepId:  r.stepID,
		Name:    r.namePtr,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
	})
}
