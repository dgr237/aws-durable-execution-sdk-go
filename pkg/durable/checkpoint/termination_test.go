package checkpoint_test

import (
	"testing"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

func TestTerminationManager_TerminatesOnce(t *testing.T) {
	tm := checkpoint.NewTerminationManager()

	tm.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointFailed,
		Message: "first",
	})
	tm.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonSerdesFailed,
		Message: "second",
	})

	select {
	case result := <-tm.TerminationChannel():
		if result.Message != "first" {
			t.Errorf("expected 'first', got %q", result.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("termination channel did not receive")
	}
}

func TestTerminationManager_CallbackFired(t *testing.T) {
	tm := checkpoint.NewTerminationManager()
	fired := false
	tm.RegisterTerminationCallback(func() { fired = true })

	tm.Terminate(types.TerminationResult{Reason: types.TerminationReasonCheckpointTerminating})

	// Drain channel
	<-tm.TerminationChannel()

	if !fired {
		t.Error("expected callback to be fired")
	}
}

func TestTerminationManager_IsTerminated(t *testing.T) {
	tm := checkpoint.NewTerminationManager()
	if tm.IsTerminated() {
		t.Error("should not be terminated initially")
	}
	tm.Terminate(types.TerminationResult{Reason: types.TerminationReasonCheckpointTerminating})
	<-tm.TerminationChannel()
	if !tm.IsTerminated() {
		t.Error("should be terminated after Terminate()")
	}
}
