package checkpoint

import (
	"context"
	"crypto/md5"
	"fmt"
	"sync"

	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// hashID mirrors the JS SDK's hashId function (checkpoint-manager.ts):
//
//	createHash("md5").update(input).digest("hex").substring(0, 16)
//
// All operation IDs and ParentIds must be hashed before being sent to AWS.
// The AWS durable execution service stores and looks up operations by hashed ID,
// so cross-reference via ParentId must also use the hashed form.
func hashID(id string) string {
	sum := md5.Sum([]byte(id))
	return fmt.Sprintf("%x", sum)[:16]
}

const (
	maxPayloadSize  = 750 * 1024 // 750KB
	maxItemsInBatch = 250
)

// Client is the interface for communicating with the durable execution backend.
type Client interface {
	Checkpoint(ctx context.Context, params types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error)
	GetExecutionState(params types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error)
}

type queuedCheckpoint struct {
	stepID string
	data   types.OperationUpdate
	doneCh chan error
}

// Manager manages checkpoint queuing and batching for a single durable execution.
type Manager struct {
	mu                  sync.Mutex
	queue               []queuedCheckpoint
	isProcessing        bool
	isTerminating       bool
	currentToken        string
	durableExecutionArn string
	client              Client
	terminationMgr      *TerminationManager
	logger              types.Logger

	// For coordinating queue-flush waiters
	queueDoneCh chan struct{}

	// finished ancestors for child context pruning
	finishedAncestors map[string]bool

	// operation lifecycle tracking
	operations map[string]types.OperationInfo
}

// NewManager creates a new checkpoint Manager.
func NewManager(
	arn, initialToken string,
	client Client,
	tm *TerminationManager,
	logger types.Logger,
) *Manager {
	return &Manager{
		durableExecutionArn: arn,
		currentToken:        initialToken,
		client:              client,
		terminationMgr:      tm,
		logger:              logger,
		queueDoneCh:         make(chan struct{}, 1),
		finishedAncestors:   make(map[string]bool),
		operations:          make(map[string]types.OperationInfo),
	}
}

// SetTerminating marks the manager as terminating; new checkpoints are dropped.
func (m *Manager) SetTerminating() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isTerminating = true
}

// MarkAncestorFinished records that a child-context parent step has completed.
func (m *Manager) MarkAncestorFinished(stepID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishedAncestors[stepID] = true
}

// MarkOperationState records the lifecycle state of an operation.
func (m *Manager) MarkOperationState(stepID string, state types.OperationLifecycleState, metadata types.OperationMetadata) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operations[stepID] = types.OperationInfo{
		State:    state,
		Metadata: metadata,
	}
}

