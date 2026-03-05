# AWS Lambda Durable Execution SDK for Go

> Build resilient, long-running AWS Lambda functions with automatic state persistence, retry logic, and workflow orchestration.

## Overview

AWS Lambda durable functions extend Lambda's programming model to build multi-step applications and AI workflows with automatic state persistence. Applications can run for days or months, survive failures, and only incur charges for actual compute time.

**Core Primitives:**

- **Steps** - Execute business logic with automatic checkpointing and transparent retries
- **Waits** - Suspend execution without compute charges (for delays, human approvals, scheduled tasks)
- **Durable Invokes** - Reliable function chaining for modular, composable architectures
- **Map/Parallel** - Process arrays and execute parallel branches with concurrency control

## Installation

```bash
go get github.com/aws/aws-durable-execution-sdk-go
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	
	"github.com/aws/aws-lambda-go/lambda"
	durable "github.com/aws/aws-durable-execution-sdk-go"
)

type OrderEvent struct {
	OrderID string `json:"orderId"`
	Amount  int    `json:"amount"`
}

func handler(ctx context.Context, event OrderEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, func(durableCtx *durable.Context) (interface{}, error) {
		// Step 1: Validate order
		validatedOrder, err := durable.Step(durableCtx, "validate-order", func(stepCtx durable.StepContext) (interface{}, error) {
			return validateOrder(event)
		})
		if err != nil {
			return nil, err
		}
		
		// Step 2: Process payment
		_, err = durable.Step(durableCtx, "process-payment", func(stepCtx durable.StepContext) (interface{}, error) {
			return processPayment(validatedOrder)
		})
		if err != nil {
			return nil, err
		}
		
		// Step 3: Wait for fulfillment delay
		if err := durable.Wait(durableCtx, "fulfillment-delay", durable.Duration{Minutes: 5}); err != nil {
			return nil, err
		}
		
		// Step 4: Ship order
		_, err = durable.Step(durableCtx, "ship-order", func(stepCtx durable.StepContext) (interface{}, error) {
			return shipOrder(validatedOrder)
		})
		if err != nil {
			return nil, err
		}
		
		return map[string]interface{}{"success": true, "orderId": event.OrderID}, nil
	})
}

func main() {
	lambda.Start(handler)
}
```

## Critical Rules

### ⚠️ The Replay Model

Durable functions use a "replay" execution model. On replay (after wait/failure/resume), code runs from the beginning. Steps that already completed return their checkpointed results WITHOUT re-executing. Code OUTSIDE steps executes again on every replay.

### Rule 1: Deterministic Code Outside Steps

ALL code outside steps MUST be deterministic.

```go
// ❌ WRONG: Non-deterministic code outside steps
id := uuid.New().String()      // Different on each replay!
timestamp := time.Now().Unix() // Different on each replay!

// ✅ CORRECT: Non-deterministic code inside steps
id, _ := durable.Step(ctx, "generate-id", func(stepCtx durable.StepContext) (interface{}, error) {
    return uuid.New().String(), nil
})
timestamp, _ := durable.Step(ctx, "get-time", func(stepCtx durable.StepContext) (interface{}, error) {
    return time.Now().Unix(), nil
})
```

### Rule 2: No Nested Durable Operations

You CANNOT call durable operations inside a step function.

```go
// ❌ WRONG: Nested durable operations
durable.Step(ctx, "process", func(stepCtx durable.StepContext) (interface{}, error) {
    durable.Wait(ctx, "delay", durable.Duration{Seconds: 1}) // ERROR!
    return nil, nil
})

// ✅ CORRECT: Use RunInChildContext for grouping
durable.RunInChildContext(ctx, "process", func(childCtx *durable.Context) (interface{}, error) {
    if err := durable.Wait(childCtx, "delay", durable.Duration{Seconds: 1}); err != nil {
        return nil, err
    }
    return durable.Step(childCtx, "work", func(stepCtx durable.StepContext) (interface{}, error) {
        return doWork(), nil
    })
})
```

### Rule 3: Return Values, Not Mutations

Variables mutated inside steps are NOT preserved across replays. Always use return values.

```go
// ❌ WRONG: Counter mutations lost
counter := 0
durable.Step(ctx, "increment", func(stepCtx durable.StepContext) (interface{}, error) {
    counter++ // This is lost on replay!
    return nil, nil
})
fmt.Println(counter) // 0 on replay!

// ✅ CORRECT: Return values from steps
result, _ := durable.Step(ctx, "increment", func(stepCtx durable.StepContext) (interface{}, error) {
    return counter + 1, nil
})
counter = result.(int)
```

### Rule 4: Side Effects Outside Steps Repeat

