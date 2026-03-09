package operations

import (
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ---------------------------------------------------------------------------
// MapOption
// ---------------------------------------------------------------------------

type MapOption[TIn, TOut any] func(*MapRunner[TIn, TOut])

func WithMapSerdes[TIn, TOut any](s types.Serdes) MapOption[TIn, TOut] {
	return func(r *MapRunner[TIn, TOut]) { r.serdes = s }
}

func WithMapMaxConcurrency[TIn, TOut any](n int) MapOption[TIn, TOut] {
	return func(r *MapRunner[TIn, TOut]) { r.maxConcurrency = n }
}

func WithMapItemNamer[TIn, TOut any](fn func(item TIn, index int) string) MapOption[TIn, TOut] {
	return func(r *MapRunner[TIn, TOut]) { r.itemNamer = fn }
}

// ---------------------------------------------------------------------------
// CallbackOption
// ---------------------------------------------------------------------------

type CallbackOption[TResult any] func(*CallbackRunner[TResult])

func WithCallbackTimeout[TResult any](d types.Duration) CallbackOption[TResult] {
	return func(r *CallbackRunner[TResult]) { r.timeout = &d }
}

// ---------------------------------------------------------------------------
// InvokeOption
// ---------------------------------------------------------------------------

type InvokeOption[TIn, TOut any] func(*InvokeRunner[TIn, TOut])

func WithInvokeSerdes[TIn, TOut any](s types.Serdes) InvokeOption[TIn, TOut] {
	return func(r *InvokeRunner[TIn, TOut]) { r.serdes = s }
}

// ---------------------------------------------------------------------------
// ParallelOption
// ---------------------------------------------------------------------------

type ParallelOption[TOut any] func(*ParallelRunner[TOut])

func WithParallelSerdes[TOut any](s types.Serdes) ParallelOption[TOut] {
	return func(r *ParallelRunner[TOut]) { r.serdes = s }
}

func WithParallelMaxConcurrency[TOut any](n int) ParallelOption[TOut] {
	return func(r *ParallelRunner[TOut]) { r.maxConcurrency = n }
}

// ---------------------------------------------------------------------------
// ChildContextOption
// ---------------------------------------------------------------------------

type ChildContextOption[T any] func(*ChildContextRunner[T])

func WithChildSerdes[T any](s types.Serdes) ChildContextOption[T] {
	return func(r *ChildContextRunner[T]) { r.serdes = s }
}

func WithChildSubType[T any](st types.OperationSubType) ChildContextOption[T] {
	return func(r *ChildContextRunner[T]) { r.childSubType = &st }
}

// ---------------------------------------------------------------------------
// StepOption
// ---------------------------------------------------------------------------

type StepOption[TOut any] func(*StepRunner[TOut])

func WithStepSerdes[TOut any](s types.Serdes) StepOption[TOut] {
	return func(r *StepRunner[TOut]) { r.serdes = s }
}

func WithStepSemantics[TOut any](sem types.StepSemantics) StepOption[TOut] {
	return func(r *StepRunner[TOut]) { r.semantics = sem }
}

func WithStepRetryStrategy[TOut any](fn func(err error, attempt int) types.RetryDecision) StepOption[TOut] {
	return func(r *StepRunner[TOut]) { r.retryStrategy = fn }
}

// ---------------------------------------------------------------------------
// WaitForCallbackOption
// ---------------------------------------------------------------------------

type WaitForCallbackOption[T any] func(*WaitForCallbackRunner[T])

func WithWaitForCallbackSerdes[T any](s types.Serdes) WaitForCallbackOption[T] {
	return func(r *WaitForCallbackRunner[T]) { r.serdes = s }
}

func WithWaitForCallbackTimeout[T any](d types.Duration) WaitForCallbackOption[T] {
	return func(r *WaitForCallbackRunner[T]) { r.timeout = &d }
}

// ---------------------------------------------------------------------------
// WaitForConditionOption
// ---------------------------------------------------------------------------

type WaitForConditionOption[TState any] func(*WaitForConditionRunner[TState])

func WithConditionSerdes[TState any](s types.Serdes) WaitForConditionOption[TState] {
	return func(r *WaitForConditionRunner[TState]) { r.serdes = s }
}

func WithConditionWaitStrategy[TState any](fn func(state TState, attempt int) types.WaitStrategyResult) WaitForConditionOption[TState] {
	return func(r *WaitForConditionRunner[TState]) { r.waitStrategy = fn }
}
