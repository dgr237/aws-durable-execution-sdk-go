// Package main demonstrates batch image processing using Map operation.
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

// BatchProcessEvent represents a batch processing request.
type BatchProcessEvent struct {
	Images       []string `json:"images"`
	OutputBucket string   `json:"outputBucket"`
	Quality      int      `json:"quality"`
}

// ImageJob represents a single image to process.
type ImageJob struct {
	URL    string `json:"url"`
	Name   string `json:"name"`
	Format string `json:"format"`
}

// ProcessResult represents the result of processing an image.
type ProcessResult struct {
	OriginalURL  string `json:"originalUrl"`
	ProcessedURL string `json:"processedUrl"`
	Size         int64  `json:"size"`
	Duration     int    `json:"duration"` // milliseconds
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var batchEvent BatchProcessEvent
		if err := durable.ExtractInput(event, &batchEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		durablecontext.Logger(durableCtx).Info("Starting batch image processing for %d images", len(batchEvent.Images))

		// Step 1: Parse and validate image URLs
		jobs, err := operations.Step(durableCtx, "parse-images", func(stepCtx context.Context) ([]ImageJob, error) {
			durablecontext.Logger(stepCtx).Info("Parsing image URLs...")

			jobs := make([]ImageJob, 0, len(batchEvent.Images))
			for i, url := range batchEvent.Images {
				jobs = append(jobs, ImageJob{
					URL:    url,
					Name:   fmt.Sprintf("image-%d", i+1),
					Format: "jpeg",
				})
			}

			return jobs, nil
		})
		if err != nil {
			return nil, err
		}

		durablecontext.Logger(durableCtx).Info("Parsed %d image jobs", len(jobs))

		// Step 2: Process images in parallel using Map
		// Process up to 10 images concurrently
		cfg := config.MapConfig{
			MaxConcurrency: 10,
			CompletionConfig: &config.CompletionConfig{
				// Require at least 80% success
				ToleratedFailurePercentage: func() *float64 { v := 20.0; return &v }(),
			},
			CheckpointAsync: true,
		}

		results, err := operations.Map(
			durableCtx,
			"process-images",
			jobs,
			processImageFunc(batchEvent.OutputBucket, batchEvent.Quality),
			cfg,
		)
		if err != nil {
			return nil, err
		}

		// Check for errors
		if err := results.ThrowIfError(); err != nil {
			durablecontext.Logger(durableCtx).Error("Some images failed to process: %v", err)
		}

		successCount := len(results.GetResults())
		failureCount := len(results.GetErrors())

		durablecontext.Logger(durableCtx).Info("Processing complete. Success: %d, Failed: %d", successCount, failureCount)

		// Step 3: Generate summary report
		summary, err := operations.Step(durableCtx, "generate-summary", func(stepCtx context.Context) (map[string]interface{}, error) {
			durablecontext.Logger(stepCtx).Info("Generating summary report...")

			successResults := results.GetResults()
			totalSize := int64(0)
			totalDuration := 0

			for _, result := range successResults {
				totalSize += result.Size
				totalDuration += result.Duration
			}

			avgDuration := 0
			if len(successResults) > 0 {
				avgDuration = totalDuration / len(successResults)
			}

			return map[string]interface{}{
				"totalImages":    len(jobs),
				"successCount":   successCount,
				"failureCount":   failureCount,
				"totalSizeBytes": totalSize,
				"avgDurationMs":  avgDuration,
			}, nil
		})
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"success": true,
			"summary": summary,
			"results": results.GetResults(),
			"errors":  results.GetErrors(),
		}, nil
	})
}

// processImageFunc returns a function that processes a single image.
func processImageFunc(outputBucket string, quality int) func(context.Context, ImageJob, int) (ProcessResult, error) {
	return func(ctx context.Context, job ImageJob, index int) (ProcessResult, error) {
		durablecontext.Logger(ctx).Info("Processing image %d: %s", index+1, job.Name)

		// Step: Download image
		_, err := operations.Step(ctx, "download", func(stepCtx context.Context) ([]byte, error) {
			durablecontext.Logger(stepCtx).Info("Downloading image from: %s", job.URL)
			// Simulate download
			// In production: aws s3 download, http client, etc.
			return []byte("image-data"), nil
		})
		if err != nil {
			return ProcessResult{}, err
		}

		// Step: Process image (resize, compress, etc.)
		processedData, err := operations.Step(ctx, "process", func(stepCtx context.Context) ([]byte, error) {
			durablecontext.Logger(stepCtx).Info("Processing image...")
			// Simulate processing
			// In production: image manipulation libraries
			return []byte("processed-image-data"), nil
		})
		if err != nil {
			return ProcessResult{}, err
		}

		// Step: Upload to S3
		uploadURL, err := operations.Step(ctx, "upload", func(stepCtx context.Context) (string, error) {
			durablecontext.Logger(stepCtx).Info("Uploading to S3 bucket: %s", outputBucket)
			// Simulate upload
			// In production: AWS S3 PutObject
			url := fmt.Sprintf("s3://%s/%s-processed.jpg", outputBucket, job.Name)
			return url, nil
		})
		if err != nil {
			return ProcessResult{}, err
		}

		return ProcessResult{
			OriginalURL:  job.URL,
			ProcessedURL: uploadURL,
			Size:         int64(len(processedData)),
			Duration:     150, // simulated processing time
		}, nil
	}
}
