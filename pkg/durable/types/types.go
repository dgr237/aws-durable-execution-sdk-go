// Package types defines the core data types for the AWS Durable Execution SDK for Go.
package types

import (
	"context"
	"time"
)

// CheckpointStrategy controls how the SDK groups durable operation updates into
// checkpoint API calls (spec §16.4).
type CheckpointStrategy int

const (
	// CheckpointStrategyEager (default) sends a checkpoint API call for each operation
	// update. Maximises durability at the cost of more API calls.
	CheckpointStrategyEager CheckpointStrategy = iota

	// CheckpointStrategyBatched groups concurrent operation updates into a single
	// checkpoint call. Reduces API call count with a minimal durability trade-off;
	// the entire batch is atomic so all-or-nothing semantics are preserved.
	CheckpointStrategyBatched

	// CheckpointStrategyOptimistic enqueues updates without blocking the caller.
	// Errors are surfaced only when WaitForQueueCompletion is called. Provides the
	// best throughput at the cost of potentially re-executing more work on failure.
	CheckpointStrategyOptimistic
)

// DurableExecutionMode represents the current execution mode of the durable function.
type DurableExecutionMode string

const (
	// ExecutionMode is the normal execution mode for new operations.
	ExecutionMode DurableExecutionMode = "ExecutionMode"
	// ReplayMode is the replay mode when recovering from a previous invocation.
	ReplayMode DurableExecutionMode = "ReplayMode"
	// ReplaySucceededContext is the mode when replaying an already-succeeded context.
	ReplaySucceededContext DurableExecutionMode = "ReplaySucceededContext"
)

// InvocationStatus represents the final status of a durable execution invocation.
type InvocationStatus string

const (
	// InvocationStatusSucceeded indicates the execution completed successfully.
	InvocationStatusSucceeded InvocationStatus = "SUCCEEDED"
	// InvocationStatusFailed indicates the execution failed with an unrecoverable error.
	InvocationStatusFailed InvocationStatus = "FAILED"
	// InvocationStatusPending indicates the execution is continuing asynchronously.
	InvocationStatusPending InvocationStatus = "PENDING"
)

// OperationType defines the high-level type of a durable operation.
type OperationType string

const (
	OperationTypeStep          OperationType = "STEP"
	OperationTypeExecution     OperationType = "EXECUTION"
	OperationTypeCallback      OperationType = "CALLBACK"
	OperationTypeWait          OperationType = "WAIT"
	OperationTypeContext       OperationType = "CONTEXT"
	OperationTypeChainedInvoke OperationType = "CHAINED_INVOKE"
)

// OperationStatus defines the current status of a durable operation.
type OperationStatus string

const (
	OperationStatusStarted    OperationStatus = "STARTED"
	OperationStatusInProgress OperationStatus = "IN_PROGRESS"
	OperationStatusSucceeded  OperationStatus = "SUCCEEDED"
	OperationStatusFailed     OperationStatus = "FAILED"
	OperationStatusCancelled  OperationStatus = "CANCELLED"
	OperationStatusStopped    OperationStatus = "STOPPED"
	OperationStatusTimedOut   OperationStatus = "TIMED_OUT"
)

// OperationAction defines the checkpoint action for an operation.
type OperationAction string

const (
	OperationActionStart   OperationAction = "START"
	OperationActionSucceed OperationAction = "SUCCEED"
	OperationActionFail    OperationAction = "FAIL"
	OperationActionRetry   OperationAction = "RETRY"
	OperationActionCancel  OperationAction = "CANCEL"
)

// OperationSubType provides fine-grained categorization of durable operations.
type OperationSubType string

const (
	OperationSubTypeStep              OperationSubType = "Step"
	OperationSubTypeWait              OperationSubType = "Wait"
	OperationSubTypeCallback          OperationSubType = "Callback"
	OperationSubTypeRunInChildContext OperationSubType = "RunInChildContext"
	OperationSubTypeMap               OperationSubType = "Map"
	OperationSubTypeMapIteration      OperationSubType = "MapIteration"
	OperationSubTypeParallel          OperationSubType = "Parallel"
	OperationSubTypeParallelBranch    OperationSubType = "ParallelBranch"
	OperationSubTypeWaitForCallback   OperationSubType = "WaitForCallback"
	OperationSubTypeWaitForCondition  OperationSubType = "WaitForCondition"
	OperationSubTypeChainedInvoke     OperationSubType = "ChainedInvoke"
)

