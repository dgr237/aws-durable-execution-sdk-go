// Package errors defines error types for the AWS Durable Execution SDK.
package errors

import (
	"errors"
	"fmt"
	"strings"
)

// DurableError is the base error type for all durable execution errors.
type DurableError struct {
	// Message is the human-readable error message.
	Message string
	// OperationID is the step ID where the error occurred.
	OperationID string
	// OperationName is the optional step name where the error occurred.
	OperationName *string
	// Cause is the underlying error that triggered this durable error.
	Cause error
}

func (e *DurableError) Error() string {
	name := ""
	if e.OperationName != nil {
		name = fmt.Sprintf(" (name: %s)", *e.OperationName)
	}
	if e.Cause != nil {
		return fmt.Sprintf("durable error in operation %s%s: %s: %v", e.OperationID, name, e.Message, e.Cause)
	}
	return fmt.Sprintf("durable error in operation %s%s: %s", e.OperationID, name, e.Message)
}

func (e *DurableError) Unwrap() error { return e.Cause }

// StepError is returned when a step fails after all retry attempts are exhausted.
type StepError struct {
	*DurableError
}

func NewStepError(stepID string, name *string, cause error) *StepError {
	return &StepError{
		DurableError: &DurableError{
			Message:       "step failed",
			OperationID:   stepID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *StepError) Error() string {
	return fmt.Sprintf("StepError: %s", e.DurableError.Error())
}

// CallbackError is returned when a callback fails, times out, or is rejected.
type CallbackError struct {
	*DurableError
}

func NewCallbackError(operationID string, name *string, cause error) *CallbackError {
	return &CallbackError{
		DurableError: &DurableError{
			Message:       "callback failed",
			OperationID:   operationID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *CallbackError) Error() string {
	return fmt.Sprintf("CallbackError: %s", e.DurableError.Error())
}

// ParallelError is returned when a parallel step fails, times out, or is rejected.
type ParallelError struct {
	*DurableError
}

func NewParallelError(operationID string, name *string, cause error) *ParallelError {
	return &ParallelError{
		DurableError: &DurableError{
			Message:       "parallel failed",
			OperationID:   operationID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *ParallelError) Error() string {
	return fmt.Sprintf("ParallelError: %s", e.DurableError.Error())
}

// ChildContextError is returned when a child context function fails.
type ChildContextError struct {
	*DurableError
}

func NewChildContextError(operationID string, name *string, cause error) *ChildContextError {
	return &ChildContextError{
		DurableError: &DurableError{
			Message:       "child context failed",
			OperationID:   operationID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *ChildContextError) Error() string {
	return fmt.Sprintf("ChildContextError: %s", e.DurableError.Error())
}

// InvokeError is returned when an invoked Lambda function fails.
type InvokeError struct {
	*DurableError
}

func NewInvokeError(operationID string, name *string, cause error) *InvokeError {
	return &InvokeError{
		DurableError: &DurableError{
			Message:       "invoke failed",
			OperationID:   operationID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *InvokeError) Error() string {
	return fmt.Sprintf("InvokeError: %s", e.DurableError.Error())
}

// WaitConditionError is returned when a wait-for-condition operation fails.
type WaitConditionError struct {
	*DurableError
}

func NewWaitConditionError(operationID string, name *string, cause error) *WaitConditionError {
	return &WaitConditionError{
		DurableError: &DurableError{
			Message:       "wait condition failed",
			OperationID:   operationID,
			OperationName: name,
			Cause:         cause,
		},
	}
}

func (e *WaitConditionError) Error() string {
	return fmt.Sprintf("WaitConditionError: %s", e.DurableError.Error())
}

// NonDeterministicError is returned when replay detects a non-deterministic operation sequence.
type NonDeterministicError struct {
	Message string
}

func (e *NonDeterministicError) Error() string {
	return fmt.Sprintf("NonDeterministicError: %s", e.Message)
}

// UnrecoverableError marks an error that should terminate the execution and not be retried.
// Wrap any error with this to signal that the Lambda function should terminate.
type UnrecoverableError struct {
	Cause error
}

func NewUnrecoverableError(cause error) *UnrecoverableError {
	return &UnrecoverableError{Cause: cause}
}

func (e *UnrecoverableError) Error() string {
	return fmt.Sprintf("UnrecoverableError: %v", e.Cause)
}

func (e *UnrecoverableError) Unwrap() error { return e.Cause }

// IsUnrecoverableError returns true if the error is or wraps an UnrecoverableError.
func IsUnrecoverableError(err error) bool {
	var unrecoverable *UnrecoverableError
	return errors.As(err, &unrecoverable)
}

// CheckpointUnrecoverableInvocationError is returned when a checkpoint fails in a way that
// requires Lambda termination (not just FAILED status).
type CheckpointUnrecoverableInvocationError struct {
	Cause error
}

func (e *CheckpointUnrecoverableInvocationError) Error() string {
	return fmt.Sprintf("CheckpointUnrecoverableInvocationError: %v", e.Cause)
}

func (e *CheckpointUnrecoverableInvocationError) Unwrap() error { return e.Cause }

// CheckpointUnrecoverableExecutionError is returned when a checkpoint fails due to an
// execution-level error that should mark the execution as FAILED.
type CheckpointUnrecoverableExecutionError struct {
	Cause error
}

func (e *CheckpointUnrecoverableExecutionError) Error() string {
	return fmt.Sprintf("CheckpointUnrecoverableExecutionError: %v", e.Cause)
}

func (e *CheckpointUnrecoverableExecutionError) Unwrap() error { return e.Cause }

// IsUnrecoverableInvocationError returns true if the error should cause Lambda termination.
func IsUnrecoverableInvocationError(err error) bool {
	var invErr *CheckpointUnrecoverableInvocationError
	if errors.As(err, &invErr) {
		return true
	}
	var unrecoverable *UnrecoverableError
	return errors.As(err, &unrecoverable)
}

// SerdesError is returned when serialization or deserialization fails.
type SerdesError struct {
	Message   string
	StepID    string
	StepName  *string
	Operation string // "serialize" or "deserialize"
	Cause     error
}

func (e *SerdesError) Error() string {
	name := ""
	if e.StepName != nil {
		name = fmt.Sprintf(" (name: %s)", *e.StepName)
	}
	return fmt.Sprintf("SerdesError: %s during %s for step %s%s: %v",
		e.Message, e.Operation, e.StepID, name, e.Cause)
}

func (e *SerdesError) Unwrap() error { return e.Cause }

// SerdesFailedError is a fatal serdes error that should terminate the Lambda.
type SerdesFailedError struct {
	Message string
}

func (e *SerdesFailedError) Error() string {
	return fmt.Sprintf("SerdesFailedError: %s", e.Message)
}

// StepInterruptedError is returned when a step is interrupted during execution.
type StepInterruptedError struct {
	StepID string
}

func (e *StepInterruptedError) Error() string {
	return fmt.Sprintf("StepInterruptedError: step %s was interrupted", e.StepID)
}

// TerminatedError is returned when execution is terminated for a checkpoint.
// This error signals that the operation has been suspended and will resume on the next invocation.
type TerminatedError struct {
	Message string
}

func (e *TerminatedError) Error() string {
	return fmt.Sprintf("TerminatedError: %s", e.Message)
}

// AggregateError collects multiple errors from a batch operation.
// Returned by combinators such as All and Any when all branches fail.
type AggregateError struct {
	Errors []error
}

func NewAggregateError(errs []error) *AggregateError {
	return &AggregateError{Errors: errs}
}

func (e *AggregateError) Error() string {
	return fmt.Sprintf("AggregateError: %d error(s): %v", len(e.Errors), e.Errors)
}

func (e *AggregateError) Unwrap() []error { return e.Errors }

// ErrorFromObject creates a Go error from a serialized error object.
func ErrorFromObject(msg string, cause error) error {
	return fmt.Errorf("%s: %w", msg, cause)
}

// runtimeErrorPrefixes are the Lambda error-type prefixes for errors that occur
// outside the handler context and are automatically retried by Lambda (spec §8.1).
var runtimeErrorPrefixes = []string{"Runtime.", "Sandbox.", "Extension."}

// sandboxTimedOut has the Sandbox. prefix but is classified as a handler error
// by Lambda and is therefore NOT automatically retried (spec §8.1).
const sandboxTimedOut = "Sandbox.Timedout"

// IsRuntimeError reports whether errorType identifies a Lambda runtime/platform
// error. These are prefixed with "Runtime.", "Sandbox.", or "Extension." and are
// automatically retried by Lambda up to 3 times; the SDK is not involved in their
// handling. Exception: "Sandbox.Timedout" is treated as a handler error.
func IsRuntimeError(errorType string) bool {
	if errorType == sandboxTimedOut {
		return false
	}
	for _, prefix := range runtimeErrorPrefixes {
		if strings.HasPrefix(errorType, prefix) {
			return true
		}
	}
	return false
}