// CheckpointBatch queues multiple operation updates as a single atomic batch.
// This is critical for parent-child relationships (e.g. Map START + iteration STARTs)
// because AWS resolves ParentId references within the same batch call.
// If sent as separate calls, AWS rejects the child's ParentId because the parent
// was registered in a prior call and AWS does not expose its internal operation IDs.
func (m *Manager) CheckpointBatch(updates []types.OperationUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	m.mu.Lock()
	if m.isTerminating {
		m.mu.Unlock()
		return nil
	}
	// All items in the batch share the same doneCh so the entire batch is sent
	// together in one processQueue iteration. The caller blocks on doneCh until
	// the batch API call completes (success or failure).
	doneCh := make(chan error, len(updates))
	for _, update := range updates {
		m.queue = append(m.queue, queuedCheckpoint{
			stepID: update.Id,
			data:   update,
			doneCh: doneCh,
		})
	}
	needsStart := !m.isProcessing
	if m.logger != nil {
		m.logger.Info(fmt.Sprintf("CheckpointBatch: Queued %d operations atomically, queue size: %d",
			len(updates), len(m.queue)))
	}
	m.mu.Unlock()

	if needsStart {
		go m.processQueue()
	}

	// Wait for any one completion signal - since all share the same doneCh,
	// only the first resolution is needed (the batch was sent as one API call).
	// However, processQueue signals each item individually, so drain len(updates) signals.
	var firstErr error
	for range updates {
		if err := <-doneCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Checkpoint queues a single operation update for persistence.
// It returns once the checkpoint has been sent to the backend (or immediately if terminating).
func (m *Manager) Checkpoint(stepID string, update types.OperationUpdate) error {
	m.mu.Lock()
	if m.isTerminating {
		m.mu.Unlock()
		return nil
	}
	doneCh := make(chan error, 1)
	m.queue = append(m.queue, queuedCheckpoint{
		stepID: stepID,
		data:   update,
		doneCh: doneCh,
	})
	needsStart := !m.isProcessing

	if m.logger != nil {
		m.logger.Info(fmt.Sprintf("Queued checkpoint for step %s (action: %s, type: %s), queue size: %d",
			stepID, update.Action, update.Type, len(m.queue)))
	}

	m.mu.Unlock()

	if needsStart {
		go m.processQueue()
	}

	return <-doneCh
}

// WaitForQueueCompletion blocks until all queued checkpoints have been processed.
func (m *Manager) WaitForQueueCompletion() error {
	for {
		m.mu.Lock()
		empty := len(m.queue) == 0 && !m.isProcessing
		m.mu.Unlock()
		if empty {
			return nil
		}
		// Wait a bit and check again (simple polling; a condition variable would be cleaner)
		// Using a channel signal for efficiency
		<-m.queueDoneCh
	}
}

func (m *Manager) notifyDone() {
	select {
	case m.queueDoneCh <- struct{}{}:
	default:
	}
}

func (m *Manager) processQueue() {
	m.mu.Lock()
	if m.isProcessing {
		m.mu.Unlock()
		return
	}
	m.isProcessing = true
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("processQueue: Started processing checkpoint queue")
	}

	defer func() {
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.notifyDone()
		if m.logger != nil {
			m.logger.Info("processQueue: Finished processing checkpoint queue")
		}
	}()

	for {
		m.mu.Lock()
		if len(m.queue) == 0 {
			m.mu.Unlock()
			return
		}
		// Drain the current queue into a batch
		batch := make([]queuedCheckpoint, len(m.queue))
		copy(batch, m.queue)
		m.queue = nil
		token := m.currentToken
		queueSize := len(batch)
		m.mu.Unlock()

		if m.logger != nil {
			m.logger.Info(fmt.Sprintf("processQueue: Processing batch of %d checkpoints", queueSize))
		}

		// Build the operation update list, hashing all IDs to match the JS SDK.
		// The JS SDK (checkpoint-manager.ts) calls hashId() on every Id and ParentId
		// before the API call. AWS stores and looks up operations by their hashed ID,
		// so ParentId cross-references must also use the hashed form.
		updates := make([]types.OperationUpdate, 0, len(batch))
		for _, item := range batch {
			op := item.data
			op.Id = hashID(op.Id)
			if op.ParentId != nil {
				hashed := hashID(*op.ParentId)
				op.ParentId = &hashed
			}
			updates = append(updates, op)
		}

		if m.logger != nil {
			m.logger.Info("processQueue: About to send checkpoint to service")
			// Log details of each operation being checkpointed
			for i, op := range updates {
				parentStr := "<nil>"
				if op.ParentId != nil {
					parentStr = *op.ParentId
				}
				m.logger.Info(fmt.Sprintf("  Operation %d: ID=%s, Action=%s, Type=%s, ParentId=%s", i, op.Id, op.Action, op.Type, parentStr))
			}
		}

		// Send the checkpoint
		resp, err := m.client.Checkpoint(context.Background(), types.CheckpointDurableExecutionRequest{
			DurableExecutionArn: m.durableExecutionArn,
			CheckpointToken:     token,
			Operations:          updates,
		})

		if m.logger != nil {
			if err != nil {
				m.logger.Info(fmt.Sprintf("processQueue: Checkpoint failed with error: %v", err))
			} else {
				m.logger.Info("processQueue: Checkpoint succeeded")
			}
		}

		if err != nil {
			classified := m.classifyCheckpointError(err)
			// Signal all waiters in this batch
			for _, item := range batch {
				item.doneCh <- classified
			}
			// Signal termination
			m.terminationMgr.Terminate(types.TerminationResult{
				Reason:  types.TerminationReasonCheckpointFailed,
				Error:   classified,
				Message: fmt.Sprintf("checkpoint failed: %v", err),
			})
			return
		}

		// Update the token for the next checkpoint
		if resp != nil && resp.NextCheckpointToken != nil {
			m.mu.Lock()
			m.currentToken = *resp.NextCheckpointToken
			m.mu.Unlock()

			if m.logger != nil {
				m.logger.Info(fmt.Sprintf("Checkpoint succeeded, new token received"))
			}
		} else {
			if m.logger != nil {
				m.logger.Warn("Checkpoint response did not include NextCheckpointToken")
			}
		}

		// Resolve all waiters
		for _, item := range batch {
			item.doneCh <- nil
		}
	}
}

// classifyCheckpointError converts a raw checkpoint error to the appropriate SDK error type.
func (m *Manager) classifyCheckpointError(err error) error {
	if err == nil {
		return nil
	}
	// For now, treat all checkpoint failures as unrecoverable invocation errors
	// (Lambda must restart). In a real implementation, you'd inspect the error code.
	return &durableErrors.CheckpointUnrecoverableInvocationError{Cause: err}
}

// CurrentToken returns the current checkpoint token.
func (m *Manager) CurrentToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentToken
}