Side effects (logging, API calls) outside steps happen on EVERY replay.

**Exception:** `durableCtx.Logger()` is replay-aware and safe to use anywhere.

```go
// ❌ WRONG
fmt.Println("Starting")  // Logs multiple times!
sendEmail(...)            // Sends multiple emails!

// ✅ CORRECT
durableCtx.Logger().Info("Starting")  // Deduplicated automatically
durable.Step(ctx, "send-email", func(stepCtx durable.StepContext) (interface{}, error) {
    return sendEmail(...)
})
```

## IAM Permissions

Durable functions require the `AWSLambdaBasicDurableExecutionRolePolicy` managed policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:PutLogEvents"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "lambda:CheckpointDurableExecutions",
        "lambda:GetDurableExecutionState"
      ],
      "Resource": "*"
    }
  ]
}
```

For durable invokes, also add `lambda:InvokeFunction` on target function ARNs.

## API Reference

### Handler Wrapper

```go
func WithDurableExecution(ctx context.Context, handler func(*Context) (interface{}, error)) (interface{}, error)
```

Wraps your Lambda handler to enable durable execution capabilities.

### Step - Atomic Operations

```go
func Step[T any](ctx *Context, name string, fn func(StepContext) (T, error)) (T, error)
func StepWithConfig[T any](ctx *Context, name string, fn func(StepContext) (T, error), config StepConfig) (T, error)
```

Execute a function with automatic checkpointing and retry logic.

### Wait - Pause Execution

```go
func Wait(ctx *Context, name string, duration Duration) error
```

Suspend execution for a specified duration without incurring compute charges.

### Invoke - Call Other Functions

```go
func Invoke[T any](ctx *Context, name string, functionArn string, payload interface{}) (T, error)
```

Invoke another durable Lambda function. **Must use qualified function ARN** (with version or alias).

### RunInChildContext - Group Operations

```go
func RunInChildContext[T any](ctx *Context, name string, fn func(*Context) (T, error)) (T, error)
```

Create a child context for grouping related operations.

### WaitForCallback - External Integration

```go
func WaitForCallback[T any](ctx *Context, name string, submitter func(callbackID string) error, config WaitForCallbackConfig) (T, error)
```

Wait for an external system to provide a callback result.

### WaitForCondition - Polling

```go
func WaitForCondition[S any, T any](ctx *Context, name string, check func(S, WaitForConditionCheckContext) (S, error), config WaitForConditionConfig[S]) (T, error)
```

Poll a condition until it's met with configurable retry strategy.

### Map - Process Arrays

```go
func Map[T any, R any](ctx *Context, name string, items []T, fn func(*Context, T, int) (R, error), config MapConfig) (*BatchResult[R], error)
```

Process an array of items with concurrency control.

### Parallel - Parallel Branches

```go
func Parallel[T any](ctx *Context, name string, branches []ParallelBranch[T], config ParallelConfig) (*BatchResult[T], error)
```

Execute multiple branches in parallel with concurrency control.

## Common Patterns

### Multi-Step Workflow

```go
func handler(ctx context.Context, event map[string]interface{}) (interface{}, error) {
	return durable.WithDurableExecution(ctx, func(durableCtx *durable.Context) (interface{}, error) {
		validated, err := durable.Step(durableCtx, "validate", func(stepCtx durable.StepContext) (interface{}, error) {
			return validateInput(event)
		})
		if err != nil {
			return nil, err
		}
		
		processed, err := durable.Step(durableCtx, "process", func(stepCtx durable.StepContext) (interface{}, error) {
			return processData(validated)
		})
		if err != nil {
			return nil, err
		}
		
		if err := durable.Wait(durableCtx, "cooldown", durable.Duration{Seconds: 30}); err != nil {
			return nil, err
		}
		
		_, err = durable.Step(durableCtx, "notify", func(stepCtx durable.StepContext) (interface{}, error) {
			return sendNotification(processed), nil
		})
		if err != nil {
			return nil, err
		}
		
		return map[string]interface{}{"success": true, "data": processed}, nil
	})
}
```

### GenAI Agent (Agentic Loop)

```go
func handler(ctx context.Context, event AIEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, func(durableCtx *durable.Context) (interface{}, error) {
		messages := []Message{{Role: "user", Content: event.Prompt}}
		
		for {
			result, err := durable.Step(durableCtx, "invoke-model", func(stepCtx durable.StepContext) (interface{}, error) {
				return invokeAIModel(messages)
			})
			if err != nil {
				return nil, err
			}
			
			modelResult := result.(ModelResult)
			if modelResult.Tool == nil {
				return modelResult.Response, nil
			}
			
			toolResult, err := durable.Step(durableCtx, fmt.Sprintf("tool-%s", modelResult.Tool.Name), 
				func(stepCtx durable.StepContext) (interface{}, error) {
					return executeTool(modelResult.Tool, modelResult.Response)
				})
			if err != nil {
				return nil, err
			}
			
			messages = append(messages, Message{Role: "assistant", Content: toolResult.(string)})
		}
	})
}
```

### Human-in-the-Loop Approval

```go
func handler(ctx context.Context, event ApprovalEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, func(durableCtx *durable.Context) (interface{}, error) {
		plan, err := durable.Step(durableCtx, "generate-plan", func(stepCtx durable.StepContext) (interface{}, error) {
			return generatePlan(event)
		})
		if err != nil {
			return nil, err
		}
		
		answer, err := durable.WaitForCallback(durableCtx, "wait-for-approval", 
			func(callbackID string) error {
				return sendApprovalEmail(event.ApproverEmail, plan, callbackID)
			},
			durable.WaitForCallbackConfig{
				Timeout: durable.Duration{Hours: 24},
			})
		if err != nil {
			return nil, err
		}
		
		if answer.(string) == "APPROVED" {
			_, err = durable.Step(durableCtx, "execute", func(stepCtx durable.StepContext) (interface{}, error) {
				return performAction(plan), nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"status": "completed"}, nil
		}
		
		return map[string]interface{}{"status": "rejected"}, nil
	})
}
```

## Advanced Features

### Jitter Strategy

Jitter helps prevent thundering herd problems by randomizing retry delays when multiple executions fail simultaneously.

```go
// Full jitter - randomizes delay between 0 and calculated delay
durable.StepWithConfig(ctx, "api-call", func(stepCtx durable.StepContext) (string, error) {
    return callExternalAPI()
}, durable.StepConfig{
    RetryStrategy: durable.CreateRetryStrategy(durable.RetryStrategyConfig{
        MaxAttempts:  6,
        InitialDelay: durable.NewDurationFromSeconds(5),
        MaxDelay:     durable.NewDurationFromSeconds(60),
        BackoffRate:  2,
        Jitter:       durable.JitterStrategyFull, // Prevents thundering herd
    }),
})

