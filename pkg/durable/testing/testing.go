package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	durableconfig "github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/state"
)

// InvocationStatus represents the status of a test invocation.
type InvocationStatus string

const (
	StatusSucceeded InvocationStatus = "SUCCEEDED"
	StatusFailed    InvocationStatus = "FAILED"
	StatusSuspended InvocationStatus = "SUSPENDED"
)

// OperationResult represents the result of an operation in a test.
type OperationResult struct {
	OperationID   string
	OperationName string
	Type          client.OperationType
	Status        client.OperationStatus
	Result        interface{}
	Error         error
}

// ExecutionResult represents the result of a test execution.
type ExecutionResult struct {
	status     InvocationStatus
	result     interface{}
	err        error
	operations map[string]*OperationResult
	mu         sync.RWMutex
}

// Status returns the invocation status.
func (e *ExecutionResult) Status() InvocationStatus {
	return e.status
}

// Result returns the execution result.
func (e *ExecutionResult) Result() interface{} {
	return e.result
}

// Error returns the execution error if any.
func (e *ExecutionResult) Error() error {
	return e.err
}

// GetOperation retrieves an operation by name.
func (e *ExecutionResult) GetOperation(name string) (*OperationResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, op := range e.operations {
		if op.OperationName == name {
			return op, nil
		}
	}

	return nil, fmt.Errorf("operation not found: %s", name)
}

// GetOperationByID retrieves an operation by ID.
func (e *ExecutionResult) GetOperationByID(id string) (*OperationResult, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if op, exists := e.operations[id]; exists {
		return op, nil
	}

	return nil, fmt.Errorf("operation not found: %s", id)
}

// AllOperations returns all operations.
func (e *ExecutionResult) AllOperations() []*OperationResult {
	e.mu.RLock()
	defer e.mu.RUnlock()

	ops := make([]*OperationResult, 0, len(e.operations))
	for _, op := range e.operations {
		ops = append(ops, op)
	}
	return ops
}

// RunConfig configures a test run.
type RunConfig struct {
	Payload interface{}
	Timeout int // seconds
}

// LocalDurableTestRunnerConfig configures the test runner.
type LocalDurableTestRunnerConfig struct {
	SkipTime bool // If true, time-based operations complete immediately
}

// LocalDurableTestRunner runs durable functions in a local test environment.
type LocalDurableTestRunner struct {
	handler durable.HandlerFunc
	config  LocalDurableTestRunnerConfig
	client  *mockLambdaClient
}

// NewLocalDurableTestRunner creates a new test runner.
func NewLocalDurableTestRunner(handler durable.HandlerFunc, config LocalDurableTestRunnerConfig) *LocalDurableTestRunner {
	return &LocalDurableTestRunner{
		handler: handler,
		config:  config,
		client:  newMockLambdaClient(),
	}
}

