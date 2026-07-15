// Package types contains stub type definitions for analysis test data.
package types

// DurableContext is the orchestration-level context.
type DurableContext interface{ durableCtx() }

// StepContext is the restricted context inside step callbacks.
type StepContext interface{ stepCtx() }

// Duration represents a time duration.
type Duration struct{ Seconds int }
