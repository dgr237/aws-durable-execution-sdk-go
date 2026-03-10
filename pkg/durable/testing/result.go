package durabletest

import (
	"encoding/json"
	"fmt"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ExecutionStatus is the final status of a test execution.
type ExecutionStatus string

const (
	ExecutionStatusSucceeded ExecutionStatus = "SUCCEEDED"
	ExecutionStatusFailed    ExecutionStatus = "FAILED"
)

// Invocation captures metadata for a single handler invocation during the test.
type Invocation struct {
	// Index is the zero-based invocation number (0 = first, 1 = first re-invocation, …).
	Index int
	// Error is non-nil if the Lambda handler itself returned an error (distinct from
	// a FAILED execution status, which is a normal durable outcome).
	Error error
}

// TestResultError contains error information from a failed execution.
type TestResultError struct {
	ErrorType    string
	ErrorMessage string
}

func (e *TestResultError) Error() string {
	if e.ErrorType != "" {
		return e.ErrorType + ": " + e.ErrorMessage
	}
	return e.ErrorMessage
}

// TestResult holds the final outcome of a durable execution test run.
type TestResult[TResult any] struct {
	status      ExecutionStatus
	result      *TResult
	resultErr   *TestResultError
	operations  []*DurableOperation
	invocations []Invocation
}

// Status returns the final execution status (SUCCEEDED or FAILED).
func (r *TestResult[TResult]) Status() ExecutionStatus { return r.status }

// GetResult returns the handler's result value.
// Returns an error if the execution failed.
func (r *TestResult[TResult]) GetResult() (TResult, error) {
	if r.status != ExecutionStatusSucceeded {
		var zero TResult
		return zero, fmt.Errorf("execution failed: %s", r.resultErr)
	}
	if r.result == nil {
		var zero TResult
		return zero, nil
	}
	return *r.result, nil
}

// GetError returns the error from a failed execution, or nil if succeeded.
func (r *TestResult[TResult]) GetError() *TestResultError { return r.resultErr }

// GetOperations returns all non-EXECUTION operations in checkpoint order.
func (r *TestResult[TResult]) GetOperations() []*DurableOperation { return r.operations }

// GetOperation returns the first operation with the given name, or nil if not found.
func (r *TestResult[TResult]) GetOperation(name string) *DurableOperation {
	for _, op := range r.operations {
		if op.Name() == name {
			return op
		}
	}
	return nil
}

// GetOperationsByName returns all operations with the given name.
func (r *TestResult[TResult]) GetOperationsByName(name string) []*DurableOperation {
	var out []*DurableOperation
	for _, op := range r.operations {
		if op.Name() == name {
			out = append(out, op)
		}
	}
	return out
}

// GetOperationByIndex returns the operation at the given zero-based index
// (in checkpoint order, excluding the EXECUTION operation).
func (r *TestResult[TResult]) GetOperationByIndex(i int) *DurableOperation {
	if i < 0 || i >= len(r.operations) {
		return nil
	}
	return r.operations[i]
}

// GetInvocations returns metadata for each Lambda invocation made during the test.
func (r *TestResult[TResult]) GetInvocations() []Invocation { return r.invocations }

// ---- internal builders -----------------------------------------------------

func buildSucceededResult[TResult any](
	ops []*DurableOperation,
	invocations []Invocation,
	resultStr *string,
) *TestResult[TResult] {
	var val TResult
	if resultStr != nil && *resultStr != "" {
		_ = json.Unmarshal([]byte(*resultStr), &val)
	}
	return &TestResult[TResult]{
		status:      ExecutionStatusSucceeded,
		result:      &val,
		operations:  ops,
		invocations: invocations,
	}
}

func buildFailedResult[TResult any](
	ops []*DurableOperation,
	invocations []Invocation,
	errObj *types.ErrorObject,
	fallbackMsg string,
) *TestResult[TResult] {
	var testErr *TestResultError
	if errObj != nil {
		testErr = &TestResultError{
			ErrorType:    errObj.ErrorType,
			ErrorMessage: errObj.ErrorMessage,
		}
	} else if fallbackMsg != "" {
		testErr = &TestResultError{ErrorMessage: fallbackMsg}
	}
	return &TestResult[TResult]{
		status:      ExecutionStatusFailed,
		resultErr:   testErr,
		operations:  ops,
		invocations: invocations,
	}
}
