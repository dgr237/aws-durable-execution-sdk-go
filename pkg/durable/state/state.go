package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/dgr237/aws-durable-execution-sdk-go/internal/checkpoint"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
)

// ReplayStatus indicates whether execution is in replay mode.
type ReplayStatus string

const (
	ReplayStatusReplaying ReplayStatus = "REPLAYING"
	ReplayStatusRecording ReplayStatus = "RECORDING"
)

// CheckpointedResult represents the result of a checkpointed operation.
type CheckpointedResult struct {
	Operation *client.Operation
	Status    client.OperationStatus
	Result    *string
	Error     *client.ErrorObject
}

// IsSucceeded checks if the operation succeeded.
func (c *CheckpointedResult) IsSucceeded() bool {
	return c.Status == client.OperationStatusSucceeded
}

// IsFailed checks if the operation failed.
func (c *CheckpointedResult) IsFailed() bool {
	return c.Status == client.OperationStatusFailed
}

// IsPending checks if the operation is pending.
func (c *CheckpointedResult) IsPending() bool {
	return c.Status == client.OperationStatusPending
}

// IsReady checks if the operation is ready.
func (c *CheckpointedResult) IsReady() bool {
	return c.Status == client.OperationStatusReady
}

// IsStarted checks if the operation is started.
func (c *CheckpointedResult) IsStarted() bool {
	return c.Status == client.OperationStatusStarted
}

// CreateCheckpointedResultFromOperation creates a result from an operation.
func CreateCheckpointedResultFromOperation(op client.Operation) CheckpointedResult {
	var result *string
	var errorObj *client.ErrorObject

	switch op.Type {
	case client.OperationTypeStep:
		if op.StepDetails != nil {
			result = op.StepDetails.Result
			errorObj = op.StepDetails.Error
		}
	case client.OperationTypeCallback:
		if op.CallbackDetails != nil {
			result = op.CallbackDetails.Result
			errorObj = op.CallbackDetails.Error
		}
	case client.OperationTypeChainedInvoke:
		if op.ChainedInvokeDetails != nil {
			result = op.ChainedInvokeDetails.Result
			errorObj = op.ChainedInvokeDetails.Error
		}
	case client.OperationTypeContext:
		if op.ContextDetails != nil {
			result = op.ContextDetails.Result
			errorObj = op.ContextDetails.Error
		}
	}

	return CheckpointedResult{
		Operation: &op,
		Status:    op.Status,
		Result:    result,
		Error:     errorObj,
	}
}

// ExecutionState manages the state of a durable execution.
type ExecutionState struct {
	mu                  sync.RWMutex
	operations          map[string]client.Operation
	client              client.LambdaServiceClient
	replayStatus        ReplayStatus
	checkpointQueue     chan client.OperationUpdate
	checkpointWaitGroup sync.WaitGroup
	closed              bool
}

// NewExecutionState creates a new execution state.
func NewExecutionState(ctx context.Context, lambdaClient client.LambdaServiceClient, initialState client.InitialExecutionState) *ExecutionState {
	operations := make(map[string]client.Operation)
	for _, op := range initialState.Operations {
		if op.Id != nil {
			operations[*op.Id] = op
		}
	}

	// Determine replay status
	replayStatus := ReplayStatusRecording
	if len(operations) > 0 {
		replayStatus = ReplayStatusReplaying
	}

	state := &ExecutionState{
		operations:      operations,
		client:          lambdaClient,
		replayStatus:    replayStatus,
		checkpointQueue: make(chan client.OperationUpdate, 100),
		closed:          false,
	}

	// Start checkpoint processor with Lambda context
	state.checkpointWaitGroup.Add(1)
	go state.processCheckpoints(ctx)

	return state
}

// GetCheckpointResult retrieves the checkpointed result for an operation.
func (s *ExecutionState) GetCheckpointResult(operationID string) CheckpointedResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if op, exists := s.operations[operationID]; exists {
		return CreateCheckpointedResultFromOperation(op)
	}

	// No checkpoint exists
	return CheckpointedResult{
		Operation: nil,
		Status:    "",
		Result:    nil,
		Error:     nil,
	}
}

// Checkpoint adds an operation update to the checkpoint queue.
func (s *ExecutionState) Checkpoint(update client.OperationUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("execution state is closed")
	}

	// Add to queue for async processing
	s.checkpointQueue <- update

	// Update local state immediately for replay purposes
	s.updateLocalState(update)

	return nil
}

// CheckpointSync performs a synchronous checkpoint.
// The context parameter allows callers to pass their current context,
// respecting any cancellation signals or timeouts.
func (s *ExecutionState) CheckpointSync(ctx context.Context, update client.OperationUpdate) error {
	s.mu.Lock()
	s.updateLocalState(update)
	s.mu.Unlock()

	// Send directly to Lambda service
	output, err := s.client.CheckpointDurableExecution(ctx, []client.OperationUpdate{update})
	if err != nil {
		return fmt.Errorf("failed to checkpoint: %w", err)
	}

	_ = output
	return nil
}

// updateLocalState updates the local operation state (must be called with lock held).
func (s *ExecutionState) updateLocalState(update client.OperationUpdate) {
	id := aws.ToString(update.Id)
	if existing, exists := s.operations[id]; exists {
		// Update existing operation
		updated := s.applyUpdate(existing, update)
		s.operations[id] = updated
	} else {
		// Create new operation from update
		op := s.createOperationFromUpdate(update)
		s.operations[id] = op
	}
}

