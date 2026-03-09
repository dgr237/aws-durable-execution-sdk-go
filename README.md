# AWS Durable Execution SDK for Go

A Go implementation of the [AWS Durable Execution SDK](https://aws.amazon.com/lambda/durable-execution/), faithfully ported from the TypeScript (`aws-durable-execution-sdk-js`) reference implementation.

## Overview

The SDK enables developers to write multi-step, fault-tolerant Lambda functions that **automatically persist their state** as they progress. Each durable operation (step, wait, callback, etc.) checkpoints its result. If the Lambda function times out or is interrupted, the service replays it from the beginning, skipping already-completed operations and restoring their results from the checkpoint store.

### Key Capabilities

- Each operation can run up to the Lambda function's 15-minute timeout
- The entire multi-operation workflow can execute for extended periods asynchronously
- Functions only pay for active compute time (not waiting time)
- Built-in retry with exponential backoff, jitter, and customisable strategies
- Concurrency-controlled parallel and map operations
- External callback support for human-in-the-loop workflows
- Polling via `WaitForCondition`
- Child contexts for grouped, isolated operation namespaces

---

## Installation

```bash
go get github.com/aws/durable-execution-sdk-go
```

**Requirements:**
- Go 1.21 or later (tested with Go 1.21+)
- AWS Lambda execution environment with Durable Execution enabled

## Quick Start

```go
package main

import (
    "fmt"

    "github.com/aws/aws-lambda-go/lambda"
    durable "github.com/aws/durable-execution-sdk-go/pkg/durable"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/types"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

type OrderEvent struct {
    UserID  string
    Amount  float64
}

type OrderResult struct {
    OrderID string
    Status  string
}

var handler = durable.WithDurableExecution(func(event OrderEvent, ctx types.DurableContext) (OrderResult, error) {
    // Step 1: Validate order (retried automatically on failure)
    validatedRaw, err := operations.Step(ctx, "validate-order", func(sc types.StepContext) (any, error) {
        return validateOrder(event)
    }, &types.StepConfig{
        RetryStrategy: utils.Presets.ExponentialBackoff(),
    })
    if err != nil {
        return OrderResult{}, err
    }

    // Step 2: Wait for payment approval from an external system
    _, err = operations.WaitForCallback[any](ctx, "payment-approval", func(sc types.StepContext, callbackID string) error {
        return sendPaymentRequest(event.UserID, event.Amount, callbackID)
    }, &types.WaitForCallbackConfig{
        Timeout: &types.Duration{Hours: 1},
    })
    if err != nil {
        return OrderResult{}, err
    }

    // Step 3: Confirm the order
    orderIDRaw, err := operations.Step(ctx, "confirm-order", func(sc types.StepContext) (any, error) {
        return confirmOrder(validatedRaw)
    }, nil)
    if err != nil {
        return OrderResult{}, err
    }

    return OrderResult{
        OrderID: orderIDRaw.(string),
        Status:  "confirmed",
    }, nil
}, nil)

func main() {
    lambda.Start(handler)
}
```

---

## Core Concepts

### Replay Model

When a durable function is first invoked, it runs normally and persists the result of each operation as a **checkpoint**. When the function is re-invoked (due to timeout, wait expiry, or callback receipt), it replays from the beginning:

- For each operation that already completed, the SDK **skips execution** and returns the stored result.
- When it reaches the first operation that hasn't completed yet, it **executes normally**.

> **Important**: Code *outside* a step runs on every replay. Non-deterministic code (timestamps, UUIDs, random numbers, API calls) **must** be placed inside a `ctx.Step(...)` call.

### Step IDs

Each call to `ctx.Step(...)`, `ctx.Wait(...)`, etc. is assigned a deterministic hierarchical ID based on its position in the code (e.g., `"1"`, `"2"`, `"1-1"` for a child context). This ensures the replay order is stable as long as the code structure is unchanged.

---

## API Reference

### `ctx.Step(name, fn, config)`

Executes a function as a durable step. The result is checkpointed after success.

```go
result, err := ctx.Step("fetch-user", func(sc types.StepContext) (any, error) {
    return fetchUserFromDB(userID)
}, &types.StepConfig{
    RetryStrategy: retry.Presets.ExponentialBackoff(),
    Semantics:     types.StepSemanticsAtLeastOncePerRetry,
})
```

**Config options:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `RetryStrategy` | `func(error, int) RetryDecision` | default 3-attempt backoff | Called on failure to determine retry |
| `Semantics` | `StepSemantics` | `AtLeastOncePerRetry` | Execution guarantee per retry attempt |
| `Serdes` | `Serdes` | JSON | Custom serialization |

**`StepSemantics`:**

- `AtLeastOncePerRetry` (default): The step executes at least once per retry attempt. Safe for idempotent operations.
- `AtMostOncePerRetry`: A checkpoint is created *before* execution. Use for non-idempotent operations; combine with `NoRetry` for strict at-most-once.

### `ctx.Wait(name, duration)`

Pauses execution for the specified duration. The Lambda invocation terminates and is resumed by the service after the timer fires.

```go
err := ctx.Wait("cooling-off-period", types.Duration{Days: 7})
```

### `ctx.RunInChildContext(name, fn, config)`

Runs a function in an isolated child context with its own step counter. Use this to group related operations or enable concurrency.

```go
result, err := ctx.RunInChildContext("process-batch", func(child types.DurableContext) (any, error) {
    step1, _ := child.Step("validate", func(sc types.StepContext) (any, error) { ... }, nil)
    step2, _ := child.Step("transform", func(sc types.StepContext) (any, error) { ... }, nil)
    return step2, nil
}, nil)
```

### `ctx.Invoke(name, funcID, input, config)`

Invokes another Lambda function (durable or non-durable) and waits for its result.

```go
result, err := ctx.Invoke(
    "process-payment",
    "arn:aws:lambda:us-east-1:123456789012:function:payment-processor:1",
    map[string]any{"amount": 100},
    nil,
)
```

### `ctx.WaitForCallback(name, submitter, config)`

Runs `submitter` to hand off work to an external system, then suspends until the external system calls the `SendDurableExecutionCallbackSuccess` or `SendDurableExecutionCallbackFailure` APIs.

```go
result, err := ctx.WaitForCallback("human-approval", func(sc types.StepContext, callbackID string) error {
    return sendApprovalEmail(approverEmail, callbackID)
}, &types.WaitForCallbackConfig{
    Timeout: &types.Duration{Hours: 24},
})
```

### `ctx.CreateCallback(name, config)`

Low-level callback creation. Returns a channel and the callback ID separately.

```go
ch, callbackID, err := ctx.CreateCallback("my-callback", nil)
// Send callbackID to external system
result := <-ch
```

### `ctx.WaitForCondition(name, checkFn, config)`

Polls `checkFn` until a condition is met, controlled by `WaitStrategy`.

```go
finalState, err := ctx.WaitForCondition("wait-for-job", func(state any, sc types.StepContext) (any, error) {
    return checkJobStatus(state.(JobState).JobID)
}, types.WaitForConditionConfig{
    InitialState: JobState{JobID: "job-123", Status: "pending"},
    WaitStrategy: func(state any, attempt int) types.WaitStrategyResult {
        if state.(JobState).Status == "completed" {
            return types.WaitStrategyResult{ShouldContinue: false}
        }
        delay := types.Duration{Seconds: min(attempt*5, 60)}
        return types.WaitStrategyResult{ShouldContinue: true, Delay: &delay}
    },
})
```

### `ctx.Map(name, items, mapFn, config)`

Processes an array of items with durable operations and optional concurrency control.

```go
results, err := ctx.Map("process-users", toAnySlice(users),
    func(ctx types.DurableContext, item any, index int, items []any) (any, error) {
        return processUser(item.(User))
    },
    &types.MapConfig{MaxConcurrency: 5},
)
for _, r := range results.Items {
    if r.Err == nil {
        fmt.Println(r.Value)
    }
}
```

### `ctx.Parallel(name, branches, config)`

Executes multiple branch functions concurrently with optional concurrency control.

```go
results, err := ctx.Parallel("parallel-tasks", []func(types.DurableContext) (any, error){
    func(ctx types.DurableContext) (any, error) {
        return ctx.Step("task-1", func(sc types.StepContext) (any, error) { return doTask1() }, nil)
    },
    func(ctx types.DurableContext) (any, error) {
        return ctx.Step("task-2", func(sc types.StepContext) (any, error) { return doTask2() }, nil)
    },
}, &types.ParallelConfig{MaxConcurrency: 2})
```

### Promise Combinators

For coordinating already-running channels:

```go
// Wait for all to succeed
results, err := ctx.PromiseAll("all", []<-chan types.StepResult{ch1, ch2, ch3})

// Wait for all to settle (success or failure)
settled, _ := ctx.PromiseAllSettled("settle", channels)

// First to succeed wins
first, err := ctx.PromiseAny("any", channels)

// First to settle wins
winner, err := ctx.PromiseRace("race", channels)
```

> **Prefer `ctx.Map()` and `ctx.Parallel()`** for durable concurrent operations. Promise combinators are for lightweight, non-durable coordination.

---

## Retry Strategies

### Built-in Presets

```go
import "github.com/aws/durable-execution-sdk-go/utils/retry"

// Exponential backoff with full jitter (default for most use cases)
retry.Presets.ExponentialBackoff()

// No retries
retry.Presets.NoRetry()

// Fixed delay
retry.Presets.FixedDelay(types.Duration{Seconds: 10}, 5)
```

### Custom Strategy

```go
customRetry := retry.CreateRetryStrategy(retry.RetryStrategyConfig{
    MaxAttempts:  5,
    InitialDelay: &types.Duration{Seconds: 2},
    MaxDelay:     &types.Duration{Minutes: 2},
    BackoffRate:  1.5,
    Jitter:       types.JitterStrategyHalf,
})
```

---

## Custom Serialization

Implement `types.Serdes` for custom serialization (e.g., storing large payloads in S3):

```go
type S3Serdes struct{ bucket string }

func (s S3Serdes) Serialize(value any, entityID, arn string) (string, error) {
    data, _ := json.Marshal(value)
    key := fmt.Sprintf("%s/%s.json", arn, entityID)
    // upload to S3...
    return "s3://" + s.bucket + "/" + key, nil
}

func (s S3Serdes) Deserialize(pointer, entityID, arn string) (any, error) {
    // download from S3 and unmarshal...
}
```

---

## Testing

Inject a mock client via `Config`:

```go
type mockClient struct {
    checkpoints []types.CheckpointDurableExecutionRequest
}

func (m *mockClient) Checkpoint(r types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error) {
    m.checkpoints = append(m.checkpoints, r)
    token := "next-token"
    return &types.CheckpointDurableExecutionResponse{NextCheckpointToken: &token}, nil
}

func (m *mockClient) CheckpointToken() string {
    return "test-token"
}

// In your test:
mock := &mockClient{}
handler := durable.WithDurableExecution(myHandler, &durable.Config{Client: mock})
```

---

## Differences from the TypeScript SDK

| Feature | TypeScript | Go |
|---------|-----------|-----|
| Operations API | Methods on context (e.g., `ctx.step()`) | Functions in `operations` package (e.g., `operations.Step()`) |
| Generic types | `DurableContext<TLogger>` | `types.DurableContext` interface |
| Async model | Promises / async-await | Goroutines + channels |
| Replay suspension | JS event loop | `select {}` + goroutine |
| Step results | Typed generics | `any` (use type assertions) |
| Optional names | Overloaded signatures | Empty string `""` = unnamed |
| Logger | Structured custom logger | `types.Logger` interface |

---

## Architecture

```
pkg/durable/
├── durable.go                     # WithDurableExecution entry point
├── types/types.go                 # All public types and interfaces
├── operations/                    # Durable operations API
│   ├── step.go                    # Step operation
│   ├── wait.go                    # Wait operation
│   ├── wait_for_callback.go       # WaitForCallback operation
│   ├── wait_for_condition.go      # WaitForCondition operation
│   ├── create_callback.go         # CreateCallback operation
│   ├── invoke.go                  # Invoke operation
│   ├── run_in_child_context.go    # RunInChildContext operation
│   ├── map.go                     # Map batch operation
│   └── parallel.go                # Parallel batch operation
├── context/
│   ├── durable_context.go         # DurableContext implementation
│   ├── execution_context.go       # Builds ExecutionContext from invocation input
│   ├── step_id.go                 # Hierarchical step ID generation and replay validation
│   └── factory.go                 # NewRootContext constructor
├── checkpoint/
│   ├── manager.go                 # Checkpoint batching and queue management
│   └── termination.go             # TerminationManager lifecycle coordination
├── client/
│   └── client.go                  # AWS Lambda client adapter
├── errors/
│   └── errors.go                  # SDK error types
└── utils/
    ├── serdes.go                   # Default JSON Serdes + helpers
    ├── logger.go                   # DefaultLogger, ModeAwareLogger, NopLogger
    └── retry.go                    # RetryStrategyConfig + presets
```

---

## Configuration and Deployment

### Lambda Function Requirements

Your Lambda function must be configured with:

1. **Durable Execution enabled** via AWS Console, CLI, or IaC
2. **Qualified ARN**: Deploy with version or alias (not `$LATEST`)
3. **IAM Permissions**: Lambda execution role needs:
   ```json
   {
     "Effect": "Allow",
     "Action": [
       "lambda:CheckpointDurableExecution",
       "lambda:GetDurableExecutionState"
     ],
     "Resource": "*"
   }
   ```

4. **For cross-Lambda invocation**, also add:
   ```json
   {
     "Effect": "Allow",
     "Action": "lambda:InvokeFunction",
     "Resource": "arn:aws:lambda:*:*:function:*"
   }
   ```

### Environment Configuration

The SDK automatically detects Lambda environment. No additional configuration needed for most cases.

**Custom logger:**
```go
ctx.ConfigureLogger(types.LoggerConfig{
    CustomLogger: myLogger,
    ModeAware:    aws.Bool(true), // Suppress logs during replay
})
```

**Custom client (for testing):**
```go
handler := durable.WithDurableExecution(myHandler, &durable.Config{
    Client: mockClient,
})
```

### Container Image Deployment

The SDK works with both ZIP and container image deployments:

```dockerfile
FROM public.ecr.aws/lambda/go:1.24

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o main

CMD ["main"]
```

---

## Examples

See the [`examples/`](./examples/) directory for complete working examples:

- **[order-processing](./examples/order_processing/)**: End-to-end order workflow with steps, callbacks, and parallel processing
- **[lambda-invoke-map](./examples/lamba-invoke-map/)**: Cross-Lambda orchestration with Map for fan-out processing

---

## Contributing

Contributions are welcome! Please ensure:

1. All tests pass: `go test ./...`
2. Code is formatted: `go fmt ./...`
3. Linter passes: `golangci-lint run`
4. New features include tests and documentation

---

## License

This SDK is distributed under the Apache License, Version 2.0. See [LICENSE](./LICENSE) for more information.

---

## Resources

- [AWS Lambda Durable Execution Documentation](https://docs.aws.amazon.com/lambda/latest/dg/durable-execution.html)
- [TypeScript SDK (Reference Implementation)](https://github.com/aws/aws-durable-execution-sdk-js)
- [AWS Lambda Developer Guide](https://docs.aws.amazon.com/lambda/latest/dg/)

---

## Support

For issues, questions, or contributions, please open an issue on GitHub.

