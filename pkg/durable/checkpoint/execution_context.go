// Package context provides the execution context and durable context implementations.
package checkpoint

import (
	"context"
	"crypto/md5"
	"fmt"

	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// Client is the interface for communicating with the durable execution backend.
type Client interface {
	Checkpoint(ctx context.Context, params types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error)
	GetExecutionState(ctx context.Context, params types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error)
}

// hashStepID hashes a step ID exactly as the JS SDK does (MD5, 16 hex chars).
// AWS stores and returns all operation IDs in hashed form, so lookups must
// also use the hashed form. Mirrors hashId() in step-id-utils.ts.
func hashStepID(id string) string {
	sum := md5.Sum([]byte(id))
	return fmt.Sprintf("%x", sum)[:16]
}

// ExecutionContext holds the shared state for a single durable execution invocation.
// It is created once per invocation and shared by all DurableContext instances.
type ExecutionContext struct {
	// DurableExecutionArn uniquely identifies this durable execution.
	DurableExecutionArn string
	// ExecutionOperationId is the ID of the root EXECUTION operation.
	ExecutionOperationId string
	// RequestID is the Lambda request ID for this invocation.
	RequestID string
	// TenantID is the optional tenant identifier extracted from the ARN.
	TenantID *string
	// Client is the backend client for checkpoint and state operations.
	Client Client
	// TerminationManager coordinates execution termination.
	TerminationManager *TerminationManager
	// StepData holds the operation history indexed by step ID.
	StepData map[string]*types.Operation
	// PendingCompletions tracks step IDs that have sent START but not SUCCEED/FAIL.
	PendingCompletions map[string]bool
}

// GetStepData returns the operation record for the given step ID, or nil if not found.
// AWS stores and returns operation IDs in hashed form (MD5, 16 chars), so the
// lookup key must be hashed to match. Mirrors getStepData() in step-id-utils.ts.
func (ec *ExecutionContext) GetStepData(stepID string) *types.Operation {
	if op, ok := ec.StepData[hashStepID(stepID)]; ok {
		return op
	}
	return nil
}

// ValidateDurableExecutionEvent checks that the event is a valid durable execution input.
func ValidateDurableExecutionEvent(event types.DurableExecutionInvocationInput) error {
	if event.DurableExecutionArn == "" {
		return &durableErrors.UnrecoverableError{
			Cause: fmt.Errorf(
				"unexpected payload provided to start the durable execution.\n" +
					"Check your resource configurations to confirm the durability is set",
			),
		}
	}
	if event.CheckpointToken == "" {
		return &durableErrors.UnrecoverableError{
			Cause: fmt.Errorf("missing CheckpointToken in durable execution input"),
		}
	}
	return nil
}
