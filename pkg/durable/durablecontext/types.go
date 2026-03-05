package durablecontext

import (
	"context"
	"encoding/json"
	"fmt"
)

// ParallelBranch represents a branch in a parallel operation.
type ParallelBranch[T any] struct {
	Name string
	Func func(ctx context.Context) (T, error)
}

// BatchResult represents the result of a map or parallel operation.
type BatchResult[T any] struct {
	results []BatchItemResult[T]
}

// BatchItemResult represents the result of a single item in a batch operation.
type BatchItemResult[T any] struct {
	Index  int
	Result T
	Error  error
}

// GetResults returns all successful results.
func (b *BatchResult[T]) GetResults() []T {
	var results []T
	for _, item := range b.results {
		if item.Error == nil {
			results = append(results, item.Result)
		}
	}
	return results
}

// GetErrors returns all errors.
func (b *BatchResult[T]) GetErrors() []error {
	var errors []error
	for _, item := range b.results {
		if item.Error != nil {
			errors = append(errors, item.Error)
		}
	}
	return errors
}

// AllResults returns all item results (both successes and failures).
func (b *BatchResult[T]) AllResults() []BatchItemResult[T] {
	return b.results
}

// ThrowIfError returns an error if any item failed.
func (b *BatchResult[T]) ThrowIfError() error {
	errors := b.GetErrors()
	if len(errors) > 0 {
		return fmt.Errorf("batch operation had %d failures", len(errors))
	}
	return nil
}

// NewBatchResult creates a new batch result.
func NewBatchResult[T any](results []BatchItemResult[T]) *BatchResult[T] {
	return &BatchResult[T]{results: results}
}

// OperationIdentifier uniquely identifies an operation within an execution.
type OperationIdentifier struct {
	OperationID   string
	OperationName string
}

// ToJSON serializes the identifier to JSON.
func (o OperationIdentifier) ToJSON() (string, error) {
	data, err := json.Marshal(o)
	if err != nil {
		return "", fmt.Errorf("failed to serialize operation identifier: %w", err)
	}
	return string(data), nil
}

// OperationIDSequence generates sequential operation IDs.
type OperationIDSequence struct {
	counter int
}

// NewOperationIDSequence creates a new operation ID sequence.
func NewOperationIDSequence() *OperationIDSequence {
	return &OperationIDSequence{counter: 0}
}

// Next generates the next operation ID.
func (s *OperationIDSequence) Next(name string) OperationIdentifier {
	s.counter++
	return OperationIdentifier{
		OperationID:   fmt.Sprintf("%d", s.counter),
		OperationName: name,
	}
}