// Duration represents a time duration for durable execution operations.
// At least one field must be set.
type Duration struct {
	Days    int
	Hours   int
	Minutes int
	Seconds int
}

// ToSeconds converts the Duration to total seconds.
func (d Duration) ToSeconds() int64 {
	return int64(d.Days)*86400 +
		int64(d.Hours)*3600 +
		int64(d.Minutes)*60 +
		int64(d.Seconds)
}

// ToDuration converts the Duration to a time.Duration.
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d.ToSeconds()) * time.Second
}

// StepDetails contains the execution result details for a step operation.
type StepDetails struct {
	Result *string `json:"Result,omitempty"`
}

// ExecutionDetails contains metadata about the execution context.
type ExecutionDetails struct {
	InputPayload *string `json:"InputPayload,omitempty"`
}

// CallbackDetails contains details about a callback operation in a durable execution.
type CallbackDetails struct {
	// CallbackId is the callback ID generated by the DurableContext.
	CallbackId *string `json:"CallbackId,omitempty"`
	// Result is the response payload from the callback operation.
	Result *string `json:"Result,omitempty"`
	// Error contains details about the failure if the callback failed.
	Error *ErrorObject `json:"Error,omitempty"`
}

// ChainedInvokeDetails contains details about a chained function invocation.
type ChainedInvokeDetails struct {
	// Result is the response payload from the chained invocation.
	Result *string `json:"Result,omitempty"`
	// Error contains details about the chained invocation failure.
	Error *ErrorObject `json:"Error,omitempty"`
}

// ContextDetails contains details about a durable execution context.
type ContextDetails struct {
	// Result is the response payload from the context.
	Result *string `json:"Result,omitempty"`
	// Error contains details about the context failure.
	Error *ErrorObject `json:"Error,omitempty"`
	// ReplayChildren indicates whether child operations should be included in state.
	ReplayChildren *bool `json:"ReplayChildren,omitempty"`
}

// WaitDetails contains details about a wait operation.
type WaitDetails struct {
	// ScheduledEndTimestamp is when the wait operation is scheduled to complete.
	ScheduledEndTimestamp *FlexibleTime `json:"ScheduledEndTimestamp,omitempty"`
}

// ErrorObject contains error information for a failed operation.
type ErrorObject struct {
	ErrorType    string `json:"ErrorType,omitempty"`
	ErrorMessage string `json:"ErrorMessage,omitempty"`
	StackTrace   string `json:"StackTrace,omitempty"`
}

// CallbackOptions contains configuration options for callback operations.
type CallbackOptions struct {
	// TimeoutSeconds is the timeout for the callback operation in seconds.
	TimeoutSeconds int32
	// HeartbeatTimeoutSeconds is the heartbeat timeout in seconds.
	HeartbeatTimeoutSeconds int32
}

// ChainedInvokeOptions contains configuration options for chained function invocations.
type ChainedInvokeOptions struct {
	// FunctionName is the name or ARN of the Lambda function to invoke.
	FunctionName *string
	// TenantId is the tenant identifier for the chained invocation.
	TenantId *string
}

// ContextOptions contains configuration options for a durable execution context.
type ContextOptions struct {
	// ReplayChildren indicates whether child state should be included in responses.
	ReplayChildren *bool
}

// StepOptions contains configuration options for a step operation.
type StepOptions struct {
	// NextAttemptDelaySeconds is the delay in seconds before the next retry attempt.
	NextAttemptDelaySeconds *int32
}

// WaitOptions specifies how long to pause the durable execution.
type WaitOptions struct {
	// WaitSeconds is the duration to wait, in seconds.
	WaitSeconds *int32
}

