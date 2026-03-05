// Package main demonstrates human-in-the-loop approval workflow.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/client"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/durablecontext"
	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/operations"
)

// ApprovalEvent represents an approval request.
type ApprovalEvent struct {
	RequestID     string                 `json:"requestId"`
	ApproverEmail string                 `json:"approverEmail"`
	Title         string                 `json:"title"`
	Description   string                 `json:"description"`
	Data          map[string]interface{} `json:"data"`
}

// Plan represents an execution plan that needs approval.
type Plan struct {
	PlanID        string   `json:"planId"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Actions       []string `json:"actions"`
	EstimatedCost float64  `json:"estimatedCost"`
}

// ApprovalResult represents the approval decision.
type ApprovalResult struct {
	Approved   bool   `json:"approved"`
	Reason     string `json:"reason"`
	ApprovedBy string `json:"approvedBy"`
}

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event client.DurableExecutionEvent) (interface{}, error) {
	return durable.WithDurableExecution(ctx, event, func(durableCtx context.Context) (interface{}, error) {
		// Extract user input from the event payload
		var approvalEvent ApprovalEvent
		if err := durable.ExtractInput(event, &approvalEvent); err != nil {
			return nil, fmt.Errorf("failed to parse input: %w", err)
		}

		durablecontext.Logger(durableCtx).Info("Starting approval workflow for: %s", approvalEvent.RequestID)

		// Step 1: Generate execution plan
		plan, err := operations.Step(durableCtx, "generate-plan", func(stepCtx context.Context) (Plan, error) {
			durablecontext.Logger(stepCtx).Info("Generating execution plan...")

			return Plan{
				PlanID:      fmt.Sprintf("plan-%s", approvalEvent.RequestID),
				Title:       approvalEvent.Title,
				Description: approvalEvent.Description,
				Actions: []string{
					"Provision infrastructure",
					"Deploy application",
					"Run tests",
					"Update DNS",
				},
				EstimatedCost: 125.50,
			}, nil
		})
		if err != nil {
			return nil, err
		}

		durablecontext.Logger(durableCtx).Info("Plan generated: %s (Cost: $%.2f)", plan.PlanID, plan.EstimatedCost)

		// Step 2: Wait for approval via callback
		// The callback ID will be sent to the approver, who can use it to approve/reject
		approval, err := operations.WaitForCallback[ApprovalResult](
			durableCtx,
			"wait-for-approval",
			func(callbackID string) error {
				// Send approval request email with callback ID
				return sendApprovalEmail(approvalEvent.ApproverEmail, plan, callbackID)
			},
			config.WaitForCallbackConfig{
				Timeout: func() *config.Duration { d := config.NewDurationFromHours(24); return &d }(), // 24 hour timeout
			},
		)
		if err != nil {
			return nil, err
		}

		durablecontext.Logger(durableCtx).Info("Approval received: %v", approval.Approved)

		// Step 3: Process based on approval decision
		if !approval.Approved {
			durablecontext.Logger(durableCtx).Info("Request rejected: %s", approval.Reason)
			return map[string]interface{}{
				"success": false,
				"status":  "rejected",
				"reason":  approval.Reason,
				"planId":  plan.PlanID,
			}, nil
		}

		// Step 4: Execute the plan
		result, err := operations.Step(durableCtx, "execute-plan", func(stepCtx context.Context) (map[string]interface{}, error) {
			durablecontext.Logger(stepCtx).Info("Executing approved plan...")

			// Simulate plan execution
			// In real implementation, this would perform the actual actions
			return map[string]interface{}{
				"executed": true,
				"actions":  len(plan.Actions),
				"cost":     plan.EstimatedCost,
			}, nil
		})
		if err != nil {
			return nil, err
		}

		durablecontext.Logger(durableCtx).Info("Plan executed successfully")

		// Step 5: Send completion notification
		_, err = operations.Step(durableCtx, "notify-completion", func(stepCtx context.Context) (bool, error) {
			durablecontext.Logger(stepCtx).Info("Sending completion notification...")

			// Send email to approver about completion
			return sendCompletionEmail(approvalEvent.ApproverEmail, plan, result), nil
		})
		if err != nil {
			return nil, err
		}

		return map[string]interface{}{
			"success":    true,
			"status":     "completed",
			"planId":     plan.PlanID,
			"approvedBy": approval.ApprovedBy,
			"result":     result,
		}, nil
	})
}

// sendApprovalEmail sends an approval request email with callback ID.
func sendApprovalEmail(email string, plan Plan, callbackID string) error {
	// In production, this would use Amazon SES or similar
	log.Printf("Sending approval email to %s", email)
	log.Printf("  Plan: %s", plan.Title)
	log.Printf("  Cost: $%.2f", plan.EstimatedCost)
	log.Printf("  Callback ID: %s", callbackID)
	log.Printf("  Approve URL: https://example.com/approve?id=%s", callbackID)
	log.Printf("  Reject URL: https://example.com/reject?id=%s", callbackID)

	// Store callback ID in database for later retrieval
	// When user clicks approve/reject, webhook calls Lambda callback API with this ID

	return nil
}

// sendCompletionEmail sends a completion notification.
func sendCompletionEmail(email string, plan Plan, result map[string]interface{}) bool {
	log.Printf("Sending completion email to %s", email)
	log.Printf("  Plan: %s", plan.Title)
	log.Printf("  Status: Completed")
	log.Printf("  Result: %v", result)

	return true
}

// Example webhook handler that would be called when user approves/rejects:
//
// func approvalWebhook(w http.ResponseWriter, r *http.Request) {
//     callbackID := r.URL.Query().Get("id")
//     action := r.URL.Query().Get("action") // "approve" or "reject"
//
//     result := ApprovalResult{
//         Approved:   action == "approve",
//         Reason:     r.URL.Query().Get("reason"),
//         ApprovedBy: r.URL.Query().Get("user"),
//     }
//
//     // Call Lambda callback API
//     client := lambda.New(session.Must(session.NewSession()))
//     payload, _ := json.Marshal(result)
//
//     if action == "approve" {
//         client.SendDurableExecutionCallbackSuccess(&lambda.SendDurableExecutionCallbackSuccessInput{
//             CallbackId: aws.String(callbackID),
//             Result:     aws.String(string(payload)),
//         })
//     } else {
//         client.SendDurableExecutionCallbackFailure(&lambda.SendDurableExecutionCallbackFailureInput{
//             CallbackId: aws.String(callbackID),
//             Error:      aws.String("User rejected"),
//         })
//     }
// }