// Run executes the handler in test mode.
func (r *LocalDurableTestRunner) Run(config RunConfig) (*ExecutionResult, error) {
	// Serialize payload
	payloadJSON, err := json.Marshal(config.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create initial state
	initialState := client.InitialExecutionState{
		Operations: []client.Operation{},
	}

	// Create execution state
	executionState := state.NewExecutionState(context.Background(), r.client, initialState)
	defer executionState.Close()

	// Create context
	executionContext := durablecontext.ExecutionContext{
		DurableExecutionArn: "arn:aws:lambda:us-east-1:123456789012:function:test:$LATEST",
	}

	logger := durablecontext.NewSimpleLogger()
	durableCtx := durablecontext.NewDurableContext(context.Background(), executionState, logger, executionContext, durableconfig.DefaultSerDes())

	// Execute handler
	result, err := r.handler(durableCtx)

	// Create execution result
	execResult := &ExecutionResult{
		result:     result,
		err:        err,
		operations: make(map[string]*OperationResult),
	}

	if err != nil {
		if durable.IsSuspendExecution(err) {
			execResult.status = StatusSuspended
		} else {
			execResult.status = StatusFailed
		}
	} else {
		execResult.status = StatusSucceeded
	}

	// Collect operations from mock client
	for id, op := range r.client.operations {
		opResult := &OperationResult{
			OperationID:   id,
			OperationName: getOperationName(op),
			Type:          op.Type,
			Status:        op.Status,
		}

		// Extract result based on operation type
		switch op.Type {
		case client.OperationTypeStep:
			if op.StepDetails != nil {
				if op.StepDetails.Result != nil {
					var value interface{}
					_ = json.Unmarshal([]byte(*op.StepDetails.Result), &value)
					opResult.Result = value
				}
				if op.StepDetails.Error != nil {
					opResult.Error = fmt.Errorf("%s: %s", aws.ToString(op.StepDetails.Error.ErrorType), aws.ToString(op.StepDetails.Error.ErrorMessage))
				}
			}
		}

		execResult.operations[id] = opResult
	}

	_ = payloadJSON // Use payload if needed

	return execResult, nil
}

// GetOperation retrieves an operation result by name.
func (r *LocalDurableTestRunner) GetOperation(name string) (*OperationResult, error) {
	for _, op := range r.client.operations {
		if getOperationName(op) == name {
			return &OperationResult{
				OperationID:   aws.ToString(op.Id),
				OperationName: getOperationName(op),
				Type:          op.Type,
				Status:        op.Status,
			}, nil
		}
	}
	return nil, fmt.Errorf("operation not found: %s", name)
}

func getOperationName(op client.Operation) string {
	if op.Name != nil {
		return *op.Name
	}
	return ""
}

// mockLambdaClient is a mock Lambda client for testing.
type mockLambdaClient struct {
	operations map[string]client.Operation
	updates    []client.OperationUpdate
	mu         sync.RWMutex
}

func newMockLambdaClient() *mockLambdaClient {
	return &mockLambdaClient{
		operations: make(map[string]client.Operation),
		updates:    make([]client.OperationUpdate, 0),
	}
}

func (m *mockLambdaClient) GetDurableExecutionState(ctx context.Context, marker string) (*client.StateOutput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ops := make([]client.Operation, 0, len(m.operations))
	for _, op := range m.operations {
		ops = append(ops, op)
	}

	return &client.StateOutput{
		Operations: ops,
		NextMarker: marker,
	}, nil
}

func (m *mockLambdaClient) CheckpointDurableExecution(ctx context.Context, updates []client.OperationUpdate) (*client.CheckpointOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, update := range updates {
		m.updates = append(m.updates, update)

		// Update or create operation
		id := aws.ToString(update.Id)
		if existing, exists := m.operations[id]; exists {
			m.operations[id] = applyUpdateToOp(existing, update)
		} else {
			m.operations[id] = createOpFromUpdate(update)
		}
	}

	return &client.CheckpointOutput{}, nil
}

func (m *mockLambdaClient) InvokeFunction(ctx context.Context, functionArn string, payload []byte) ([]byte, error) {
	return []byte(`{"success": true}`), nil
}

func applyUpdateToOp(op client.Operation, update client.OperationUpdate) client.Operation {
	switch update.Action {
	case client.OperationActionStart:
		op.Status = client.OperationStatusStarted
	case client.OperationActionSucceed:
		op.Status = client.OperationStatusSucceeded
	case client.OperationActionFail:
		op.Status = client.OperationStatusFailed
	}

	if update.Payload != nil {
		switch update.Type {
		case client.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &client.StepDetails{}
			}
			op.StepDetails.Result = update.Payload
		}
	}
	if update.Error != nil {
		switch update.Type {
		case client.OperationTypeStep:
			if op.StepDetails == nil {
				op.StepDetails = &client.StepDetails{}
			}
			op.StepDetails.Error = update.Error
		}
	}
	if update.WaitOptions != nil {
		if op.WaitDetails == nil {
			op.WaitDetails = &client.WaitDetails{}
		}
	}

	return op
}

func createOpFromUpdate(update client.OperationUpdate) client.Operation {
	var status client.OperationStatus
	switch update.Action {
	case client.OperationActionStart:
		status = client.OperationStatusStarted
	case client.OperationActionSucceed:
		status = client.OperationStatusSucceeded
	case client.OperationActionFail:
		status = client.OperationStatusFailed
	default:
		status = client.OperationStatusStarted
	}

	op := client.Operation{
		Id:      update.Id,
		Name:    update.Name,
		Type:    update.Type,
		Status:  status,
		SubType: update.SubType,
	}
	if update.Payload != nil && update.Type == client.OperationTypeStep {
		op.StepDetails = &client.StepDetails{Result: update.Payload}
	}
	if update.Error != nil && update.Type == client.OperationTypeStep {
		if op.StepDetails == nil {
			op.StepDetails = &client.StepDetails{}
		}
		op.StepDetails.Error = update.Error
	}
	if update.WaitOptions != nil {
		op.WaitDetails = &client.WaitDetails{}
	}

	return op
}
