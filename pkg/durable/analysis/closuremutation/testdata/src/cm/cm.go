// Package cm is the test-data package for the durableClosureMutation analyzer.
package cm

import (
	"github.com/aws/durable-execution-sdk-go/pkg/durable/operations"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// ── GOOD: reading outer variables is allowed ─────────────────────────────────

func goodRead(dc types.DurableContext) {
	multiplier := 2
	_, _ = operations.Step(dc, "read", func(sc types.StepContext) (any, error) {
		return multiplier * 10, nil // reading outer var is fine
	})
}

// ── GOOD: modifying local variables declared inside the callback ──────────────

func goodLocal(dc types.DurableContext) {
	_, _ = operations.Step(dc, "local", func(sc types.StepContext) (any, error) {
		local := 0
		local++     // local to callback — fine
		local += 10 // compound assignment — fine
		return local, nil
	})
}

// ── GOOD: shadowed variable (inner := shadows outer) ─────────────────────────

func goodShadow(dc types.DurableContext) {
	x := 1
	_, _ = operations.Step(dc, "shadow", func(sc types.StepContext) (any, error) {
		x := 99 // shadows outer x — this declaration is local
		x++     // modifies the local x, not outer — fine
		return x, nil
	})
	_ = x
}

// ── GOOD: callback parameter mutation is allowed ──────────────────────────────

func goodParam(dc types.DurableContext) {
	items := []int{1, 2, 3}
	_, _ = operations.Map(dc, "m", items, func(child types.DurableContext, item int, idx int, all []int) (any, error) {
		item *= 2 // item is a parameter of this callback — fine
		return item, nil
	})
}

// ── BAD: assigning to an outer variable inside Step ───────────────────────────

func badAssign(dc types.DurableContext) {
	result := ""
	_, _ = operations.Step(dc, "assign", func(sc types.StepContext) (any, error) {
		result = "done" // want `variable "result" from outer scope modified inside a durable callback; skipped during replay`
		return nil, nil
	})
	_ = result
}

// ── BAD: increment/decrement of outer variable ───────────────────────────────

func badIncDec(dc types.DurableContext) {
	counter := 0
	_, _ = operations.Step(dc, "inc", func(sc types.StepContext) (any, error) {
		counter++ // want `variable "counter" from outer scope modified inside a durable callback; skipped during replay`
		return nil, nil
	})
	_ = counter
}

// ── BAD: compound assignment to outer variable ────────────────────────────────

func badCompound(dc types.DurableContext) {
	total := 0
	_, _ = operations.Step(dc, "compound", func(sc types.StepContext) (any, error) {
		total += 5 // want `variable "total" from outer scope modified inside a durable callback; skipped during replay`
		return nil, nil
	})
	_ = total
}

// ── BAD: outer mutation inside Map callback ───────────────────────────────────

func badMap(dc types.DurableContext) {
	count := 0
	items := []int{1, 2, 3}
	_, _ = operations.Map(dc, "map", items, func(child types.DurableContext, item int, idx int, all []int) (any, error) {
		count++ // want `variable "count" from outer scope modified inside a durable callback; skipped during replay`
		return item, nil
	})
	_ = count
}

// ── BAD: outer mutation inside RunInChildContext callback ─────────────────────

func badChild(dc types.DurableContext) {
	state := "initial"
	_, _ = operations.RunInChildContext(dc, "child", func(child types.DurableContext) (any, error) {
		state = "updated" // want `variable "state" from outer scope modified inside a durable callback; skipped during replay`
		return nil, nil
	})
	_ = state
}

// ── BAD: outer mutation inside WaitForCallback submitter ─────────────────────

func badCallback(dc types.DurableContext) {
	registered := false
	_, _ = operations.WaitForCallback[string](dc, "cb", func(sc types.StepContext, id string) error {
		registered = true // want `variable "registered" from outer scope modified inside a durable callback; skipped during replay`
		return nil
	})
	_ = registered
}
