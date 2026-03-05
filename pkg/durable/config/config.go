package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration represents a time duration with helper constructors.
type Duration struct {
	Seconds int `json:"seconds"`
}

// Validate checks if the duration is valid.
func (d Duration) Validate() error {
	if d.Seconds < 0 {
		return fmt.Errorf("duration seconds must be non-negative")
	}
	return nil
}

// ToDuration converts to time.Duration.
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d.Seconds) * time.Second
}

// NewDurationFromSeconds creates a Duration from seconds.
func NewDurationFromSeconds(seconds int) Duration {
	return Duration{Seconds: seconds}
}

// NewDurationFromMinutes creates a Duration from minutes.
func NewDurationFromMinutes(minutes int) Duration {
	return Duration{Seconds: minutes * 60}
}

// NewDurationFromHours creates a Duration from hours.
func NewDurationFromHours(hours int) Duration {
	return Duration{Seconds: hours * 3600}
}

// NewDurationFromDays creates a Duration from days.
func NewDurationFromDays(days int) Duration {
	return Duration{Seconds: days * 86400}
}

// StepSemantics defines the execution semantics for a step.
type StepSemantics string

const (
	// StepSemanticsAtLeastOnce guarantees the step executes at least once,
	// possibly multiple times on retries.
	StepSemanticsAtLeastOnce StepSemantics = "AT_LEAST_ONCE"

	// StepSemanticsAtMostOnce guarantees the step executes at most once.
	// If interrupted, it will not retry.
	StepSemanticsAtMostOnce StepSemantics = "AT_MOST_ONCE"
)

// JitterStrategy defines the jitter strategy for retry delays.
// Jitter helps prevent thundering herd problems by randomizing retry delays.
type JitterStrategy string

const (
	// JitterStrategyNone uses exact calculated delay without randomization.
	JitterStrategyNone JitterStrategy = "NONE"

	// JitterStrategyFull randomizes delay between 0 and calculated delay.
	// This provides maximum jitter to spread out retry attempts.
	JitterStrategyFull JitterStrategy = "FULL"

	// JitterStrategyHalf randomizes delay between 50% and 100% of calculated delay.
	// This provides moderate jitter while maintaining predictable minimum delays.
	JitterStrategyHalf JitterStrategy = "HALF"
)

// NestingType controls how child contexts are created for batch operations.
// This affects observability, cost, and scale limits for Map and Parallel operations.
type NestingType string

const (
	// NestingTypeNested creates full CONTEXT operations for each branch/iteration.
	// Provides high observability but consumes more operations and has lower scale.
	NestingTypeNested NestingType = "NESTED"

	// NestingTypeFlat uses virtual contexts without checkpointing for each branch.
	// Reduces operation consumption by ~30% and enables higher scale, but lower observability.
	NestingTypeFlat NestingType = "FLAT"
)

// RetryStrategy defines how a step should retry on failure.
type RetryStrategy func(error, int) RetryDecision

// RetryDecision represents the decision of whether to retry and when.
type RetryDecision struct {
	ShouldRetry bool
	Delay       *Duration
}

// StepConfig configures step execution behavior.
type StepConfig struct {
	// RetryStrategy determines retry behavior. If nil, defaults to standard retry.
	RetryStrategy RetryStrategy

	// Semantics defines execution semantics (AT_LEAST_ONCE or AT_MOST_ONCE).
	Semantics StepSemantics

	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool
}

// DefaultStepConfig returns the default step configuration.
func DefaultStepConfig() StepConfig {
	return StepConfig{
		RetryStrategy:   nil, // Will use default retry strategy
		Semantics:       StepSemanticsAtLeastOnce,
		CheckpointAsync: false,
	}
}

// InvokeConfig configures invoke operation behavior.
type InvokeConfig struct {
	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool
}

// DefaultInvokeConfig returns the default invoke configuration.
func DefaultInvokeConfig() InvokeConfig {
	return InvokeConfig{
		CheckpointAsync: false,
	}
}

// WaitForCallbackConfig configures wait-for-callback operations.
type WaitForCallbackConfig struct {
	// Timeout is the maximum time to wait for the callback.
	Timeout *Duration

	// HeartbeatTimeout is the maximum time between heartbeats.
	HeartbeatTimeout *Duration

	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool
}

