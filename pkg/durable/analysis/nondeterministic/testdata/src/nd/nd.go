// Package nd is the test-data package for the durableNonDeterministic analyzer.
package nd

import (
	"math/rand"
	"time"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ── GOOD: non-deterministic calls inside Step are allowed ────────────────────

func goodStep(dc types.DurableContext) {
	_, _ = operations.Step(dc, "ts", func(sc types.StepContext) (any, error) {
		return time.Now(), nil // safe inside step
	})
	_, _ = operations.Step(dc, "rng", func(sc types.StepContext) (any, error) {
		return rand.Int(), nil // safe inside step
	})
}

// ── GOOD: non-deterministic calls inside Map callback are allowed ─────────────

func goodMap(dc types.DurableContext) {
	items := []int{1, 2, 3}
	_, _ = operations.Map(dc, "m", items, func(child types.DurableContext, item int, idx int, all []int) (any, error) {
		return time.Now(), nil // safe inside map callback
	})
}

// ── GOOD: non-deterministic calls inside RunInChildContext are allowed ────────

func goodChild(dc types.DurableContext) {
	_, _ = operations.RunInChildContext(dc, "c", func(child types.DurableContext) (any, error) {
		return time.Now(), nil // safe inside child context
	})
}

// ── GOOD: non-deterministic inside WaitForCallback submitter ─────────────────

func goodCallback(dc types.DurableContext) {
	_, _ = operations.WaitForCallback[string](dc, "cb", func(sc types.StepContext, id string) error {
		_ = time.Now() // safe inside submitter
		return nil
	})
}

// ── GOOD: non-deterministic inside WaitForCondition checkFn ──────────────────

func goodCondition(dc types.DurableContext) {
	_, _ = operations.WaitForCondition(dc, "cond", func(sc types.StepContext, state int) (int, error) {
		_ = time.Now() // safe inside checkFn
		return state, nil
	})
}

// ── GOOD: deterministic calls are always allowed outside step ─────────────────

func goodDeterministic(dc types.DurableContext) {
	t := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // deterministic: fixed date
	_ = t
	_ = time.Second // constant, deterministic
}

// ── BAD: non-deterministic calls outside any step callback ────────────────────

func badTimeNow(dc types.DurableContext) {
	t := time.Now() // want `non-deterministic call "time\.Now" must be inside a Step callback`
	_ = t
}

func badTimeSince(dc types.DurableContext) {
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d := time.Since(t0) // want `non-deterministic call "time\.Since" must be inside a Step callback`
	_ = d
}

func badTimeUntil(dc types.DurableContext) {
	t0 := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	d := time.Until(t0) // want `non-deterministic call "time\.Until" must be inside a Step callback`
	_ = d
}

func badRandInt(dc types.DurableContext) {
	n := rand.Int() // want `non-deterministic call "math/rand\.Int" must be inside a Step callback`
	_ = n
}

func badRandFloat(dc types.DurableContext) {
	f := rand.Float64() // want `non-deterministic call "math/rand\.Float64" must be inside a Step callback`
	_ = f
}

// ── BAD: non-deterministic in a closure defined outside any step ──────────────

func badInOuterClosure(dc types.DurableContext) {
	// The FuncLit is not passed directly to a durable operation, so it is
	// not a step callback — the time.Now() inside is flagged.
	gen := func() time.Time {
		return time.Now() // want `non-deterministic call "time\.Now" must be inside a Step callback`
	}
	_ = gen
}

// ── BAD: non-deterministic between steps ─────────────────────────────────────

func badBetweenSteps(dc types.DurableContext) {
	_, _ = operations.Step(dc, "s1", func(sc types.StepContext) (any, error) {
		return 1, nil // safe
	})

	t := time.Now() // want `non-deterministic call "time\.Now" must be inside a Step callback`
	_ = t

	_, _ = operations.Step(dc, "s2", func(sc types.StepContext) (any, error) {
		return 2, nil // safe
	})
}
