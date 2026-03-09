package context

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/durable-execution-sdk-go/checkpoint"
	durableErrors "github.com/aws/durable-execution-sdk-go/errors"
	"github.com/aws/durable-execution-sdk-go/types"
	"github.com/aws/durable-execution-sdk-go/utils"
)

var _ types.DurableContext = (*DurableContext)(nil)

// DurableContext is the concrete implementation of types.DurableContext.
type DurableContext struct {
	mu            sync.Mutex
	execCtx       *ExecutionContext
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

// stepContextImpl is a lightweight context passed to step functions.
type stepContextImpl struct {
	logger types.Logger
}

func (s *stepContextImpl) Logger() types.Logger { return s.logger }

// newDurableContext creates a new DurableContext for the root execution.
func newDurableContext(
	execCtx *ExecutionContext,
	lambdaCtx *types.LambdaContext,
	checkpointMgr *checkpoint.Manager,
	mode types.DurableExecutionMode,
	logger types.Logger,
) *DurableContext {
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

// newChildDurableContext creates a child DurableContext with its own step namespace.
func (d *DurableContext) newChildDurableContext(prefix string, parentID string, mode types.DurableExecutionMode) *DurableContext {
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

func (d *DurableContext) nextStepID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stepCounter++
	return CreateStepID(d.stepPrefix, d.stepCounter)
}

func (d *DurableContext) isExecutionMode() bool {
	return d.mode == types.ExecutionMode
}

// LambdaCtx returns the underlying Lambda context.
func (d *DurableContext) LambdaCtx() *types.LambdaContext { return d.lambdaCtx }

// ExecutionArn returns the ARN of the current durable execution.
func (d *DurableContext) ExecutionArn() string { return d.execCtx.DurableExecutionArn }

// Logger returns a mode-aware logger (silent during replay when modeAware is true).
func (d *DurableContext) Logger() types.Logger {
	if d.modeAware && !d.isExecutionMode() {
		return utils.NopLogger{}
	}
	return d.logger
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

// ---------------------------------------------------------------------------
// Step
// ---------------------------------------------------------------------------

func Step[TOut any](d *DurableContext, name string, fn func(ctx types.StepContext) (TOut, error), cfg *types.StepConfig) (TOut, error) {
	stepID := d.nextStepID()
	var zero TOut
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	serdes := utils.DefaultSerdes
	semantics := types.StepSemanticsAtLeastOncePerRetry
	var retryStrategy func(err error, attempt int) types.RetryDecision

	if cfg != nil {
		if cfg.Serdes != nil {
			serdes = cfg.Serdes
		}
		if cfg.Semantics != "" {
			semantics = cfg.Semantics
		}
		if cfg.RetryStrategy != nil {
			retryStrategy = cfg.RetryStrategy
		}
	}

	subType := types.OperationSubTypeStep

	// Check for stored result (replay path)
	stored := d.execCtx.GetStepData(stepID)
	if err := ValidateReplayConsistency(stepID, types.OperationTypeStep, namePtr, &subType, stored); err != nil {
		d.execCtx.TerminationManager.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		return zero, err
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		// Replay: return stored result
		if d.rawLogger != nil {
			stepName := "unnamed"
			if namePtr != nil {
				stepName = *namePtr
			}
			d.rawLogger.Info(fmt.Sprintf("Replaying step %s (ID: %s) as SUCCEEDED", stepName, stepID))
		}

		d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
			StepId:  stepID,
			Name:    namePtr,
			Type:    types.OperationTypeStep,
			SubType: &subType,
		})

		var resultPtr *string
		if stored.StepDetails != nil {
			resultPtr = stored.StepDetails.Result
		}
		result, err := utils.SafeDeserialize[TOut](serdes, resultPtr, stepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return zero, &durableErrors.SerdesError{
				Message: "failed to deserialize stored step result",
				StepID:  stepID, StepName: namePtr,
				Operation: "deserialize", Cause: err,
			}
		}
		return result, nil
	}

	if stored != nil && stored.Status == types.OperationStatusFailed {
		// Replay: step previously failed - re-surface the error
		d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
			StepId: stepID, Name: namePtr,
			Type: types.OperationTypeStep, SubType: &subType,
		})
		var cause error
		if stored.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.Error)
		}
		return zero, durableErrors.NewStepError(stepID, namePtr, cause)
	}

	// Execution path: run the step with retry support
	stepCtx := &stepContextImpl{logger: d.logger}

	attemptCount := 0
	for {
		attemptCount++

		// AtMostOncePerRetry: checkpoint START before executing
		if semantics == types.StepSemanticsAtMostOncePerRetry {
			if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
				Id:      stepID,
				Action:  types.OperationActionStart,
				Type:    types.OperationTypeStep,
				SubType: &subType,
				Name:    namePtr,
			}); err != nil {
				return zero, err
			}
		}

		result, stepErr := fn(stepCtx)

		if stepErr == nil {
			// Step succeeded - serialize and checkpoint
			serialized, serErr := utils.SafeSerialize(serdes, result, stepID, d.execCtx.DurableExecutionArn)
			if serErr != nil {
				d.execCtx.TerminationManager.Terminate(types.TerminationResult{
					Reason:  types.TerminationReasonSerdesFailed,
					Message: fmt.Sprintf("failed to serialize step result: %v", serErr),
				})
				return zero, &durableErrors.SerdesFailedError{Message: serErr.Error()}
			}

			var payloadPtr *string
			if serialized != "" {
				payloadPtr = &serialized
			}

			if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
				Id:      stepID,
				Action:  types.OperationActionSucceed,
				Type:    types.OperationTypeStep,
				SubType: &subType,
				Name:    namePtr,
				Payload: payloadPtr,
			}); err != nil {
				return zero, err
			}

			d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
				StepId: stepID, Name: namePtr,
				Type: types.OperationTypeStep, SubType: &subType,
			})
			return result, nil
		}

		// Step failed - check if it's unrecoverable
		if durableErrors.IsUnrecoverableError(stepErr) {
			return zero, stepErr
		}

		// Apply retry strategy
		retry := false
		var delay *types.Duration

		if retryStrategy != nil {
			decision := retryStrategy(stepErr, attemptCount)
			retry = decision.ShouldRetry
			delay = decision.Delay
		}

		if !retry {
			// Checkpoint the failure
			errObj := utils.SafeStringify(stepErr)
			_ = d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
				Id:      stepID,
				Action:  types.OperationActionFail,
				Type:    types.OperationTypeStep,
				SubType: &subType,
				Name:    namePtr,
				Error:   errObj,
			})

			d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
				StepId: stepID, Name: namePtr,
				Type: types.OperationTypeStep, SubType: &subType,
			})
			return zero, durableErrors.NewStepError(stepID, namePtr, stepErr)
		}

		// Wait before retrying
		if delay != nil {
			time.Sleep(delay.ToDuration())
		} else {
			time.Sleep(time.Second)
		}
	}
}

