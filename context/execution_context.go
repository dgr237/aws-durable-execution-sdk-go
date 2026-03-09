// Package context provides the execution context and durable context implementations.
package context

import (
	"crypto/md5"
	"fmt"

	"github.com/aws/durable-execution-sdk-go/checkpoint"
	durableErrors "github.com/aws/durable-execution-sdk-go/errors"
	"github.com/aws/durable-execution-sdk-go/types"
	"github.com/aws/durable-execution-sdk-go/utils"
)

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
	Client checkpoint.Client
	// TerminationManager coordinates execution termination.
	TerminationManager *checkpoint.TerminationManager
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

// InitializeExecutionContext builds an ExecutionContext from the invocation input by
// loading the full operation history (handling pagination) and resolving the execution mode.
func InitializeExecutionContext(
	event types.DurableExecutionInvocationInput,
	lambdaCtx *types.LambdaContext,
	client checkpoint.Client,
) (*ExecutionContext, types.DurableExecutionMode, string, error) {
	// Collect all operations, handling pagination
	allOps := make([]types.Operation, 0, len(event.InitialExecutionState.Operations))
	allOps = append(allOps, event.InitialExecutionState.Operations...)

	nextMarker := event.InitialExecutionState.NextMarker
	for nextMarker != nil && *nextMarker != "" {
		resp, err := client.GetExecutionState(types.GetDurableExecutionStateRequest{
			DurableExecutionArn: event.DurableExecutionArn,
			CheckpointToken:     event.CheckpointToken,
			NextMarker:          nextMarker,
		})
		if err != nil {
			return nil, "", "", fmt.Errorf("failed to get execution state page: %w", err)
		}
		allOps = append(allOps, resp.Operations...)
		nextMarker = resp.NextMarker
	}

	// Build the step data map indexed by operation ID
	stepData := make(map[string]*types.Operation, len(allOps))
	var executionOperationId string
	for i := range allOps {
		op := allOps[i]
		stepData[op.Id] = &op
		// Find the root EXECUTION operation - this is the parent for all root-level steps
		if op.Type == types.OperationTypeExecution {
			executionOperationId = op.Id
		}
	}

	// Determine the execution mode based on whether there is any step history
	mode := types.ExecutionMode
	if len(allOps) > 1 {
		// If we have operations beyond the initial execution trigger, we're replaying
		mode = types.ReplayMode
	}

	// Log initialization details for debugging
	logger := utils.NewDefaultLogger(event.DurableExecutionArn, lambdaCtx.AwsRequestID, "")
	logger.Info(fmt.Sprintf("Initialized execution context with %d operations in %s mode", len(allOps), mode))
	if len(allOps) > 0 && len(allOps) <= 10 {
		// Log operation IDs for small sets (avoid spam)
		for _, op := range allOps {
			logger.Info(fmt.Sprintf("  Loaded operation: ID=%s, Type=%s, Status=%s", op.Id, op.Type, op.Status))
		}
	}

	ec := &ExecutionContext{
		DurableExecutionArn:  event.DurableExecutionArn,
		ExecutionOperationId: executionOperationId,
		RequestID:            lambdaCtx.AwsRequestID,
		Client:               client,
		TerminationManager:   checkpoint.NewTerminationManager(),
		StepData:             stepData,
		PendingCompletions:   make(map[string]bool),
	}

	return ec, mode, event.CheckpointToken, nil
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
