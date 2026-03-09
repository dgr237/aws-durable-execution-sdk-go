// Package client provides the AWS SDK Lambda client adapter for the
// durable execution backend.
package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdaTypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/durable-execution-sdk-go/types"
)

// LambdaClient wraps the AWS Lambda SDK client and implements the checkpoint.Client interface.
type LambdaClient struct {
	lambda *lambda.Client
}

// NewDefaultLambdaClient creates a LambdaClient using the default AWS configuration.
func NewDefaultLambdaClient(ctx context.Context) (*LambdaClient, error) {
	cfg, err := awsConfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}
	return &LambdaClient{lambda: lambda.NewFromConfig(cfg)}, nil
}

// NewLambdaClientFromConfig creates a LambdaClient from an existing AWS config.
func NewLambdaClientFromConfig(cfg aws.Config) *LambdaClient {
	return &LambdaClient{lambda: lambda.NewFromConfig(cfg)}
}

// Checkpoint sends a checkpoint request to the Lambda durable execution service.
// It uses the Lambda CheckpointDurableExecution API.
func (c *LambdaClient) Checkpoint(ctx context.Context, params types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error) {
	// Convert local OperationUpdate types to AWS SDK types
	updates := make([]lambdaTypes.OperationUpdate, len(params.Operations))
	for i, op := range params.Operations {
		updates[i] = convertToSDKOperationUpdate(op)
	}

	// Call the AWS Lambda CheckpointDurableExecution API
	resp, err := c.lambda.CheckpointDurableExecution(ctx, &lambda.CheckpointDurableExecutionInput{
		DurableExecutionArn: aws.String(params.DurableExecutionArn),
		CheckpointToken:     aws.String(params.CheckpointToken),
		Updates:             updates,
	})
	if err != nil {
		return nil, fmt.Errorf("checkpoint failed: %w", err)
	}

	return &types.CheckpointDurableExecutionResponse{
		NextCheckpointToken: resp.CheckpointToken,
	}, nil
}

// convertToSDKOperationUpdate converts local OperationUpdate to AWS SDK OperationUpdate
func convertToSDKOperationUpdate(op types.OperationUpdate) lambdaTypes.OperationUpdate {
	update := lambdaTypes.OperationUpdate{
		Id:                   aws.String(op.Id),
		Action:               lambdaTypes.OperationAction(op.Action),
		Type:                 lambdaTypes.OperationType(op.Type),
		Name:                 op.Name,
		ParentId:             op.ParentId,
		Payload:              op.Payload,
		Error:                convertToSDKErrorObject(op.Error),
		CallbackOptions:      convertToSDKCallbackOptions(op.CallbackOptions),
		ChainedInvokeOptions: convertToSDKChainedInvokeOptions(op.ChainedInvokeOptions),
		ContextOptions:       convertToSDKContextOptions(op.ContextOptions),
		StepOptions:          convertToSDKStepOptions(op.StepOptions),
		WaitOptions:          convertToSDKWaitOptions(op.WaitOptions),
	}

	// Convert SubType if present
	if op.SubType != nil {
		subType := string(*op.SubType)
		update.SubType = &subType
	}

	return update
}

// convertToSDKCallbackOptions converts local CallbackOptions to AWS SDK CallbackOptions
func convertToSDKCallbackOptions(opts *types.CallbackOptions) *lambdaTypes.CallbackOptions {
	if opts == nil {
		return nil
	}
	return &lambdaTypes.CallbackOptions{
		TimeoutSeconds:          opts.TimeoutSeconds,
		HeartbeatTimeoutSeconds: opts.HeartbeatTimeoutSeconds,
	}
}

// convertToSDKChainedInvokeOptions converts local ChainedInvokeOptions to AWS SDK ChainedInvokeOptions
func convertToSDKChainedInvokeOptions(opts *types.ChainedInvokeOptions) *lambdaTypes.ChainedInvokeOptions {
	if opts == nil {
		return nil
	}
	return &lambdaTypes.ChainedInvokeOptions{
		FunctionName: opts.FunctionName,
		TenantId:     opts.TenantId,
	}
}

// convertToSDKContextOptions converts local ContextOptions to AWS SDK ContextOptions
func convertToSDKContextOptions(opts *types.ContextOptions) *lambdaTypes.ContextOptions {
	if opts == nil {
		return nil
	}
	return &lambdaTypes.ContextOptions{
		ReplayChildren: opts.ReplayChildren,
	}
}

// convertToSDKStepOptions converts local StepOptions to AWS SDK StepOptions
func convertToSDKStepOptions(opts *types.StepOptions) *lambdaTypes.StepOptions {
	if opts == nil {
		return nil
	}
	return &lambdaTypes.StepOptions{
		NextAttemptDelaySeconds: opts.NextAttemptDelaySeconds,
	}
}

// convertToSDKWaitOptions converts local WaitOptions to AWS SDK WaitOptions
func convertToSDKWaitOptions(opts *types.WaitOptions) *lambdaTypes.WaitOptions {
	if opts == nil {
		return nil
	}
	return &lambdaTypes.WaitOptions{
		WaitSeconds: opts.WaitSeconds,
	}
}

// convertToSDKErrorObject converts local ErrorObject to AWS SDK ErrorObject
func convertToSDKErrorObject(err *types.ErrorObject) *lambdaTypes.ErrorObject {
	if err == nil {
		return nil
	}

	sdkErr := &lambdaTypes.ErrorObject{
		ErrorType:    aws.String(err.ErrorType),
		ErrorMessage: aws.String(err.ErrorMessage),
	}

	// Convert single StackTrace string to []string
	if err.StackTrace != "" {
		sdkErr.StackTrace = []string{err.StackTrace}
	}

	return sdkErr
}

