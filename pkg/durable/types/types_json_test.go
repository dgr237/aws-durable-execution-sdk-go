package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOperationJSONSerialization(t *testing.T) {
	// Test that Operation can be marshaled and unmarshaled with time.Time fields
	startTime := NewFlexibleTime(time.Date(2026, 3, 8, 12, 56, 49, 225000000, time.UTC))
	endTime := NewFlexibleTime(time.Date(2026, 3, 8, 12, 56, 51, 401000000, time.UTC))
	name := "test-operation"
	parentId := "parent-123"

	op := Operation{
		Id:             "op-123",
		Type:           OperationTypeStep,
		Name:           &name,
		Status:         OperationStatusSucceeded,
		StartTimestamp: &startTime,
		EndTimestamp:   &endTime,
		ParentId:       &parentId,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(op)
	if err != nil {
		t.Fatalf("Failed to marshal Operation: %v", err)
	}

	// Unmarshal from JSON
	var op2 Operation
	if err := json.Unmarshal(jsonData, &op2); err != nil {
		t.Fatalf("Failed to unmarshal Operation: %v", err)
	}

	// Verify fields
	if op2.Id != op.Id {
		t.Errorf("Id mismatch: got %v, want %v", op2.Id, op.Id)
	}
	if op2.Type != op.Type {
		t.Errorf("Type mismatch: got %v, want %v", op2.Type, op.Type)
	}
	if op2.Status != op.Status {
		t.Errorf("Status mismatch: got %v, want %v", op2.Status, op.Status)
	}
	if op2.StartTimestamp == nil || !op2.StartTimestamp.Equal(op.StartTimestamp.Time) {
		t.Errorf("StartTimestamp mismatch: got %v, want %v", op2.StartTimestamp, op.StartTimestamp)
	}
	if op2.EndTimestamp == nil || !op2.EndTimestamp.Equal(op.EndTimestamp.Time) {
		t.Errorf("EndTimestamp mismatch: got %v, want %v", op2.EndTimestamp, op.EndTimestamp)
	}
}

func TestInitialExecutionStateJSONSerialization(t *testing.T) {
	// Test that InitialExecutionState can be marshaled and unmarshaled
	startTime := NewFlexibleTime(time.Date(2026, 3, 8, 12, 56, 49, 225000000, time.UTC))
	marker := "next-marker-123"

	state := InitialExecutionState{
		Operations: []Operation{
			{
				Id:             "op-1",
				Type:           OperationTypeStep,
				Status:         OperationStatusSucceeded,
				StartTimestamp: &startTime,
			},
		},
		NextMarker: &marker,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Failed to marshal InitialExecutionState: %v", err)
	}

	// Unmarshal from JSON
	var state2 InitialExecutionState
	if err := json.Unmarshal(jsonData, &state2); err != nil {
		t.Fatalf("Failed to unmarshal InitialExecutionState: %v", err)
	}

	// Verify fields
	if len(state2.Operations) != len(state.Operations) {
		t.Errorf("Operations length mismatch: got %v, want %v", len(state2.Operations), len(state.Operations))
	}
	if state2.Operations[0].Id != state.Operations[0].Id {
		t.Errorf("Operation Id mismatch: got %v, want %v", state2.Operations[0].Id, state.Operations[0].Id)
	}
	if state2.Operations[0].StartTimestamp == nil || !state2.Operations[0].StartTimestamp.Equal(state.Operations[0].StartTimestamp.Time) {
		t.Errorf("StartTimestamp mismatch: got %v, want %v", state2.Operations[0].StartTimestamp, state.Operations[0].StartTimestamp)
	}
}

func TestDurableExecutionInvocationInputJSONDeserialization(t *testing.T) {
	// Test realistic JSON payload from AWS Lambda service
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
					"EndTimestamp": "2026-03-08T12:56:51.401Z"
				}
			]
		}
	}`

	var input DurableExecutionInvocationInput
	if err := json.Unmarshal([]byte(jsonPayload), &input); err != nil {
		t.Fatalf("Failed to unmarshal DurableExecutionInvocationInput: %v", err)
	}

	// Verify fields
	if input.DurableExecutionArn == "" {
		t.Error("DurableExecutionArn is empty")
	}
	if input.CheckpointToken == "" {
		t.Error("CheckpointToken is empty")
	}
	if len(input.InitialExecutionState.Operations) != 1 {
		t.Errorf("Expected 1 operation, got %d", len(input.InitialExecutionState.Operations))
	}

	op := input.InitialExecutionState.Operations[0]
	if op.Id != "op-1" {
		t.Errorf("Operation Id mismatch: got %v, want op-1", op.Id)
	}
	if op.StartTimestamp == nil {
		t.Error("StartTimestamp is nil")
	}
	if op.EndTimestamp == nil {
		t.Error("EndTimestamp is nil")
	}
}

func TestWaitDetailsJSONSerialization(t *testing.T) {
	// Test WaitDetails with ScheduledEndTimestamp
	scheduledTime := NewFlexibleTime(time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC))

	details := WaitDetails{
		ScheduledEndTimestamp: &scheduledTime,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("Failed to marshal WaitDetails: %v", err)
	}

	// Unmarshal from JSON
	var details2 WaitDetails
	if err := json.Unmarshal(jsonData, &details2); err != nil {
		t.Fatalf("Failed to unmarshal WaitDetails: %v", err)
	}

	// Verify fields
	if details2.ScheduledEndTimestamp == nil || !details2.ScheduledEndTimestamp.Equal(details.ScheduledEndTimestamp.Time) {
		t.Errorf("ScheduledEndTimestamp mismatch: got %v, want %v", details2.ScheduledEndTimestamp, details.ScheduledEndTimestamp)
	}
}
