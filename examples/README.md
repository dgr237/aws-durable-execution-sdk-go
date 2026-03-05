# AWS Lambda Durable Execution SDK for Go - Examples

This directory contains example applications demonstrating various use cases for the AWS Lambda Durable Execution SDK.

## Examples

### 1. Order Processing (`order-processing/`)

A complete e-commerce order processing workflow demonstrating:
- Multi-step transaction processing
- Data validation
- Payment processing simulation
- Wait operations for inventory preparation
- Email notifications

**Key Features:**
- Sequential step execution
- Error handling and validation
- Wait operations
- Automatic retry on failure

**Run:**
```bash
cd order-processing
go build -o bootstrap main.go
# Deploy to AWS Lambda with durable execution enabled
```

### 2. AI Agent (`ai-agent/`)

An agentic AI workflow that demonstrates:
- Iterative AI model invocation
- Tool execution (function calling)
- Agentic loops
- Multi-turn conversations

**Key Features:**
- While loop pattern for agent iterations
- Dynamic tool execution
- Rate limiting with wait operations
- Conversation state management

**Use Cases:**
- AI assistants
- Code generation agents
- Research agents
- Customer service bots

### 3. Approval Workflow (`approval-workflow/`)

Human-in-the-loop approval system demonstrating:
- Callback-based approval requests
- External system integration
- Email notifications with callback IDs
- Timeout handling

**Key Features:**
- `WaitForCallback` operation
- External webhook integration
- Timeout configuration
- Conditional execution based on approval

**Use Cases:**
- Deployment approvals
- Expense approvals
- Content moderation
- Policy enforcement

### 4. Batch Image Processing (`batch-processing/`)

Parallel batch processing demonstrating:
- Map operation for concurrent processing
- Concurrency control
- Completion criteria
- Error tolerance

**Key Features:**
- `Map` operation with 10 concurrent workers
- Failure tolerance (20%)
- Progress tracking
- Summary report generation

**Use Cases:**
- Media processing
- Data transformation
- Batch ETL
- Report generation

## Testing Examples

Each example includes unit tests using the testing framework:

```go
import (
    durable "github.com/dgr237/aws-durable-execution-sdk-go"
    "github.com/dgr237/aws-durable-execution-sdk-go/testing"
)

func TestWorkflow(t *testing.T) {
    runner := testing.NewLocalDurableTestRunner(handler, testing.LocalDurableTestRunnerConfig{
        SkipTime: true,
    })
    
    result, err := runner.Run(testing.RunConfig{
        Payload: testEvent,
    })
    
    // Assert on result
}
```

Run tests:
```bash
cd order-processing
go test -v
```

## Deployment

All examples can be deployed using AWS SAM, CDK, or CloudFormation:

### SAM Template Example

```yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31

Resources:
  OrderProcessingFunction:
    Type: AWS::Serverless::Function
    Properties:
      FunctionName: order-processing
      Runtime: provided.al2023
      Handler: bootstrap
      CodeUri: ./order-processing
      DurableConfig:
        ExecutionTimeout: 3600
        RetentionPeriodInDays: 7
      Policies:
        - arn:aws:iam::aws:policy/service-role/AWSLambdaBasicDurableExecutionRolePolicy
      AutoPublishAlias: prod
```

Deploy:
```bash
sam build
sam deploy --guided
```

### Build for Lambda

```bash
GOOS=linux GOARCH=amd64 go build -o bootstrap main.go
zip function.zip bootstrap
aws lambda create-function \
  --function-name my-durable-function \
  --runtime provided.al2023 \
  --handler bootstrap \
  --zip-file fileb://function.zip \
  --role arn:aws:iam::ACCOUNT:role/lambda-durable-role \
  --durable-config ExecutionTimeout=3600,RetentionPeriodInDays=7
```

## Common Patterns

### Multi-Step Workflow
```go
result1, _ := durable.Step(ctx, "step1", func(s durable.StepContext) (T, error) { ... })
result2, _ := durable.Step(ctx, "step2", func(s durable.StepContext) (T, error) { ... })
```

### Parallel Processing
```go
results, _ := durable.Map(ctx, "process-items", items, func(ctx *durable.Context, item T, i int) (R, error) {
    return processItem(ctx, item)
}, config)
```

### Wait for External Event
```go
result, _ := durable.WaitForCallback(ctx, "approval", func(callbackID string) error {
    return sendEmailWithCallbackID(callbackID)
}, config)
```

### Conditional Execution
```go
if condition {
    result, _ := durable.Step(ctx, "branch-a", funcA)
} else {
    result, _ := durable.Step(ctx, "branch-b", funcB)
}
```

## Best Practices

1. **Name all operations**: Use descriptive names for debugging and monitoring
2. **Handle errors**: Always check and handle step errors
3. **Use appropriate waits**: Don't busy-wait, use `durable.Wait()` for delays
4. **Keep steps idempotent**: Steps may execute multiple times on replay
5. **Minimize step size**: Smaller steps = better granularity and recovery
6. **Use child contexts**: Group related operations with `RunInChildContext`
7. **Test thoroughly**: Use the testing framework to validate workflows

## Resources

- [Main README](../README.md)
- [AWS Lambda Durable Functions Documentation](https://docs.aws.amazon.com/lambda/latest/dg/durable-functions.html)
- [AGENTS.md](../AGENTS.md) - Comprehensive agent guide
