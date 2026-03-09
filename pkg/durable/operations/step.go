package operations

import (
	"fmt"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/context"
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
	fn            func(ctx types.StepContext) (TOut, error)
	serdes        types.Serdes
	semantics     types.StepSemantics
	retryStrategy func(err error, attempt int) types.RetryDecision
	subType       types.OperationSubType
	stepID        string
}

func newStepRunner[TOut any](
	d types.DurableContext,
	name string,
	fn func(ctx types.StepContext) (TOut, error),
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
	d types.DurableContext,
	name string,
	fn func(ctx types.StepContext) (TOut, error),
	opts ...StepOption[TOut],
) (TOut, error) {
	r := newStepRunner[TOut](d, name, fn, opts)

	stored := r.d.GetStepData(r.stepID)

	if err := context.ValidateReplayConsistency(r.stepID, types.OperationTypeStep, r.namePtr, &r.subType, stored); err != nil {
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
		return r.execute()
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

func (r *StepRunner[TOut]) execute() (TOut, error) {
	stepCtx := r.d.NewStepContext()
	for attempt := 1; ; attempt++ {
		if err := r.maybeCheckpointStart(); err != nil {
			var zero TOut
			return zero, err
		}

		result, stepErr := r.fn(stepCtx)

		if stepErr == nil {
			return r.handleSuccess(result)
		}

		if shouldStop, out, err := r.handleFailure(stepErr, attempt); shouldStop {
			return out, err
		}
	}
}

// ---------------------------------------------------------------------------
// Retry loop helpers
// ---------------------------------------------------------------------------

func (r *StepRunner[TOut]) maybeCheckpointStart() error {
	if r.semantics != types.StepSemanticsAtMostOncePerRetry {
		return nil
	}
	return r.d.Checkpoint(r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
	})
}

func (r *StepRunner[TOut]) handleSuccess(result TOut) (TOut, error) {
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

	if err := r.d.Checkpoint(r.stepID, types.OperationUpdate{
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

func (r *StepRunner[TOut]) handleFailure(stepErr error, attempt int) (stop bool, _ TOut, _ error) {
	var zero TOut

	if durableErrors.IsUnrecoverableError(stepErr) {
		return true, zero, stepErr
	}

	if r.shouldRetry(stepErr, attempt) {
		r.sleepBeforeRetry(stepErr, attempt)
		return false, zero, nil
	}

	_ = r.d.Checkpoint(r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Error:   utils.SafeStringify(stepErr),
	})
	r.markCompleted()
	return true, zero, durableErrors.NewStepError(r.stepID, r.namePtr, stepErr)
}

func (r *StepRunner[TOut]) shouldRetry(err error, attempt int) bool {
	if r.retryStrategy == nil {
		return false
	}
	return r.retryStrategy(err, attempt).ShouldRetry
}

func (r *StepRunner[TOut]) sleepBeforeRetry(err error, attempt int) {
	if r.retryStrategy != nil {
		if d := r.retryStrategy(err, attempt).Delay; d != nil {
			time.Sleep(d.ToDuration())
			return
		}
	}
	time.Sleep(time.Second)
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
