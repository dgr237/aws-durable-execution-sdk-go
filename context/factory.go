package context

import (
	"github.com/aws/durable-execution-sdk-go/checkpoint"
	"github.com/aws/durable-execution-sdk-go/types"
)

// NewRootContext creates a root-level DurableContext for use by the SDK entrypoint.
func NewRootContext(
	execCtx *ExecutionContext,
	lambdaCtx *types.LambdaContext,
	mgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) *DurableContext {
	return newDurableContext(execCtx, lambdaCtx, mgr, mode, logger)
}
