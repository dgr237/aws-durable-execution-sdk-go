// Package main demonstrates a durable execution with Map operation invoking a separate task Lambda.
// This example can be deployed to AWS Lambda for testing durable execution.
//
// This is the orchestrator Lambda that:
// - Executes a Map operation to process 3 tasks in parallel
// - Each task invokes a separate task Lambda (specified by TASK_LAMBDA_NAME env var)
// - Aggregates the results and returns total wait time
//
// Environment Variables:
// - TASK_LAMBDA_NAME: The ARN or name of the task Lambda to invoke (required)
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/durable-execution-sdk-go/pkg/durable"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// Event represents the input to the orchestrator Lambda function.
type Event struct {
	// Currently no input parameters needed - could add configuration here
}

// TaskInput represents the input sent to the task Lambda.
type TaskInput struct {
	WaitTimeMs int `json:"waitTimeMs"` // How long the task should wait
	TaskNumber int `json:"taskNumber"` // Task identifier
}

// TaskResult represents the result from a task execution.
type TaskResult struct {
	TaskNumber    int   `json:"taskNumber"`
	WaitTimeMs    int   `json:"waitTimeMs"`
	CompletedAtMs int64 `json:"completedAtMs"`
}

// MainResult represents the result from main execution.
type MainResult struct {
	TotalWaitTimeMs int          `json:"totalWaitTimeMs"`
	TaskResults     []TaskResult `json:"taskResults"`
	ExecutionTimeMs int64        `json:"executionTimeMs"`
}

func main() {
	lambda.Start(durable.WithDurableExecution[Event, MainResult](mainHandler, nil))
}

func mainHandler(event Event, durableCtx types.DurableContext) (MainResult, error) {
	startTime := getStartTime(durableCtx)

	result, err := operations.Step(durableCtx, "initialize-tasks", initializeFn)
	if err != nil {
		return MainResult{}, fmt.Errorf("initialization failed: %w", err)
	}

	mapResult, err := operations.Map(
		durableCtx,
		"process-tasks",
		result,
		runTaskFn,
		operations.WithMapMaxConcurrency[TaskInput, TaskResult](3))
	if err != nil {
		return MainResult{}, fmt.Errorf("map operation failed: %w", err)
	}

	res, err := operations.Step[MainResult](durableCtx, "get-results", aggregateResultsFn(startTime, mapResult))
	if err != nil {
		return MainResult{}, fmt.Errorf("failed to get results: %w", err)
	}
	return res, nil
}

func getStartTime(durableCtx types.DurableContext) time.Time {
	startTime, _ := operations.Step(durableCtx, "start-time", func(ctx types.StepContext) (time.Time, error) {
		durableCtx.Logger().Info("Starting main execution")
		return time.Now(), nil
	}, nil)
	return startTime
}

func initializeFn(stepCtx types.StepContext) ([]TaskInput, error) {
	stepCtx.Logger().Info("Starting main execution with Map operation for 3 tasks")
	// Create 3 task configurations with random wait times between 5-10 seconds (reduced for faster testing)
	taskConfigs := make([]TaskInput, 3)
	for i := 0; i < 3; i++ {
		// Random wait time between 5000-10000 ms (5-10 seconds)
		waitTimeMs := 5000 + rand.Intn(5001)

		taskConfigs[i] = TaskInput{
			WaitTimeMs: waitTimeMs,
			TaskNumber: i + 1,
		}

		stepCtx.Logger().Info("Task %d configured with %d ms wait time", i+1, waitTimeMs)
	}
	return taskConfigs, nil
}

func runTaskFn(ctx types.DurableContext, taskInput TaskInput, index int, items []TaskInput) (TaskResult, error) {
	ctx.Logger().Info("Processing task %d with %d ms wait", taskInput.TaskNumber, taskInput.WaitTimeMs)

	// Get task Lambda ARN from environment variable (should be qualified ARN)
	taskLambdaArn := os.Getenv("TASK_LAMBDA_NAME")
	if taskLambdaArn == "" {
		return TaskResult{}, errors.New("TASK_LAMBDA_NAME environment variable not set")
	}

	// Just use the ARN as-is - AWS will handle qualification requirements
	ctx.Logger().Info("Using task Lambda ARN: %s", taskLambdaArn)

	ctx.Logger().Info("Invoking task Lambda %s for task %d", taskLambdaArn, taskInput.TaskNumber)

	// INVOKE SEPARATE TASK LAMBDA
	// Use static invoke name - NO execID!
	invokeName := fmt.Sprintf("invoke-task-%d", taskInput.TaskNumber)
	taskResult, err := operations.Invoke[TaskInput, TaskResult](
		ctx,
		invokeName,    // Static name per task number
		taskLambdaArn, // Qualified ARN
		taskInput)     // Send TaskInput to task Lambda
	if err != nil {
		return TaskResult{}, fmt.Errorf("lambda invocation failed: %w", err)
	}

	ctx.Logger().Info("Task %d invocation completed: waited %d ms",
		taskResult.TaskNumber, taskResult.WaitTimeMs)

	return taskResult, nil
}

func aggregateResultsFn(startTime time.Time, mapResult types.BatchResult[TaskResult]) func(durableCtx types.StepContext) (MainResult, error) {
	return func(durableCtx types.StepContext) (MainResult, error) {

		taskResults := make([]TaskResult, 0)
		totalWaitTimeMs := 0
		errors := make([]error, 0)
		for _, result := range mapResult.Items {
			if result.Err != nil {
				taskResults = append(taskResults, result.Value)
				totalWaitTimeMs += result.Value.WaitTimeMs
				durableCtx.Logger().Info("Task %d result: waited %d ms", result.Value.TaskNumber, result.Value.WaitTimeMs)
			}
			errors = append(errors, result.Err)
		}

		// Check for any errors
		if len(errors) > 0 {
			durableCtx.Logger().Warn("Map operation had %d failures", len(errors))
		}

		executionTime := time.Since(startTime).Milliseconds()

		durableCtx.Logger().Info("Main execution completed: %d tasks, total wait time: %d ms, execution time: %d ms",
			len(taskResults), totalWaitTimeMs, executionTime)

		return MainResult{
			TotalWaitTimeMs: totalWaitTimeMs,
			TaskResults:     taskResults,
			ExecutionTimeMs: executionTime,
		}, nil
	}
}
