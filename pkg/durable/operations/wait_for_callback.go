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
// WaitForCallbackRunner
// ---------------------------------------------------------------------------

type WaitForCallbackRunner[T any] struct {
	d         types.DurableContext
	name      string
	namePtr   *string
	submitter func(ctx context.Context, callbackID string) error
	serdes    types.Serdes
	timeout   *types.Duration
	subType   types.OperationSubType
	stepID    string
}

func newWaitForCallbackRunner[T any](
	d types.DurableContext,
	name string,
	submitter func(ctx context.Context, callbackID string) error,
	opts []WaitForCallbackOption[T],
) *WaitForCallbackRunner[T] {
	r := &WaitForCallbackRunner[T]{
		d:         d,
		name:      name,
		namePtr:   stringPtr(name),
		submitter: submitter,
		serdes:    utils.DefaultSerdes,
		subType:   types.OperationSubTypeWaitForCallback,
		stepID:    d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// WaitForCallback — public entry point
// ---------------------------------------------------------------------------

func WaitForCallback[T any](
	ctx context.Context,
	name string,
	submitter func(ctx context.Context, callbackID string) error,
	opts ...WaitForCallbackOption[T],
) (T, error) {
	d := durableCtx.GetDurableContext(ctx)
	r := newWaitForCallbackRunner[T](d, name, submitter, opts)

	stored := r.d.GetStepData(r.stepID)

	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeCallback, r.namePtr, &r.subType, stored); err != nil {
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
		return r.startFresh(ctx)
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *WaitForCallbackRunner[T]) replaySucceeded(stored *types.Operation) (T, error) {
	var zero T
	resultPtr := r.extractStoredResult(stored)
	result, err := utils.SafeDeserialize[T](r.serdes, resultPtr, r.stepID, r.d.DurableExecutionArn())
	if err != nil {
		return zero, err
	}
	return result, nil
}

func (r *WaitForCallbackRunner[T]) replayFailed(stored *types.Operation) (T, error) {
	var zero T
	r.d.Logger().Info(fmt.Sprintf("Replaying WaitForCallback %s as FAILED", r.stepID))
	return zero, durableErrors.NewCallbackError(r.stepID, r.namePtr, r.extractStoredError(stored))
}

func (r *WaitForCallbackRunner[T]) startFresh(ctx context.Context) (T, error) {
	var zero T
	callbackID := r.generateCallbackID()

	// Build a step context for the submitter
	stepCtxGo := durableCtx.NewStepContextFrom(ctx)
	if err := r.submitter(stepCtxGo, callbackID); err != nil {
		return zero, err
	}

	if err := r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
		Id:              r.stepID,
		Action:          types.OperationActionStart,
		Type:            types.OperationTypeCallback,
		SubType:         &r.subType,
		Name:            r.namePtr,
		CallbackOptions: r.callbackOptions(),
	}); err != nil {
		return zero, err
	}

	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for callback %s", callbackID),
	})
	select {}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *WaitForCallbackRunner[T]) extractStoredResult(stored *types.Operation) *string {
	if stored.CallbackDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying WaitForCallback %s from CallbackDetails", r.stepID))
		return stored.CallbackDetails.Result
	}
	if stored.StepDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying WaitForCallback %s from StepDetails (fallback)", r.stepID))
		return stored.StepDetails.Result
	}
	return nil
}

func (r *WaitForCallbackRunner[T]) extractStoredError(stored *types.Operation) error {
	if stored.CallbackDetails != nil && stored.CallbackDetails.Error != nil {
		return utils.ErrorFromErrorObject(stored.CallbackDetails.Error)
	}
	if stored.Error != nil {
		return utils.ErrorFromErrorObject(stored.Error)
	}
	return nil
}

func (r *WaitForCallbackRunner[T]) generateCallbackID() string {
	return fmt.Sprintf("cb-%s-%s", durableCtx.HashedStepID(r.stepID), utils.HashID(r.d.DurableExecutionArn()))
}

func (r *WaitForCallbackRunner[T]) callbackOptions() *types.CallbackOptions {
	if r.timeout == nil {
		return nil
	}
	return &types.CallbackOptions{
		TimeoutSeconds: int32(r.timeout.ToSeconds()),
	}
}