// Half jitter - randomizes between 50% and 100% of calculated delay
durable.StepWithConfig(ctx, "api-call", func(stepCtx durable.StepContext) (string, error) {
    return callExternalAPI()
}, durable.StepConfig{
    RetryStrategy: durable.CreateRetryStrategy(durable.RetryStrategyConfig{
        MaxAttempts:  3,
        InitialDelay: durable.NewDurationFromSeconds(1),
        BackoffRate:  2,
        Jitter:       durable.JitterStrategyHalf, // Predictable minimum delays
    }),
})

// No jitter - uses exact calculated delay
durable.StepWithConfig(ctx, "api-call", func(stepCtx durable.StepContext) (string, error) {
    return callExternalAPI()
}, durable.StepConfig{
    RetryStrategy: durable.CreateRetryStrategy(durable.RetryStrategyConfig{
        MaxAttempts:  3,
        InitialDelay: durable.NewDurationFromSeconds(1),
        BackoffRate:  2,
        Jitter:       durable.JitterStrategyNone, // Precise delays
    }),
})

// Use preset retry strategies
durable.StepWithConfig(ctx, "my-step", fn, durable.StepConfig{
    RetryStrategy: durable.RetryPresets.Standard(), // 6 attempts with full jitter
})
```

**Available Jitter Strategies:**
- `JitterStrategyNone` - Use exact calculated delay
- `JitterStrategyFull` - Random delay between 0 and calculated delay (maximum spread)
- `JitterStrategyHalf` - Random delay between 50% and 100% of calculated delay (moderate spread)

### Nesting Type for Map/Parallel

Control how child contexts are created for batch operations, affecting observability, cost, and scale limits.

```go
// NESTED mode - Full child contexts with checkpointing (default)
// - High observability: Each iteration appears in execution history
// - Higher cost: More operation consumption
// - Lower scale: Operation limits apply
durable.Map(ctx, "process-items", items, 
    func(ctx *durable.Context, item string, index int) (string, error) {
        return durable.Step(ctx, "process", func(stepCtx durable.StepContext) (string, error) {
            return processItem(item), nil
        })
    },
    durable.MapConfig{
        MaxConcurrency: 10,
        Nesting:        durable.NestingTypeNested, // Full observability
    },
)

