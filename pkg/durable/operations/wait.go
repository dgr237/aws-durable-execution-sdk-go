package operations

import (
	"fmt"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ---------------------------------------------------------------------------
// WaitRunner
// ---------------------------------------------------------------------------

// WaitRunner holds all inputs for a Wait operation and exposes each execution
// phase as a method, eliminating repetitive parameter threading.
type WaitRunner struct {
	d        types.DurableContext
	name     string
	namePtr  *string
	duration types.Duration
	subType  types.OperationSubType
	stepID   string
}

func newWaitRunner(d types.DurableContext, name string, duration types.Duration) *WaitRunner {
	return &WaitRunner{
		d:        d,
		name:     name,
		namePtr:  stringPtr(name),
		duration: duration,
		subType:  types.OperationSubTypeWait,
		stepID:   d.NextStepID(),
	}
}

// ---------------------------------------------------------------------------
// Wait — public entry point
// ---------------------------------------------------------------------------

func Wait(dc types.DurableContext, name string, duration types.Duration) error {
	r := newWaitRunner(dc, name, duration)

	stored := r.d.GetStepData(r.stepID)

	if err := durableCtx.ValidateReplayConsistency(r.stepID, types.OperationTypeWait, r.namePtr, &r.subType, stored); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		return err
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		return r.replaySucceeded()
	}

	return r.startFresh()
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

// replaySucceeded marks the wait complete and returns.
func (r *WaitRunner) replaySucceeded() error {
	r.markCompleted()
	return nil
}

// startFresh checkpoints the wait START then suspends until the timer elapses.
func (r *WaitRunner) startFresh() error {
	waitSeconds := int32(r.duration.ToSeconds())

	if err := r.d.Checkpoint(r.stepID, types.OperationUpdate{
		Id:      r.stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeWait,
		SubType: &r.subType,
		Name:    r.namePtr,
		WaitOptions: &types.WaitOptions{
			WaitSeconds: &waitSeconds,
		},
	}); err != nil {
		return err
	}

	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for %d seconds", waitSeconds),
	})

	select {} // Block until termination propagates
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// markCompleted calls MarkOperationState for a completed wait.
func (r *WaitRunner) markCompleted() {
	r.d.MarkOperationState(r.stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
		StepId:  r.stepID,
		Name:    r.namePtr,
		Type:    types.OperationTypeWait,
		SubType: &r.subType,
	})
}
