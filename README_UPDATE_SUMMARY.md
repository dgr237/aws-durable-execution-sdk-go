# README Update Summary

This document summarizes the comprehensive updates made to the README to align it with the current Go SDK implementation.

## Date
March 9, 2026

## Overview
The README has been completely updated to reflect the actual implementation of the AWS Durable Execution SDK for Go, which uses an **operations package pattern** rather than methods on the context object.

## Major Changes

### 1. **API Pattern Change**
**Before:** Methods on context (e.g., `ctx.Step()`, `ctx.Wait()`)
**After:** Functions in operations package (e.g., `operations.Step()`, `operations.Wait()`)

This is a fundamental architectural difference from the TypeScript SDK and was consistently updated throughout the README.

### 2. **Import Paths Updated**
All import statements now use the correct package structure:
```go
import (
    "github.com/aws/durable-execution-sdk-go/pkg/durable"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/types"
    "github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)
```

### 3. **All Code Examples Updated**
- Quick Start example
- API Reference examples for all operations:
  - `operations.Step()`
  - `operations.Wait()`
  - `operations.RunInChildContext()`
  - `operations.Invoke()`
  - `operations.WaitForCallback()`
  - `operations.CreateCallback()`
  - `operations.WaitForCondition()`
  - `operations.Map()`
  - `operations.Parallel()`
  - Promise combinators

### 4. **New Sections Added**

#### **Best Practices**
- ✅ Do: Place non-deterministic operations inside steps
- ✅ Do: Use child contexts for nested operations
- ✅ Do: Qualify Lambda ARNs with version or alias
- ❌ Don't: Call non-deterministic code outside steps
- ❌ Don't: Nest durable operations directly
- ❌ Don't: Use $LATEST or unqualified ARNs

#### **Common Patterns**
- Human-in-the-Loop Approval
- Polling External Systems
- Fan-Out / Fan-In with Map
- Cross-Lambda Orchestration

#### **Troubleshooting**
- Context usage errors
- Cannot start operation that already exists
- Invalid checkpoint token
- Lambda invocation with unqualified ARNs

#### **Configuration and Deployment**
- Lambda function requirements
- IAM permissions
- Environment configuration
- Container image deployment
- Examples directory references

### 5. **Retry Strategies Section Updated**
Changed from:
```go
import "github.com/aws/durable-execution-sdk-go/utils/retry"
retry.Presets.ExponentialBackoff()
```

To:
```go
import "github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
utils.Presets.ExponentialBackoff()
```

### 6. **Custom Serialization Section Updated**
Updated method signatures to match actual `types.Serdes` interface:
```go
Serialize(value any, entityID, executionArn string) (string, error)
Deserialize(pointer, entityID, executionArn string) (any, error)
```

### 7. **Testing Section Updated**
Corrected mock client interface to match actual `checkpoint.Client`:
```go
type mockClient struct {
    checkpoints []types.OperationUpdate
}

func (m *mockClient) Checkpoint(ops []types.OperationUpdate) error
func (m *mockClient) GetExecutionState(marker *string) ([]types.Operation, *string, error)
func (m *mockClient) CheckpointToken() string
```

### 8. **Differences from TypeScript SDK**
Added new row highlighting the operations package pattern:
| Feature | TypeScript | Go |
|---------|-----------|-----|
| Operations API | Methods on context | Functions in `operations` package |

### 9. **Architecture Section Updated**
Completely revised to show the actual package structure:
```
pkg/durable/
├── durable.go
├── types/types.go
├── operations/          # NEW: Durable operations API
│   ├── step.go
│   ├── wait.go
│   ├── invoke.go
│   ├── map.go
│   └── parallel.go
├── context/
├── checkpoint/
├── client/
├── errors/
└── utils/
```

## Technical Accuracy Improvements

### Type Parameters
Updated generic function signatures:
- `operations.Invoke[TIn, TOut]` instead of untyped `ctx.Invoke`
- `operations.WaitForCallback[TResult]` with proper type annotation

### Configuration Structs
All config types properly namespaced with `types.` prefix:
- `types.StepConfig`
- `types.InvokeConfig`
- `types.WaitForCallbackConfig`
- `types.MapConfig`
- `types.ParallelConfig`
- `types.WaitForConditionConfig[TState]`

### Error Handling
Examples now show proper error handling patterns throughout.

### Realistic Examples
- Added complete imports in examples
- Included proper type assertions for `any` returns
- Showed realistic Lambda ARNs with qualified versions

## Files Modified
- `README.md` - Complete rewrite with ~497 lines (expanded from 407)

## Files Created
- `RAW_OPERATION_FIX.md` - Documentation of the raw operation fields fix
- `README_UPDATE_SUMMARY.md` - This summary document

## Testing
All code examples have been verified to:
1. Use correct import paths from the actual codebase
2. Match function signatures from the implementation
3. Follow Go idioms and best practices
4. Demonstrate realistic usage patterns

## Breaking Changes from Previous README
Users following the old README would need to:
1. Update all import paths to use `/pkg/durable/*` structure
2. Change from `ctx.Step()` to `operations.Step(ctx, ...)`
3. Change from `ctx.Wait()` to `operations.Wait(ctx, ...)`
4. Apply same pattern to all other operations
5. Update retry strategy imports to use `utils` package
6. Update test mocks to match actual `checkpoint.Client` interface

## Next Steps
Recommended follow-up actions:
1. Update example code in `examples/` directory to match README patterns
2. Add code generation or linting to ensure examples stay in sync
3. Consider adding a migration guide for users of old API
4. Add godoc comments that reference README sections
5. Create video tutorials or blog posts using updated examples

## Validation
The updated README has been validated to:
- ✅ Match actual package structure in `pkg/durable/`
- ✅ Use correct import paths from `go.mod`
- ✅ Reference actual function signatures from implementation
- ✅ Include proper Go 1.21+ requirements
- ✅ Show realistic AWS Lambda ARN formats
- ✅ Include comprehensive troubleshooting guidance
- ✅ Provide practical, copy-pasteable code examples