// ---------------------------------------------------------------------------
// Wait
// ---------------------------------------------------------------------------

func Wait(d *DurableContext, name string, duration types.Duration) error {
	stepID := d.nextStepID()
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	subType := types.OperationSubTypeWait

	// Check if already completed in replay
	stored := d.execCtx.GetStepData(stepID)
	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
			StepId: stepID, Name: namePtr,
			Type: types.OperationTypeStep, SubType: &subType,
		})
		return nil
	}

	waitSeconds := duration.ToSeconds()

	// Checkpoint the wait operation
	waitSecondsInt32 := int32(waitSeconds)
	if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
		Id:      stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: &subType,
		Name:    namePtr,
		WaitOptions: &types.WaitOptions{
			WaitSeconds: &waitSecondsInt32,
		},
	}); err != nil {
		return err
	}

	// Signal termination (execution will be resumed after the wait period)
	d.execCtx.TerminationManager.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for %d seconds", waitSeconds),
	})

	// Block here - the termination signal will propagate up and stop the handler
	select {}
}

// ---------------------------------------------------------------------------
// RunInChildContext
// ---------------------------------------------------------------------------

func RunInChildContext[T any](d *DurableContext, name string, fn func(ctx *DurableContext) (T, error), cfg *types.ChildConfig) (T, error) {
	var zero T
	stepID := d.nextStepID()
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	subType := types.OperationSubTypeRunInChildContext

	// Check replay
	stored := d.execCtx.GetStepData(stepID)
	if err := ValidateReplayConsistency(stepID, types.OperationTypeStep, namePtr, &subType, stored); err != nil {
		d.execCtx.TerminationManager.Terminate(types.TerminationResult{
			Reason: types.TerminationReasonContextValidationError,
			Error:  err, Message: err.Error(),
		})
		return zero, err
	}

	serdes := utils.DefaultSerdes
	if cfg != nil && cfg.Serdes != nil {
		serdes = cfg.Serdes
	}

	var childSubType *types.OperationSubType
	if cfg != nil && cfg.SubType != nil {
		st := types.OperationSubType(*cfg.SubType)
		childSubType = &st
	}
	if childSubType == nil {
		childSubType = &subType
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		// Replay: return stored result
		d.checkpointMgr.MarkAncestorFinished(stepID)
		d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
			StepId: stepID, Name: namePtr,
			Type: types.OperationTypeStep, SubType: childSubType,
		})

		var resultPtr *string
		if stored.StepDetails != nil {
			resultPtr = stored.StepDetails.Result
		}
		result, err := utils.SafeDeserialize[T](serdes, resultPtr, stepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return zero, err
		}
		return result, nil
	}

	if stored != nil && stored.Status == types.OperationStatusFailed {
		d.checkpointMgr.MarkAncestorFinished(stepID)
		var cause error
		if stored.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.Error)
		}
		return zero, durableErrors.NewChildContextError(stepID, namePtr, cause)
	}

	// Determine child mode
	childMode := d.mode

	// Create child context
	childCtx := d.newChildDurableContext(stepID, stepID, childMode)

	// Execute the child function
	result, err := fn(childCtx)

	if err != nil {
		errObj := utils.SafeStringify(err)
		_ = d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
			Id: stepID, Action: types.OperationActionFail,
			Type: types.OperationTypeStep, SubType: childSubType,
			Name: namePtr, Error: errObj,
		})
		d.checkpointMgr.MarkAncestorFinished(stepID)
		return zero, durableErrors.NewChildContextError(stepID, namePtr, err)
	}

	serialized, serErr := utils.SafeSerialize(serdes, result, stepID, d.execCtx.DurableExecutionArn)
	if serErr != nil {
		d.execCtx.TerminationManager.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: serErr.Error(),
		})
		return zero, &durableErrors.SerdesFailedError{Message: serErr.Error()}
	}

	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}

	if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
		Id: stepID, Action: types.OperationActionSucceed,
		Type: types.OperationTypeStep, SubType: childSubType,
		Name: namePtr, Payload: payloadPtr,
	}); err != nil {
		return zero, err
	}

	d.checkpointMgr.MarkAncestorFinished(stepID)
	d.checkpointMgr.MarkOperationState(stepID, types.OperationLifecycleStateCompleted, types.OperationMetadata{
		StepId: stepID, Name: namePtr,
		Type: types.OperationTypeStep, SubType: childSubType,
	})
	return result, nil
}

