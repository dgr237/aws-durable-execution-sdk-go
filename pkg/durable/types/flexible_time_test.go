package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFlexibleTime_UnmarshalJSON_UnixMillis(t *testing.T) {
	// Test unmarshaling from Unix milliseconds (as number)
	jsonData := `{"timestamp": 1772897053035}`

	var data struct {
		Timestamp FlexibleTime `json:"timestamp"`
	}

	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Expected time: 2026-03-08 12:57:33.035 UTC
	expected := time.UnixMilli(1772897053035)
	if !data.Timestamp.Equal(expected) {
		t.Errorf("Time mismatch: got %v, want %v", data.Timestamp.Time, expected)
	}
}

func TestFlexibleTime_UnmarshalJSON_RFC3339(t *testing.T) {
	// Test unmarshaling from RFC3339 string
	jsonData := `{"timestamp": "2026-03-08T12:56:49.225Z"}`

	var data struct {
		Timestamp FlexibleTime `json:"timestamp"`
	}

	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	expected, _ := time.Parse(time.RFC3339, "2026-03-08T12:56:49.225Z")
	if !data.Timestamp.Equal(expected) {
		t.Errorf("Time mismatch: got %v, want %v", data.Timestamp.Time, expected)
	}
}

func TestFlexibleTime_UnmarshalJSON_Pointer(t *testing.T) {
	// Test unmarshaling into pointer field with Unix milliseconds
	jsonData := `{"timestamp": 1772897053035}`

	var data struct {
		Timestamp *FlexibleTime `json:"timestamp,omitempty"`
	}

	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if data.Timestamp == nil {
		t.Fatal("Timestamp is nil")
	}

	expected := time.UnixMilli(1772897053035)
	if !data.Timestamp.Equal(expected) {
		t.Errorf("Time mismatch: got %v, want %v", data.Timestamp.Time, expected)
	}
}

func TestFlexibleTime_MarshalJSON(t *testing.T) {
	// Test marshaling to RFC3339 string
	ts := NewFlexibleTime(time.Date(2026, 3, 8, 12, 56, 49, 225000000, time.UTC))

	data := struct {
		Timestamp FlexibleTime `json:"timestamp"`
	}{
		Timestamp: ts,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Should be RFC3339 format
	expectedJSON := `{"timestamp":"2026-03-08T12:56:49.225Z"}`
	if string(jsonData) != expectedJSON {
		t.Errorf("JSON mismatch:\ngot:  %s\nwant: %s", string(jsonData), expectedJSON)
	}
}

func TestOperation_UnmarshalJSON_WithUnixMillis(t *testing.T) {
	// Test Operation with Unix millisecond timestamps
	jsonData := `{
		"Id": "84e4a8b5-9a18-3280-97f1-f116f888cfe3",
		"Name": "test-operation",
		"StartTimestamp": 1772897053035,
		"Status": "STARTED",
		"Type": "EXECUTION",
		"ExecutionDetails": {
			"InputPayload": "{\"key1\":\"value1\"}"
		}
	}`

	var op Operation
	if err := json.Unmarshal([]byte(jsonData), &op); err != nil {
		t.Fatalf("Failed to unmarshal Operation: %v", err)
	}

	if op.Id != "84e4a8b5-9a18-3280-97f1-f116f888cfe3" {
		t.Errorf("Id mismatch: got %v, want %v", op.Id, "84e4a8b5-9a18-3280-97f1-f116f888cfe3")
	}

	if op.StartTimestamp == nil {
		t.Fatal("StartTimestamp is nil")
	}

	expected := time.UnixMilli(1772897053035)
	if !op.StartTimestamp.Equal(expected) {
		t.Errorf("StartTimestamp mismatch: got %v, want %v", op.StartTimestamp.Time, expected)
	}
}

func TestOperation_UnmarshalJSON_WithRFC3339(t *testing.T) {
	// Test Operation with RFC3339 string timestamps
	jsonData := `{
		"Id": "op-1",
		"Type": "STEP",
		"Status": "SUCCEEDED",
		"StartTimestamp": "2026-03-08T12:56:49.225Z",
		"EndTimestamp": "2026-03-08T12:56:51.401Z"
	}`

	var op Operation
	if err := json.Unmarshal([]byte(jsonData), &op); err != nil {
		t.Fatalf("Failed to unmarshal Operation: %v", err)
	}

	if op.StartTimestamp == nil {
		t.Fatal("StartTimestamp is nil")
	}
	if op.EndTimestamp == nil {
		t.Fatal("EndTimestamp is nil")
	}

	expectedStart, _ := time.Parse(time.RFC3339, "2026-03-08T12:56:49.225Z")
	expectedEnd, _ := time.Parse(time.RFC3339, "2026-03-08T12:56:51.401Z")

	if !op.StartTimestamp.Equal(expectedStart) {
		t.Errorf("StartTimestamp mismatch: got %v, want %v", op.StartTimestamp.Time, expectedStart)
	}
	if !op.EndTimestamp.Equal(expectedEnd) {
		t.Errorf("EndTimestamp mismatch: got %v, want %v", op.EndTimestamp.Time, expectedEnd)
	}
}

func TestDurableExecutionInvocationInput_UnmarshalJSON_RealAWSPayload(t *testing.T) {
	// Test with the exact payload format from AWS
	jsonData := `{
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

	var input DurableExecutionInvocationInput
	if err := json.Unmarshal([]byte(jsonData), &input); err != nil {
		t.Fatalf("Failed to unmarshal DurableExecutionInvocationInput: %v", err)
	}

	if input.DurableExecutionArn == "" {
		t.Error("DurableExecutionArn is empty")
	}
	if input.CheckpointToken != "token-123" {
		t.Errorf("CheckpointToken mismatch: got %v, want token-123", input.CheckpointToken)
	}
	if len(input.InitialExecutionState.Operations) != 1 {
		t.Fatalf("Expected 1 operation, got %d", len(input.InitialExecutionState.Operations))
	}

	op := input.InitialExecutionState.Operations[0]
	if op.StartTimestamp == nil {
		t.Fatal("StartTimestamp is nil")
	}

	expected := time.UnixMilli(1772897053035)
	if !op.StartTimestamp.Equal(expected) {
		t.Errorf("StartTimestamp mismatch: got %v (%d), want %v (%d)",
			op.StartTimestamp.Time, op.StartTimestamp.UnixMilli(),
			expected, expected.UnixMilli())
	}
}
