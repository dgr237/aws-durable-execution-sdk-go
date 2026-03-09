package context

import (
	"fmt"
	"strings"

	durableErrors "github.com/aws/durable-execution-sdk-go/errors"
	"github.com/aws/durable-execution-sdk-go/types"
	"github.com/aws/durable-execution-sdk-go/utils"
)

// CreateStepID creates a hierarchical step ID.
// If prefix is empty, the ID is just the counter. Otherwise it is "prefix-counter".
func CreateStepID(prefix string, counter int) string {
	if prefix == "" {
		return fmt.Sprintf("%d", counter)
	}
	return fmt.Sprintf("%s-%d", prefix, counter)
}

// GetParentID extracts the parent ID from a hierarchical step ID (e.g. "1-2-3" -> "1-2").
func GetParentID(stepID string) string {
	idx := strings.LastIndex(stepID, "-")
	if idx <= 0 {
		return ""
	}
	return stepID[:idx]
}

// HashedStepID returns a shortened display hash of a step ID.
func HashedStepID(stepID string) string {
	return utils.HashID(stepID)
}

// ValidateReplayConsistency checks that a replayed operation matches the expected type and name.
// If the stored operation has a different type or name, it returns a NonDeterministicError.
func ValidateReplayConsistency(
	stepID string,
	expectedType types.OperationType,
	expectedName *string,
	expectedSubType *types.OperationSubType,
	stored *types.Operation,
) error {
	if stored == nil {
		// No stored data - this is a new operation, nothing to validate
		return nil
	}

	if stored.Type != expectedType {
		return &durableErrors.NonDeterministicError{
			Message: fmt.Sprintf(
				"replay consistency error for step %s: expected type %s but found %s",
				stepID, expectedType, stored.Type,
			),
		}
	}

	// Name validation: if both are set and differ, that's non-deterministic
	if expectedName != nil && stored.Name != nil {
		if *expectedName != *stored.Name {
			return &durableErrors.NonDeterministicError{
				Message: fmt.Sprintf(
					"replay consistency error for step %s: expected name %q but found %q",
					stepID, *expectedName, *stored.Name,
				),
			}
		}
	}

	return nil
}
