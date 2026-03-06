package operations

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
)

// Map processes an array of items with concurrency control.
// Each item is processed by the provided function, and results are collected.
func Map[T any, R any](ctx context.Context, name string, items []T, fn func(context.Context, T, int) (R, error), cfg config.MapConfig) (*durablecontext.BatchResult[R], error) {
	// Generate operation ID for the map operation
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if map already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Map already completed, deserialize results
		durablecontext.Logger(ctx).Debug("Map already completed: %s", name)
		if result.Result != nil {
			var batchResult durablecontext.BatchResult[R]
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &batchResult); err != nil {
				return nil, fmt.Errorf("failed to deserialize map result: %w", err)
			}
			return &batchResult, nil
		}
		return durablecontext.NewBatchResult[R]([]durablecontext.BatchItemResult[R]{}), nil
	}

	if result.IsFailed() {
		return nil, &durable.CallableRuntimeError{
			Message:       "map operation failed previously",
			OperationName: name,
		}
	}

	// Execute map operation
	durablecontext.Logger(ctx).Info("Executing map: %s with %d items", name, len(items))

	subType := client.OperationSubTypeMap

	startUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    types.OperationTypeContext,
		Action:  types.OperationActionStart,
		SubType: &subType,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(startUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, startUpdate)
	}

	// Process items with concurrency control
	results, err := processMapItems(ctx, name, items, fn, cfg)
	if err != nil {
		// Check if it's a suspension
		if durable.IsSuspendExecution(err) {
			return nil, err
		}

		// Map failed
		errorObj := &types.ErrorObject{
			ErrorType:    aws.String("MapError"),
			ErrorMessage: aws.String(err.Error()),
		}

		failUpdate := types.OperationUpdate{
			Id:     aws.String(opID.OperationID),
			Type:   types.OperationTypeContext,
			Action: types.OperationActionFail,
			Error:  errorObj,
		}

		_ = durablecontext.State(ctx).Checkpoint(failUpdate)

		return nil, &durable.CallableRuntimeError{
			Message:       "map operation failed",
			OperationName: name,
			Cause:         err,
		}
	}

	// Check completion criteria
	batchResult := durablecontext.NewBatchResult(results)
	if err := checkCompletionCriteria(batchResult, cfg.CompletionConfig, len(items)); err != nil {
		return nil, err
	}

	// Map succeeded
	durablecontext.Logger(ctx).Info("Map succeeded: %s", name)

	serialized, err := durablecontext.SerDes(ctx).Serialize(batchResult)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize map result: %w", err)
	}

	successUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Type:    types.OperationTypeContext,
		Action:  types.OperationActionSucceed,
		Payload: &serialized,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(successUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, successUpdate)
	}

	return batchResult, nil
}

// processMapItems processes items with concurrency control.
func processMapItems[T any, R any](ctx context.Context, mapName string, items []T, fn func(context.Context, T, int) (R, error), cfg config.MapConfig) ([]durablecontext.BatchItemResult[R], error) {
	itemCount := len(items)
	results := make([]durablecontext.BatchItemResult[R], itemCount)
	resultsMu := sync.Mutex{}

	// Determine concurrency level
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = itemCount // No limit
	}

	// Create semaphore for concurrency control
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	errChan := make(chan error, itemCount)

	for i, item := range items {
		wg.Add(1)
		go func(index int, item T) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Create child context for this iteration
			iterationName := fmt.Sprintf("%s-iteration-%d", mapName, index)
			iterResult, err := RunInChildContext(ctx, iterationName, func(childCtx context.Context) (R, error) {
				return fn(childCtx, item, index)
			})

			resultsMu.Lock()
			results[index] = durablecontext.BatchItemResult[R]{
				Index:  index,
				Result: iterResult,
				Error:  err,
			}
			resultsMu.Unlock()

			if err != nil && durable.IsSuspendExecution(err) {
				// Propagate suspension
				errChan <- err
			}
		}(i, item)
	}

	wg.Wait()
	close(errChan)

	// Check for suspension errors
	for err := range errChan {
		if durable.IsSuspendExecution(err) {
			return nil, err
		}
	}

	return results, nil
}

