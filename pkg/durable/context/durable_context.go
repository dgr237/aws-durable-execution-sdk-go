package context

import (
	"context"
	"sync"
	"time"

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
// Returns (nil, false) if none is present.
func GetDurableContext(ctx context.Context) (types.DurableContext, bool) {
	dc, ok := ctx.Value(durableContextKey{}).(types.DurableContext)
	return dc, ok
}

// WithStepContext returns a new context.Context carrying sc.
func WithStepContext(ctx context.Context, sc types.StepContext) context.Context {
	return context.WithValue(ctx, stepContextKey{}, sc)
}

// GetStepContext retrieves the StepContext stored in ctx.
// Returns (nil, false) if none is present.
func GetStepContext(ctx context.Context) (types.StepContext, bool) {
	sc, ok := ctx.Value(stepContextKey{}).(types.StepContext)
	return sc, ok
}

// NewStepContext creates a StepContext from a DurableContext.
func NewStepContext(dc types.DurableContext) types.StepContext {
	return &StepContext{
		logger: dc.Logger(),
		ctx:    dc.Context(),
	}
}

// NewStepContextFrom creates a step context from the DurableContext in goCtx and
// returns a new context.Context carrying it. Kept for backward compatibility.
func NewStepContextFrom(goCtx context.Context) context.Context {
	dc, ok := GetDurableContext(goCtx)
	if !ok {
		panic("no Durable context set")
	}
	sc := NewStepContext(dc)
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
	goCtx         context.Context
}

func (d *DurableContext) GetStepData(stepID string) *types.Operation {
	return d.execCtx.GetStepData(stepID)
}

func (d *DurableContext) DurableExecutionArn() string {
	return d.execCtx.DurableExecutionArn
}

// ExecutionStartTime returns the start time recorded on the root EXECUTION operation.
// The bool is false if the timestamp is not present in the loaded state.
func (d *DurableContext) ExecutionStartTime() (time.Time, bool) {
	op, ok := d.execCtx.StepData[d.execCtx.ExecutionOperationId]
	if !ok || op == nil || op.StartTimestamp == nil {
		return time.Time{}, false
	}
	return op.StartTimestamp.Time, true
}

// Context returns the underlying Go context.Context stored in this DurableContext.
func (d *DurableContext) Context() context.Context { return d.goCtx }

// NewChildDurableContext creates a child DurableContext derived from goCtx.
func (d *DurableContext) NewChildDurableContext(goCtx context.Context, prefix string, parentID string, mode types.DurableExecutionMode) types.DurableContext {
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
		goCtx:         goCtx,
	}
	return child
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
	ctx    context.Context
}

func (s *StepContext) Logger() types.Logger     { return s.logger }
func (s *StepContext) Context() context.Context { return s.ctx }

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// NewDurableContext creates a new DurableContext and returns it as types.DurableContext.
func NewDurableContext(
	goCtx context.Context,
	execCtx *checkpoint.ExecutionContext,
	lambdaCtx *types.LambdaContext,
	checkpointMgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) types.DurableContext {
	dc := &DurableContext{
		execCtx:       execCtx,
		lambdaCtx:     lambdaCtx,
		checkpointMgr: checkpointMgr,
		mode:          mode,
		logger:        logger,
		rawLogger:     logger,
		modeAware:     true,
		goCtx:         goCtx,
	}
	return dc
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

func (d *DurableContext) Checkpoint(stepID string, update types.OperationUpdate) error {
	return d.checkpointMgr.Checkpoint(d.goCtx, stepID, update)
}

func (d *DurableContext) CheckpointBatch(batch []types.OperationUpdate) error {
	return d.checkpointMgr.CheckpointBatch(d.goCtx, batch)
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
