# Custom Client Example

This example demonstrates how to use a custom AWS Lambda client with the durable execution SDK using the options pattern.

## Overview

The options pattern allows you to inject a custom `DurableClient` into `WithDurableExecution`. This is useful when you need:

- Custom AWS configuration (region, credentials, endpoint)
- Specific retry strategies
- Custom middleware or interceptors
- Testing with mock clients

## Usage

### Default Client (No Options)

```go
// Uses NewDurableClient internally
result, err := durable.WithDurableExecution(ctx, func(durableCtx *durable.Context) (interface{}, error) {
    // Your durable handler logic
    return "result", nil
})
```

### Custom Client with Options

```go
// Create custom AWS config
cfg, err := awsconfig.LoadDefaultConfig(ctx,
    awsconfig.WithRegion("us-west-2"),
    awsconfig.WithRetryMaxAttempts(5),
)

// Create custom Lambda client
customClient := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
    // Add custom options
})

// Use WithClient option
result, err := durable.WithDurableExecution(ctx, handler, 
    durable.WithClient(customClient))
```

## Benefits

1. **Flexibility**: Use different AWS configurations per execution
2. **Testing**: Inject mock clients for unit tests
3. **Regional Deployment**: Configure specific endpoints
4. **Advanced Configuration**: Full control over Lambda client behavior

## Example Scenarios

### Multi-Region Deployment

```go
// Different regions for different environments
var region string
if os.Getenv("ENV") == "production" {
    region = "us-east-1"
} else {
    region = "us-west-2"
}

cfg, _ := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
client := awslambda.NewFromConfig(cfg)

result, err := durable.WithDurableExecution(ctx, handler, durable.WithClient(client))
```

### Testing with Mock Client

```go
// Create mock client for testing
mockClient := &MockDurableClient{
    GetDurableExecutionStateFunc: func(...) {...},
    CheckpointDurableExecutionFunc: func(...) {...},
}

result, err := durable.WithDurableExecution(ctx, handler, durable.WithClient(mockClient))
```

## Running the Example

```bash
# Build
go build -o custom-client main.go

# Deploy to AWS Lambda (requires AWS CLI configured)
zip function.zip custom-client
aws lambda create-function \
  --function-name custom-client-example \
  --runtime provided.al2 \
  --handler custom-client \
  --zip-file fileb://function.zip \
  --role arn:aws:iam::ACCOUNT_ID:role/lambda-role

# Test invoke
aws lambda invoke \
  --function-name custom-client-example \
  --payload '{"orderId":"12345","amount":99.99}' \
  response.json
```

## API Reference

### WithClient(client DurableClient) DurableExecutionOption

Sets a custom DurableClient for durable execution.

**Parameters:**
- `client`: A DurableClient implementation (typically `*lambda.Client` from AWS SDK v2)

**Returns:**
- `DurableExecutionOption`: A functional option for `WithDurableExecution`

### DurableClient Interface

```go
type DurableClient interface {
    Invoke(ctx context.Context, input *lambda.InvokeInput, fnOpts ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
    GetDurableExecutionState(ctx context.Context, input *lambda.GetDurableExecutionStateInput, fnOpts ...func(*lambda.Options)) (*lambda.GetDurableExecutionStateOutput, error)
    CheckpointDurableExecution(ctx context.Context, input *lambda.CheckpointDurableExecutionInput, fnOpts ...func(*lambda.Options)) (*lambda.CheckpointDurableExecutionOutput, error)
}
```

The AWS SDK v2 `*lambda.Client` implements this interface automatically.