// ---------------------------------------------------------------------------
// Invoke
// ---------------------------------------------------------------------------

func Invoke[TIn, TOut any](d *DurableContext, name string, funcID string, input TIn, cfg *types.InvokeConfig) (TOut, error) {
	var zero TOut
	stepID := d.nextStepID()
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	subType := types.OperationSubTypeChainedInvoke
	serdes := utils.DefaultSerdes
	if cfg != nil && cfg.Serdes != nil {
		serdes = cfg.Serdes
	}

	// Check replay
	stored := d.execCtx.GetStepData(stepID)
	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		var resultPtr *string
		// For chained invocations, result is in ChainedInvokeDetails, not StepDetails
		if stored.ChainedInvokeDetails != nil {
			resultPtr = stored.ChainedInvokeDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying chained invoke %s from ChainedInvokeDetails", stepID))
			}
		} else if stored.StepDetails != nil {
			// Fallback to StepDetails for backward compatibility
			resultPtr = stored.StepDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying chained invoke %s from StepDetails (fallback)", stepID))
			}
		}
		result, err := utils.SafeDeserialize[TOut](serdes, resultPtr, stepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return zero, err
		}
		return result, nil
	}

	if stored != nil && stored.Status == types.OperationStatusFailed {
		if d.logger != nil {
			d.logger.Info(fmt.Sprintf("Replaying chained invoke %s as FAILED", stepID))
		}
		var cause error
		// Check ChainedInvokeDetails.Error first, then fallback to operation Error
		if stored.ChainedInvokeDetails != nil && stored.ChainedInvokeDetails.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.ChainedInvokeDetails.Error)
		} else if stored.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.Error)
		}
		return zero, durableErrors.NewInvokeError(stepID, namePtr, cause)
	}

	// If operation is STARTED, it means we already initiated it, so suspend and wait
	if stored != nil && stored.Status == types.OperationStatusStarted {
		if d.rawLogger != nil {
			d.rawLogger.Info(fmt.Sprintf("Chained invoke %s already started, suspending to wait for completion", stepID))
		}
		d.execCtx.TerminationManager.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointTerminating,
			Message: fmt.Sprintf("waiting for chained invoke %s to complete", funcID),
		})
		// Return termination error so goroutine can complete
		return zero, &durableErrors.TerminatedError{Message: "execution terminated waiting for completion"}
	}

	if d.rawLogger != nil {
		d.rawLogger.Info(fmt.Sprintf("Chained invoke %s not in replay state, invoking %s", stepID, funcID))
	}

	// Serialize input
	if d.rawLogger != nil {
		d.rawLogger.Info(fmt.Sprintf("About to serialize input for %s", stepID))
	}
	b, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("failed to serialize invoke input: %w", err)
	}
	inputStr := string(b)
	if d.rawLogger != nil {
		d.rawLogger.Info(fmt.Sprintf("Input serialized successfully for %s, length: %d", stepID, len(inputStr)))
	}

	// Checkpoint the invoke START with ChainedInvokeOptions
	if d.rawLogger != nil {
		d.rawLogger.Info(fmt.Sprintf("About to checkpoint START for %s", stepID))
	}

	// Checkpoint the invoke START with ChainedInvokeOptions
	update := types.OperationUpdate{
		Id:      stepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeChainedInvoke,
		SubType: &subType,
		Name:    namePtr,
		ChainedInvokeOptions: &types.ChainedInvokeOptions{
			FunctionName: &funcID,
		},
		Payload: &inputStr,
	}

	// Only set ParentId if we have a parent (not root context)
	if d.parentID != "" {
		update.ParentId = &d.parentID
	}

	if err := d.checkpointMgr.Checkpoint(stepID, update); err != nil {
		return zero, err
	}

	// Terminate - result will come in next invocation via replay
	d.execCtx.TerminationManager.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("invoking %s", funcID),
	})

	// Return a termination error to signal that this operation is suspended
	// The goroutine will complete, and the main handler will check IsTerminated()
	return zero, &durableErrors.TerminatedError{Message: "execution terminated for checkpoint"}
}

