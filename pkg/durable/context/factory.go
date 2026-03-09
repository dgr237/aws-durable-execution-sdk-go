package context

import (
	"context"
	"fmt"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// NewRootContext creates a root-level context.Context carrying a DurableContext for use by the SDK entrypoint.
func NewRootContext(
	goCtx context.Context,
	execCtx *checkpoint.ExecutionContext,
	lambdaCtx *types.LambdaContext,
	mgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) context.Context {
	return NewDurableContext(goCtx, execCtx, lambdaCtx, mgr, mode, logger)
}

// InitializeExecutionContext builds an ExecutionContext from the invocation input by
// loading the full operation history (handling pagination) and resolving the execution mode.
func InitializeExecutionContext(
	ctx context.Context,
	event types.DurableExecutionInvocationInput,
	lambdaCtx *types.LambdaContext,
	client checkpoint.Client,
) (*checkpoint.ExecutionContext, types.DurableExecutionMode, string, error) {
	// Collect all operations, handling pagination
	allOps := make([]types.Operation, 0, len(event.InitialExecutionState.Operations))
	allOps = append(allOps, event.InitialExecutionState.Operations...)

	nextMarker := event.InitialExecutionState.NextMarker
	for nextMarker != nil && *nextMarker != "" {
		resp, err := client.GetExecutionState(ctx, types.GetDurableExecutionStateRequest{
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

	ec := &checkpoint.ExecutionContext{
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
