// Package checkpoint provides the checkpoint manager and termination manager
// for the AWS Durable Execution SDK.
package checkpoint

import (
	"sync"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// TerminationManager coordinates the termination lifecycle of a durable execution.
// It signals when the execution should stop (e.g., due to a checkpoint failure or
// a deliberate wait/suspend operation) and provides a channel for the main handler
// goroutine to race against.
type TerminationManager struct {
	mu              sync.Mutex
	terminationCh   chan types.TerminationResult
	terminateCalled bool
	callbacks       []func()
}

// NewTerminationManager creates a new TerminationManager.
func NewTerminationManager() *TerminationManager {
	return &TerminationManager{
		terminationCh: make(chan types.TerminationResult, 1),
	}
}

// TerminationChannel returns the channel that receives a result when execution should terminate.
func (tm *TerminationManager) TerminationChannel() <-chan types.TerminationResult {
	return tm.terminationCh
}

// Terminate signals that the execution should stop with the given result.
// Only the first call has any effect; subsequent calls are no-ops.
func (tm *TerminationManager) Terminate(result types.TerminationResult) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.terminateCalled {
		return
	}
	tm.terminateCalled = true

	// Run any registered callbacks (e.g. checkpoint manager setTerminating)
	for _, cb := range tm.callbacks {
		cb()
	}

	// Non-blocking send; the channel is buffered with size 1
	select {
	case tm.terminationCh <- result:
	default:
	}
}

// RegisterTerminationCallback registers a function to be called when Terminate() is invoked.
func (tm *TerminationManager) RegisterTerminationCallback(cb func()) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.callbacks = append(tm.callbacks, cb)
}

// IsTerminated returns true if termination has been requested.
func (tm *TerminationManager) IsTerminated() bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.terminateCalled
}