// Operation represents a completed durable operation from execution history.
type Operation struct {
	// Id is the unique identifier for this operation.
	Id string `json:"Id,omitempty"`
	// Type is the high-level operation type.
	Type OperationType `json:"Type,omitempty"`
	// SubType provides a more specific categorization of the operation.
	SubType *OperationSubType `json:"SubType,omitempty"`
	// Name is the optional human-readable name for the operation.
	Name *string `json:"Name,omitempty"`
	// Status is the current status of the operation.
	Status OperationStatus `json:"Status,omitempty"`
	// StartTimestamp is the date and time when the operation started.
	StartTimestamp *FlexibleTime `json:"StartTimestamp,omitempty"`
	// EndTimestamp is the date and time when the operation ended.
	EndTimestamp *FlexibleTime `json:"EndTimestamp,omitempty"`
	// ParentId is the unique identifier of the parent operation, if this operation is running within a child context.
	ParentId *string `json:"ParentId,omitempty"`
	// StepDetails contains the step result when Status is SUCCEEDED.
	StepDetails *StepDetails `json:"StepDetails,omitempty"`
	// ExecutionDetails contains execution metadata.
	ExecutionDetails *ExecutionDetails `json:"ExecutionDetails,omitempty"`
	// CallbackDetails contains details about a callback operation.
	CallbackDetails *CallbackDetails `json:"CallbackDetails,omitempty"`
	// ChainedInvokeDetails contains details about a chained function invocation.
	ChainedInvokeDetails *ChainedInvokeDetails `json:"ChainedInvokeDetails,omitempty"`
	// ContextDetails contains details about the context, if this operation represents a context.
	ContextDetails *ContextDetails `json:"ContextDetails,omitempty"`
	// WaitDetails contains details about the wait operation, if this operation represents a wait.
	WaitDetails *WaitDetails `json:"WaitDetails,omitempty"`
	// Error contains error information when Status is FAILED.
	Error *ErrorObject `json:"Error,omitempty"`
}

// OperationUpdate is used to update a checkpoint with operation information.
type OperationUpdate struct {
	// Id is the unique operation identifier.
	Id string
	// Action is the checkpoint action (START, SUCCEED, FAIL).
	Action OperationAction
	// Type is the high-level operation type.
	Type OperationType
	// SubType is the optional operation sub-type.
	SubType *OperationSubType
	// Name is the optional human-readable name.
	Name *string
	// ParentId is the unique identifier of the parent operation, if this operation is running within a child context.
	ParentId *string
	// Payload is the serialized result for successful operations.
	Payload *string
	// Error contains error information for failed operations.
	Error *ErrorObject
	// CallbackOptions contains configuration for callback operations.
	CallbackOptions *CallbackOptions
	// ChainedInvokeOptions contains configuration for chained function invocations.
	ChainedInvokeOptions *ChainedInvokeOptions
	// ContextOptions contains configuration for context operations.
	ContextOptions *ContextOptions
	// StepOptions contains configuration for step operations.
	StepOptions *StepOptions
	// WaitOptions contains configuration for wait operations.
	WaitOptions *WaitOptions
}

// InitialExecutionState contains the operation history for replay.
type InitialExecutionState struct {
	// Operations is the array of completed operations with their results.
	Operations []Operation `json:"Operations,omitempty"`
	// NextMarker is the pagination marker for large operation histories.
	NextMarker *string `json:"NextMarker,omitempty"`
}

// DurableExecutionInvocationInput is the payload the durable execution service provides to durable functions.
type DurableExecutionInvocationInput struct {
	// DurableExecutionArn uniquely identifies this durable execution instance.
	DurableExecutionArn string `json:"DurableExecutionArn"`
	// CheckpointToken is the unique token for the next checkpoint in this execution.
	CheckpointToken string `json:"CheckpointToken"`
	// InitialExecutionState contains the operation history and pagination information.
	InitialExecutionState InitialExecutionState `json:"InitialExecutionState"`
}

// DurableExecutionInvocationOutput is the response structure returned from durable functions.
type DurableExecutionInvocationOutput struct {
	// Status is the execution outcome.
	Status InvocationStatus `json:"Status"`
	// Result is the optional serialized result (for SUCCEEDED status).
	Result *string `json:"Result,omitempty"`
	// Error contains failure details (for FAILED status).
	Error *ErrorObject `json:"Error,omitempty"`
}

// CheckpointDurableExecutionRequest is the request for creating a checkpoint.
type CheckpointDurableExecutionRequest struct {
	// DurableExecutionArn identifies the execution being checkpointed.
	DurableExecutionArn string
	// CheckpointToken is the idempotency token for this checkpoint.
	CheckpointToken string
	// Operations is the list of operation updates to persist.
	Operations []OperationUpdate
	// NextCheckpointToken is the token to use for the next checkpoint.
	NextCheckpointToken *string
}

// CheckpointDurableExecutionResponse is the response from a checkpoint request.
type CheckpointDurableExecutionResponse struct {
	// NextCheckpointToken is the token for the subsequent checkpoint.
	NextCheckpointToken *string
}

// GetDurableExecutionStateRequest is the request for retrieving execution state.
type GetDurableExecutionStateRequest struct {
	// DurableExecutionArn identifies the execution to query.
	DurableExecutionArn string
	// CheckpointToken is the token that identifies the current state of the execution.
	CheckpointToken string
	// NextMarker is the pagination marker for large histories.
	NextMarker *string
}

