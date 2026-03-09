// Package utils provides utility functions for the AWS Durable Execution SDK.
package utils

import (
	"encoding/json"
	"fmt"

	"github.com/aws/durable-execution-sdk-go/types"
)

// JSONSerdes is the default Serdes implementation using JSON encoding.
// It satisfies the types.Serdes interface.
type JSONSerdes struct{}

// Serialize marshals the value to JSON.
func (s JSONSerdes) Serialize(value any, entityID string, executionArn string) (string, error) {
	if value == nil {
		return "", nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("json serialize error for entity %s: %w", entityID, err)
	}
	return string(b), nil
}

// Deserialize unmarshals JSON data into an any value.
func (s JSONSerdes) Deserialize(data string, entityID string, executionArn string) (any, error) {
	if data == "" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		return nil, fmt.Errorf("json deserialize error for entity %s: %w", entityID, err)
	}
	return v, nil
}

// DefaultSerdes is the default JSON Serdes instance.
var DefaultSerdes types.Serdes = JSONSerdes{}

// SafeSerialize serializes a value using the provided Serdes, returning "" on nil.
func SafeSerialize[TIn any](s types.Serdes, value TIn, entityID, executionArn string) (string, error) {
	if s == nil {
		s = DefaultSerdes
	}
	if &value == nil {
		return "", nil
	}
	return s.Serialize(value, entityID, executionArn)
}

// SafeDeserialize deserializes a string using the provided Serdes.
func SafeDeserialize[T any](s types.Serdes, data *string, entityID, executionArn string) (T, error) {
	var zero T
	if s == nil {
		s = DefaultSerdes
	}
	if data == nil || *data == "" {
		return zero, nil
	}

	// For the default JSON serdes, unmarshal directly into the target type
	// to preserve type information for complex types like structs and slices
	if _, isJSON := s.(JSONSerdes); isJSON || s == DefaultSerdes {
		var result T
		if err := json.Unmarshal([]byte(*data), &result); err != nil {
			return zero, fmt.Errorf("json deserialize error for entity %s: %w", entityID, err)
		}
		return result, nil
	}

	// For custom serdes, use the Deserialize method and attempt type assertion
	val, err := s.Deserialize(*data, entityID, executionArn)
	if err != nil {
		return zero, err
	}
	typedVal, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("deserialized value is not of expected type")
	}
	return typedVal, nil
}

// SafeStringify converts an error to an ErrorObject for checkpoint storage.
func SafeStringify(err error) *types.ErrorObject {
	if err == nil {
		return nil
	}
	return &types.ErrorObject{
		ErrorType:    fmt.Sprintf("%T", err),
		ErrorMessage: err.Error(),
	}
}

// ErrorFromErrorObject reconstructs a Go error from an ErrorObject.
func ErrorFromErrorObject(obj *types.ErrorObject) error {
	if obj == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", obj.ErrorType, obj.ErrorMessage)
}

// HashID creates a shortened hash of a step ID for display purposes.
// In the JS SDK this uses a CRC32 hash; here we use a simple FNV-1a variant.
func HashID(id string) string {
	var h uint32 = 2166136261
	for _, c := range []byte(id) {
		h ^= uint32(c)
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}

// DurationToSeconds converts a Duration to total seconds as int64.
func DurationToSeconds(d types.Duration) int64 {
	return d.ToSeconds()
}

// StrPtr returns a pointer to a string, or nil if the string is empty.
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
