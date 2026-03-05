// Package main demonstrates advanced features of the durable execution SDK.
// This example showcases:
// - JitterStrategy for retry delays
// - NestingType for Map/Parallel operations
// - CreateCallback for external integrations
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
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/retry"
)

type Event struct {
	Items []string `json:"items"`
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var inputEvent Event
		if err := durable.ExtractInput(event, &inputEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		durablecontext.Logger(durableCtx).Info("Starting advanced features demonstration")

		// Example 1: Using JitterStrategy with retry
		result1, err := operations.StepWithConfig(durableCtx, "step-with-jitter", func(stepCtx context.Context) (string, error) {
			return "Step with jitter completed", nil
		}, config.StepConfig{
			RetryStrategy: retry.CreateRetryStrategy(retry.RetryStrategyConfig{
				MaxAttempts:  5,
				InitialDelay: config.NewDurationFromSeconds(2),
				MaxDelay:     config.NewDurationFromSeconds(30),
				BackoffRate:  2,
				Jitter:       config.JitterStrategyFull, // Full jitter to prevent thundering herd
			}),
			Semantics: config.StepSemanticsAtLeastOnce,
		})
		if err != nil {
			return nil, err
		}
		durablecontext.Logger(durableCtx).Info("Result with jitter: %s", result1)

		// Example 2: Using Map with NestingType.FLAT for cost optimization
		items := []string{"item1", "item2", "item3", "item4", "item5"}
		mapResult, err := operations.Map(durableCtx, "map-with-flat-nesting", items,
			func(ctx context.Context, item string, index int) (string, error) {
				// Process each item
				result, err := operations.Step(ctx, fmt.Sprintf("process-%s", item), func(stepCtx context.Context) (string, error) {
					return fmt.Sprintf("processed-%s", item), nil
				})
				return result, err
			},
			config.MapConfig{
				MaxConcurrency: 3,
				Nesting:        config.NestingTypeFlat, // Use FLAT for ~30% cost reduction
			},
		)
		if err != nil {
			return nil, err
		}
		durablecontext.Logger(durableCtx).Info("Map completed with %d successes, %d failures",
			len(mapResult.GetResults()), len(mapResult.GetErrors()))

		// Example 3: Using CreateCallback for external approval workflow
		callbackResult, callbackID, err := operations.CreateCallback[string](
			durableCtx,
			"approval-callback",
			config.DefaultWaitForCallbackConfig(),
		)
		if err != nil {
			return nil, err
		}

		durablecontext.Logger(durableCtx).Info("Created callback with ID: %s", callbackID)
		// In a real scenario, you would send callbackID to an external system:
		// sendApprovalRequest(callbackID, requestData)

		// Wait for the callback result
		approvalResult, err := callbackResult.Wait()
		if err != nil {
			// If this is a suspension, that's expected - execution will resume later
			if _, ok := err.(*durable.SuspendExecutionError); ok {
				return nil, err
			}
			return nil, err
		}
		durablecontext.Logger(durableCtx).Info("Approval result: %s", approvalResult)

		// Example 4: Using Parallel with NestingType.NESTED for full observability
		branches := []durablecontext.ParallelBranch[string]{
			{
				Name: "branch-1",
				Func: func(ctx context.Context) (string, error) {
					return operations.Step(ctx, "branch-1-step", func(stepCtx context.Context) (string, error) {
						return "Branch 1 result", nil
					})
				},
			},
			{
				Name: "branch-2",
				Func: func(ctx context.Context) (string, error) {
					return operations.Step(ctx, "branch-2-step", func(stepCtx context.Context) (string, error) {
						return "Branch 2 result", nil
					})
				},
			},
			{
				Name: "branch-3",
				Func: func(ctx context.Context) (string, error) {
					return operations.Step(ctx, "branch-3-step", func(stepCtx context.Context) (string, error) {
						return "Branch 3 result", nil
					})
				},
			},
		}

		parallelResult, err := operations.Parallel(durableCtx, "parallel-with-nested", branches,
			config.ParallelConfig{
				MaxConcurrency: 2,
				Nesting:        config.NestingTypeNested, // Use NESTED for full observability
			},
		)
		if err != nil {
			return nil, err
		}
		durablecontext.Logger(durableCtx).Info("Parallel completed with %d successes",
			len(parallelResult.GetResults()))

		// Example 5: Using half jitter strategy
		result5, err := operations.StepWithConfig(durableCtx, "step-with-half-jitter", func(stepCtx context.Context) (string, error) {
			return "Step with half jitter completed", nil
		}, config.StepConfig{
			RetryStrategy: retry.CreateRetryStrategy(retry.RetryStrategyConfig{
				MaxAttempts:  3,
				InitialDelay: config.NewDurationFromSeconds(1),
				MaxDelay:     config.NewDurationFromSeconds(10),
				BackoffRate:  2,
				Jitter:       config.JitterStrategyHalf, // Half jitter for predictable minimum delays
			}),
		})
		if err != nil {
			return nil, err
		}
		durablecontext.Logger(durableCtx).Info("Result with half jitter: %s", result5)

		return map[string]interface{}{
			"success":          true,
			"jitterResult":     result1,
			"mapResults":       len(mapResult.GetResults()),
			"parallelResults":  len(parallelResult.GetResults()),
			"halfJitterResult": result5,
		}, nil
	})
}