// GetDurableExecutionStateResponse is the response containing execution state.
type GetDurableExecutionStateResponse struct {
	// Operations is the array of operations in execution history.
	Operations []Operation
	// NextMarker is the pagination marker for subsequent requests.
	NextMarker *string
}

// TerminationReason describes why an execution was terminated.
type TerminationReason string

const (
	// TerminationReasonCheckpointFailed indicates a checkpoint operation failed.
	TerminationReasonCheckpointFailed TerminationReason = "CHECKPOINT_FAILED"
	// TerminationReasonSerdesFailed indicates a serialization/deserialization failure.
	TerminationReasonSerdesFailed TerminationReason = "SERDES_FAILED"
	// TerminationReasonContextValidationError indicates a context usage error.
	TerminationReasonContextValidationError TerminationReason = "CONTEXT_VALIDATION_ERROR"
	// TerminationReasonCheckpointTerminating indicates a termination checkpoint.
	TerminationReasonCheckpointTerminating TerminationReason = "CHECKPOINT_TERMINATING"
)

// TerminationResult is the result returned when a termination event occurs.
type TerminationResult struct {
	// Reason describes why the execution is being terminated.
	Reason TerminationReason
	// Error is the underlying error that caused termination (if any).
	Error error
	// Message is a human-readable description of the termination reason.
	Message string
}

// RetryDecision is the decision returned by a retry strategy function.
type RetryDecision struct {
	// ShouldRetry indicates whether the operation should be retried.
	ShouldRetry bool
	// Delay is the duration to wait before the next retry attempt.
	Delay *Duration
}

// StepSemantics controls how step execution is checkpointed and replayed.
type StepSemantics string

const (
	// StepSemanticsAtLeastOncePerRetry is the default; the step executes at least once per retry.
	StepSemanticsAtLeastOncePerRetry StepSemantics = "AT_LEAST_ONCE_PER_RETRY"
	// StepSemanticsAtMostOncePerRetry checkpoints before execution to prevent double-execution per retry.
	StepSemanticsAtMostOncePerRetry StepSemantics = "AT_MOST_ONCE_PER_RETRY"
)

// JitterStrategy controls how jitter is applied to retry delays.
type JitterStrategy string

const (
	// JitterStrategyNone applies no jitter.
	JitterStrategyNone JitterStrategy = "NONE"
	// JitterStrategyFull applies full jitter (random between 0 and calculated delay).
	JitterStrategyFull JitterStrategy = "FULL"
	// JitterStrategyHalf applies half jitter (random between 50% and 100% of calculated delay).
	JitterStrategyHalf JitterStrategy = "HALF"
)

// BatchCompletionConfig configures completion behavior for batch operations (map/parallel).
type BatchCompletionConfig struct {
	// MinSuccessful is the minimum number of items that must succeed.
	MinSuccessful *int
	// ToleratedFailureCount is the number of failures tolerated before stopping.
	ToleratedFailureCount *int
	// ToleratedFailurePercentage is the percentage of failures tolerated.
	ToleratedFailurePercentage *float64
}

// BatchResultItem represents a single result from a batch operation.
type BatchResultItem[T any] struct {
	// Value is the successful result value.
	Value T
	// Err is the error if this item failed.
	Err error
	// Index is the original index of this item.
	Index int
	// Name is the optional name of this item.
	Name *string
}

// BatchResult is the result of a map or parallel batch operation.
type BatchResult[T any] struct {
	// Items contains all results (both successes and failures).
	Items []BatchResultItem[T]
	// CompletionReason describes why the batch completed (e.g. "ALL_SUCCEEDED", "MIN_SUCCESSFUL_MET", "MAX_FAILURES_REACHED").
	CompletionReason string
}

// TotalCount returns the total number of items in the batch.
func (b BatchResult[T]) TotalCount() int { return len(b.Items) }

// SuccessCount returns the number of successfully completed items.
func (b BatchResult[T]) SuccessCount() int {
	count := 0
	for _, item := range b.Items {
		if item.Err == nil {
			count++
		}
	}
	return count
}

// FailureCount returns the number of failed items.
func (b BatchResult[T]) FailureCount() int {
	count := 0
	for _, item := range b.Items {
		if item.Err != nil {
			count++
		}
	}
	return count
}