// ---------------------------------------------------------------------------
// WaitForCallback / CreateCallback
// ---------------------------------------------------------------------------

func CreateCallback[TResult any](d *DurableContext, name string, cfg *types.CreateCallbackConfig) (<-chan types.CallbackResult[TResult], string, error) {
	stepID := d.nextStepID()
	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	subType := types.OperationSubTypeCallback

	stored := d.execCtx.GetStepData(stepID)
	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		ch := make(chan types.CallbackResult[TResult], 1)
		var resultPtr *string
		// For callbacks, result is in CallbackDetails, not StepDetails
		if stored.CallbackDetails != nil {
			resultPtr = stored.CallbackDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying callback %s from CallbackDetails", stepID))
			}
		} else if stored.StepDetails != nil {
			// Fallback to StepDetails for backward compatibility
			resultPtr = stored.StepDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying callback %s from StepDetails (fallback)", stepID))
			}
		}
		var result TResult
		if resultPtr != nil && *resultPtr != "" {
			_ = json.Unmarshal([]byte(*resultPtr), &result)
		}
		ch <- types.CallbackResult[TResult]{Value: result}
		close(ch)
		return ch, "", nil
	}

	// Generate a callback ID
	callbackID := fmt.Sprintf("cb-%s-%s", HashedStepID(stepID), utils.HashID(d.execCtx.DurableExecutionArn))

	var callbackOptions *types.CallbackOptions
	if cfg != nil && cfg.Timeout != nil {
		timeoutSeconds := int32(cfg.Timeout.ToSeconds())
		callbackOptions = &types.CallbackOptions{
			TimeoutSeconds: timeoutSeconds,
		}
	}

	if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
		Id:              stepID,
		Action:          types.OperationActionStart,
		Type:            types.OperationTypeCallback,
		SubType:         &subType,
		Name:            namePtr,
		CallbackOptions: callbackOptions,
	}); err != nil {
		return nil, "", err
	}

	// Return a channel that will receive the result when the callback completes
	ch := make(chan types.CallbackResult[TResult], 1)

	// The result will arrive in the next invocation via replay; for now signal termination
	d.execCtx.TerminationManager.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for callback %s", callbackID),
	})

	return ch, callbackID, nil
}

func (d *DurableContext) WaitForCallback(
	name string,
	submitter func(ctx types.StepContext, callbackID string) error,
	cfg *types.WaitForCallbackConfig,
) (any, error) {
	return WaitForCallback[any](d, name, submitter, cfg)
}

func WaitForCallback[T any](
	d *DurableContext,
	name string,
	submitter func(ctx types.StepContext, callbackID string) error,
	cfg *types.WaitForCallbackConfig,
) (T, error) {
	var zero T
	stepID := d.nextStepID()
	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	subType := types.OperationSubTypeWaitForCallback

	serdes := utils.DefaultSerdes
	if cfg != nil && cfg.Serdes != nil {
		serdes = cfg.Serdes
	}

	stored := d.execCtx.GetStepData(stepID)
	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		var resultPtr *string
		// For callbacks, result is in CallbackDetails, not StepDetails
		if stored.CallbackDetails != nil {
			resultPtr = stored.CallbackDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying WaitForCallback %s from CallbackDetails", stepID))
			}
		} else if stored.StepDetails != nil {
			// Fallback to StepDetails for backward compatibility
			resultPtr = stored.StepDetails.Result
			if d.logger != nil {
				d.logger.Info(fmt.Sprintf("Replaying WaitForCallback %s from StepDetails (fallback)", stepID))
			}
		}
		result, err := utils.SafeDeserialize[T](serdes, resultPtr, stepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return zero, err
		}
		return result, nil
	}

	if stored != nil && stored.Status == types.OperationStatusFailed {
		if d.logger != nil {
			d.logger.Info(fmt.Sprintf("Replaying WaitForCallback %s as FAILED", stepID))
		}
		var cause error
		// Check CallbackDetails.Error first, then fallback to operation Error
		if stored.CallbackDetails != nil && stored.CallbackDetails.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.CallbackDetails.Error)
		} else if stored.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.Error)
		}
		return zero, durableErrors.NewCallbackError(stepID, namePtr, cause)
	}

	callbackID := fmt.Sprintf("cb-%s-%s", HashedStepID(stepID), utils.HashID(d.execCtx.DurableExecutionArn))

	var callbackOptions *types.CallbackOptions
	if cfg != nil && cfg.Timeout != nil {
		timeoutSeconds := int32(cfg.Timeout.ToSeconds())
		callbackOptions = &types.CallbackOptions{
			TimeoutSeconds: timeoutSeconds,
		}
	}

	// Run the submitter
	stepCtx := &stepContextImpl{logger: d.logger}
	if err := submitter(stepCtx, callbackID); err != nil {
		return zero, err
	}

	if err := d.checkpointMgr.Checkpoint(stepID, types.OperationUpdate{
		Id:              stepID,
		Action:          types.OperationActionStart,
		Type:            types.OperationTypeCallback,
		SubType:         &subType,
		Name:            namePtr,
		CallbackOptions: callbackOptions,
	}); err != nil {
		return zero, err
	}

	d.execCtx.TerminationManager.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for callback %s", callbackID),
	})
	select {}
}

