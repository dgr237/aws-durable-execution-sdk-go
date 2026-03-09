package main

import (
	"encoding/json"
	"fmt"

	"github.com/aws/durable-execution-sdk-go/types"
)

func main() {
	fmt.Println("=== Test 1: Unix Millisecond Timestamp (from AWS) ===")
	// Simulate the ACTUAL JSON payload that AWS Lambda sends
	awsPayload := `{
		"CheckpointToken": "token-123",
		"DurableExecutionArn": "arn:aws:lambda:us-east-1:005853707519:function:test-2:1/durable-execution/ea321a62-7f52-4a07-9cdb-cc89b2061c4e/84e4a8b5-9a18-3280-97f1-f116f888cfe3",
		"InitialExecutionState": {
			"Operations": [
				{
					"ExecutionDetails": {
						"InputPayload": "{\"key1\":\"value1\",\"key2\":\"value2\",\"key3\":\"value3\"}"
					},
					"Id": "84e4a8b5-9a18-3280-97f1-f116f888cfe3",
					"Name": "ea321a62-7f52-4a07-9cdb-cc89b2061c4e",
					"StartTimestamp": 1772897053035,
					"Status": "STARTED",
					"Type": "EXECUTION"
				}
			]
		}
	}`

	var input1 types.DurableExecutionInvocationInput
	if err := json.Unmarshal([]byte(awsPayload), &input1); err != nil {
		fmt.Printf("❌ ERROR: Failed to unmarshal AWS payload: %v\n", err)
		return
	}

	fmt.Println("✅ SUCCESS: AWS payload with Unix millisecond timestamp unmarshaled!")
	fmt.Printf("DurableExecutionArn: %s\n", input1.DurableExecutionArn)
	fmt.Printf("Number of Operations: %d\n", len(input1.InitialExecutionState.Operations))

	if len(input1.InitialExecutionState.Operations) > 0 {
		op := input1.InitialExecutionState.Operations[0]
		fmt.Printf("\nOperation:\n")
		fmt.Printf("  Id: %s\n", op.Id)
		fmt.Printf("  Type: %s\n", op.Type)
		fmt.Printf("  Status: %s\n", op.Status)
		if op.StartTimestamp != nil {
			fmt.Printf("  StartTimestamp: %s (Unix millis: %d)\n",
				op.StartTimestamp.Format("2006-01-02 15:04:05.000 MST"),
				op.StartTimestamp.UnixMilli())
		}
		if op.ExecutionDetails != nil && op.ExecutionDetails.InputPayload != nil {
			fmt.Printf("  ExecutionDetails.InputPayload: %s\n", *op.ExecutionDetails.InputPayload)
		}
	}

	fmt.Println("\n=== Test 2: RFC3339 String Timestamp ===")
	// Simulate the JSON payload with RFC3339 timestamps (SDK format)
	jsonPayload := `{
		"DurableExecutionArn": "arn:aws:lambda:us-east-1:123456789012:function:my-function:durable:abc123",
		"CheckpointToken": "token-123",
		"InitialExecutionState": {
			"Operations": [
				{
					"Id": "op-1",
					"Type": "STEP",
					"Status": "SUCCEEDED",
					"StartTimestamp": "2026-03-08T12:56:49.225Z",
					"EndTimestamp": "2026-03-08T12:56:51.401Z",
					"StepDetails": {
						"Result": "{\"value\": 42}"
					}
				},
				{
					"Id": "op-2",
					"Type": "CALLBACK",
					"Status": "IN_PROGRESS",
					"StartTimestamp": "2026-03-08T12:56:51.500Z",
					"CallbackDetails": {
						"CallbackId": "cb-123"
					}
				},
				{
					"Id": "op-3",
					"Type": "STEP",
					"SubType": "WAIT",
					"Status": "IN_PROGRESS",
					"StartTimestamp": "2026-03-08T12:56:52.000Z",
					"WaitDetails": {
						"ScheduledEndTimestamp": "2026-03-08T13:00:00.000Z"
					}
				}
			],
			"NextMarker": "marker-abc123"
		}
	}`

	var input types.DurableExecutionInvocationInput
	if err := json.Unmarshal([]byte(jsonPayload), &input); err != nil {
		fmt.Printf("❌ ERROR: Failed to unmarshal: %v\n", err)
		return
	}

	fmt.Println("✅ SUCCESS: SDK payload with RFC3339 timestamp unmarshaled!")
	fmt.Printf("DurableExecutionArn: %s\n", input.DurableExecutionArn)
	fmt.Printf("CheckpointToken: %s\n", input.CheckpointToken)
	fmt.Printf("Number of Operations: %d\n", len(input.InitialExecutionState.Operations))

	for i, op := range input.InitialExecutionState.Operations {
		fmt.Printf("\nOperation %d:\n", i+1)
		fmt.Printf("  Id: %s\n", op.Id)
		fmt.Printf("  Type: %s\n", op.Type)
		fmt.Printf("  Status: %s\n", op.Status)
		if op.StartTimestamp != nil {
			fmt.Printf("  StartTimestamp: %s\n", op.StartTimestamp.Format("2006-01-02 15:04:05.000 MST"))
		}
		if op.EndTimestamp != nil {
			fmt.Printf("  EndTimestamp: %s\n", op.EndTimestamp.Format("2006-01-02 15:04:05.000 MST"))
		}
		if op.StepDetails != nil && op.StepDetails.Result != nil {
			fmt.Printf("  StepDetails.Result: %s\n", *op.StepDetails.Result)
		}
		if op.CallbackDetails != nil && op.CallbackDetails.CallbackId != nil {
			fmt.Printf("  CallbackDetails.CallbackId: %s\n", *op.CallbackDetails.CallbackId)
		}
		if op.WaitDetails != nil && op.WaitDetails.ScheduledEndTimestamp != nil {
			fmt.Printf("  WaitDetails.ScheduledEndTimestamp: %s\n", op.WaitDetails.ScheduledEndTimestamp.Format("2006-01-02 15:04:05.000 MST"))
		}
	}

	if input.InitialExecutionState.NextMarker != nil {
		fmt.Printf("\nNextMarker: %s\n", *input.InitialExecutionState.NextMarker)
	}
}
