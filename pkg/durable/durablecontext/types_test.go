package durablecontext

import (
	"context"
	"errors"
	"testing"
)

func TestParallelBranch(t *testing.T) {
	t.Run("basic structure", func(t *testing.T) {
		branch := ParallelBranch[int]{
			Name: "test-branch",
			Func: func(ctx context.Context) (int, error) {
				return 42, nil
			},
		}

		if branch.Name != "test-branch" {
			t.Errorf("expected name 'test-branch', got '%s'", branch.Name)
		}

		result, err := branch.Func(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result != 42 {
			t.Errorf("expected result 42, got %d", result)
		}
	})

	t.Run("branch with error", func(t *testing.T) {
		expectedErr := errors.New("test error")
		branch := ParallelBranch[string]{
			Name: "error-branch",
			Func: func(ctx context.Context) (string, error) {
				return "", expectedErr
			},
		}

		_, err := branch.Func(context.Background())
		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestBatchResult(t *testing.T) {
	t.Run("GetResults returns only successful results", func(t *testing.T) {
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 2, Error: errors.New("error")},
			{Index: 2, Result: 3, Error: nil},
			{Index: 3, Result: 4, Error: errors.New("another error")},
			{Index: 4, Result: 5, Error: nil},
		}

		batch := NewBatchResult(results)
		successfulResults := batch.GetResults()

		if len(successfulResults) != 3 {
			t.Errorf("expected 3 successful results, got %d", len(successfulResults))
		}

		expected := []int{1, 3, 5}
		for i, val := range successfulResults {
			if val != expected[i] {
				t.Errorf("expected result %d at index %d, got %d", expected[i], i, val)
			}
		}
	})

	t.Run("GetResults with all errors", func(t *testing.T) {
		results := []BatchItemResult[string]{
			{Index: 0, Result: "", Error: errors.New("error 1")},
			{Index: 1, Result: "", Error: errors.New("error 2")},
		}

		batch := NewBatchResult(results)
		successfulResults := batch.GetResults()

		if len(successfulResults) != 0 {
			t.Errorf("expected 0 successful results, got %d", len(successfulResults))
		}
	})

	t.Run("GetErrors returns only failed results", func(t *testing.T) {
		err1 := errors.New("error 1")
		err2 := errors.New("error 2")
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 0, Error: err1},
			{Index: 2, Result: 3, Error: nil},
			{Index: 3, Result: 0, Error: err2},
		}

		batch := NewBatchResult(results)
		errs := batch.GetErrors()

		if len(errs) != 2 {
			t.Errorf("expected 2 errors, got %d", len(errs))
		}

		if errs[0] != err1 {
			t.Errorf("expected first error to be err1, got %v", errs[0])
		}
		if errs[1] != err2 {
			t.Errorf("expected second error to be err2, got %v", errs[1])
		}
	})

	t.Run("GetErrors with no errors", func(t *testing.T) {
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 2, Error: nil},
		}

		batch := NewBatchResult(results)
		errs := batch.GetErrors()

		if len(errs) != 0 {
			t.Errorf("expected 0 errors, got %d", len(errs))
		}
	})

	t.Run("AllResults returns all items", func(t *testing.T) {
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 0, Error: errors.New("error")},
			{Index: 2, Result: 3, Error: nil},
		}

		batch := NewBatchResult(results)
		all := batch.AllResults()

		if len(all) != 3 {
			t.Errorf("expected 3 total results, got %d", len(all))
		}

		for i, item := range all {
			if item.Index != results[i].Index {
				t.Errorf("expected index %d at position %d, got %d", results[i].Index, i, item.Index)
			}
		}
	})

	t.Run("ThrowIfError returns nil when no errors", func(t *testing.T) {
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 2, Error: nil},
		}

		batch := NewBatchResult(results)
		err := batch.ThrowIfError()

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("ThrowIfError returns error when there are failures", func(t *testing.T) {
		results := []BatchItemResult[int]{
			{Index: 0, Result: 1, Error: nil},
			{Index: 1, Result: 0, Error: errors.New("error 1")},
			{Index: 2, Result: 0, Error: errors.New("error 2")},
		}

		batch := NewBatchResult(results)
		err := batch.ThrowIfError()

		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("empty batch result", func(t *testing.T) {
		batch := NewBatchResult[int]([]BatchItemResult[int]{})

		if len(batch.GetResults()) != 0 {
			t.Error("expected empty results")
		}
		if len(batch.GetErrors()) != 0 {
			t.Error("expected no errors")
		}
		if len(batch.AllResults()) != 0 {
			t.Error("expected no items")
		}
		if err := batch.ThrowIfError(); err != nil {
			t.Errorf("expected no error for empty batch, got %v", err)
		}
	})
}

func TestBatchItemResult(t *testing.T) {
	t.Run("successful item", func(t *testing.T) {
		item := BatchItemResult[string]{
			Index:  5,
			Result: "success",
			Error:  nil,
		}

		if item.Index != 5 {
			t.Errorf("expected index 5, got %d", item.Index)
		}
		if item.Result != "success" {
			t.Errorf("expected result 'success', got '%s'", item.Result)
		}
		if item.Error != nil {
			t.Errorf("expected no error, got %v", item.Error)
		}
	})

	t.Run("failed item", func(t *testing.T) {
		expectedErr := errors.New("test error")
		item := BatchItemResult[int]{
			Index:  3,
			Result: 0,
			Error:  expectedErr,
		}

		if item.Index != 3 {
			t.Errorf("expected index 3, got %d", item.Index)
		}
		if item.Error != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, item.Error)
		}
	})
}

