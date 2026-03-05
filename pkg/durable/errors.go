package durable

import "fmt"

// DurableExecutionsError is the base error type for all SDK errors.
type DurableExecutionsError struct {
	Message string
	Cause   error
}

func (e *DurableExecutionsError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *DurableExecutionsError) Unwrap() error {
	return e.Cause
}

// ValidationError indicates invalid input or configuration.
type ValidationError struct {
	Message string
	Cause   error
}

func (e *ValidationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("validation error: %s: %v", e.Message, e.Cause)
	}
	return fmt.Sprintf("validation error: %s", e.Message)
}

func (e *ValidationError) Unwrap() error {
	return e.Cause
}

// InvocationError indicates a failure during function invocation.
type InvocationError struct {
	Message      string
	FunctionArn  string
	Cause        error
	ErrorType    string
	ErrorMessage string
	StatusCode   int
}

func (e *InvocationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("invocation error [%s]: %s: %v", e.FunctionArn, e.Message, e.Cause)
	}
	return fmt.Sprintf("invocation error [%s]: %s", e.FunctionArn, e.Message)
}

func (e *InvocationError) Unwrap() error {
	return e.Cause
}

// CallableRuntimeError indicates a failure in user-provided callable.
type CallableRuntimeError struct {
	Message       string
	OperationName string
	Cause         error
}

func (e *CallableRuntimeError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("callable error [%s]: %s: %v", e.OperationName, e.Message, e.Cause)
	}
	return fmt.Sprintf("callable error [%s]: %s", e.OperationName, e.Message)
}

func (e *CallableRuntimeError) Unwrap() error {
	return e.Cause
}

// StepInterruptedError indicates an AT_MOST_ONCE step was interrupted.
type StepInterruptedError struct {
	Message       string
	OperationName string
}

func (e *StepInterruptedError) Error() string {
	return fmt.Sprintf("step interrupted [%s]: %s", e.OperationName, e.Message)
}

// CheckpointError indicates a failure during checkpointing.
type CheckpointError struct {
	Message string
	Cause   error
}

func (e *CheckpointError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("checkpoint error: %s: %v", e.Message, e.Cause)
	}
	return fmt.Sprintf("checkpoint error: %s", e.Message)
}

func (e *CheckpointError) Unwrap() error {
	return e.Cause
}

// ExecutionError indicates a general execution failure.
type ExecutionError struct {
	Message string
	Cause   error
}

func (e *ExecutionError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("execution error: %s: %v", e.Message, e.Cause)
	}
	return fmt.Sprintf("execution error: %s", e.Message)
}

func (e *ExecutionError) Unwrap() error {
	return e.Cause
}

// CallbackError indicates a callback-related failure.
type CallbackError struct {
	Message string
	Cause   error
}

func (e *CallbackError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("callback error: %s: %v", e.Message, e.Cause)
	}
	return fmt.Sprintf("callback error: %s", e.Message)
}

func (e *CallbackError) Unwrap() error {
	return e.Cause
}

// SuspendExecutionError indicates execution should be suspended.
type SuspendExecutionError struct {
	Message string
}

func (e *SuspendExecutionError) Error() string {
	return fmt.Sprintf("suspend execution: %s", e.Message)
}

// IsSuspendExecution checks if an error is a suspension signal.
func IsSuspendExecution(err error) bool {
	_, ok := err.(*SuspendExecutionError)
	return ok
}
