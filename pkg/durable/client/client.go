package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// SDK type aliases – these are direct references to the AWS Lambda SDK types,
// eliminating any need for conversion between our types and the SDK types.
type (
	Operation       = types.Operation
	OperationUpdate = types.OperationUpdate
	OperationType   = types.OperationType
	OperationStatus = types.OperationStatus
	OperationAction = types.OperationAction
	// OperationSubType is a string subtype that further categorises an operation.
	// The Lambda SDK represents this as a plain *string field (SubType).
	OperationSubType     = string
	ErrorObject          = types.ErrorObject
	ExecutionDetails     = types.ExecutionDetails
	StepDetails          = types.StepDetails
	WaitDetails          = types.WaitDetails
	CallbackDetails      = types.CallbackDetails
	ChainedInvokeDetails = types.ChainedInvokeDetails
	ContextDetails       = types.ContextDetails
	// Option types used when building OperationUpdate values.
	StepOptions          = types.StepOptions
	WaitOptions          = types.WaitOptions
	CallbackOptions      = types.CallbackOptions
	ChainedInvokeOptions = types.ChainedInvokeOptions
	ContextOptions       = types.ContextOptions
)

// OperationType constants re-exported from the SDK.
const (
	OperationTypeExecution     = types.OperationTypeExecution
	OperationTypeContext       = types.OperationTypeContext
	OperationTypeStep          = types.OperationTypeStep
	OperationTypeWait          = types.OperationTypeWait
	OperationTypeCallback      = types.OperationTypeCallback
	OperationTypeChainedInvoke = types.OperationTypeChainedInvoke
)

// OperationStatus constants re-exported from the SDK.
const (
	OperationStatusStarted   = types.OperationStatusStarted
	OperationStatusPending   = types.OperationStatusPending
	OperationStatusReady     = types.OperationStatusReady
	OperationStatusSucceeded = types.OperationStatusSucceeded
	OperationStatusFailed    = types.OperationStatusFailed
	OperationStatusCancelled = types.OperationStatusCancelled
	OperationStatusTimedOut  = types.OperationStatusTimedOut
	OperationStatusStopped   = types.OperationStatusStopped
)

// OperationAction constants re-exported from the SDK.
const (
	OperationActionStart   = types.OperationActionStart
	OperationActionSucceed = types.OperationActionSucceed
	OperationActionFail    = types.OperationActionFail
	OperationActionRetry   = types.OperationActionRetry
	OperationActionCancel  = types.OperationActionCancel
)

// OperationSubType string constants. The Lambda SDK exposes SubType as a
// plain *string; these constants provide well-known values.
const (
	OperationSubTypeStep              OperationSubType = "STEP"
	OperationSubTypeWait              OperationSubType = "WAIT"
	OperationSubTypeCallback          OperationSubType = "CALLBACK"
	OperationSubTypeRunInChildContext OperationSubType = "RUN_IN_CHILD_CONTEXT"
	OperationSubTypeMap               OperationSubType = "MAP"
	OperationSubTypeMapIteration      OperationSubType = "MAP_ITERATION"
	OperationSubTypeParallel          OperationSubType = "PARALLEL"
	OperationSubTypeParallelBranch    OperationSubType = "PARALLEL_BRANCH"
	OperationSubTypeWaitForCallback   OperationSubType = "WAIT_FOR_CALLBACK"
	OperationSubTypeWaitForCondition  OperationSubType = "WAIT_FOR_CONDITION"
	OperationSubTypeChainedInvoke     OperationSubType = "CHAINED_INVOKE"
)

// DurableExecutionEvent represents the event payload passed to a durable Lambda function.
// This matches the structure used by the JavaScript SDK.
type DurableExecutionEvent struct {
	CheckpointToken       string                `json:"CheckpointToken"`
	DurableExecutionArn   string                `json:"DurableExecutionArn"`
	InitialExecutionState InitialExecutionState `json:"InitialExecutionState"`
}

// InitialExecutionState represents the initial state loaded from Lambda.
type InitialExecutionState struct {
	Operations []Operation `json:"Operations"`
	NextMarker string      `json:"NextMarker,omitempty"`
}

// GetExecutionOperation returns the execution operation from the initial state.
// Due to payload size limitations, the operations list may be empty when loading
// the initial page of results. This is expected behavior, and the function returns
// nil (not an error) in this case. The execution operation will be available after
// loading subsequent pages via NextMarker.
func (i *InitialExecutionState) GetExecutionOperation() (*Operation, error) {
	if len(i.Operations) == 0 {
		// No operations found in initial execution state.
		// This can happen due to payload size limitations and is expected behavior.
		return nil, nil
	}

	if i.Operations[0].Type != OperationTypeExecution {
		return nil, fmt.Errorf("first operation is not an execution operation: %s", i.Operations[0].Type)
	}

	return &i.Operations[0], nil
}

