package operations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// InvokeRunner
// ---------------------------------------------------------------------------

type InvokeRunner[TIn, TOut any] struct {
	d       types.DurableContext
	name    string
	namePtr *string
	funcID  string
	input   TIn
	serdes  types.Serdes
	stepID  string
}

func newInvokeRunner[TIn, TOut any](
	d types.DurableContext,
	name string,
	funcID string,
	input TIn,
	opts []InvokeOption[TIn, TOut],
) *InvokeRunner[TIn, TOut] {
	r := &InvokeRunner[TIn, TOut]{
		d:       d,
		name:    name,
		namePtr: stringPtr(name),
		funcID:  funcID,
		input:   input,
		serdes:  utils.DefaultSerdes,
		stepID:  d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// Invoke — public entry point
// ---------------------------------------------------------------------------

func Invoke[TIn, TOut any](
	ctx context.Context,
	name string,
	funcID string,
	input TIn,
	opts ...InvokeOption[TIn, TOut],
) (TOut, error) {
	d, err := durableCtx.GetDurableContext(ctx)
	if err != nil {
		panic("durable: no DurableContext found in ctx — pass the context.Context received by your HandlerFunc, not context.Background()")
	}
	r := newInvokeRunner[TIn, TOut](d, name, funcID, input, opts)

	stored := r.d.GetStepData(r.stepID)

	subType := types.OperationSubTypeChainedInvoke
	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeChainedInvoke, r.namePtr, &subType, stored); err != nil {
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
	case stored != nil && stored.Status == types.OperationStatusStarted:
		return r.suspendStarted()
	default:
		return r.startFresh(ctx)
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *InvokeRunner[TIn, TOut]) replaySucceeded(stored *types.Operation) (TOut, error) {
	var zero TOut
	resultPtr := r.extractStoredResult(stored)
	result, err := utils.SafeDeserialize[TOut](r.serdes, resultPtr, r.stepID, r.d.DurableExecutionArn())
	if err != nil {
		return zero, err
	}
	return result, nil
}

func (r *InvokeRunner[TIn, TOut]) replayFailed(stored *types.Operation) (TOut, error) {
	var zero TOut
	r.d.Logger().Info(fmt.Sprintf("Replaying chained invoke %s as FAILED", r.stepID))
	return zero, durableErrors.NewInvokeError(r.stepID, r.namePtr, r.extractStoredError(stored))
}

func (r *InvokeRunner[TIn, TOut]) suspendStarted() (TOut, error) {
	var zero TOut
	r.d.Logger().Info(fmt.Sprintf("Chained invoke %s already started, suspending to wait for completion", r.stepID))
	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for chained invoke %s to complete", r.funcID),
	})
	return zero, &durableErrors.TerminatedError{Message: "execution terminated waiting for completion"}
}

func (r *InvokeRunner[TIn, TOut]) startFresh(ctx context.Context) (TOut, error) {
	var zero TOut
	r.d.Logger().Info(fmt.Sprintf("Chained invoke %s not in replay state, invoking %s", r.stepID, r.funcID))

	inputStr, err := r.serializeInput()
	if err != nil {
		return zero, err
	}

	if err := r.checkpointStart(ctx, inputStr); err != nil {
		return zero, err
	}

	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("invoking %s", r.funcID),
	})
	return zero, &durableErrors.TerminatedError{Message: "execution terminated for checkpoint"}
}

// ---------------------------------------------------------------------------
// Checkpointing
// ---------------------------------------------------------------------------

func (r *InvokeRunner[TIn, TOut]) checkpointStart(ctx context.Context, inputStr string) error {
	r.d.Logger().Info(fmt.Sprintf("About to checkpoint START for %s", r.stepID))

	update := types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeChainedInvoke,
		SubType: subTypePtr(types.OperationSubTypeChainedInvoke),
		Name:    r.namePtr,
		ChainedInvokeOptions: &types.ChainedInvokeOptions{
			FunctionName: &r.funcID,
		},
		Payload: &inputStr,
	}

	if r.d.ParentID() != "" {
		update.ParentId = aws.String(r.d.ParentID())
	}

	return r.d.Checkpoint(ctx, r.stepID, update)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *InvokeRunner[TIn, TOut]) serializeInput() (string, error) {
	r.d.Logger().Info(fmt.Sprintf("About to serialize input for %s", r.stepID))
	b, err := json.Marshal(r.input)
	if err != nil {
		return "", fmt.Errorf("failed to serialize invoke input: %w", err)
	}
	s := string(b)
	r.d.Logger().Info(fmt.Sprintf("Input serialized successfully for %s, length: %d", r.stepID, len(s)))
	return s, nil
}

func (r *InvokeRunner[TIn, TOut]) extractStoredResult(stored *types.Operation) *string {
	if stored.ChainedInvokeDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying chained invoke %s from ChainedInvokeDetails", r.stepID))
		return stored.ChainedInvokeDetails.Result
	}
	if stored.StepDetails != nil {
		r.d.Logger().Info(fmt.Sprintf("Replaying chained invoke %s from StepDetails (fallback)", r.stepID))
		return stored.StepDetails.Result
	}
	return nil
}

func (r *InvokeRunner[TIn, TOut]) extractStoredError(stored *types.Operation) error {
	if stored.ChainedInvokeDetails != nil && stored.ChainedInvokeDetails.Error != nil {
		return utils.ErrorFromErrorObject(stored.ChainedInvokeDetails.Error)
	}
	if stored.Error != nil {
		return utils.ErrorFromErrorObject(stored.Error)
	}
	return nil
}
