package types

import "time"

// Execution limits as defined in the AWS Lambda Durable Execution SDK Specification §14.1.
// These values reflect the documented service constraints. For current service quotas refer to
// https://docs.aws.amazon.com/lambda/latest/dg/gettingstarted-limits.html
const (
	// MaxExecutionDuration is the maximum end-to-end duration for a durable execution.
	MaxExecutionDuration = 365 * 24 * time.Hour // 1 year

	// MaxResponsePayloadBytes is the maximum size of a Lambda response payload.
	// Results exceeding this limit are automatically checkpointed via an EXECUTION SUCCEED
	// operation and the response Result field is returned empty (spec §3.4).
	MaxResponsePayloadBytes = 6 * 1024 * 1024 // 6 MB

	// MinWaitSeconds is the minimum duration accepted by a WAIT operation (spec §4.5).
	MinWaitSeconds = 1

	// MaxWaitDuration is the maximum duration accepted by a WAIT operation.
	// Subject to MaxExecutionDuration.
	MaxWaitDuration = MaxExecutionDuration

	// MaxCallbackTimeoutSeconds is the maximum timeout configurable for a CALLBACK operation.
	// Subject to overall execution duration limits.
	MaxCallbackTimeoutSeconds = int64(365 * 24 * 60 * 60) // 1 year in seconds

	// DefaultMaxRetryAttempts is the recommended default maximum number of retry attempts
	// for a STEP operation before it is permanently failed (spec §4.4.5).
	DefaultMaxRetryAttempts = 3
)