// ---------------------------------------------------------------------------
// WaitForCondition
// ---------------------------------------------------------------------------
func WaitForCondition[TState any](d *DurableContext, name string, checkFn func(state TState, ctx types.StepContext) (TState, error), cfg types.WaitForConditionConfig[TState]) (TState, error) {
	var zero TState
	var namePtr *string
	if name != "" {
		namePtr = &name
	}
	subType := types.OperationSubTypeWaitForCondition

	// Use a child context for the polling loop
	childStepID := d.nextStepID()

	stored := d.execCtx.GetStepData(childStepID)
	serdes := utils.DefaultSerdes
	if cfg.Serdes != nil {
		serdes = cfg.Serdes
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		var resultPtr *string
		if stored.StepDetails != nil {
			resultPtr = stored.StepDetails.Result
		}
		return utils.SafeDeserialize[TState](serdes, resultPtr, childStepID, d.execCtx.DurableExecutionArn)
	}

	if stored != nil && stored.Status == types.OperationStatusFailed {
		var cause error
		if stored.Error != nil {
			cause = utils.ErrorFromErrorObject(stored.Error)
		}
		return zero, durableErrors.NewWaitConditionError(childStepID, namePtr, cause)
	}

	// Polling loop
	state := cfg.InitialState
	stepCtx := &stepContextImpl{logger: d.logger}
	attempt := 0

	for {
		attempt++
		newState, err := checkFn(state, stepCtx)
		if err != nil {
			errObj := utils.SafeStringify(err)
			_ = d.checkpointMgr.Checkpoint(childStepID, types.OperationUpdate{
				Id: childStepID, Action: types.OperationActionFail,
				Type: types.OperationTypeStep, SubType: &subType,
				Name: namePtr, Error: errObj,
			})
			return zero, durableErrors.NewWaitConditionError(childStepID, namePtr, err)
		}
		state = newState

		if cfg.WaitStrategy == nil {
			// Default: stop after first check
			break
		}

		result := cfg.WaitStrategy(state, attempt)
		if !result.ShouldContinue {
			break
		}

		// Wait before next poll
		delay := result.Delay
		if delay == nil {
			time.Sleep(5 * time.Second)
		} else {
			// Checkpoint the wait, then terminate
			waitSeconds := int32(delay.ToSeconds())
			innerStepID := fmt.Sprintf("%s-wait-%d", childStepID, attempt)
			if err := d.checkpointMgr.Checkpoint(innerStepID, types.OperationUpdate{
				Id:      innerStepID,
				Action:  types.OperationActionStart,
				Type:    types.OperationTypeStep,
				SubType: &subType,
				WaitOptions: &types.WaitOptions{
					WaitSeconds: &waitSeconds,
				},
			}); err != nil {
				return zero, err
			}
			d.execCtx.TerminationManager.Terminate(types.TerminationResult{
				Reason:  types.TerminationReasonCheckpointTerminating,
				Message: fmt.Sprintf("waiting for condition: poll %d", attempt),
			})
			select {}
		}
	}

	serialized, serErr := utils.SafeSerialize[TState](serdes, state, childStepID, d.execCtx.DurableExecutionArn)
	if serErr != nil {
		return zero, serErr
	}
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}

	if err := d.checkpointMgr.Checkpoint(childStepID, types.OperationUpdate{
		Id: childStepID, Action: types.OperationActionSucceed,
		Type: types.OperationTypeStep, SubType: &subType,
		Name: namePtr, Payload: payloadPtr,
	}); err != nil {
		return zero, err
	}

	return state, nil
}