// DefaultWaitForCallbackConfig returns the default wait-for-callback configuration.
func DefaultWaitForCallbackConfig() WaitForCallbackConfig {
	return WaitForCallbackConfig{
		CheckpointAsync: false,
	}
}

// WaitStrategy determines if a wait-for-condition should continue waiting.
type WaitStrategy[S any] func(state S, attempt int) WaitStrategyDecision

// WaitStrategyDecision represents the decision of whether to continue waiting.
type WaitStrategyDecision struct {
	ShouldContinue bool
	Delay          *Duration
}

// ConditionPredicate tests if a condition is met.
type ConditionPredicate[S any] func(state S) bool

// WaitForConditionConfig configures wait-for-condition operations.
type WaitForConditionConfig[S any] struct {
	// InitialState is the initial state for the condition check.
	InitialState S

	// Condition is the predicate to test if waiting should stop.
	Condition ConditionPredicate[S]

	// WaitStrategy determines delays between checks.
	WaitStrategy WaitStrategy[S]

	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool
}

// CompletionConfig defines when parallel/map operations are considered complete.
type CompletionConfig struct {
	// MinSuccessful is the minimum number of successful completions required.
	// If nil, no minimum is enforced.
	MinSuccessful *int

	// ToleratedFailureCount is the maximum number of failures allowed.
	// If nil, no limit on failure count.
	ToleratedFailureCount *int

	// ToleratedFailurePercentage is the maximum percentage of failures allowed (0.0-100.0).
	// If nil, no percentage limit.
	ToleratedFailurePercentage *float64
}

// MapConfig configures map operations.
type MapConfig struct {
	// MaxConcurrency limits the number of concurrent iterations.
	// If 0, no limit is enforced.
	MaxConcurrency int

	// CompletionConfig defines completion criteria.
	CompletionConfig *CompletionConfig

	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool

	// Nesting controls how child contexts are created for each iteration.
	// NESTED (default): Full child contexts with checkpointing.
	// FLAT: Virtual contexts without checkpointing for ~30% cost reduction.
	Nesting NestingType
}

// DefaultMapConfig returns the default map configuration.
func DefaultMapConfig() MapConfig {
	return MapConfig{
		MaxConcurrency:  0, // No limit
		CheckpointAsync: false,
		Nesting:         NestingTypeNested,
	}
}

// ParallelConfig configures parallel operations.
type ParallelConfig struct {
	// MaxConcurrency limits the number of concurrent branches.
	// If 0, no limit is enforced.
	MaxConcurrency int

	// CompletionConfig defines completion criteria.
	CompletionConfig *CompletionConfig

	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool

	// Nesting controls how child contexts are created for each branch.
	// NESTED (default): Full child contexts with checkpointing.
	// FLAT: Virtual contexts without checkpointing for ~30% cost reduction.
	Nesting NestingType
}

// DefaultParallelConfig returns the default parallel configuration.
func DefaultParallelConfig() ParallelConfig {
	return ParallelConfig{
		MaxConcurrency:  0, // No limit
		CheckpointAsync: false,
		Nesting:         NestingTypeNested,
	}
}

// ChildConfig configures child context operations.
type ChildConfig struct {
	// CheckpointAsync creates checkpoints asynchronously when true.
	CheckpointAsync bool
}

// DefaultChildConfig returns the default child context configuration.
func DefaultChildConfig() ChildConfig {
	return ChildConfig{
		CheckpointAsync: false,
	}
}

// SerDes defines serialization/deserialization interface.
type SerDes interface {
	Serialize(v interface{}) (string, error)
	Deserialize(data string, v interface{}) error
}

// JSONSerDes implements SerDes using JSON encoding.
type JSONSerDes struct{}

// Serialize serializes a value to JSON string.
func (s JSONSerDes) Serialize(v interface{}) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("failed to serialize: %w", err)
	}
	return string(data), nil
}

// Deserialize deserializes a JSON string to a value.
func (s JSONSerDes) Deserialize(data string, v interface{}) error {
	if data == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(data), v); err != nil {
		return fmt.Errorf("failed to deserialize: %w", err)
	}
	return nil
}

// DefaultSerDes returns the default SerDes implementation.
func DefaultSerDes() SerDes {
	return JSONSerDes{}
}