// Values returns only the successful values from the batch result.
func (b BatchResult[T]) Values() []T {
	var result []T
	for _, item := range b.Items {
		if item.Err == nil {
			result = append(result, item.Value)
		}
	}
	return result
}

// GetErrors returns only the errors from failed items.
func (b BatchResult[T]) GetErrors() []error {
	var errs []error
	for _, item := range b.Items {
		if item.Err != nil {
			errs = append(errs, item.Err)
		}
	}
	return errs
}

// GetItems returns all items (both successes and failures).
func (b BatchResult[T]) GetItems() []BatchResultItem[T] { return b.Items }

// HasErrors returns true if any batch item failed.
func (b BatchResult[T]) HasErrors() bool {
	for _, item := range b.Items {
		if item.Err != nil {
			return true
		}
	}
	return false
}

// ThrowIfErrors returns the first error encountered, or nil if all items succeeded.
func (b BatchResult[T]) ThrowIfErrors() error {
	for _, item := range b.Items {
		if item.Err != nil {
			return item.Err
		}
	}
	return nil
}

// LambdaContext is a simplified Lambda context passed to handlers.
type LambdaContext struct {
	// FunctionName is the name of the Lambda function.
	FunctionName string
	// FunctionVersion is the version of the Lambda function.
	FunctionVersion string
	// InvokedFunctionArn is the ARN of the invoked Lambda function.
	InvokedFunctionArn string
	// MemoryLimitInMB is the memory limit in MB.
	MemoryLimitInMB int
	// AwsRequestID is the unique identifier for the invocation request.
	AwsRequestID string
	// LogGroupName is the CloudWatch log group name.
	LogGroupName string
	// LogStreamName is the CloudWatch log stream name.
	LogStreamName string
}

// OperationLifecycleState represents the lifecycle state of an operation.
type OperationLifecycleState string

const (
	OperationLifecycleStatePending   OperationLifecycleState = "PENDING"
	OperationLifecycleStateRunning   OperationLifecycleState = "RUNNING"
	OperationLifecycleStateCompleted OperationLifecycleState = "COMPLETED"
)

// OperationMetadata is metadata attached to a lifecycle state change.
type OperationMetadata struct {
	StepId   string
	Name     *string
	Type     OperationType
	SubType  *OperationSubType
	ParentId *string
}

// OperationInfo tracks the lifecycle state and metadata for an operation.
type OperationInfo struct {
	State    OperationLifecycleState
	Metadata OperationMetadata
}

// WaitStrategyResult controls the polling behavior of waitForCondition.
type WaitStrategyResult struct {
	// ShouldContinue indicates whether polling should continue.
	ShouldContinue bool
	// Delay is the duration to wait before the next poll.
	Delay *Duration
}

// NamedParallelBranch is a named function branch for parallel execution.
type NamedParallelBranch[T any] struct {
	// Name is the branch identifier.
	Name string
	// Fn is the branch function to execute.
	Fn func(dc DurableContext) (T, error)
}

// PromiseSettledResult represents the outcome of a settled promise.
type PromiseSettledResult[T any] struct {
	// Status is either "fulfilled" or "rejected".
	Status string
	// Value is the result if fulfilled.
	Value T
	// Err is the error if rejected.
	Err error
}

// CreateCallbackResult contains the callback promise and its ID.
type CreateCallbackResult[T any] struct {
	// Promise resolves when the external system submits the callback.
	Promise chan callbackOutcome[T]
	// CallbackId is the ID to pass to the external system.
	CallbackId string
}

type callbackOutcome[T any] struct {
	Value T
	Err   error
}

// DurableContext is the main interface passed to durable handlers.
// It provides all methods for durable operations with automatic checkpointing and replay.
type DurableContext interface {
	// LambdaCtx returns the underlying Lambda context.
	LambdaCtx() *LambdaContext
	// Logger returns the logger for this context (mode-aware; silent during replay).
	Logger() Logger
	// ConfigureLogger updates logger configuration.
	ConfigureLogger(config LoggerConfig)
	// Context returns the underlying Go context.Context stored in this DurableContext.
	Context() context.Context

	NextStepID() string
	Checkpoint(stepID string, update OperationUpdate) error
	CheckpointBatch(batch []OperationUpdate) error
	ParentID() string
	// NewChildDurableContext creates a child DurableContext derived from goCtx.
	NewChildDurableContext(goCtx context.Context, prefix string, parentID string, mode DurableExecutionMode) DurableContext
	Mode() DurableExecutionMode
	MarkOperationState(stepID string, state OperationLifecycleState, metadata OperationMetadata)
	MarkAncestorFinished(stepID string)
	IsTerminated() bool
	Terminate(result TerminationResult)
	GetStepData(stepID string) *Operation
	DurableExecutionArn() string
	// ExecutionStartTime returns the start time of the durable execution as recorded
	// by the service, and whether the value is available. Use this instead of
	// time.Now() for replay-safe timestamps (spec §14.3).
	ExecutionStartTime() (time.Time, bool)
}

