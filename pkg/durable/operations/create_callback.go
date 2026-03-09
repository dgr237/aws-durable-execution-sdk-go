package operations

import (
	"context"
	"fmt"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// CallbackRunner
// ---------------------------------------------------------------------------

type CallbackRunner[TResult any] struct {
	d       types.DurableContext
	name    string
	namePtr *string
	timeout *types.Duration
	stepID  string
}

func newCallbackRunner[TResult any](
	d types.DurableContext,
	name string,
	opts []CallbackOption[TResult],
) *CallbackRunner[TResult] {
	r := &CallbackRunner[TResult]{
		d:       d,
		name:    name,
		namePtr: stringPtr(name),
		stepID:  d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// CreateCallback — public entry point
// ---------------------------------------------------------------------------

func CreateCallback[TResult any](
	ctx context.Context,
	name string,
	opts ...CallbackOption[TResult],
) (<-chan types.CallbackResult[TResult], string, error) {
	d := durableCtx.GetDurableContext(ctx)
	r := newCallbackRunner[TResult](d, name, opts)

	stored := d.GetStepData(r.stepID)

	subType := types.OperationSubTypeCallback
	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeCallback, r.namePtr, &subType, stored); err != nil {
		d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		return nil, "", err
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		callbackID := r.storedCallbackID(stored)
		return r.replaySucceeded(stored), callbackID, nil
	}

	return r.startFresh(ctx)
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *CallbackRunner[TResult]) replaySucceeded(stored *types.Operation) <-chan types.CallbackResult[TResult] {
	resultPtr := r.extractStoredResult(stored)

	result, err := utils.SafeDeserialize[TResult](utils.DefaultSerdes, resultPtr, r.stepID, r.d.DurableExecutionArn())

	ch := make(chan types.CallbackResult[TResult], 1)
	ch <- types.CallbackResult[TResult]{Value: result, Err: err}
	close(ch)
	return ch
}

func (r *CallbackRunner[TResult]) startFresh(ctx context.Context) (<-chan types.CallbackResult[TResult], string, error) {
	callbackID := r.generateCallbackID()

	if err := r.d.Checkpoint(ctx, r.stepID, types.OperationUpdate{
		Id:              r.stepID,
		Action:          types.OperationActionStart,
		Type:            types.OperationTypeCallback,
		SubType:         subTypePtr(types.OperationSubTypeCallback),
		Name:            r.namePtr,
		CallbackOptions: r.callbackOptions(),
	}); err != nil {
		return nil, "", err
	}

	ch := make(chan types.CallbackResult[TResult], 1)
	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for callback %s", callbackID),
	})

	return ch, callbackID, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *CallbackRunner[TResult]) extractStoredResult(stored *types.Operation) *string {
	if stored.CallbackDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying callback %s from CallbackDetails", r.stepID))
		return stored.CallbackDetails.Result
	}
	if stored.StepDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying callback %s from StepDetails (fallback)", r.stepID))
		return stored.StepDetails.Result
	}
	return nil
}

func (r *CallbackRunner[TResult]) generateCallbackID() string {
	return fmt.Sprintf("cb-%s-%s", durableCtx.HashedStepID(r.stepID), utils.HashID(r.d.DurableExecutionArn()))
}

func (r *CallbackRunner[TResult]) storedCallbackID(stored *types.Operation) string {
	if stored.CallbackDetails != nil && stored.CallbackDetails.CallbackId != nil {
		return *stored.CallbackDetails.CallbackId
	}
	return r.generateCallbackID()
}

func (r *CallbackRunner[TResult]) callbackOptions() *types.CallbackOptions {
	if r.timeout == nil {
		return nil
	}
	return &types.CallbackOptions{
		TimeoutSeconds: int32(r.timeout.ToSeconds()),
	}
}

// subTypePtr takes the address of a SubType value.
func subTypePtr(s types.OperationSubType) *types.OperationSubType {
	return &s
}
