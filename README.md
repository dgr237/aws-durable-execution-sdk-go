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
    "context"

    "github.com/aws/aws-lambda-go/lambda"
    durable "github.com/aws/durable-execution-sdk-go/pkg/durable"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/types"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

type OrderEvent struct {
    UserID string
    Amount float64
}

type OrderResult struct {
    OrderID string
    Status  string
}

var handler = durable.WithDurableExecution(func(ctx context.Context, event OrderEvent) (OrderResult, error) {
    // Step 1: Validate order (retried automatically on failure)
    validated, err := operations.Step(ctx, "validate-order", func(ctx context.Context) (string, error) {
        return validateOrder(event)
    }, operations.WithStepRetryStrategy[string](utils.Presets.ExponentialBackoff()))
    if err != nil {
        return OrderResult{}, err
    }

    // Step 2: Wait for payment approval from an external system
    _, err = operations.WaitForCallback[any](ctx, "payment-approval",
        func(ctx context.Context, callbackID string) error {
            return sendPaymentRequest(event.UserID, event.Amount, callbackID)
        },
        operations.WithWaitForCallbackTimeout[any](types.Duration{Hours: 1}),
    )
    if err != nil {
        return OrderResult{}, err
    }

    // Step 3: Confirm the order
    orderID, err := operations.Step(ctx, "confirm-order", func(ctx context.Context) (string, error) {
        return confirmOrder(validated)
    })
    if err != nil {
        return OrderResult{}, err
    }

    return OrderResult{OrderID: orderID, Status: "confirmed"}, nil
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

> **Important**: Code *outside* an `operations.Step(...)` call runs on every replay. Non-deterministic code (timestamps, UUIDs, random numbers, API calls) **must** be placed inside a step. Use `durable.CurrentTime(ctx)` instead of `time.Now()` for replay-safe current time.

### Step IDs

Each call to `operations.Step(...)`, `operations.Wait(...)`, etc. is assigned a deterministic hierarchical ID based on its position in the code (e.g., `"1"`, `"2"`, `"1-1"` for a child context). This ensures the replay order is stable as long as the code structure is unchanged.

### Handler Signature

The handler passed to `WithDurableExecution` must have the signature:

```go
func(ctx context.Context, event TEvent) (TResult, error)
```

The `ctx` carries an embedded `DurableContext`. Pass it directly to all `operations.*` functions — do not replace it with `context.Background()`.

---

## API Reference

All durable operations are package-level functions in the `operations` package that accept a `context.Context` as their first argument.

### `operations.Step`

Executes a function as a durable step. The result is checkpointed after success.

```go
result, err := operations.Step(ctx, "fetch-user", func(ctx context.Context) (User, error) {
    return fetchUserFromDB(userID)
}, operations.WithStepRetryStrategy[User](utils.Presets.ExponentialBackoff()),
   operations.WithStepSemantics[User](types.StepSemanticsAtLeastOncePerRetry))
```

**Functional options:**

| Option | Description |
|--------|-------------|
| `WithStepRetryStrategy[TOut](fn)` | Called on failure to determine retry; `fn` has signature `func(err error, attempt int) types.RetryDecision` |
| `WithStepSemantics[TOut](sem)` | `StepSemanticsAtLeastOncePerRetry` (default) or `StepSemanticsAtMostOncePerRetry` |
| `WithStepSerdes[TOut](s)` | Custom serialization (default: JSON) |

**`StepSemantics`:**

- `AtLeastOncePerRetry` (default): The step executes at least once per retry attempt. Safe for idempotent operations.
- `AtMostOncePerRetry`: A checkpoint is created *before* execution. Use for non-idempotent operations; combine with `NoRetry` for strict at-most-once.

### `operations.Wait`

Pauses execution for the specified duration. The Lambda invocation terminates and is resumed by the service after the timer fires.

```go
err := operations.Wait(ctx, "cooling-off-period", types.Duration{Days: 7})
```

### `operations.RunInChildContext`

Runs a function in an isolated child context with its own step counter. Use this to group related operations or enable concurrency.

```go
result, err := operations.RunInChildContext(ctx, "process-batch", func(ctx context.Context) (BatchResult, error) {
    step1, _ := operations.Step(ctx, "validate", func(ctx context.Context) (any, error) { ... })
    step2, _ := operations.Step(ctx, "transform", func(ctx context.Context) (any, error) { ... })
    return step2, nil
})
```

**Functional options:** `WithChildSerdes[T](s)`

### `operations.Invoke`

Invokes another Lambda function (durable or non-durable) and waits for its result.

```go
result, err := operations.Invoke[map[string]any, PaymentResult](
    ctx,
    "process-payment",
    "arn:aws:lambda:us-east-1:123456789012:function:payment-processor:1",
    map[string]any{"amount": 100},
)
```

**Functional options:** `WithInvokeSerdes[TIn, TOut](s)`

### `operations.WaitForCallback`

Runs `submitter` to hand off work to an external system, then suspends until the external system calls the `SendDurableExecutionCallbackSuccess` or `SendDurableExecutionCallbackFailure` APIs.

```go
result, err := operations.WaitForCallback[ApprovalResult](ctx, "human-approval",
    func(ctx context.Context, callbackID string) error {
        return sendApprovalEmail(approverEmail, callbackID)
    },
    operations.WithWaitForCallbackTimeout[ApprovalResult](types.Duration{Hours: 24}),
)
```

**Functional options:**

| Option | Description |
|--------|-------------|
| `WithWaitForCallbackTimeout[T](d)` | Maximum duration to wait before failing |
| `WithWaitForCallbackSerdes[T](s)` | Custom serialization |

### `operations.CreateCallback`

Low-level callback creation. Returns a result channel, the callback ID, and an error.

```go
ch, callbackID, err := operations.CreateCallback[MyResult](ctx, "my-callback")
if err != nil {
    return Result{}, err
}
// Send callbackID to external system
callbackResult := <-ch
if callbackResult.Err != nil {
    return Result{}, callbackResult.Err
}
result := callbackResult.Value
```

**Functional options:** `WithCallbackTimeout[TResult](d)`

### `operations.WaitForCondition`

Polls `checkFn` until the wait strategy says to stop. The `initialState` argument is required.

```go
finalState, err := operations.WaitForCondition(ctx, "wait-for-job",
    func(state JobState, ctx context.Context) (JobState, error) {
        return checkJobStatus(state.JobID)
    },
    JobState{JobID: "job-123", Status: "pending"},
    operations.WithConditionWaitStrategy(func(state JobState, attempt int) types.WaitStrategyResult {
        if state.Status == "completed" {
            return types.WaitStrategyResult{ShouldContinue: false}
        }
        delay := types.Duration{Seconds: min(attempt*5, 60)}
        return types.WaitStrategyResult{ShouldContinue: true, Delay: &delay}
    }),
)
```

**Functional options:**

| Option | Description |
|--------|-------------|
| `WithConditionWaitStrategy[TState](fn)` | Controls polling delay and termination |
| `WithConditionSerdes[TState](s)` | Custom serialization for state |

### `operations.Map`

Processes a typed slice of items with durable operations and optional concurrency control.

```go
results, err := operations.Map(ctx, "process-users", users,
    func(ctx context.Context, user User, index int, items []User) (ProcessedUser, error) {
        return processUser(user)
    },
    operations.WithMapMaxConcurrency[User, ProcessedUser](5),
)
for _, r := range results.Items {
    if r.Err == nil {
        fmt.Println(r.Value)
    }
}
```

**Functional options:**

| Option | Description |
|--------|-------------|
| `WithMapMaxConcurrency[TIn, TOut](n)` | Maximum concurrent iterations |
| `WithMapCompletionConfig[TIn, TOut](cfg)` | Early-exit policy (`MinSuccessful`, `ToleratedFailureCount`, `ToleratedFailurePercentage`) |
| `WithMapItemNamer[TIn, TOut](fn)` | Custom name for each iteration |
| `WithMapSerdes[TIn, TOut](s)` | Custom serialization |

### `operations.Parallel`

Executes multiple branch functions concurrently with optional concurrency control.

```go
results, err := operations.Parallel(ctx, "parallel-tasks", []func(context.Context) (any, error){
    func(ctx context.Context) (any, error) {
        return operations.Step(ctx, "task-1", func(ctx context.Context) (any, error) { return doTask1() })
    },
    func(ctx context.Context) (any, error) {
        return operations.Step(ctx, "task-2", func(ctx context.Context) (any, error) { return doTask2() })
    },
}, operations.WithParallelMaxConcurrency[any](2))
```

**Functional options:**

| Option | Description |
|--------|-------------|
| `WithParallelMaxConcurrency[TOut](n)` | Maximum concurrent branches |
| `WithParallelCompletionConfig[TOut](cfg)` | Early-exit policy |
| `WithParallelSerdes[TOut](s)` | Custom serialization |

### Promise Combinators

Higher-level wrappers over `Parallel` for common coordination patterns:

```go
// Wait for all to succeed — returns []TOut or AggregateError
results, err := operations.All(ctx, "all", branches)

// Wait for all to settle — returns BatchResult[TOut] regardless of failures
settled, _ := operations.AllSettled(ctx, "settle", branches)

// First to succeed wins — returns TOut or AggregateError if all fail
first, err := operations.Any(ctx, "any", branches)

// First to settle wins — returns TOut of first completed branch
winner, err := operations.Race(ctx, "race", branches)
```

All combinators accept the same `...ParallelOption[TOut]` variadic options as `Parallel`.

---

## Retry Strategies

Import `"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"` for retry helpers.

### Built-in Presets

```go
// Exponential backoff with full jitter (maxAttempts=3, initialDelay=5s, maxDelay=5m)
utils.Presets.ExponentialBackoff()

// No retries
utils.Presets.NoRetry()

// Fixed delay
utils.Presets.FixedDelay(types.Duration{Seconds: 10}, 5)
```

### Custom Strategy

```go
customRetry := utils.CreateRetryStrategy(utils.RetryStrategyConfig{
    MaxAttempts:  5,
    InitialDelay: &types.Duration{Seconds: 2},
    MaxDelay:     &types.Duration{Minutes: 2},
    BackoffRate:  1.5,
    Jitter:       types.JitterStrategyHalf,
})

// Use with a step:
result, err := operations.Step(ctx, "my-step", fn,
    operations.WithStepRetryStrategy[MyType](customRetry))
```

---

## Determinism Helpers

Code between steps runs on every replay and must be deterministic. Use the helpers in the `durable` package:

```go
import durable "github.com/aws/durable-execution-sdk-go/pkg/durable"

// Use instead of time.Now() — returns the execution's start time from checkpointed state
startedAt, err := durable.CurrentTime(ctx)
```

Forbidden patterns outside steps: `time.Now()`, `rand.*`, `uuid.New()`, unordered map iteration, direct API/DB calls. See `determinism.go` for the full rules.

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

Pass to any operation via its `WithXxxSerdes` option (e.g., `WithStepSerdes[TOut](mySerdes)`).

---

## Checkpoint Strategy

Control how operation updates are batched into API calls via `Config.CheckpointStrategy`:

```go
handler := durable.WithDurableExecution(myHandler, &durable.Config{
    CheckpointStrategy: types.CheckpointStrategyEager,      // default — one API call per update
    // CheckpointStrategy: types.CheckpointStrategyBatched,  // batch updates together
    // CheckpointStrategy: types.CheckpointStrategyOptimistic, // fire-and-forget (fastest, less durable)
})
```

---

## Testing

Inject a mock client via `Config`. The `checkpoint.Client` interface requires two methods:

```go
import (
    "context"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/checkpoint"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

type mockClient struct{}

func (m *mockClient) Checkpoint(ctx context.Context, r types.CheckpointDurableExecutionRequest) (*types.CheckpointDurableExecutionResponse, error) {
    token := "next-token"
    return &types.CheckpointDurableExecutionResponse{NextCheckpointToken: &token}, nil
}

func (m *mockClient) GetExecutionState(ctx context.Context, r types.GetDurableExecutionStateRequest) (*types.GetDurableExecutionStateResponse, error) {
    return &types.GetDurableExecutionStateResponse{}, nil
}

// In your test:
handler := durable.WithDurableExecution(myHandler, &durable.Config{Client: &mockClient{}})
```

---

## Differences from the TypeScript SDK

| Feature | TypeScript | Go |
|---------|-----------|-----|
| Operations API | Methods on context (e.g., `ctx.step()`) | Functions in `operations` package (e.g., `operations.Step(ctx, ...)`) |
| Handler signature | `(event, ctx)` | `(ctx context.Context, event TEvent)` |
| Options | Config structs | Functional options (e.g., `WithStepRetryStrategy`) |
| Generic types | `DurableContext<TLogger>` | `types.DurableContext` interface |
| Async model | Promises / async-await | Goroutines + channels |
| Replay suspension | JS event loop | `select {}` + goroutine |
| Step results | Typed generics | Typed generics via Go type parameters |
| Logger | Structured custom logger | `types.Logger` interface |

---

## Architecture

```
pkg/durable/
├── durable.go                     # WithDurableExecution entry point, Config
├── determinism.go                 # CurrentTime helper and determinism documentation
├── types/
│   ├── types.go                   # All public types and interfaces
│   └── limits.go                  # Execution limit constants (§14.1)
├── operations/                    # Durable operations API
│   ├── step.go                    # Step operation
│   ├── wait.go                    # Wait operation
│   ├── wait_for_callback.go       # WaitForCallback operation
│   ├── wait_for_condition.go      # WaitForCondition operation
│   ├── create_callback.go         # CreateCallback operation
│   ├── invoke.go                  # Invoke operation
│   ├── run_in_child_context.go    # RunInChildContext operation
│   ├── map.go                     # Map batch operation
│   ├── parallel.go                # Parallel batch operation
│   ├── combinators.go             # All, AllSettled, Any, Race
│   ├── completion.go              # BatchCompletionConfig evaluation
│   └── options.go                 # Functional option types for all operations
├── context/
│   ├── durable_context.go         # DurableContext implementation
│   ├── execution_context.go       # ExecutionContext + Client interface
│   ├── step_id.go                 # Hierarchical step ID generation and replay validation
│   └── factory.go                 # NewRootContext constructor
├── checkpoint/
│   ├── manager.go                 # Checkpoint batching and queue management
│   └── termination.go             # TerminationManager lifecycle coordination
├── client/
│   └── client.go                  # AWS Lambda client adapter
├── errors/
│   └── errors.go                  # SDK error types and runtime error classification
└── utils/
    ├── serdes.go                   # Default JSON Serdes + helpers
    ├── logger.go                   # DefaultLogger, NopLogger
    └── retry.go                    # RetryStrategyConfig, CreateRetryStrategy, Presets
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

The SDK automatically detects the Lambda environment. No additional configuration is needed for most cases.

**Custom logger:**
```go
import durablecontext "github.com/aws/durable-execution-sdk-go/pkg/durable/context"

dc, err := durablecontext.GetDurableContext(ctx)
if err != nil {
    return Result{}, err
}
dc.ConfigureLogger(types.LoggerConfig{
    CustomLogger: myLogger,
    ModeAware:    aws.Bool(true), // Suppress logs during replay
})
```

**Custom client (for testing or staging):**
```go
handler := durable.WithDurableExecution(myHandler, &durable.Config{
    Client: myCustomClient,
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
