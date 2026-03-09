package operations

import (
	"context"

	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// All runs all branches in parallel and returns a slice of their results.
// If any branch fails, All returns an AggregateError containing all failures.
// Corresponds to Promise.all semantics.
func All[TOut any](
	ctx context.Context,
	name string,
	branches []func(ctx context.Context) (TOut, error),
	opts ...ParallelOption[TOut],
) ([]TOut, error) {
	batch, err := Parallel(ctx, name, branches, opts...)
	if err != nil {
		return nil, err
	}
	if batch.FailureCount() > 0 {
		return nil, durableErrors.NewAggregateError(batch.GetErrors())
	}
	results := make([]TOut, len(batch.Items))
	for i, item := range batch.Items {
		results[i] = item.Value
	}
	return results, nil
}

// AllSettled runs all branches in parallel and returns a BatchResult regardless
// of individual branch outcomes. Corresponds to Promise.allSettled semantics.
func AllSettled[TOut any](
	ctx context.Context,
	name string,
	branches []func(ctx context.Context) (TOut, error),
	opts ...ParallelOption[TOut],
) (types.BatchResult[TOut], error) {
	return Parallel(ctx, name, branches, opts...)
}

// Any runs all branches in parallel and returns the first successful result.
// If all branches fail, Any returns an AggregateError.
// Corresponds to Promise.any semantics.
func Any[TOut any](
	ctx context.Context,
	name string,
	branches []func(ctx context.Context) (TOut, error),
	opts ...ParallelOption[TOut],
) (TOut, error) {
	var zero TOut
	minSuccessful := 1
	cfg := &types.BatchCompletionConfig{MinSuccessful: &minSuccessful}
	opts = append([]ParallelOption[TOut]{WithParallelCompletionConfig[TOut](cfg)}, opts...)

	batch, err := Parallel(ctx, name, branches, opts...)
	if err != nil {
		return zero, err
	}
	for _, item := range batch.Items {
		if item.Err == nil {
			return item.Value, nil
		}
	}
	return zero, durableErrors.NewAggregateError(batch.GetErrors())
}

// Race runs all branches in parallel and returns the result of the first branch
// to complete, whether it succeeded or failed.
// Corresponds to Promise.race semantics.
func Race[TOut any](
	ctx context.Context,
	name string,
	branches []func(ctx context.Context) (TOut, error),
	opts ...ParallelOption[TOut],
) (TOut, error) {
	var zero TOut
	minSuccessful := 1
	toleratedFailures := 0
	cfg := &types.BatchCompletionConfig{
		MinSuccessful:         &minSuccessful,
		ToleratedFailureCount: &toleratedFailures,
	}
	opts = append([]ParallelOption[TOut]{WithParallelCompletionConfig[TOut](cfg)}, opts...)

	batch, err := Parallel(ctx, name, branches, opts...)
	if err != nil {
		return zero, err
	}
	// Return first completed item (success preferred over failure in iteration order).
	for _, item := range batch.Items {
		if item.Err == nil {
			return item.Value, nil
		}
	}
	for _, item := range batch.Items {
		if item.Err != nil {
			return zero, item.Err
		}
	}
	return zero, nil
}
