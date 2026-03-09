package context

import (
	"context"
	"sync"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// Context keys
// ---------------------------------------------------------------------------

type durableContextKey struct{}
type stepContextKey struct{}

// WithDurableContext returns a new context.Context carrying dc.
func WithDurableContext(ctx context.Context, dc types.DurableContext) context.Context {
	return context.WithValue(ctx, durableContextKey{}, dc)
}

// GetDurableContext retrieves the DurableContext stored in ctx.
// Returns nil if none is present.
func GetDurableContext(ctx context.Context) types.DurableContext {
	dc, _ := ctx.Value(durableContextKey{}).(types.DurableContext)
	return dc
}

// WithStepContext returns a new context.Context carrying sc.
func WithStepContext(ctx context.Context, sc types.StepContext) context.Context {
	return context.WithValue(ctx, stepContextKey{}, sc)
}

// GetStepContext retrieves the StepContext stored in ctx.
// Returns nil if none is present.
func GetStepContext(ctx context.Context) types.StepContext {
	sc, _ := ctx.Value(stepContextKey{}).(types.StepContext)
	return sc
}

// NewStepContextFrom creates a step context from the DurableContext in goCtx and
// returns a new context.Context carrying it.
func NewStepContextFrom(goCtx context.Context) context.Context {
	dc := GetDurableContext(goCtx)
	if dc == nil {
		return goCtx
	}
	sc := &StepContext{
		logger: dc.Logger(),
	}
	return WithStepContext(goCtx, sc)
}

// ---------------------------------------------------------------------------
// DurableContext implementation
// ---------------------------------------------------------------------------

var _ types.DurableContext = (*DurableContext)(nil)

// DurableContext is the concrete implementation of types.DurableContext.
type DurableContext struct {
	mu            sync.Mutex
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

// NewChildDurableContext creates a child DurableContext and embeds it into a new
// context.Context derived from goCtx.
func (d *DurableContext) NewChildDurableContext(goCtx context.Context, prefix string, parentID string, mode types.DurableExecutionMode) context.Context {
	child := &DurableContext{
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
	return WithDurableContext(goCtx, child)
}

func (d *DurableContext) IsTerminated() bool {
	return d.execCtx.TerminationManager.IsTerminated()
}

func (d *DurableContext) Terminate(result types.TerminationResult) {
	d.execCtx.TerminationManager.Terminate(result)
}

// ---------------------------------------------------------------------------
// StepContext implementation
// ---------------------------------------------------------------------------

// StepContext is a lightweight context passed to step functions.
type StepContext struct {
	logger types.Logger
}

func (s *StepContext) Logger() types.Logger { return s.logger }

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// NewDurableContext creates a new DurableContext and embeds it into the provided goCtx.
func NewDurableContext(
	goCtx context.Context,
	execCtx *checkpoint.ExecutionContext,
	lambdaCtx *types.LambdaContext,
	checkpointMgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) context.Context {
	dc := &DurableContext{
		execCtx:       execCtx,
		lambdaCtx:     lambdaCtx,
		checkpointMgr: checkpointMgr,
		mode:          mode,
		logger:        logger,
		rawLogger:     logger,
		modeAware:     true,
	}
	return WithDurableContext(goCtx, dc)
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

func (d *DurableContext) Checkpoint(ctx context.Context, stepID string, update types.OperationUpdate) error {
	return d.checkpointMgr.Checkpoint(ctx, stepID, update)
}

func (d *DurableContext) CheckpointBatch(ctx context.Context, batch []types.OperationUpdate) error {
	return d.checkpointMgr.CheckpointBatch(ctx, batch)
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
