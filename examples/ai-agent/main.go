// Package main demonstrates an AI agent workflow with tool execution.
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

// AIEvent represents an AI agent invocation request.
type AIEvent struct {
	Prompt      string  `json:"prompt"`
	MaxSteps    int     `json:"maxSteps"`
	Temperature float64 `json:"temperature"`
}

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Tool represents an AI tool that can be called.
type Tool struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ModelResponse represents the AI model's response.
type ModelResponse struct {
	Content      string `json:"content"`
	ToolCall     *Tool  `json:"toolCall,omitempty"`
	FinishReason string `json:"finishReason"`
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var aiEvent AIEvent
		if err := durable.ExtractInput(event, &aiEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		durablecontext.Logger(durableCtx).Info("Starting AI agent for prompt: %s", aiEvent.Prompt)

		// Initialize conversation with user prompt
		messages := []Message{
			{Role: "user", Content: aiEvent.Prompt},
		}

		maxSteps := aiEvent.MaxSteps
		if maxSteps == 0 {
			maxSteps = 10 // Default max steps
		}

		// Agentic loop - continue until no more tool calls
		for step := 0; step < maxSteps; step++ {
			durablecontext.Logger(durableCtx).Info("Agent step %d", step+1)

			// Step: Invoke AI model
			response, err := operations.Step(durableCtx, fmt.Sprintf("invoke-model-%d", step),
				func(stepCtx context.Context) (ModelResponse, error) {
					durablecontext.Logger(stepCtx).Info("Invoking AI model...")

					// Simulate AI model invocation
					// In real implementation, this would call Amazon Bedrock or similar
					return invokeAIModel(messages, aiEvent.Temperature)
				})
			if err != nil {
				return nil, err
			}

			// Check if we're done (no tool call)
			if response.ToolCall == nil {
				durablecontext.Logger(durableCtx).Info("Agent completed. Finish reason: %s", response.FinishReason)
				return map[string]interface{}{
					"success":  true,
					"response": response.Content,
					"steps":    step + 1,
				}, nil
			}

			// Execute the tool
			durablecontext.Logger(durableCtx).Info("Executing tool: %s", response.ToolCall.Name)

			toolResult, err := operations.Step(durableCtx, fmt.Sprintf("tool-%s-%d", response.ToolCall.Name, step),
				func(stepCtx context.Context) (string, error) {
					durablecontext.Logger(stepCtx).Info("Calling tool: %s", response.ToolCall.Name)
					return executeTool(response.ToolCall)
				})
			if err != nil {
				return nil, err
			}

			// Add assistant message with tool result
			messages = append(messages, Message{
				Role:    "assistant",
				Content: fmt.Sprintf("Tool %s returned: %s", response.ToolCall.Name, toolResult),
			})

			// Short wait between iterations to avoid rate limits
			if err := operations.Wait(durableCtx, fmt.Sprintf("rate-limit-%d", step),
				config.NewDurationFromSeconds(1)); err != nil {
				return nil, err
			}
		}

		// Max steps reached
		durablecontext.Logger(durableCtx).Warn("Agent reached max steps: %d", maxSteps)
		return map[string]interface{}{
			"success": false,
			"error":   "Max steps reached",
			"steps":   maxSteps,
		}, nil
	})
}

// invokeAIModel simulates calling an AI model.
func invokeAIModel(messages []Message, temperature float64) (ModelResponse, error) {
	// This is a simulation. In production, you would call:
	// - Amazon Bedrock
	// - OpenAI API
	// - Anthropic Claude
	// etc.

	// Simulate tool call for demonstration
	if len(messages) == 1 {
		return ModelResponse{
			Content: "I need to check the weather",
			ToolCall: &Tool{
				Name: "get_weather",
				Arguments: map[string]interface{}{
					"location": "San Francisco",
				},
			},
			FinishReason: "tool_calls",
		}, nil
	}

	// Final response
	return ModelResponse{
		Content:      "The weather in San Francisco is sunny and 72°F.",
		ToolCall:     nil,
		FinishReason: "stop",
	}, nil
}

// executeTool executes a tool and returns the result.
func executeTool(tool *Tool) (string, error) {
	switch tool.Name {
	case "get_weather":
		location, ok := tool.Arguments["location"].(string)
		if !ok {
			return "", fmt.Errorf("invalid location argument")
		}
		// Simulate weather API call
		return fmt.Sprintf("Weather in %s: Sunny, 72°F", location), nil

	case "search_web":
		query, ok := tool.Arguments["query"].(string)
		if !ok {
			return "", fmt.Errorf("invalid query argument")
		}
		// Simulate web search
		return fmt.Sprintf("Search results for '%s': [Result 1, Result 2, Result 3]", query), nil

	case "calculate":
		expression, ok := tool.Arguments["expression"].(string)
		if !ok {
			return "", fmt.Errorf("invalid expression argument")
		}
		// Simulate calculation
		return fmt.Sprintf("Result of %s = 42", expression), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", tool.Name)
	}
}