// StepContext is passed to step functions for I/O inside step callbacks.
type StepContext interface {
	Logger() Logger
	// Context returns the underlying Go context.Context for use with I/O operations.
	Context() context.Context
}

// StepResult is a typed result channel item from a durable operation.
type StepResult struct {
	Value any
	Err   error
}

// CallbackResult is the result of a callback operation.
type CallbackResult[TResult any] struct {
	Value TResult
	Err   error
}

// Logger defines structured logging methods for durable contexts.
type Logger interface {
	Info(message string, fields ...any)
	Warn(message string, fields ...any)
	Error(message string, err error, fields ...any)
	Debug(message string, fields ...any)
}

// LoggerConfig configures logger behavior.
type LoggerConfig struct {
	// CustomLogger replaces the default logger.
	CustomLogger Logger
	// ModeAware controls whether logging is suppressed during replay (default: true).
	ModeAware *bool
}

// StepConfig configures the behavior of a step operation.
type StepConfig struct {
	// RetryStrategy is called on failure to determine whether and when to retry.
	RetryStrategy func(err error, attemptCount int) RetryDecision
	// Semantics controls how the step is checkpointed (default: AtLeastOncePerRetry).
	Semantics StepSemantics
	// Serdes provides custom serialization/deserialization for the step result.
	Serdes Serdes
}

// ChildConfig configures a child context operation.
type ChildConfig struct {
	// SubType provides a custom sub-type label for observability.
	SubType *string
	// Serdes provides custom serialization/deserialization.
	Serdes Serdes
}

// InvokeConfig configures a cross-function invocation.
type InvokeConfig struct {
	// Serdes provides custom serialization/deserialization for input and output.
	Serdes Serdes
}

// CreateCallbackConfig configures a callback creation operation.
type CreateCallbackConfig struct {
	// Timeout is the maximum time to wait for the callback.
	Timeout *Duration
	// Serdes provides custom serialization/deserialization for the callback result.
	Serdes Serdes
}

// WaitForCallbackConfig configures a wait-for-callback operation.
type WaitForCallbackConfig struct {
	// Timeout is the maximum time to wait for the callback.
	Timeout *Duration
	// RetryStrategy controls retry behavior for the submitter function.
	RetryStrategy func(err error, attemptCount int) RetryDecision
	// Serdes provides custom serialization/deserialization.
	Serdes Serdes
}

// WaitForConditionConfig configures a wait-for-condition polling operation.
type WaitForConditionConfig[TState any] struct {
	// InitialState is the starting state for the condition check.
	InitialState TState
	// WaitStrategy determines whether to continue polling and the next delay.
	WaitStrategy func(state TState, attempt int) WaitStrategyResult
	// Serdes provides custom serialization/deserialization.
	Serdes Serdes
}

// MapConfig configures a map batch operation.
type MapConfig struct {
	// MaxConcurrency limits the number of items processed simultaneously (0 = unlimited).
	MaxConcurrency int
	// CompletionConfig configures early completion behavior.
	CompletionConfig *BatchCompletionConfig
	// ItemNamer provides custom names for each map item.
	ItemNamer func(item any, index int) string
	// Serdes provides custom serialization/deserialization.
	Serdes Serdes
}

// ParallelConfig configures a parallel batch operation.
type ParallelConfig struct {
	// MaxConcurrency limits the number of branches executing simultaneously (0 = unlimited).
	MaxConcurrency int
	// CompletionConfig configures early completion behavior.
	CompletionConfig *BatchCompletionConfig
	// Serdes provides custom serialization/deserialization.
	Serdes Serdes
}

// Serdes provides custom serialization and deserialization for durable operations.
type Serdes interface {
	// Serialize converts a value to a string for storage.
	Serialize(value any, entityID string, executionArn string) (string, error)
	// Deserialize converts a stored string back to a value.
	Deserialize(data string, entityID string, executionArn string) (any, error)
}
