package utils_test

import (
	"errors"
	"testing"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

func TestExponentialBackoff_MaxAttempts(t *testing.T) {
	strategy := utils.Presets.ExponentialBackoff()

	for attempt := 1; attempt <= 3; attempt++ {
		decision := strategy(errors.New("error"), attempt)
		if attempt < 3 && !decision.ShouldRetry {
			t.Errorf("attempt %d: expected ShouldRetry=true", attempt)
		}
	}

	// 4th attempt should not retry
	decision := strategy(errors.New("error"), 4)
	if decision.ShouldRetry {
		t.Error("attempt 4: expected ShouldRetry=false")
	}
}

func TestNoRetry(t *testing.T) {
	strategy := utils.Presets.NoRetry()
	decision := strategy(errors.New("any error"), 1)
	if decision.ShouldRetry {
		t.Error("expected ShouldRetry=false")
	}
}

func TestFixedDelay(t *testing.T) {
	delay := types.Duration{Seconds: 10}
	strategy := utils.Presets.FixedDelay(delay, 3)

	// First attempt: should retry
	d1 := strategy(errors.New("e"), 1)
	if !d1.ShouldRetry {
		t.Error("attempt 1: expected retry")
	}
	if d1.Delay == nil || d1.Delay.Seconds != 10 {
		t.Errorf("attempt 1: unexpected delay %v", d1.Delay)
	}

	// 4th attempt: stop retrying
	d4 := strategy(errors.New("e"), 4)
	if d4.ShouldRetry {
		t.Error("attempt 4: expected no retry")
	}
}

func TestCreateRetryStrategy_RetryableErrors(t *testing.T) {
	strategy := utils.CreateRetryStrategy(utils.RetryStrategyConfig{
		MaxAttempts:     5,
		RetryableErrors: []string{"timeout"},
	})

	// Retryable error
	d := strategy(errors.New("connection timeout"), 1)
	if !d.ShouldRetry {
		t.Error("expected retry for timeout error")
	}

	// Non-retryable error
	d2 := strategy(errors.New("validation failed"), 1)
	if d2.ShouldRetry {
		t.Error("expected no retry for non-timeout error")
	}
}

func TestCreateRetryStrategy_DefaultBackoff(t *testing.T) {
	strategy := utils.CreateRetryStrategy(utils.RetryStrategyConfig{})

	// Default: 3 attempts, all errors retryable
	d := strategy(errors.New("any"), 1)
	if !d.ShouldRetry {
		t.Error("expected retry on attempt 1")
	}
	if d.Delay == nil {
		t.Error("expected non-nil delay")
	}
}

// retry package alias for test file
var _ = utils.Presets