// FLAT mode - Virtual contexts without checkpointing
// - Lower observability: Iterations don't appear as separate operations
// - ~30% cost reduction: Skips CONTEXT operation overhead
// - Higher scale: More iterations possible within operation limits
durable.Map(ctx, "process-items", items, 
    func(ctx *durable.Context, item string, index int) (string, error) {
        return durable.Step(ctx, "process", func(stepCtx durable.StepContext) (string, error) {
            return processItem(item), nil
        })
    },
    durable.MapConfig{
        MaxConcurrency: 10,
        Nesting:        durable.NestingTypeFlat, // Cost optimized
    },
)

// Same applies to Parallel
durable.Parallel(ctx, "parallel-tasks", branches, durable.ParallelConfig{
    MaxConcurrency: 5,
    Nesting:        durable.NestingTypeFlat, // ~30% cost reduction
})
```

**When to use NESTED vs FLAT:**
- Use **NESTED** when you need full execution visibility and debugging
- Use **FLAT** when processing many items and cost optimization is priority
- Use **FLAT** when approaching operation count limits (need higher scale)

### CreateCallback - Advanced External Integration

`CreateCallback` provides an alternative to `WaitForCallback` for scenarios where you need more control over callback lifecycle.

```go
// Create a callback and get the callback ID
result, callbackID, err := durable.CreateCallback[ApprovalResponse](
    ctx,
    "approval-request",
    durable.DefaultWaitForCallbackConfig(),
)
if err != nil {
    return nil, err
}

// Send the callback ID to external system
if err := sendToExternalSystem(callbackID, requestData); err != nil {
    return nil, err
}

// Later, wait for the result
approval, err := result.Wait()
if err != nil {
    // Check if it's a suspension (expected during first execution)
    if _, ok := err.(*durable.SuspendExecutionError); ok {
        return nil, err // Execution will resume when callback arrives
    }
    return nil, err
}

// Use the approval result
durableCtx.Logger().Info("Received approval: %v", approval)
```

**CreateCallback vs WaitForCallback:**
- `CreateCallback`: Returns immediately with callback ID, allows separate submission and waiting
- `WaitForCallback`: Combines callback creation and submission in one call (more convenient)

Choose `CreateCallback` when:
- You need the callback ID before submitting to external system
- You want to separate callback creation from submission logic
- You need to pass the callback ID through multiple functions

## Testing

The SDK includes a comprehensive testing framework:

```go
import (
	"testing"
	
	durable "github.com/aws/aws-durable-execution-sdk-go"
	"github.com/aws/aws-durable-execution-sdk-go/testing"
)

func TestWorkflow(t *testing.T) {
	runner := testing.NewLocalDurableTestRunner(handler, testing.LocalDurableTestRunnerConfig{
		SkipTime: true,
	})
	
	execution, err := runner.Run(testing.RunConfig{
		Payload: map[string]interface{}{"userId": "123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	if execution.Status() != testing.StatusSucceeded {
		t.Errorf("Expected SUCCEEDED, got %v", execution.Status())
	}
	
	// Get operations by name (not by index!)
	fetchStep, err := execution.GetOperation("fetch-user")
	if err != nil {
		t.Fatal(err)
	}
	
	if fetchStep.Type() != testing.OperationTypeStep {
		t.Errorf("Expected STEP, got %v", fetchStep.Type())
	}
}
```

## Infrastructure as Code

### AWS SAM

```yaml
AWSTemplateFormatVersion: '2010-09-09'
Transform: AWS::Serverless-2016-10-31

Resources:
  DurableFunction:
    Type: AWS::Serverless::Function
    Properties:
      FunctionName: myDurableFunction
      Runtime: provided.al2023
      Handler: bootstrap
      CodeUri: ./bin
      DurableConfig:
        ExecutionTimeout: 3600
        RetentionPeriodInDays: 7
      Policies:
        - arn:aws:iam::aws:policy/service-role/AWSLambdaBasicDurableExecutionRolePolicy
      AutoPublishAlias: prod
```

### AWS CDK (Go)

```go
import (
	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
)

durableFunction := awslambda.NewFunction(stack, jsii.String("DurableFunction"), &awslambda.FunctionProps{
	Runtime: awslambda.Runtime_PROVIDED_AL2023(),
	Handler: jsii.String("bootstrap"),
	Code:    awslambda.Code_FromAsset(jsii.String("./bin"), nil),
	DurableConfig: &awslambda.DurableConfig{
		ExecutionTimeout: awscdk.Duration_Hours(jsii.Number(1)),
		RetentionPeriod:  awscdk.Duration_Days(jsii.Number(7)),
	},
})

version := durableFunction.CurrentVersion()
awslambda.NewAlias(stack, jsii.String("ProdAlias"), &awslambda.AliasProps{
	AliasName: jsii.String("prod"),
	Version:   version,
})
```

## License

This library is licensed under the Apache 2.0 License.
