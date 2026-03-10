package durable

// Determinism helpers for AWS Durable Execution SDK (spec §2.5, §14.3).
//
// # Why Determinism Matters
//
// When a durable function resumes after a timeout or failure, the SDK replays the
// handler from the beginning. During replay every durable operation (Step, Wait,
// Invoke, etc.) is skipped and its checkpointed result is returned instantly.
// Code that runs between operations must produce the same observable behaviour on
// every replay; if it does not, the function may execute a different sequence of
// operations than it did originally, causing data corruption or an unrecoverable
// determinism-violation error.
//
// # Rules for User Code
//
//  1. Do NOT call time.Now(), rand.Float64(), uuid.New(), or similar functions
//     outside durable operations — their results differ between replays.
//  2. Do NOT make API calls, DB writes, or file I/O outside durable operations
//     if the result would influence which operations are executed.
//  3. Do NOT iterate over Go maps when the iteration order affects which
//     operations are called or in what order — Go randomises map iteration by
//     design. Instead, collect the keys into a slice, sort it, and range over
//     the slice.
//  4. Logging and other side-effects that do NOT influence operation order are
//     acceptable outside durable operations.
//
// # Replay-Safe Helpers
//
// This file provides drop-in replacements for common non-deterministic functions.
// Use them outside of durable operations to keep your handler deterministic.

import (
	"context"
	"errors"
	"time"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
)

// CurrentTime returns the start time of the current durable execution, derived
// from the EXECUTION operation's StartTimestamp recorded by the service.
//
// Use CurrentTime instead of time.Now() in handler code that runs outside of
// durable operations. Because the value comes from checkpointed state it is
// identical on every replay of the same execution.
//
// Example:
//
//	startedAt, err := durable.CurrentTime(ctx)
//	if err != nil {
//	    return Result{}, err
//	}
//	// Use startedAt instead of time.Now()
func CurrentTime(ctx context.Context) (time.Time, error) {
	dc, err := durableCtx.GetDurableContext(ctx)
	if err != nil {
		return time.Time{}, errors.New("durable.CurrentTime: no DurableContext in ctx — pass the context.Context from your HandlerFunc")
	}
	t, ok := dc.ExecutionStartTime()
	if !ok {
		return time.Time{}, errors.New("durable.CurrentTime: execution start time unavailable — EXECUTION operation has no StartTimestamp")
	}
	return t, nil
}