// ---------------------------------------------------------------------------
// Map
// ---------------------------------------------------------------------------
func Map[TIn, TOut any](
	d *DurableContext,
	name string,
	items []TIn,
	mapFn func(ctx *DurableContext, item TIn, index int, items []TIn) (TOut, error),
	cfg *types.MapConfig,
) (types.BatchResult[TOut], error) {
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	subType := types.OperationSubTypeMap
	iterSubType := types.OperationSubTypeMapIteration

	outerStepID := d.nextStepID()

	// Log at start to confirm Map is being called
	if d.rawLogger != nil {
		d.rawLogger.Info(fmt.Sprintf("Map function called for %s (stepID: %s)", name, outerStepID))
	}

	stored := d.execCtx.GetStepData(outerStepID)

	serdes := utils.DefaultSerdes
	maxConcurrency := 0
	if cfg != nil {
		if cfg.Serdes != nil {
			serdes = cfg.Serdes
		}
		maxConcurrency = cfg.MaxConcurrency
	}

	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		// Full batch replay - reconstruct from stored result
		var resultPtr *string
		if stored.StepDetails != nil {
			resultPtr = stored.StepDetails.Result
		}
		raw, err := utils.SafeDeserialize[[]TOut](serdes, resultPtr, outerStepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return types.BatchResult[TOut]{}, err
		}
		// raw should be []any from JSON
		batch := types.BatchResult[TOut]{}
		for i, v := range raw {
			batch.Items = append(batch.Items, types.BatchResultItem[TOut]{Value: v, Index: i})
		}

		return batch, nil
	}

	// If map operation is already STARTED, check iterations and suspend if needed
	if stored != nil && stored.Status == types.OperationStatusStarted {
		if d.rawLogger != nil {
			d.rawLogger.Info(fmt.Sprintf("Map %s already started, checking iteration status", outerStepID))
		}

		// Check if any iterations are still STARTED (in progress).
		// Check the ChainedInvoke child (stepID-iteration-N-1), not the CONTEXT wrapper,
		// since the CONTEXT stays STARTED until the Map closes it out.
		anyStarted := false
		for i := range items {
			invokeID := fmt.Sprintf("%s-iteration-%d-1", outerStepID, i)
			invokeStored := d.execCtx.GetStepData(invokeID)
			if invokeStored == nil || invokeStored.Status == types.OperationStatusStarted {
				anyStarted = true
				break
			}
		}

		// If any iterations are still in progress, suspend
		if anyStarted {
			if d.rawLogger != nil {
				d.rawLogger.Info(fmt.Sprintf("Map %s has iterations still in progress, suspending", outerStepID))
			}
			d.execCtx.TerminationManager.Terminate(types.TerminationResult{
				Reason:  types.TerminationReasonCheckpointTerminating,
				Message: fmt.Sprintf("waiting for map %s iterations to complete", name),
			})
			select {} // Suspend execution
		}

		// If all iterations succeeded, we can complete the map
		// Fall through to process results and checkpoint success
	}

	results := make([]TOut, len(items))
	errs := make([]error, len(items))

	// Determine if we need to checkpoint iterations
	// If the map is already started, iterations already exist in AWS and should NOT be checkpointed again
	mapAlreadyStarted := stored != nil && stored.Status == types.OperationStatusStarted

	// Step 1: Checkpoint Map START + ALL iteration STARTs in a single atomic batch.
	// This is CRITICAL: AWS resolves ParentId references within a single checkpoint call.
	// Also: the JS SDK uses OperationType.CONTEXT (not STEP) for all child-context
	// operations including Map containers and Map iterations.
	if !mapAlreadyStarted {
		mapStartUpdate := types.OperationUpdate{
			Id: outerStepID, Action: types.OperationActionStart,
			Type: types.OperationTypeContext, SubType: &subType,
			Name: namePtr,
		}
		if d.parentID != "" {
			mapStartUpdate.ParentId = &d.parentID
		}

		batch := []types.OperationUpdate{mapStartUpdate}

		for i, item := range items {
			iterName := fmt.Sprintf("%s-iteration-%d", outerStepID, i)
			iterStored := d.execCtx.GetStepData(iterName)

			// If iteration already succeeded, use stored result
			if iterStored != nil && iterStored.Status == types.OperationStatusSucceeded {
				var resultPtr *string
				if iterStored.StepDetails != nil {
					resultPtr = iterStored.StepDetails.Result
				}
				result, err := utils.SafeDeserialize[TOut](serdes, resultPtr, iterName, d.execCtx.DurableExecutionArn)
				if err == nil {
					results[i] = result
				}
				errs[i] = err
				continue
			}

			// If iteration already started, skip (already exists in AWS)
			if iterStored != nil && iterStored.Status == types.OperationStatusStarted {
				if d.rawLogger != nil {
					d.rawLogger.Info(fmt.Sprintf("Map iteration %s already started (exists in AWS), skipping checkpoint", iterName))
				}
				continue
			}

			// If iteration failed, record the error
			if iterStored != nil && iterStored.Status == types.OperationStatusFailed {
				if iterStored.Error != nil {
					errs[i] = utils.ErrorFromErrorObject(iterStored.Error)
				}
				continue
			}

			var itemName *string
			if cfg != nil && cfg.ItemNamer != nil {
				n := cfg.ItemNamer(item, i)
				itemName = &n
			}

			outerStepIDCopy := outerStepID // capture for pointer
			if d.rawLogger != nil {
				d.rawLogger.Info(fmt.Sprintf("Adding iteration %s START to batch with ParentId=%s", iterName, outerStepID))
			}
			batch = append(batch, types.OperationUpdate{
				Id: iterName, Action: types.OperationActionStart,
				Type: types.OperationTypeContext, SubType: &iterSubType,
				Name:     itemName,
				ParentId: &outerStepIDCopy,
			})
		}

		if d.rawLogger != nil {
			d.rawLogger.Info(fmt.Sprintf("Sending atomic batch: Map START + %d iteration STARTs", len(batch)-1))
		}
		if err := d.checkpointMgr.CheckpointBatch(batch); err != nil {
			return types.BatchResult[TOut]{}, err
		}
		if d.rawLogger != nil {
			d.rawLogger.Info("Atomic batch committed, now executing map functions...")
		}
	} else {
		// Map already started, so iterations already exist in AWS.
		// Pre-populate results and errors from the ChainedInvoke child operation
		// (stepID-iteration-N-1), which holds the actual result in ChainedInvokeDetails.
		for i := range items {
			invokeID := fmt.Sprintf("%s-iteration-%d-1", outerStepID, i)
			invokeStored := d.execCtx.GetStepData(invokeID)

			if invokeStored != nil && invokeStored.Status == types.OperationStatusSucceeded {
				var resultPtr *string
				if invokeStored.ChainedInvokeDetails != nil {
					resultPtr = invokeStored.ChainedInvokeDetails.Result
				} else if invokeStored.StepDetails != nil {
					resultPtr = invokeStored.StepDetails.Result
				}
				result, err := utils.SafeDeserialize[TOut](serdes, resultPtr, invokeID, d.execCtx.DurableExecutionArn)
				if err == nil {
					results[i] = result
				}
				errs[i] = err
			} else if invokeStored != nil && invokeStored.Status == types.OperationStatusFailed {
				if invokeStored.ChainedInvokeDetails != nil && invokeStored.ChainedInvokeDetails.Error != nil {
					errs[i] = utils.ErrorFromErrorObject(invokeStored.ChainedInvokeDetails.Error)
				} else if invokeStored.Error != nil {
					errs[i] = utils.ErrorFromErrorObject(invokeStored.Error)
				}
			}
		}
		if d.rawLogger != nil {
			d.rawLogger.Info("Map already started, iterations already exist in AWS, skipping iteration checkpoints")
		}
	}

	// Step 3: Execute all map functions in parallel
	type work struct {
		index int
	}

	workCh := make(chan work, len(items))
	for i := range items {
		// Only add work for iterations that need execution — skip if the
		// ChainedInvoke child already completed (succeeded or failed).
		invokeID := fmt.Sprintf("%s-iteration-%d-1", outerStepID, i)
		invokeStored := d.execCtx.GetStepData(invokeID)
		if invokeStored != nil && (invokeStored.Status == types.OperationStatusSucceeded || invokeStored.Status == types.OperationStatusFailed) {
			continue
		}
		workCh <- work{index: i}
	}
	close(workCh)

	concurrency := maxConcurrency
	if concurrency <= 0 || concurrency > len(items) {
		concurrency = len(items)
	}

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range workCh {
				i := job.index
				item := items[i]
				iterName := fmt.Sprintf("%s-iteration-%d", outerStepID, i)

				// Create child context with iteration as parent
				childCtx := d.newChildDurableContext(iterName, iterName, d.mode)

				var itemName *string
				if cfg != nil && cfg.ItemNamer != nil {
					n := cfg.ItemNamer(item, i)
					itemName = &n
				}

				result, err := mapFn(childCtx, item, i, items)
				errs[i] = err
				if err == nil {
					results[i] = result
					serialized, _ := utils.SafeSerialize[TOut](serdes, result, iterName, d.execCtx.DurableExecutionArn)
					var p *string
					if serialized != "" {
						p = &serialized
					}
					_ = d.checkpointMgr.Checkpoint(iterName, types.OperationUpdate{
						Id: iterName, Action: types.OperationActionSucceed,
						Type: types.OperationTypeContext, SubType: &iterSubType,
						Name:     itemName,
						Payload:  p,
						ParentId: &outerStepID,
					})
				} else if _, isTerminated := err.(*durableErrors.TerminatedError); isTerminated {
					// Operation was suspended (e.g., chained invoke started)
					// Don't checkpoint failure - the operation is in progress
					// The iteration will complete on the next invocation
					if d.rawLogger != nil {
						d.rawLogger.Info(fmt.Sprintf("Map iteration %s suspended due to checkpoint", iterName))
					}
				} else {
					// Actual failure - checkpoint it
					errObj := utils.SafeStringify(err)
					_ = d.checkpointMgr.Checkpoint(iterName, types.OperationUpdate{
						Id: iterName, Action: types.OperationActionFail,
						Type: types.OperationTypeContext, SubType: &iterSubType,
						Name:     itemName,
						Error:    errObj,
						ParentId: &outerStepID,
					})
				}
			}
		}()
	}
	wg.Wait()

	// Check if any iterations are still STARTED (in progress).
	// An iteration is done when its ChainedInvoke child (stepID-iteration-N-1) has
	// SUCCEEDED or FAILED. The iteration CONTEXT wrapper itself stays STARTED until
	// the Map closes it, so we must check the invoke child, not the context parent.
	anyStarted := false
	for i := range items {
		invokeID := fmt.Sprintf("%s-iteration-%d-1", outerStepID, i)
		invokeStored := d.execCtx.GetStepData(invokeID)
		if invokeStored == nil || invokeStored.Status == types.OperationStatusStarted {
			anyStarted = true
			break
		}
	}

	// If any iterations are still in progress, suspend to wait for them
	if anyStarted {
		if d.rawLogger != nil {
			d.rawLogger.Info(fmt.Sprintf("Map %s has iterations still in progress, suspending", outerStepID))
		}
		d.execCtx.TerminationManager.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointTerminating,
			Message: fmt.Sprintf("waiting for map %s iterations to complete", name),
		})
		select {} // Suspend execution
	}

	// Build batch result
	batchResult := types.BatchResult[TOut]{Items: make([]types.BatchResultItem[TOut], len(items))}
	values := make([]TOut, len(items))
	for i := range items {
		batchResult.Items[i] = types.BatchResultItem[TOut]{
			Value: results[i],
			Err:   errs[i],
			Index: i,
		}
		values[i] = results[i]
	}

	// Checkpoint success of the map operation
	serialized, _ := utils.SafeSerialize[[]TOut](serdes, values, outerStepID, d.execCtx.DurableExecutionArn)
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}
	_ = d.checkpointMgr.Checkpoint(outerStepID, types.OperationUpdate{
		Id: outerStepID, Action: types.OperationActionSucceed,
		Type: types.OperationTypeContext, SubType: &subType,
		Name: namePtr, Payload: payloadPtr,
	})

	return batchResult, nil
}

