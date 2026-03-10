// Package operations contains stub operation definitions for analysis test data.
package operations

import "github.com/aws/durable-execution-sdk-go/pkg/durable/types"

func Step[TOut any](dc types.DurableContext, name string, fn func(sc types.StepContext) (TOut, error)) (TOut, error) {
	var z TOut
	return z, nil
}

func Map[TIn, TOut any](dc types.DurableContext, name string, items []TIn, mapFn func(dc types.DurableContext, item TIn, index int, items []TIn) (TOut, error)) (TOut, error) {
	var z TOut
	return z, nil
}

func Parallel[TOut any](dc types.DurableContext, name string, branches []func(dc types.DurableContext) (TOut, error)) (TOut, error) {
	var z TOut
	return z, nil
}

func RunInChildContext[T any](dc types.DurableContext, name string, fn func(dc types.DurableContext) (T, error)) (T, error) {
	var z T
	return z, nil
}

func Wait(dc types.DurableContext, name string, d types.Duration) error { return nil }

func WaitForCallback[T any](dc types.DurableContext, name string, submitter func(sc types.StepContext, callbackID string) error) (T, error) {
	var z T
	return z, nil
}

func WaitForCondition[TState any](dc types.DurableContext, name string, checkFn func(sc types.StepContext, state TState) (TState, error)) (TState, error) {
	var z TState
	return z, nil
}