// GetInputPayload extracts the input payload from the execution operation.
// Returns nil if the execution operation is not available (e.g., due to payload
// size limitations) or if the operation has no execution details.
func (i *InitialExecutionState) GetInputPayload() (*string, error) {
	// It is possible that backend will not provide an execution operation
	// for the initial page of results due to payload size limitations.
	op, err := i.GetExecutionOperation()
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, nil
	}

	if op.ExecutionDetails != nil {
		return op.ExecutionDetails.InputPayload, nil
	}

	return nil, nil
}

// CheckpointOutput represents the result of a checkpoint operation.
type CheckpointOutput struct {
	// Empty for now, may contain metrics or status in the future
}

// StateOutput represents the result of getting execution state.
type StateOutput struct {
	Operations []Operation
	NextMarker string
}

type DurableClient interface {
	Invoke(ctx context.Context, input *lambda.InvokeInput, fnOpts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
	GetDurableExecutionState(ctx context.Context, input *lambda.GetDurableExecutionStateInput, fnOpts ...func(*lambda.Options)) (*lambda.GetDurableExecutionStateOutput, error)
	CheckpointDurableExecution(ctx context.Context, input *lambda.CheckpointDurableExecutionInput, fnOpts ...func(*lambda.Options)) (*lambda.CheckpointDurableExecutionOutput, error)
}

// LambdaServiceClient defines the interface for Lambda service operations.
type LambdaServiceClient interface {
	GetDurableExecutionState(ctx context.Context, marker string) (*StateOutput, error)
	CheckpointDurableExecution(ctx context.Context, updates []OperationUpdate) (*CheckpointOutput, error)
	InvokeFunction(ctx context.Context, functionArn string, payload []byte) ([]byte, error)
}

// Client implements LambdaServiceClient using AWS SDK v2.
type Client struct {
	client              DurableClient
	durableExecutionArn string
	checkpointToken     string
	tokenMu             sync.RWMutex
}

// NewDurableClient creates a new durable client using AWS SDK.
func NewDurableClient(ctx context.Context) (DurableClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return lambda.NewFromConfig(cfg), nil
}

// NewLambdaServiceClient creates a new Lambda service client.
func NewLambdaServiceClient(client DurableClient, durableExecutionArn, checkpointToken string) *Client {
	return &Client{
		client:              client,
		durableExecutionArn: durableExecutionArn,
		checkpointToken:     checkpointToken,
	}
}

// GetDurableExecutionState retrieves the current execution state.
func (c *Client) GetDurableExecutionState(ctx context.Context, marker string) (*StateOutput, error) {
	c.tokenMu.RLock()
	token := c.checkpointToken
	c.tokenMu.RUnlock()

	input := &lambda.GetDurableExecutionStateInput{
		DurableExecutionArn: aws.String(c.durableExecutionArn),
		CheckpointToken:     aws.String(token),
	}

	if marker != "" {
		input.Marker = aws.String(marker)
	}

	output, err := c.client.GetDurableExecutionState(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get durable execution state: %w", err)
	}

	nextMarker := ""
	if output.NextMarker != nil {
		nextMarker = *output.NextMarker
	}

	return &StateOutput{
		Operations: output.Operations,
		NextMarker: nextMarker,
	}, nil
}

// CheckpointDurableExecution creates a checkpoint with operation updates.
func (c *Client) CheckpointDurableExecution(ctx context.Context, updates []OperationUpdate) (*CheckpointOutput, error) {
	c.tokenMu.RLock()
	token := c.checkpointToken
	c.tokenMu.RUnlock()

	input := &lambda.CheckpointDurableExecutionInput{
		DurableExecutionArn: aws.String(c.durableExecutionArn),
		CheckpointToken:     aws.String(token),
		Updates:             updates,
	}

	output, err := c.client.CheckpointDurableExecution(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to checkpoint durable execution: %w", err)
	}

	// Update checkpoint token for next checkpoint
	if output.CheckpointToken != nil {
		c.tokenMu.Lock()
		c.checkpointToken = aws.ToString(output.CheckpointToken)
		c.tokenMu.Unlock()
	}

	return &CheckpointOutput{}, nil
}

// InvokeFunction invokes another Lambda function.
func (c *Client) InvokeFunction(ctx context.Context, functionArn string, payload []byte) ([]byte, error) {
	input := &lambda.InvokeInput{
		FunctionName: aws.String(functionArn),
		Payload:      payload,
	}

	output, err := c.client.Invoke(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke function: %w", err)
	}

	if output.FunctionError != nil {
		return nil, fmt.Errorf("function returned error: %s (status: %d)", *output.FunctionError, output.StatusCode)
	}

	return output.Payload, nil
}