// GetExecutionState retrieves execution state from the Lambda durable execution service.
// It uses the Lambda GetDurableExecutionState API.
func (c *LambdaClient) GetExecutionState(params types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error) {
	// Call the AWS Lambda GetDurableExecutionState API
	resp, err := c.lambda.GetDurableExecutionState(context.Background(), &lambda.GetDurableExecutionStateInput{
		DurableExecutionArn: aws.String(params.DurableExecutionArn),
		CheckpointToken:     aws.String(params.CheckpointToken),
		Marker:              params.NextMarker,
	})
	if err != nil {
		return nil, fmt.Errorf("get execution state failed: %w", err)
	}

	// Convert AWS SDK Operations to local Operations
	operations := make([]types.Operation, len(resp.Operations))
	for i, op := range resp.Operations {
		operations[i] = convertFromSDKOperation(op)
	}

	return &types.GetDurableExecutionStateResponse{
		Operations: operations,
		NextMarker: resp.NextMarker,
	}, nil
}

// convertFromSDKOperation converts AWS SDK Operation to local Operation type
func convertFromSDKOperation(op lambdaTypes.Operation) types.Operation {
	operation := types.Operation{
		Id:                   aws.ToString(op.Id),
		Type:                 types.OperationType(op.Type),
		Name:                 op.Name,
		Status:               types.OperationStatus(op.Status),
		StartTimestamp:       types.NewFlexibleTimePtr(op.StartTimestamp),
		EndTimestamp:         types.NewFlexibleTimePtr(op.EndTimestamp),
		ParentId:             op.ParentId,
		StepDetails:          convertFromSDKStepDetails(op.StepDetails),
		ExecutionDetails:     convertFromSDKExecutionDetails(op.ExecutionDetails),
		CallbackDetails:      convertFromSDKCallbackDetails(op.CallbackDetails),
		ChainedInvokeDetails: convertFromSDKChainedInvokeDetails(op.ChainedInvokeDetails),
		ContextDetails:       convertFromSDKContextDetails(op.ContextDetails),
		WaitDetails:          convertFromSDKWaitDetails(op.WaitDetails),
	}

	// Convert SubType if present
	if op.SubType != nil {
		subType := types.OperationSubType(*op.SubType)
		operation.SubType = &subType
	}

	// Extract error from StepDetails if present
	if op.StepDetails != nil && op.StepDetails.Error != nil {
		operation.Error = convertFromSDKErrorObject(op.StepDetails.Error)
	}

	return operation
}

// convertFromSDKStepDetails converts AWS SDK StepDetails to local StepDetails
func convertFromSDKStepDetails(details *lambdaTypes.StepDetails) *types.StepDetails {
	if details == nil {
		return nil
	}
	return &types.StepDetails{
		Result: details.Result,
	}
}

// convertFromSDKExecutionDetails converts AWS SDK ExecutionDetails to local ExecutionDetails
func convertFromSDKExecutionDetails(details *lambdaTypes.ExecutionDetails) *types.ExecutionDetails {
	if details == nil {
		return nil
	}
	return &types.ExecutionDetails{
		InputPayload: details.InputPayload,
	}
}

// convertFromSDKCallbackDetails converts AWS SDK CallbackDetails to local CallbackDetails
func convertFromSDKCallbackDetails(details *lambdaTypes.CallbackDetails) *types.CallbackDetails {
	if details == nil {
		return nil
	}
	return &types.CallbackDetails{
		CallbackId: details.CallbackId,
		Result:     details.Result,
		Error:      convertFromSDKErrorObject(details.Error),
	}
}

// convertFromSDKChainedInvokeDetails converts AWS SDK ChainedInvokeDetails to local ChainedInvokeDetails
func convertFromSDKChainedInvokeDetails(details *lambdaTypes.ChainedInvokeDetails) *types.ChainedInvokeDetails {
	if details == nil {
		return nil
	}
	return &types.ChainedInvokeDetails{
		Result: details.Result,
		Error:  convertFromSDKErrorObject(details.Error),
	}
}

// convertFromSDKContextDetails converts AWS SDK ContextDetails to local ContextDetails
func convertFromSDKContextDetails(details *lambdaTypes.ContextDetails) *types.ContextDetails {
	if details == nil {
		return nil
	}
	return &types.ContextDetails{
		Result:         details.Result,
		Error:          convertFromSDKErrorObject(details.Error),
		ReplayChildren: details.ReplayChildren,
	}
}

// convertFromSDKWaitDetails converts AWS SDK WaitDetails to local WaitDetails
func convertFromSDKWaitDetails(details *lambdaTypes.WaitDetails) *types.WaitDetails {
	if details == nil {
		return nil
	}
	return &types.WaitDetails{
		ScheduledEndTimestamp: types.NewFlexibleTimePtr(details.ScheduledEndTimestamp),
	}
}

// convertFromSDKErrorObject converts AWS SDK ErrorObject to local ErrorObject
func convertFromSDKErrorObject(err *lambdaTypes.ErrorObject) *types.ErrorObject {
	if err == nil {
		return nil
	}

	localErr := &types.ErrorObject{
		ErrorType:    aws.ToString(err.ErrorType),
		ErrorMessage: aws.ToString(err.ErrorMessage),
	}

	// Convert []string StackTrace to single string (join with newlines)
	if len(err.StackTrace) > 0 {
		localErr.StackTrace = err.StackTrace[0]
		for i := 1; i < len(err.StackTrace); i++ {
			localErr.StackTrace += "\n" + err.StackTrace[i]
		}
	}

	return localErr
}
