package operations

import (
	"context"
	"fmt"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// ChildContextRunner
// ---------------------------------------------------------------------------

type ChildContextRunner[T any] struct {
	d            types.DurableContext
	name         string
	namePtr      *string
	fn           func(ctx context.Context, dc types.DurableContext) (T, error)
	serdes       types.Serdes
	childSubType *types.OperationSubType
	stepID       string
}

func newChildContextRunner[T any](
	d types.DurableContext,
	name string,
	fn func(ctx context.Context, dc types.DurableContext) (T, error),
	opts []ChildContextOption[T],
) *ChildContextRunner[T] {
	defaultSubType := types.OperationSubTypeRunInChildContext
	r := &ChildContextRunner[T]{
		d:            d,
		name:         name,
		namePtr:      stringPtr(name),
		fn:           fn,
		serdes:       utils.DefaultSerdes,
		childSubType: &defaultSubType,
		stepID:       d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// RunInChildContext — public entry point
// ---------------------------------------------------------------------------

func RunInChildContext[T any](
	dc types.DurableContext,
	name string,
	fn func(ctx context.Context, dc types.DurableContext) (T, error),
	opts ...ChildContextOption[T],
) (T, error) {
	r := newChildContextRunner[T](dc, name, fn, opts)

	subType := types.OperationSubTypeRunInChildContext
	stored := dc.GetStepData(r.stepID)

	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeStep, r.namePtr, &subType, stored); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		var zero T
		return zero, err
	}

	switch {
	case stored != nil && stored.Status == types.OperationStatusSucceeded:
		return r.replaySucceeded(stored)
	case stored != nil && stored.Status == types.OperationStatusFailed:
		return r.replayFailed(stored)
	default:
		return r.startFresh()
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *ChildContextRunner[T]) replaySucceeded(stored *types.Operation) (T, error) {
	var zero T
	r.markCompleted()

	var resultPtr *string
	if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	result, err := utils.SafeDeserialize[T](r.serdes, resultPtr, r.stepID, r.d.DurableExecutionArn())
	if err != nil {
		return zero, err
	}
	return result, nil
}

func (r *ChildContextRunner[T]) replayFailed(stored *types.Operation) (T, error) {
	var zero T
	r.d.MarkAncestorFinished(r.stepID)

	var cause error
	if stored.Error != nil {
		cause = utils.ErrorFromErrorObject(stored.Error)
	}
	return zero, durableErrors.NewChildContextError(r.stepID, r.namePtr, cause)
}

func (r *ChildContextRunner[T]) startFresh() (T, error) {
	childDc := r.d.NewChildDurableContext(r.d.Context(), r.stepID, r.stepID, r.d.Mode())
	result, err := r.fn(childDc.Context(), childDc)

	if err != nil {
		return r.handleExecutionError(err)
	}

	return r.handleExecutionSuccess(result)
}

// ---------------------------------------------------------------------------
// Result handling
// ---------------------------------------------------------------------------

func (r *ChildContextRunner[T]) handleExecutionError(err error) (T, error) {
	var zero T
	if cpErr := r.d.Checkpoint(r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: r.childSubType,
		Name:    r.namePtr,
		Error:   utils.SafeStringify(err),
	}); cpErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   cpErr,
			Message: fmt.Sprintf("failed to checkpoint FAIL for child context %s: %v", r.stepID, cpErr),
		})
		return zero, cpErr
	}
	r.d.MarkAncestorFinished(r.stepID)
	return zero, durableErrors.NewChildContextError(r.stepID, r.namePtr, err)
}

func (r *ChildContextRunner[T]) handleExecutionSuccess(result T) (T, error) {
	var zero T

	serialized, serErr := utils.SafeSerialize(r.serdes, result, r.stepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: serErr.Error(),
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
		SubType: r.childSubType,
		Name:    r.namePtr,
		Payload: payloadPtr,
	}); err != nil {
		return zero, err
	}

	r.markCompleted()
	return result, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *ChildContextRunner[T]) markCompleted() {
	r.d.MarkAncestorFinished(r.stepID)
	r.d.MarkOperationState(r.stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
		StepId:  r.stepID,
		Name:    r.namePtr,
		Type:    types.OperationTypeStep,
		SubType: r.childSubType,
	})
}
