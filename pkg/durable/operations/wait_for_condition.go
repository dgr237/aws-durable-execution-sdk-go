package operations

import (
	"fmt"
	"time"

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
	checkFn      func(state TState, ctx types.StepContext) (TState, error)
	initialState TState
	serdes       types.Serdes
	waitStrategy func(state TState, attempt int) types.WaitStrategyResult
	subType      types.OperationSubType
	childStepID  string
}

func newWaitForConditionRunner[TState any](
	d types.DurableContext,
	name string,
	checkFn func(state TState, ctx types.StepContext) (TState, error),
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
	d types.DurableContext,
	name string,
	checkFn func(state TState, ctx types.StepContext) (TState, error),
	initialState TState,
	opts ...WaitForConditionOption[TState],
) (TState, error) {
	r := newWaitForConditionRunner[TState](d, name, checkFn, initialState, opts)

	stored := r.d.GetStepData(r.childStepID)

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

// poll runs the condition check in a loop, sleeping between attempts until
// the strategy says to stop or a check fails.
func (r *WaitForConditionRunner[TState]) poll() (TState, error) {
	var zero TState
	state := r.initialState
	stepCtx := r.d.NewStepContext()

	for attempt := 1; ; attempt++ {
		newState, err := r.checkFn(state, stepCtx)
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

// ---------------------------------------------------------------------------
// Poll loop helpers
// ---------------------------------------------------------------------------

// applyWaitStrategy consults the waitStrategy (if set) and either returns
// true (done — exit the loop) or sleeps/suspends before the next poll.
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

	r.suspendForDelay(result.Delay, attempt)
	return false // unreachable after suspend, but satisfies the compiler
}

func (r *WaitForConditionRunner[TState]) suspendForDelay(delay *types.Duration, attempt int) {
	waitSeconds := int32(delay.ToSeconds())
	innerStepID := fmt.Sprintf("%s-wait-%d", r.childStepID, attempt)

	_ = r.d.Checkpoint(innerStepID, types.OperationUpdate{
		Id:      innerStepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		WaitOptions: &types.WaitOptions{
			WaitSeconds: &waitSeconds,
		},
	})

	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for condition: poll %d", attempt),
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
		return zero, serErr
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
	_ = r.d.Checkpoint(r.childStepID, types.OperationUpdate{
		Id:      r.childStepID,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: &r.subType,
		Name:    r.namePtr,
		Error:   utils.SafeStringify(err),
	})
	return durableErrors.NewWaitConditionError(r.childStepID, r.namePtr, err)
}
