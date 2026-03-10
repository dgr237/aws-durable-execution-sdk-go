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
// WaitForConditionRunner
// ---------------------------------------------------------------------------

type WaitForConditionRunner[TState any] struct {
	d            types.DurableContext
	name         string
	namePtr      *string
	checkFn      func(ctx context.Context, sc types.StepContext, state TState) (TState, error)
	initialState TState
	serdes       types.Serdes
	waitStrategy func(state TState, attempt int) types.WaitStrategyResult
	subType      types.OperationSubType
	childStepID  string
}

func newWaitForConditionRunner[TState any](
	d types.DurableContext,
	name string,
	checkFn func(ctx context.Context, sc types.StepContext, state TState) (TState, error),
	initialState TState,
	opts []WaitForConditionOption[TState],
) *WaitForConditionRunner[TState] {
	r := &WaitForConditionRunner[TState]{
		d:            d,
		name:         name,
		namePtr:      stringPtr(name),
		checkFn:      checkFn,
		initialState: initialState,
		serdes:       utils.DefaultSerdes,
		subType:      types.OperationSubTypeWaitForCondition,
		childStepID:  d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// WaitForCondition — public entry point
// ---------------------------------------------------------------------------

// WaitForCondition polls checkFn until the wait strategy says to stop.
// initialState is the required starting value for the condition state machine;
// all other settings are provided via functional options.
func WaitForCondition[TState any](
	dc types.DurableContext,
	name string,
	checkFn func(ctx context.Context, sc types.StepContext, state TState) (TState, error),
	initialState TState,
	opts ...WaitForConditionOption[TState],
) (TState, error) {
	r := newWaitForConditionRunner[TState](dc, name, checkFn, initialState, opts)

	stored := r.d.GetStepData(r.childStepID)

	if err := durableCtx.ValidateReplayConsistency(r.childStepID, types.OperationTypeStep, r.namePtr, &r.subType, stored); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		var zero TState
		return zero, err
	}

	switch {
	case stored != nil && stored.Status == types.OperationStatusSucceeded:
		return r.replaySucceeded(stored)
	case stored != nil && stored.Status == types.OperationStatusFailed:
		return r.replayFailed(stored)
	default:
		return r.poll()
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *WaitForConditionRunner[TState]) replaySucceeded(stored *types.Operation) (TState, error) {
	var resultPtr *string
	if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	return utils.SafeDeserialize[TState](r.serdes, resultPtr, r.childStepID, r.d.DurableExecutionArn())
}

func (r *WaitForConditionRunner[TState]) replayFailed(stored *types.Operation) (TState, error) {
	var zero TState
	var cause error
	if stored.Error != nil {
		cause = utils.ErrorFromErrorObject(stored.Error)
	}
	return zero, durableErrors.NewWaitConditionError(r.childStepID, r.namePtr, cause)
}

func (r *WaitForConditionRunner[TState]) poll() (TState, error) {
	var zero TState

	state := r.recoverState()
	// Build a StepContext from the durable context
	sc := durableCtx.NewStepContext(r.d)

	for attempt := 1; ; attempt++ {
		newState, err := r.checkFn(r.d.Context(), sc, state)
		if err != nil {
			return zero, r.checkpointFailed(err)
		}
		state = newState

		if done := r.applyWaitStrategy(state, attempt); done {
			break
		}
	}

	return r.checkpointSucceeded(state)
}

func (r *WaitForConditionRunner[TState]) recoverState() TState {
	stored := r.d.GetStepData(r.childStepID)
	if stored == nil || stored.StepDetails == nil || stored.StepDetails.Result == nil {
		return r.initialState
	}
	recovered, err := utils.SafeDeserialize[TState](r.serdes, stored.StepDetails.Result, r.childStepID, r.d.DurableExecutionArn())
	if err != nil {
		return r.initialState
	}
	return recovered
}

// ---------------------------------------------------------------------------
// Poll loop helpers
// ---------------------------------------------------------------------------

func (r *WaitForConditionRunner[TState]) applyWaitStrategy(state TState, attempt int) (done bool) {
	if r.waitStrategy == nil {
		return true
	}

	result := r.waitStrategy(state, attempt)
	if !result.ShouldContinue {
		return true
	}

	if result.Delay == nil {
		time.Sleep(5 * time.Second)
		return false
	}

	r.suspendForRetry(result.Delay, state)
	return false
}

func (r *WaitForConditionRunner[TState]) suspendForRetry(delay *types.Duration, state TState) {
	waitSeconds := int32(delay.ToSeconds())

	serialized, serErr := utils.SafeSerialize[TState](r.serdes, state, r.childStepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize state for condition %q retry: %v", r.name, serErr),
		})
		select {}
	}
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}

	if cpErr := r.d.Checkpoint(r.childStepID, types.OperationUpdate{
		Id:      r.childStepID,
		Action:  types.OperationActionRetry,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Payload: payloadPtr,
		StepOptions: &types.StepOptions{
			NextAttemptDelaySeconds: &waitSeconds,
		},
	}); cpErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   cpErr,
			Message: fmt.Sprintf("failed to checkpoint RETRY for condition %q: %v", r.name, cpErr),
		})
		select {}
	}

	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for condition %q: retrying after %d seconds", r.name, waitSeconds),
	})
	select {}
}

// ---------------------------------------------------------------------------
// Checkpointing
// ---------------------------------------------------------------------------

func (r *WaitForConditionRunner[TState]) checkpointSucceeded(state TState) (TState, error) {
	var zero TState

	serialized, serErr := utils.SafeSerialize[TState](r.serdes, state, r.childStepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize condition state for %q: %v", r.name, serErr),
		})
		return zero, &durableErrors.SerdesFailedError{Message: serErr.Error()}
	}
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}

	if err := r.d.Checkpoint(r.childStepID, types.OperationUpdate{
		Id:      r.childStepID,
		Action:  types.OperationActionSucceed,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Payload: payloadPtr,
	}); err != nil {
		return zero, err
	}

	return state, nil
}

func (r *WaitForConditionRunner[TState]) checkpointFailed(err error) error {
	if cpErr := r.d.Checkpoint(r.childStepID, types.OperationUpdate{
		Id:      r.childStepID,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Error:   utils.SafeStringify(err),
	}); cpErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   cpErr,
			Message: fmt.Sprintf("failed to checkpoint FAIL for condition %q: %v", r.name, cpErr),
		})
		return cpErr
	}
	return durableErrors.NewWaitConditionError(r.childStepID, r.namePtr, err)
}
