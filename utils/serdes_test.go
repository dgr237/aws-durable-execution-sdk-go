package utils_test

import (
	"errors"
	"testing"

	"github.com/aws/durable-execution-sdk-go/types"
	"github.com/aws/durable-execution-sdk-go/utils"
)

// ---------------------------------------------------------------------------
// Duration tests
// ---------------------------------------------------------------------------

func TestDurationToSeconds(t *testing.T) {
	tests := []struct {
		name     string
		duration types.Duration
		wantSecs int64
	}{
		{"seconds only", types.Duration{Seconds: 30}, 30},
		{"minutes only", types.Duration{Minutes: 2}, 120},
		{"hours only", types.Duration{Hours: 1}, 3600},
		{"days only", types.Duration{Days: 1}, 86400},
		{"mixed", types.Duration{Days: 1, Hours: 2, Minutes: 3, Seconds: 4}, 93784},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.duration.ToSeconds()
			if got != tt.wantSecs {
				t.Errorf("got %d, want %d", got, tt.wantSecs)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// JSON Serdes tests
// ---------------------------------------------------------------------------

func TestJSONSerdes_RoundTrip(t *testing.T) {
	s := utils.JSONSerdes{}
	type myStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}
	original := myStruct{Name: "test", Value: 42}

	serialized, err := s.Serialize(original, "step-1", "arn:aws:lambda:::exec-1")
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	deserialized, err := s.Deserialize(serialized, "step-1", "arn:aws:lambda:::exec-1")
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}

	// JSON round-trip gives us map[string]any
	m, ok := deserialized.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", deserialized)
	}
	if m["name"] != "test" {
		t.Errorf("name: got %v, want test", m["name"])
	}
}

func TestJSONSerdes_NilValue(t *testing.T) {
	s := utils.JSONSerdes{}
	result, err := s.Serialize(nil, "s", "arn")
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty string for nil, got %q", result)
	}
}

func TestJSONSerdes_EmptyData(t *testing.T) {
	s := utils.JSONSerdes{}
	result, err := s.Deserialize("", "s", "arn")
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty string, got %v", result)
	}
}

// ---------------------------------------------------------------------------
// SafeStringify / ErrorFromErrorObject tests
// ---------------------------------------------------------------------------

func TestSafeStringify_Nil(t *testing.T) {
	result := utils.SafeStringify(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestSafeStringify_Error(t *testing.T) {
	err := errors.New("something went wrong")
	obj := utils.SafeStringify(err)
	if obj == nil {
		t.Fatal("expected non-nil ErrorObject")
	}
	if obj.ErrorMessage != "something went wrong" {
		t.Errorf("unexpected message: %s", obj.ErrorMessage)
	}
}

func TestErrorFromErrorObject(t *testing.T) {
	obj := &types.ErrorObject{
		ErrorType:    "*errors.errorString",
		ErrorMessage: "test error",
	}
	err := utils.ErrorFromErrorObject(obj)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error string")
	}
}

// ---------------------------------------------------------------------------
// HashID tests
// ---------------------------------------------------------------------------

func TestHashID_Deterministic(t *testing.T) {
	h1 := utils.HashID("1-2-3")
	h2 := utils.HashID("1-2-3")
	if h1 != h2 {
		t.Errorf("HashID is not deterministic: %s != %s", h1, h2)
	}
}

func TestHashID_Different(t *testing.T) {
	h1 := utils.HashID("1-2-3")
	h2 := utils.HashID("1-2-4")
	if h1 == h2 {
		t.Error("HashID should produce different values for different inputs")
	}
}

// ---------------------------------------------------------------------------
// StrPtr tests
// ---------------------------------------------------------------------------

func TestStrPtr(t *testing.T) {
	p := utils.StrPtr("hello")
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != "hello" {
		t.Errorf("expected 'hello', got %q", *p)
	}
}

func TestStrPtr_Empty(t *testing.T) {
	p := utils.StrPtr("")
	if p != nil {
		t.Errorf("expected nil for empty string, got non-nil")
	}
}
