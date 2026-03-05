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
go get github.com/dgr237/aws-durable-execution-sdk-go
```

## Quick Start

```go
package main

import (
	"context"
	"fmt"
	
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/operations"
)

type OrderEvent struct {
	OrderID string `json:"orderId"`
	Amount  int    `json:"amount"`
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var orderEvent OrderEvent
		if err := durable.ExtractInput(event, &orderEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}
		
		// Step 1: Validate order
		validatedOrder, err := operations.Step(durableCtx, "validate-order", func(stepCtx context.Context) (interface{}, error) {
			return validateOrder(orderEvent)
		})
		if err != nil {
			return nil, err
		}
		
		// Step 2: Process payment
		_, err = operations.Step(durableCtx, "process-payment", func(stepCtx context.Context) (interface{}, error) {
			return processPayment(validatedOrder)
		})
		if err != nil {
			return nil, err
		}
		
		// Step 3: Wait for fulfillment delay
		if err := operations.Wait(durableCtx, "fulfillment-delay", config.NewDurationFromMinutes(5)); err != nil {
			return nil, err
		}
		
		// Step 4: Ship order
		_, err = operations.Step(durableCtx, "ship-order", func(stepCtx context.Context) (interface{}, error) {
			return shipOrder(validatedOrder)
		})
		if err != nil {
			return nil, err
		}
		
		return map[string]interface{}{"success": true, "orderId": orderEvent.OrderID}, nil
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
id, _ := operations.Step(ctx, "generate-id", func(stepCtx context.Context) (string, error) {
    return uuid.New().String(), nil
})
timestamp, _ := operations.Step(ctx, "get-time", func(stepCtx context.Context) (int64, error) {
    return time.Now().Unix(), nil
})
```

### Rule 2: No Nested Durable Operations

You CANNOT call durable operations inside a step function.

```go
// ❌ WRONG: Nested durable operations
operations.Step(ctx, "process", func(stepCtx context.Context) (interface{}, error) {
    operations.Wait(ctx, "delay", config.NewDurationFromSeconds(1)) // ERROR!
    return nil, nil
})

// ✅ CORRECT: Use RunInChildContext for grouping
operations.RunInChildContext(ctx, "process", func(childCtx context.Context) (interface{}, error) {
    if err := operations.Wait(childCtx, "delay", config.NewDurationFromSeconds(1)); err != nil {
        return nil, err
    }
    return operations.Step(childCtx, "work", func(stepCtx context.Context) (interface{}, error) {
        return doWork(), nil
    })
})
```

### Rule 3: Return Values, Not Mutations

Variables mutated inside steps are NOT preserved across replays. Always use return values.

```go
// ❌ WRONG: Counter mutations lost
counter := 0
operations.Step(ctx, "increment", func(stepCtx context.Context) (interface{}, error) {
    counter++ // This is lost on replay!
    return nil, nil
})
fmt.Println(counter) // 0 on replay!

// ✅ CORRECT: Return values from steps
result, _ := operations.Step(ctx, "increment", func(stepCtx context.Context) (int, error) {
    return counter + 1, nil
})
counter = result
```

### Rule 4: Side Effects Outside Steps Repeat

Side effects (logging, API calls) outside steps happen on EVERY replay.

**Exception:** `durablecontext.Logger(ctx)` is replay-aware and safe to use anywhere.

```go
// ❌ WRONG
fmt.Println("Starting")  // Logs multiple times!
sendEmail(...)            // Sends multiple emails!

// ✅ CORRECT
durablecontext.Logger(ctx).Info("Starting")  // Deduplicated automatically
operations.Step(ctx, "send-email", func(stepCtx context.Context) (interface{}, error) {
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
func WithDurableExecution(ctx context.Context, event client.DurableExecutionEvent, handler func(context.Context) (interface{}, error), opts ...DurableExecutionOption) (interface{}, error)
```

Wraps your Lambda handler to enable durable execution capabilities. The event parameter contains checkpoint tokens and execution state from the Lambda service.

**Options:**
- `WithClient(client)`: Use a custom DurableClient instead of the default

### Step - Atomic Operations

```go
func Step[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error)
func StepWithConfig[T any](ctx context.Context, name string, fn func(context.Context) (T, error), cfg config.StepConfig) (T, error)
```

Execute a function with automatic checkpointing and retry logic.

### Wait - Pause Execution

```go
func Wait(ctx context.Context, name string, duration config.Duration) error
```

Suspend execution for a specified duration without incurring compute charges.

### Invoke - Call Other Functions

```go
func Invoke[T any](ctx context.Context, name string, functionArn string, payload interface{}) (T, error)
```

Invoke another durable Lambda function. **Must use qualified function ARN** (with version or alias).

### RunInChildContext - Group Operations

```go
func RunInChildContext[T any](ctx context.Context, name string, fn func(context.Context) (T, error)) (T, error)
```

Create a child context for grouping related operations.

### WaitForCallback - External Integration

```go
func WaitForCallback[T any](ctx context.Context, name string, submitter func(callbackID string) error, cfg config.WaitForCallbackConfig) (T, error)
```

Wait for an external system to provide a callback result.

### WaitForCondition - Polling

```go
func WaitForCondition[S any, T any](ctx context.Context, name string, check func(S, context.Context) (S, error), cfg config.WaitForConditionConfig[S]) (T, error)
```

Poll a condition until it's met with configurable retry strategy.

### Map - Process Arrays

```go
func Map[T any, R any](ctx context.Context, name string, items []T, fn func(context.Context, T, int) (R, error), cfg config.MapConfig) (*durablecontext.BatchResult[R], error)
```

Process an array of items with concurrency control.

### Parallel - Parallel Branches

```go
func Parallel[T any](ctx context.Context, name string, branches []durablecontext.ParallelBranch[T], cfg config.ParallelConfig) (*durablecontext.BatchResult[T], error)
```

Execute multiple branches in parallel with concurrency control.

## Common Patterns

### Multi-Step Workflow

```go
func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		var input map[string]interface{}
		if err := durable.ExtractInput(event, &input); err != nil {
			return nil, err
		}
		
		validated, err := operations.Step(durableCtx, "validate", func(stepCtx context.Context) (interface{}, error) {
			return validateInput(input)
		})
		if err != nil {
			return nil, err
		}
		
		processed, err := operations.Step(durableCtx, "process", func(stepCtx context.Context) (interface{}, error) {
			return processData(validated)
		})
		if err != nil {
			return nil, err
		}
		
		if err := operations.Wait(durableCtx, "cooldown", config.NewDurationFromSeconds(30)); err != nil {
			return nil, err
		}
		
		_, err = operations.Step(durableCtx, "notify", func(stepCtx context.Context) (interface{}, error) {
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
func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		var aiEvent AIEvent
		if err := durable.ExtractInput(event, &aiEvent); err != nil {
			return nil, err
		}
		
		messages := []Message{{Role: "user", Content: aiEvent.Prompt}}
		
		for {
			result, err := operations.Step(durableCtx, "invoke-model", func(stepCtx context.Context) (ModelResult, error) {
				return invokeAIModel(messages)
			})
			if err != nil {
				return nil, err
			}
			
			if result.Tool == nil {
				return result.Response, nil
			}
			
			toolResult, err := operations.Step(durableCtx, fmt.Sprintf("tool-%s", result.Tool.Name), 
				func(stepCtx context.Context) (string, error) {
					return executeTool(result.Tool, result.Response)
				})
			if err != nil {
				return nil, err
			}
			
			messages = append(messages, Message{Role: "assistant", Content: toolResult})
		}
	})
}
```

### Human-in-the-Loop Approval

```go
func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		var approvalEvent ApprovalEvent
		if err := durable.ExtractInput(event, &approvalEvent); err != nil {
			return nil, err
		}
		
		plan, err := operations.Step(durableCtx, "generate-plan", func(stepCtx context.Context) (Plan, error) {
			return generatePlan(approvalEvent)
		})
		if err != nil {
			return nil, err
		}
		
		answer, err := operations.WaitForCallback[string](durableCtx, "wait-for-approval", 
			func(callbackID string) error {
				return sendApprovalEmail(approvalEvent.ApproverEmail, plan, callbackID)
			},
			config.WaitForCallbackConfig{
				Timeout: func() *config.Duration { d := config.NewDurationFromHours(24); return &d }(),
			})
		if err != nil {
			return nil, err
		}
		
		if answer == "APPROVED" {
			_, err = operations.Step(durableCtx, "execute", func(stepCtx context.Context) (interface{}, error) {
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
operations.StepWithConfig(ctx, "api-call", func(stepCtx context.Context) (string, error) {
    return callExternalAPI()
}, config.StepConfig{
    RetryStrategy: retry.CreateRetryStrategy(retry.RetryStrategyConfig{
        MaxAttempts:  6,
        InitialDelay: config.NewDurationFromSeconds(5),
        MaxDelay:     config.NewDurationFromSeconds(60),
        BackoffRate:  2,
        Jitter:       config.JitterStrategyFull, // Prevents thundering herd
    }),
})

// Half jitter - randomizes between 50% and 100% of calculated delay
operations.StepWithConfig(ctx, "api-call", func(stepCtx context.Context) (string, error) {
    return callExternalAPI()
}, config.StepConfig{
    RetryStrategy: retry.CreateRetryStrategy(retry.RetryStrategyConfig{
        MaxAttempts:  3,
        InitialDelay: config.NewDurationFromSeconds(1),
        BackoffRate:  2,
        Jitter:       config.JitterStrategyHalf, // Predictable minimum delays
    }),
})

// No jitter - uses exact calculated delay
operations.StepWithConfig(ctx, "api-call", func(stepCtx context.Context) (string, error) {
    return callExternalAPI()
}, config.StepConfig{
    RetryStrategy: retry.CreateRetryStrategy(retry.RetryStrategyConfig{
        MaxAttempts:  3,
        InitialDelay: config.NewDurationFromSeconds(1),
        BackoffRate:  2,
        Jitter:       config.JitterStrategyNone, // Precise delays
    }),
})

// Use preset retry strategies
operations.StepWithConfig(ctx, "my-step", fn, config.StepConfig{
    RetryStrategy: retry.RetryPresets.Standard(), // 6 attempts with full jitter
})
```

**Available Jitter Strategies:**
- `JitterStrategyNone` - Use exact calculated delay
- `JitterStrategyFull` - Random delay between 0 and calculated delay (maximum spread)
- `JitterStrategyHalf` - Random delay between 50% and 100% of calculated delay (moderate spread)

### Step Semantics - Control Retry Behavior

Step semantics control what happens when a step is interrupted (e.g., Lambda timeout, instance failure).

```go
// AT_LEAST_ONCE (default) - Step may execute multiple times on retry
operations.StepWithConfig(ctx, "send-notification", func(stepCtx context.Context) (string, error) {
    return sendNotification(), nil
}, config.StepConfig{
    Semantics: config.StepSemanticsAtLeastOnce, // May retry on interruption
})

// AT_MOST_ONCE - Step will never retry if interrupted
operations.StepWithConfig(ctx, "charge-payment", func(stepCtx context.Context) (string, error) {
    return chargePayment(), nil
}, config.StepConfig{
    Semantics: config.StepSemanticsAtMostOnce, // Never retry on interruption
})
```

**When to use each:**
- **AT_LEAST_ONCE** (default): Safe for idempotent operations. Step may execute multiple times if interrupted.
- **AT_MOST_ONCE**: Critical for non-idempotent operations (payments, unique ID generation). Returns error if interrupted instead of retrying.

If an AT_MOST_ONCE step is interrupted, it returns a `StepInterruptedError` instead of retrying.

### Nesting Type for Map/Parallel

Control how child contexts are created for batch operations, affecting observability, cost, and scale limits.

```go
// NESTED mode - Full child contexts with checkpointing (default)
// - High observability: Each iteration appears in execution history
// - Higher cost: More operation consumption
// - Lower scale: Operation limits apply
operations.Map(ctx, "process-items", items, 
    func(ctx context.Context, item string, index int) (string, error) {
        return operations.Step(ctx, "process", func(stepCtx context.Context) (string, error) {
            return processItem(item), nil
        })
    },
    config.MapConfig{
        MaxConcurrency: 10,
        Nesting:        config.NestingTypeNested, // Full observability
    },
)

// FLAT mode - Virtual contexts without checkpointing
// - Lower observability: Iterations don't appear as separate operations
// - ~30% cost reduction: Skips CONTEXT operation overhead
// - Higher scale: More iterations possible within operation limits
operations.Map(ctx, "process-items", items, 
    func(ctx context.Context, item string, index int) (string, error) {
        return operations.Step(ctx, "process", func(stepCtx context.Context) (string, error) {
            return processItem(item), nil
        })
    },
    config.MapConfig{
        MaxConcurrency: 10,
        Nesting:        config.NestingTypeFlat, // Cost optimized
    },
)

// Same applies to Parallel
operations.Parallel(ctx, "parallel-tasks", branches, config.ParallelConfig{
    MaxConcurrency: 5,
    Nesting:        config.NestingTypeFlat, // ~30% cost reduction
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
result, callbackID, err := operations.CreateCallback[ApprovalResponse](
    ctx,
    "approval-request",
    config.DefaultWaitForCallbackConfig(),
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
durablecontext.Logger(ctx).Info("Received approval: %v", approval)
```

**CreateCallback vs WaitForCallback:**
- `CreateCallback`: Returns immediately with callback ID, allows separate submission and waiting
- `WaitForCallback`: Combines callback creation and submission in one call (more convenient)

Choose `CreateCallback` when:
- You need the callback ID before submitting to external system
- You want to separate callback creation from submission logic
- You need to pass the callback ID through multiple functions

### Completion Criteria for Map/Parallel

Control when Map and Parallel operations are considered complete, allowing partial failures.

```go
// Require at least 8 out of 10 items to succeed
minSuccessful := 8
results, err := operations.Map(ctx, "process-items", items, processFn, config.MapConfig{
    MaxConcurrency: 5,
    CompletionConfig: &config.CompletionConfig{
        MinSuccessful: &minSuccessful,
    },
})

// Tolerate up to 2 failures
maxFailures := 2
results, err := operations.Map(ctx, "process-items", items, processFn, config.MapConfig{
    MaxConcurrency: 5,
    CompletionConfig: &config.CompletionConfig{
        ToleratedFailureCount: &maxFailures,
    },
})

// Tolerate up to 20% failure rate
failurePercentage := 20.0
results, err := operations.Map(ctx, "process-items", items, processFn, config.MapConfig{
    MaxConcurrency: 5,
    CompletionConfig: &config.CompletionConfig{
        ToleratedFailurePercentage: &failurePercentage, // 0.0-100.0
    },
})

// Handle results
if err != nil {
    return nil, err
}

// Check if there were any errors (doesn't throw, just reports)
if len(results.GetErrors()) > 0 {
    durablecontext.Logger(ctx).Warn("Some items failed: %d failures", len(results.GetErrors()))
}

// Get only successful results
successfulResults := results.GetResults()

// Or examine all results individually
for _, itemResult := range results.AllResults() {
    if itemResult.Error != nil {
        durablecontext.Logger(ctx).Error("Item %d failed: %v", itemResult.Index, itemResult.Error)
    } else {
        durablecontext.Logger(ctx).Info("Item %d succeeded: %v", itemResult.Index, itemResult.Result)
    }
}
```

**Completion Criteria Options:**
- `MinSuccessful`: Minimum number of items that must succeed
- `ToleratedFailureCount`: Maximum number of failures allowed
- `ToleratedFailurePercentage`: Maximum percentage of failures allowed (0.0-100.0)

If completion criteria are not met, the operation fails with an error.

## Testing

The SDK includes a comprehensive testing framework:

```go
import (
	"testing"
	
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	durabletesting "github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/testing"
)

func TestWorkflow(t *testing.T) {
	runner := durabletesting.NewLocalDurableTestRunner(handler, durabletesting.LocalDurableTestRunnerConfig{
		SkipTime: true,
	})
	
	execution, err := runner.Run(durabletesting.RunConfig{
		Payload: map[string]interface{}{"userId": "123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	if execution.Status() != durabletesting.StatusSucceeded {
		t.Errorf("Expected SUCCEEDED, got %v", execution.Status())
	}
	
	// Get operations by name (not by index!)
	fetchStep, err := execution.GetOperation("fetch-user")
	if err != nil {
		t.Fatal(err)
	}
	
	if fetchStep.Type() != client.OperationTypeStep {
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
