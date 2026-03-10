// Package retry provides retry strategy builders for durable step operations.
package utils

import (
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// RetryStrategyConfig configures the behavior of a retry strategy.
type RetryStrategyConfig struct {
	// MaxAttempts is the maximum total number of attempts (including the initial attempt).
	// Defaults to 3.
	MaxAttempts int
	// InitialDelay is the delay before the first retry.
	// Defaults to {Seconds: 5}.
	InitialDelay *types.Duration
	// MaxDelay is the maximum delay between retries.
	// Defaults to {Minutes: 5}.
	MaxDelay *types.Duration
	// BackoffRate is the exponential backoff multiplier.
	// Defaults to 2.0.
	BackoffRate float64
	// Jitter is the jitter strategy to apply.
	// Defaults to JitterStrategyFull.
	Jitter types.JitterStrategy
	// RetryableErrors is a list of error message substrings that are retryable.
	// When empty (and RetryableErrorTypes is also nil), all errors are retried.
	RetryableErrors []string
}

// CreateRetryStrategy builds a retry strategy function from the provided configuration.
func CreateRetryStrategy(cfg RetryStrategyConfig) func(err error, attemptCount int) types.RetryDecision {
	// Apply defaults
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	initialDelay := cfg.InitialDelay
	if initialDelay == nil {
		initialDelay = &types.Duration{Seconds: 5}
	}

	maxDelay := cfg.MaxDelay
	if maxDelay == nil {
		maxDelay = &types.Duration{Minutes: 5}
	}

	backoffRate := cfg.BackoffRate
	if backoffRate <= 0 {
		backoffRate = 2.0
	}

	jitter := cfg.Jitter
	if jitter == "" {
		jitter = types.JitterStrategyFull
	}

	return func(err error, attemptCount int) types.RetryDecision {
		// Check if we've exceeded max attempts
		if attemptCount >= maxAttempts {
			return types.RetryDecision{ShouldRetry: false}
		}

		// Check if the error is retryable
		if !isRetryable(err, cfg.RetryableErrors) {
			return types.RetryDecision{ShouldRetry: false}
		}

		// Calculate exponential backoff delay
		initialSeconds := float64(initialDelay.ToSeconds())
		maxSeconds := float64(maxDelay.ToSeconds())

		// Exponential backoff: initialDelay * backoffRate^(attempt-1)
		calculatedSeconds := initialSeconds * math.Pow(backoffRate, float64(attemptCount-1))
		if calculatedSeconds > maxSeconds {
			calculatedSeconds = maxSeconds
		}

		// Apply jitter
		finalSeconds := applyJitter(calculatedSeconds, jitter)

		delay := &types.Duration{Seconds: int(math.Round(finalSeconds))}
		return types.RetryDecision{
			ShouldRetry: true,
			Delay:       delay,
		}
	}
}

func isRetryable(err error, retryableErrors []string) bool {
	// No filters = all errors are retryable
	if len(retryableErrors) == 0 {
		return true
	}

	if err == nil {
		return false
	}

	msg := err.Error()
	for _, pattern := range retryableErrors {
		if containsSubstring(msg, pattern) {
			return true
		}
	}
	return false
}

func containsSubstring(s, sub string) bool {
	return len(sub) > 0 && strings.Contains(s, sub)
}

func applyJitter(seconds float64, strategy types.JitterStrategy) float64 {
	switch strategy {
	case types.JitterStrategyNone:
		return seconds
	case types.JitterStrategyHalf:
		// Random between 50% and 100% of calculated delay
		half := seconds * 0.5
		return half + rand.Float64()*half //nolint:gosec
	case types.JitterStrategyFull:
		// Random between 0 and 100% of calculated delay
		return rand.Float64() * seconds //nolint:gosec
	default:
		return seconds
	}
}

// RetryPresets provides common pre-configured retry strategies.
type RetryPresets struct{}

// Presets is the global instance of RetryPresets.
var Presets = RetryPresets{}

// ExponentialBackoff returns a retry strategy with exponential backoff and full jitter.
// Defaults: maxAttempts=3, initialDelay=5s, maxDelay=5m, backoffRate=2.0.
func (RetryPresets) ExponentialBackoff() func(err error, attemptCount int) types.RetryDecision {
	return CreateRetryStrategy(RetryStrategyConfig{
		MaxAttempts:  3,
		InitialDelay: &types.Duration{Seconds: 5},
		MaxDelay:     &types.Duration{Minutes: 5},
		BackoffRate:  2.0,
		Jitter:       types.JitterStrategyFull,
	})
}

// NoRetry returns a retry strategy that never retries.
func (RetryPresets) NoRetry() func(err error, attemptCount int) types.RetryDecision {
	return func(err error, attemptCount int) types.RetryDecision {
		return types.RetryDecision{ShouldRetry: false}
	}
}

// FixedDelay returns a retry strategy with a fixed delay between attempts.
func (RetryPresets) FixedDelay(delay types.Duration, maxAttempts int) func(err error, attemptCount int) types.RetryDecision {
	return func(err error, attemptCount int) types.RetryDecision {
		if attemptCount >= maxAttempts {
			return types.RetryDecision{ShouldRetry: false}
		}
		d := delay
		return types.RetryDecision{
			ShouldRetry: true,
			Delay:       &d,
		}
	}
}

// DefaultRetryDelay returns the default delay for a given attempt count (1 second).
func DefaultRetryDelay() *types.Duration {
	return &types.Duration{Seconds: 1}
}

// SleepForDuration sleeps for the given Duration.
func SleepForDuration(d types.Duration) {
	time.Sleep(d.ToDuration())
}