// Parallel executes multiple branches in parallel with concurrency control.
func Parallel[T any](ctx context.Context, name string, branches []durablecontext.ParallelBranch[T], cfg config.ParallelConfig) (*durablecontext.BatchResult[T], error) {
	// Generate operation ID for the parallel operation
	opID := durablecontext.NextOperationID(ctx, name)

	// Check if parallel already completed
	result := durablecontext.State(ctx).GetCheckpointResult(opID.OperationID)

	if result.IsSucceeded() {
		// Parallel already completed
		durablecontext.Logger(ctx).Debug("Parallel already completed: %s", name)
		if result.Result != nil {
			var batchResult durablecontext.BatchResult[T]
			if err := durablecontext.SerDes(ctx).Deserialize(*result.Result, &batchResult); err != nil {
				return nil, fmt.Errorf("failed to deserialize parallel result: %w", err)
			}
			return &batchResult, nil
		}
		return durablecontext.NewBatchResult[T]([]durablecontext.BatchItemResult[T]{}), nil
	}

	if result.IsFailed() {
		return nil, &durable.CallableRuntimeError{
			Message:       "parallel operation failed previously",
			OperationName: name,
		}
	}

	// Execute parallel operation
	durablecontext.Logger(ctx).Info("Executing parallel: %s with %d branches", name, len(branches))

	subType := client.OperationSubTypeParallel

	startUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Name:    &name,
		Type:    types.OperationTypeContext,
		Action:  types.OperationActionStart,
		SubType: &subType,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(startUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, startUpdate)
	}

	// Process branches with concurrency control
	results, err := processParallelBranches(ctx, name, branches, cfg)
	if err != nil {
		// Check if it's a suspension
		if durable.IsSuspendExecution(err) {
			return nil, err
		}

		// Parallel failed
		errorObj := &types.ErrorObject{
			ErrorType:    aws.String("ParallelError"),
			ErrorMessage: aws.String(err.Error()),
		}

		failUpdate := types.OperationUpdate{
			Id:     aws.String(opID.OperationID),
			Type:   types.OperationTypeContext,
			Action: types.OperationActionFail,
			Error:  errorObj,
		}

		_ = durablecontext.State(ctx).Checkpoint(failUpdate)

		return nil, &durable.CallableRuntimeError{
			Message:       "parallel operation failed",
			OperationName: name,
			Cause:         err,
		}
	}

	// Check completion criteria
	batchResult := durablecontext.NewBatchResult(results)
	if err := checkCompletionCriteria(batchResult, cfg.CompletionConfig, len(branches)); err != nil {
		return nil, err
	}

	// Parallel succeeded
	durablecontext.Logger(ctx).Info("Parallel succeeded: %s", name)

	serialized, err := durablecontext.SerDes(ctx).Serialize(batchResult)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize parallel result: %w", err)
	}

	successUpdate := types.OperationUpdate{
		Id:      aws.String(opID.OperationID),
		Type:    types.OperationTypeContext,
		Action:  types.OperationActionSucceed,
		Payload: &serialized,
	}

	if cfg.CheckpointAsync {
		_ = durablecontext.State(ctx).Checkpoint(successUpdate)
	} else {
		_ = durablecontext.State(ctx).CheckpointSync(ctx, successUpdate)
	}

	return batchResult, nil
}

// processParallelBranches processes branches with concurrency control.
func processParallelBranches[T any](ctx context.Context, parallelName string, branches []durablecontext.ParallelBranch[T], cfg config.ParallelConfig) ([]durablecontext.BatchItemResult[T], error) {
	branchCount := len(branches)
	results := make([]durablecontext.BatchItemResult[T], branchCount)
	resultsMu := sync.Mutex{}

	// Determine concurrency level
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = branchCount // No limit
	}

	// Create semaphore for concurrency control
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	errChan := make(chan error, branchCount)

	for i, branch := range branches {
		wg.Add(1)
		go func(index int, branch durablecontext.ParallelBranch[T]) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Create child context for this branch
			branchName := fmt.Sprintf("%s-branch-%s", parallelName, branch.Name)
			branchResult, err := RunInChildContext(ctx, branchName, branch.Func)

			resultsMu.Lock()
			results[index] = durablecontext.BatchItemResult[T]{
				Index:  index,
				Result: branchResult,
				Error:  err,
			}
			resultsMu.Unlock()

			if err != nil && durable.IsSuspendExecution(err) {
				// Propagate suspension
				errChan <- err
			}
		}(i, branch)
	}

	wg.Wait()
	close(errChan)

	// Check for suspension errors
	for err := range errChan {
		if durable.IsSuspendExecution(err) {
			return nil, err
		}
	}

	return results, nil
}

// checkCompletionCriteria checks if batch operation meets completion criteria.
func checkCompletionCriteria[T any](result *durablecontext.BatchResult[T], cfg *config.CompletionConfig, totalCount int) error {
	if cfg == nil {
		// No completion criteria, just check if all succeeded
		errors := result.GetErrors()
		if len(errors) > 0 {
			return fmt.Errorf("batch operation had %d failures", len(errors))
		}
		return nil
	}

	successCount := len(result.GetResults())
	failureCount := len(result.GetErrors())

	// Check minimum successful
	if cfg.MinSuccessful != nil && successCount < *cfg.MinSuccessful {
		return fmt.Errorf("insufficient successful completions: got %d, need %d", successCount, *cfg.MinSuccessful)
	}

	// Check tolerated failure count
	if cfg.ToleratedFailureCount != nil && failureCount > *cfg.ToleratedFailureCount {
		return fmt.Errorf("too many failures: got %d, max %d", failureCount, *cfg.ToleratedFailureCount)
	}

	// Check tolerated failure percentage
	if cfg.ToleratedFailurePercentage != nil {
		failurePercentage := float64(failureCount) / float64(totalCount) * 100.0
		if failurePercentage > *cfg.ToleratedFailurePercentage {
			return fmt.Errorf("failure percentage too high: %.2f%%, max %.2f%%", failurePercentage, *cfg.ToleratedFailurePercentage)
		}
	}

	return nil
}