// ---------------------------------------------------------------------------
// Parallel
// ---------------------------------------------------------------------------
func Parallel[TOut any](
	d *DurableContext,
	name string,
	branches []func(ctx *DurableContext) (TOut, error),
	cfg *types.ParallelConfig,
) (types.BatchResult[TOut], error) {
	var namePtr *string
	if name != "" {
		namePtr = &name
	}

	subType := types.OperationSubTypeParallel
	branchSubType := types.OperationSubTypeParallelBranch

	outerStepID := d.nextStepID()

	serdes := utils.DefaultSerdes
	maxConcurrency := 0
	if cfg != nil {
		if cfg.Serdes != nil {
			serdes = cfg.Serdes
		}
		maxConcurrency = cfg.MaxConcurrency
	}

	stored := d.execCtx.GetStepData(outerStepID)
	if stored != nil && stored.Status == types.OperationStatusSucceeded {
		var resultPtr *string
		if stored.StepDetails != nil {
			resultPtr = stored.StepDetails.Result
		}
		raw, err := utils.SafeDeserialize[[]TOut](serdes, resultPtr, outerStepID, d.execCtx.DurableExecutionArn)
		if err != nil {
			return types.BatchResult[TOut]{}, err
		}
		batchResult := types.BatchResult[TOut]{}

		for i, v := range raw {
			batchResult.Items = append(batchResult.Items, types.BatchResultItem[TOut]{Value: v, Index: i})
		}
		return batchResult, nil
	}

	_ = d.checkpointMgr.Checkpoint(outerStepID, types.OperationUpdate{
		Id: outerStepID, Action: types.OperationActionStart,
		Type: types.OperationTypeStep, SubType: &subType,
		Name: namePtr,
	})

	results := make([]TOut, len(branches))
	errs := make([]error, len(branches))

	type work struct{ index int }
	workCh := make(chan work, len(branches))
	for i := range branches {
		workCh <- work{index: i}
	}
	close(workCh)

	concurrency := maxConcurrency
	if concurrency <= 0 || concurrency > len(branches) {
		concurrency = len(branches)
	}

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range workCh {
				i := job.index
				branchName := fmt.Sprintf("%s-branch-%d", outerStepID, i)
				// Create child context with branch as parent (not the Parallel itself)
				// Operations within the branch (like chained invokes) will reference the branch as their parent
				childCtx := d.newChildDurableContext(branchName, branchName, d.mode)

				_ = d.checkpointMgr.Checkpoint(branchName, types.OperationUpdate{
					Id: branchName, Action: types.OperationActionStart,
					Type: types.OperationTypeStep, SubType: &branchSubType,
					// TODO: ParentId should be &outerStepID
					// ParentId: &outerStepID,
				})

				result, err := branches[i](childCtx)
				errs[i] = err
				if err == nil {
					results[i] = result
					serialized, _ := utils.SafeSerialize(serdes, result, branchName, d.execCtx.DurableExecutionArn)
					var p *string
					if serialized != "" {
						p = &serialized
					}
					_ = d.checkpointMgr.Checkpoint(branchName, types.OperationUpdate{
						Id: branchName, Action: types.OperationActionSucceed,
						Type: types.OperationTypeStep, SubType: &branchSubType,
						Payload: p,
						// TODO: ParentId should be &outerStepID
						// ParentId: &outerStepID,
					})
				} else {
					errObj := utils.SafeStringify(err)
					_ = d.checkpointMgr.Checkpoint(branchName, types.OperationUpdate{
						Id: branchName, Action: types.OperationActionFail,
						Type: types.OperationTypeStep, SubType: &branchSubType,
						Error: errObj,
						// TODO: ParentId should be &outerStepID
						// ParentId: &outerStepID,
					})
				}
			}
		}()
	}
	wg.Wait()

	batchResult := types.BatchResult[TOut]{Items: make([]types.BatchResultItem[TOut], len(branches))}
	values := make([]TOut, len(branches))
	for i := range branches {
		batchResult.Items[i] = types.BatchResultItem[TOut]{
			Value: results[i], Err: errs[i], Index: i,
		}
		values[i] = results[i]
	}

	serialized, _ := utils.SafeSerialize(serdes, values, outerStepID, d.execCtx.DurableExecutionArn)
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}
	_ = d.checkpointMgr.Checkpoint(outerStepID, types.OperationUpdate{
		Id: outerStepID, Action: types.OperationActionSucceed,
		Type: types.OperationTypeStep, SubType: &subType,
		Name: namePtr, Payload: payloadPtr,
	})

	return batchResult, nil
}