// applyUpdate applies an update to an existing operation.
func (s *ExecutionState) applyUpdate(op client.Operation, update client.OperationUpdate) client.Operation {
	// Update status based on action
	switch update.Action {
	case client.OperationActionStart:
		op.Status = client.OperationStatusStarted
	case client.OperationActionSucceed:
		op.Status = client.OperationStatusSucceeded
	case client.OperationActionFail:
		op.Status = client.OperationStatusFailed
	case client.OperationActionRetry:
		op.Status = client.OperationStatusPending
	case client.OperationActionCancel:
		op.Status = client.OperationStatusCancelled
	}

	// Map Payload/Error to the appropriate Details field
	if update.Payload != nil {
		switch update.Type {
		case client.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &client.StepDetails{}
			}
			op.StepDetails.Result = update.Payload
		case client.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &client.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Result = update.Payload
		case client.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &client.ContextDetails{}
			}
			op.ContextDetails.Result = update.Payload
		case client.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &client.CallbackDetails{}
			}
			op.CallbackDetails.Result = update.Payload
		}
	}
	if update.Error != nil {
		switch update.Type {
		case client.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &client.StepDetails{}
			}
			op.StepDetails.Error = update.Error
		case client.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &client.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Error = update.Error
		case client.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &client.ContextDetails{}
			}
			op.ContextDetails.Error = update.Error
		case client.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &client.CallbackDetails{}
			}
			op.CallbackDetails.Error = update.Error
		}
	}
	if update.WaitOptions != nil && update.WaitOptions.WaitSeconds != nil {
		t := time.Now().Add(time.Duration(*update.WaitOptions.WaitSeconds) * time.Second)
		op.WaitDetails = &client.WaitDetails{ScheduledEndTimestamp: &t}
	}

	return op
}

// createOperationFromUpdate creates a new operation from an update.
func (s *ExecutionState) createOperationFromUpdate(update client.OperationUpdate) client.Operation {
	var status client.OperationStatus
	switch update.Action {
	case client.OperationActionStart:
		status = client.OperationStatusStarted
	case client.OperationActionSucceed:
		status = client.OperationStatusSucceeded
	case client.OperationActionFail:
		status = client.OperationStatusFailed
	case client.OperationActionRetry:
		status = client.OperationStatusPending
	case client.OperationActionCancel:
		status = client.OperationStatusCancelled
	default:
		status = client.OperationStatusStarted
	}

	op := client.Operation{
		Id:       update.Id,
		Name:     update.Name,
		Type:     update.Type,
		Status:   status,
		SubType:  update.SubType,
		ParentId: update.ParentId,
	}

	// Map Payload/Error to the appropriate Details field
	if update.Payload != nil {
		switch update.Type {
		case client.OperationTypeStep:
			op.StepDetails = &client.StepDetails{Result: update.Payload}
		case client.OperationTypeChainedInvoke:
			op.ChainedInvokeDetails = &client.ChainedInvokeDetails{Result: update.Payload}
		case client.OperationTypeContext:
			op.ContextDetails = &client.ContextDetails{Result: update.Payload}
		case client.OperationTypeCallback:
			op.CallbackDetails = &client.CallbackDetails{Result: update.Payload}
		}
	}
	if update.Error != nil {
		switch update.Type {
		case client.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &client.StepDetails{}
			}
			op.StepDetails.Error = update.Error
		case client.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &client.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Error = update.Error
		case client.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &client.ContextDetails{}
			}
			op.ContextDetails.Error = update.Error
		case client.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &client.CallbackDetails{}
			}
			op.CallbackDetails.Error = update.Error
		}
	}
	if update.WaitOptions != nil && update.WaitOptions.WaitSeconds != nil {
		t := time.Now().Add(time.Duration(*update.WaitOptions.WaitSeconds) * time.Second)
		op.WaitDetails = &client.WaitDetails{ScheduledEndTimestamp: &t}
	}

	return op
}

// processCheckpoints processes the checkpoint queue in the background.
func (s *ExecutionState) processCheckpoints(ctx context.Context) {
	defer s.checkpointWaitGroup.Done()

	batch := make([]client.OperationUpdate, 0, 10)
	ticker := checkpoint.NewTicker()
	defer ticker.Stop()

	for {
		select {
		case update, ok := <-s.checkpointQueue:
			if !ok {
				// Channel closed, flush remaining batch
				if len(batch) > 0 {
					s.flushBatch(ctx, batch)
				}
				return
			}

			batch = append(batch, update)

			// Flush if batch is full
			if len(batch) >= 10 {
				s.flushBatch(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C():
			// Flush batch on timeout
			if len(batch) > 0 {
				s.flushBatch(ctx, batch)
				batch = batch[:0]
			}
		}
	}
}

// flushBatch sends a batch of updates to Lambda.
func (s *ExecutionState) flushBatch(ctx context.Context, batch []client.OperationUpdate) {
	if len(batch) == 0 {
		return
	}

	_, err := s.client.CheckpointDurableExecution(ctx, batch)
	if err != nil {
		// Log error but don't fail - checkpoints are best-effort in async mode
		fmt.Printf("Warning: failed to checkpoint batch: %v\n", err)
	}
}

// Close closes the execution state and flushes pending checkpoints.
func (s *ExecutionState) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.checkpointQueue)
	s.mu.Unlock()

	// Wait for checkpoint processor to finish
	s.checkpointWaitGroup.Wait()

	return nil
}

// IsReplaying checks if execution is in replay mode.
func (s *ExecutionState) IsReplaying() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.replayStatus == ReplayStatusReplaying
}

// TransitionToRecording transitions from replay to recording mode.
func (s *ExecutionState) TransitionToRecording() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replayStatus = ReplayStatusRecording
}