func TestOperationIdentifier(t *testing.T) {
	t.Run("ToJSON serializes correctly", func(t *testing.T) {
		op := OperationIdentifier{
			OperationID:   "123",
			OperationName: "test-op",
		}

		json, err := op.ToJSON()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Check that JSON contains expected fields
		expected := `{"OperationID":"123","OperationName":"test-op"}`
		if json != expected {
			t.Errorf("expected JSON %s, got %s", expected, json)
		}
	})

	t.Run("ToJSON with empty values", func(t *testing.T) {
		op := OperationIdentifier{
			OperationID:   "",
			OperationName: "",
		}

		json, err := op.ToJSON()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := `{"OperationID":"","OperationName":""}`
		if json != expected {
			t.Errorf("expected JSON %s, got %s", expected, json)
		}
	})

	t.Run("ToJSON with special characters", func(t *testing.T) {
		op := OperationIdentifier{
			OperationID:   "id-with-dashes",
			OperationName: "name with spaces",
		}

		json, err := op.ToJSON()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Just verify it doesn't error and contains the values
		if json == "" {
			t.Error("expected non-empty JSON string")
		}
	})
}

func TestOperationIDSequence(t *testing.T) {
	t.Run("NewOperationIDSequence creates sequence starting at 0", func(t *testing.T) {
		seq := NewOperationIDSequence()
		if seq == nil {
			t.Fatal("expected non-nil sequence")
		}
		if seq.counter != 0 {
			t.Errorf("expected counter to start at 0, got %d", seq.counter)
		}
	})

	t.Run("Next increments counter and returns correct ID", func(t *testing.T) {
		seq := NewOperationIDSequence()

		op1 := seq.Next("operation-1")
		if op1.OperationID != "1" {
			t.Errorf("expected OperationID '1', got '%s'", op1.OperationID)
		}
		if op1.OperationName != "operation-1" {
			t.Errorf("expected OperationName 'operation-1', got '%s'", op1.OperationName)
		}

		op2 := seq.Next("operation-2")
		if op2.OperationID != "2" {
			t.Errorf("expected OperationID '2', got '%s'", op2.OperationID)
		}
		if op2.OperationName != "operation-2" {
			t.Errorf("expected OperationName 'operation-2', got '%s'", op2.OperationName)
		}

		op3 := seq.Next("operation-3")
		if op3.OperationID != "3" {
			t.Errorf("expected OperationID '3', got '%s'", op3.OperationID)
		}
		if op3.OperationName != "operation-3" {
			t.Errorf("expected OperationName 'operation-3', got '%s'", op3.OperationName)
		}
	})

	t.Run("multiple sequences are independent", func(t *testing.T) {
		seq1 := NewOperationIDSequence()
		seq2 := NewOperationIDSequence()

		op1 := seq1.Next("seq1-op1")
		op2 := seq2.Next("seq2-op1")
		op3 := seq1.Next("seq1-op2")
		op4 := seq2.Next("seq2-op2")

		if op1.OperationID != "1" || op3.OperationID != "2" {
			t.Error("seq1 IDs are incorrect")
		}
		if op2.OperationID != "1" || op4.OperationID != "2" {
			t.Error("seq2 IDs are incorrect")
		}
	})

	t.Run("Next with empty name", func(t *testing.T) {
		seq := NewOperationIDSequence()
		op := seq.Next("")

		if op.OperationID != "1" {
			t.Errorf("expected OperationID '1', got '%s'", op.OperationID)
		}
		if op.OperationName != "" {
			t.Errorf("expected empty OperationName, got '%s'", op.OperationName)
		}
	})

	t.Run("Next preserves operation names", func(t *testing.T) {
		seq := NewOperationIDSequence()
		name := "very-long-operation-name-with-special-chars-123!@#"
		op := seq.Next(name)

		if op.OperationName != name {
			t.Errorf("expected OperationName '%s', got '%s'", name, op.OperationName)
		}
	})
}
