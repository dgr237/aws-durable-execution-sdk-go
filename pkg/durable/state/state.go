package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
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
	Operation *types.Operation
	Status    types.OperationStatus
	Result    *string
	Error     *types.ErrorObject
}

// IsSucceeded checks if the operation succeeded.
func (c *CheckpointedResult) IsSucceeded() bool {
	return c.Status == types.OperationStatusSucceeded
}

// IsFailed checks if the operation failed.
func (c *CheckpointedResult) IsFailed() bool {
	return c.Status == types.OperationStatusFailed
}

// IsPending checks if the operation is pending.
func (c *CheckpointedResult) IsPending() bool {
	return c.Status == types.OperationStatusPending
}

// IsReady checks if the operation is ready.
func (c *CheckpointedResult) IsReady() bool {
	return c.Status == types.OperationStatusReady
}

// IsStarted checks if the operation is started.
func (c *CheckpointedResult) IsStarted() bool {
	return c.Status == types.OperationStatusStarted
}

// CreateCheckpointedResultFromOperation creates a result from an operation.
func CreateCheckpointedResultFromOperation(op types.Operation) CheckpointedResult {
	var result *string
	var errorObj *types.ErrorObject

	switch op.Type {
	case types.OperationTypeStep:
		if op.StepDetails != nil {
			result = op.StepDetails.Result
			errorObj = op.StepDetails.Error
		}
	case types.OperationTypeCallback:
		if op.CallbackDetails != nil {
			result = op.CallbackDetails.Result
			errorObj = op.CallbackDetails.Error
		}
	case types.OperationTypeChainedInvoke:
		if op.ChainedInvokeDetails != nil {
			result = op.ChainedInvokeDetails.Result
			errorObj = op.ChainedInvokeDetails.Error
		}
	case types.OperationTypeContext:
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
	operations          map[string]types.Operation
	client              client.LambdaServiceClient
	replayStatus        ReplayStatus
	checkpointQueue     chan types.OperationUpdate
	checkpointWaitGroup sync.WaitGroup
	closed              bool
}

// NewExecutionState creates a new execution state.
func NewExecutionState(ctx context.Context, lambdaClient client.LambdaServiceClient, initialState client.InitialExecutionState) *ExecutionState {
	operations := make(map[string]types.Operation)
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
		checkpointQueue: make(chan types.OperationUpdate, 100),
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
func (s *ExecutionState) Checkpoint(update types.OperationUpdate) error {
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
func (s *ExecutionState) CheckpointSync(ctx context.Context, update types.OperationUpdate) error {
	s.mu.Lock()
	s.updateLocalState(update)
	s.mu.Unlock()

	// Send directly to Lambda service
	output, err := s.client.CheckpointDurableExecution(ctx, []types.OperationUpdate{update})
	if err != nil {
		return fmt.Errorf("failed to checkpoint: %w", err)
	}

	_ = output
	return nil
}

// updateLocalState updates the local operation state (must be called with lock held).
func (s *ExecutionState) updateLocalState(update types.OperationUpdate) {
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
func (s *ExecutionState) applyUpdate(op types.Operation, update types.OperationUpdate) types.Operation {
	// Update status based on action
	switch update.Action {
	case types.OperationActionStart:
		op.Status = types.OperationStatusStarted
	case types.OperationActionSucceed:
		op.Status = types.OperationStatusSucceeded
	case types.OperationActionFail:
		op.Status = types.OperationStatusFailed
	case types.OperationActionRetry:
		op.Status = types.OperationStatusPending
	case types.OperationActionCancel:
		op.Status = types.OperationStatusCancelled
	}

	// Map Payload/Error to the appropriate Details field
	if update.Payload != nil {
		switch update.Type {
		case types.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &types.StepDetails{}
			}
			op.StepDetails.Result = update.Payload
		case types.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &types.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Result = update.Payload
		case types.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &types.ContextDetails{}
			}
			op.ContextDetails.Result = update.Payload
		case types.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &types.CallbackDetails{}
			}
			op.CallbackDetails.Result = update.Payload
		}
	}
	if update.Error != nil {
		switch update.Type {
		case types.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &types.StepDetails{}
			}
			op.StepDetails.Error = update.Error
		case types.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &types.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Error = update.Error
		case types.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &types.ContextDetails{}
			}
			op.ContextDetails.Error = update.Error
		case types.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &types.CallbackDetails{}
			}
			op.CallbackDetails.Error = update.Error
		}
	}
	if update.WaitOptions != nil && update.WaitOptions.WaitSeconds != nil {
		t := time.Now().Add(time.Duration(*update.WaitOptions.WaitSeconds) * time.Second)
		op.WaitDetails = &types.WaitDetails{ScheduledEndTimestamp: &t}
	}

	return op
}

// createOperationFromUpdate creates a new operation from an update.
func (s *ExecutionState) createOperationFromUpdate(update types.OperationUpdate) types.Operation {
	var status types.OperationStatus
	switch update.Action {
	case types.OperationActionStart:
		status = types.OperationStatusStarted
	case types.OperationActionSucceed:
		status = types.OperationStatusSucceeded
	case types.OperationActionFail:
		status = types.OperationStatusFailed
	case types.OperationActionRetry:
		status = types.OperationStatusPending
	case types.OperationActionCancel:
		status = types.OperationStatusCancelled
	default:
		status = types.OperationStatusStarted
	}

	op := types.Operation{
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
		case types.OperationTypeStep:
			op.StepDetails = &types.StepDetails{Result: update.Payload}
		case types.OperationTypeChainedInvoke:
			op.ChainedInvokeDetails = &types.ChainedInvokeDetails{Result: update.Payload}
		case types.OperationTypeContext:
			op.ContextDetails = &types.ContextDetails{Result: update.Payload}
		case types.OperationTypeCallback:
			op.CallbackDetails = &types.CallbackDetails{Result: update.Payload}
		}
	}
	if update.Error != nil {
		switch update.Type {
		case types.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &types.StepDetails{}
			}
			op.StepDetails.Error = update.Error
		case types.OperationTypeChainedInvoke:
			if op.ChainedInvokeDetails == nil {
				op.ChainedInvokeDetails = &types.ChainedInvokeDetails{}
			}
			op.ChainedInvokeDetails.Error = update.Error
		case types.OperationTypeContext:
			if op.ContextDetails == nil {
				op.ContextDetails = &types.ContextDetails{}
			}
			op.ContextDetails.Error = update.Error
		case types.OperationTypeCallback:
			if op.CallbackDetails == nil {
				op.CallbackDetails = &types.CallbackDetails{}
			}
			op.CallbackDetails.Error = update.Error
		}
	}
	if update.WaitOptions != nil && update.WaitOptions.WaitSeconds != nil {
		t := time.Now().Add(time.Duration(*update.WaitOptions.WaitSeconds) * time.Second)
		op.WaitDetails = &types.WaitDetails{ScheduledEndTimestamp: &t}
	}

	return op
}

// processCheckpoints processes the checkpoint queue in the background.
func (s *ExecutionState) processCheckpoints(ctx context.Context) {
	defer s.checkpointWaitGroup.Done()

	batch := make([]types.OperationUpdate, 0, 10)
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
func (s *ExecutionState) flushBatch(ctx context.Context, batch []types.OperationUpdate) {
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
