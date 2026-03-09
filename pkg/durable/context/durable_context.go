package context

import (
	"context"
	"sync"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

var _ types.DurableContext = (*DurableContext)(nil)

// DurableContext is the concrete implementation of types.DurableContext.
type DurableContext struct {
	mu            sync.Mutex
	ctx           context.Context
	execCtx       *checkpoint.ExecutionContext
	lambdaCtx     *types.LambdaContext
	checkpointMgr *checkpoint.Manager
	mode          types.DurableExecutionMode
	stepCounter   int
	stepPrefix    string
	parentID      string
	logger        types.Logger
	modeAware     bool
	rawLogger     types.Logger // user-supplied logger (before mode-awareness wrapping)
}

func (d *DurableContext) GetStepData(stepID string) *types.Operation {
	return d.execCtx.GetStepData(stepID)
}

func (d *DurableContext) DurableExecutionArn() string {
	return d.execCtx.DurableExecutionArn
}

func (d *DurableContext) NewStepContext() types.StepContext {
	return &StepContext{
		ctx:    d.ctx,
		logger: d.logger,
	}
}

func (d *DurableContext) IsTerminated() bool {
	return d.execCtx.TerminationManager.IsTerminated()
}

func (d *DurableContext) Terminate(result types.TerminationResult) {
	d.execCtx.TerminationManager.Terminate(result)
}

// StepContext is a lightweight context passed to step functions.
type StepContext struct {
	ctx    context.Context
	logger types.Logger
}

func (s *StepContext) Logger() types.Logger { return s.logger }

func (s *StepContext) Context() context.Context { return s.ctx }

// NewDurableContext creates a new DurableContext for the root execution.
func NewDurableContext(
	execCtx *checkpoint.ExecutionContext,
	lambdaCtx *types.LambdaContext,
	checkpointMgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) types.DurableContext {
	return &DurableContext{
		execCtx:       execCtx,
		lambdaCtx:     lambdaCtx,
		checkpointMgr: checkpointMgr,
		mode:          mode,
		logger:        logger,
		rawLogger:     logger,
		modeAware:     true,
	}
}

// NewChildDurableContext creates a child DurableContext with its own step namespace.
func (d *DurableContext) NewChildDurableContext(prefix string, parentID string, mode types.DurableExecutionMode) types.DurableContext {
	return &DurableContext{
		execCtx:       d.execCtx,
		lambdaCtx:     d.lambdaCtx,
		checkpointMgr: d.checkpointMgr,
		mode:          mode,
		stepPrefix:    prefix,
		parentID:      parentID,
		logger:        d.logger,
		rawLogger:     d.rawLogger,
		modeAware:     d.modeAware,
	}
}

func (d *DurableContext) NextStepID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stepCounter++
	return CreateStepID(d.stepPrefix, d.stepCounter)
}

func (d *DurableContext) IsExecutionMode() bool {
	return d.mode == types.ExecutionMode
}

func (d *DurableContext) Context() context.Context { return d.ctx }

// LambdaCtx returns the underlying Lambda context.
func (d *DurableContext) LambdaCtx() *types.LambdaContext { return d.lambdaCtx }

// Logger returns a mode-aware logger (silent during replay when modeAware is true).
func (d *DurableContext) Logger() types.Logger {
	if (d.modeAware && !d.IsExecutionMode()) || d.logger == nil {
		return utils.NopLogger{}
	}
	return d.logger
}

func (d *DurableContext) ParentID() string { return d.parentID }

func (d *DurableContext) ExecutionContext() *checkpoint.ExecutionContext { return d.execCtx }

func (d *DurableContext) Checkpoint(stepID string, update types.OperationUpdate) error {
	return d.checkpointMgr.Checkpoint(stepID, update)
}

func (d *DurableContext) CheckpointBatch(batch []types.OperationUpdate) error {
	return d.checkpointMgr.CheckpointBatch(batch)
}

func (d *DurableContext) Mode() types.DurableExecutionMode { return d.mode }

func (d *DurableContext) MarkAncestorFinished(stepID string) {
	d.checkpointMgr.MarkAncestorFinished(stepID)
}

func (d *DurableContext) MarkOperationState(stepID string, state types.OperationLifecycleState, metadata types.OperationMetadata) {
	d.checkpointMgr.MarkOperationState(stepID, state, metadata)
}

// ConfigureLogger updates the logger configuration.
func (d *DurableContext) ConfigureLogger(cfg types.LoggerConfig) {
	if cfg.CustomLogger != nil {
		d.rawLogger = cfg.CustomLogger
		d.logger = cfg.CustomLogger
	}
	if cfg.ModeAware != nil {
		d.modeAware = *cfg.ModeAware
	}
}
