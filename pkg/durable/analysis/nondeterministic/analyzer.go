// Package nondeterministic provides a Go static analysis pass that reports
// calls to non-deterministic functions (time.Now, math/rand.*, crypto/rand.*,
// UUID generators, etc.) made outside durable Step callbacks.
//
// During checkpoint-and-replay, the handler body runs multiple times but
// step callbacks are skipped once their result is checkpointed. Any
// non-deterministic call in the handler body (between steps) will therefore
// produce a different value on replay, breaking the determinism contract.
//
// The analyzer understands the following durable operations and treats their
// callback arguments as safe zones:
//
//   - operations.Step           (arg 2: func(StepContext) (T, error))
//   - operations.Map            (arg 3: func(DurableContext, T, int, []T) (U, error))
//   - operations.Parallel       (arg 2: []func(DurableContext) (T, error))
//   - operations.RunInChildContext (arg 2: func(DurableContext) (T, error))
//   - operations.WaitForCallback   (arg 2: func(StepContext, string) error)
//   - operations.WaitForCondition  (arg 2: func(StepContext, T) (T, error))
package nondeterministic

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the durableNonDeterministic analysis pass.
var Analyzer = &analysis.Analyzer{
	Name:     "durableNonDeterministic",
	Doc:      "reports non-deterministic calls outside durable Step callbacks",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

const durablePkg = "github.com/aws/durable-execution-sdk-go/pkg/durable/operations"

// callbackArgIndex maps each durable operation function name to the 0-based
// index of its primary FuncLit callback argument.
// "Parallel" is handled separately because its callbacks live inside a slice.
var callbackArgIndex = map[string]int{
	"Step":              2,
	"RunInChildContext": 2,
	"WaitForCallback":   2,
	"WaitForCondition":  2,
	"Map":               3,
}

const parallelBranchesArg = 2

// nonDeterministicFns lists package-level functions that produce
// non-deterministic values and must only be called inside step callbacks.
var nonDeterministicFns = map[string]map[string]bool{
	"time": {
		"Now":   true,
		"Since": true,
		"Until": true,
	},
	"math/rand": {
		"ExpFloat64": true, "Float32": true, "Float64": true,
		"Int": true, "Int31": true, "Int31n": true,
		"Int63": true, "Int63n": true, "Intn": true,
		"New": true, "NewSource": true, "NormFloat64": true,
		"Perm": true, "Read": true, "Shuffle": true,
		"Uint32": true, "Uint64": true,
	},
	"math/rand/v2": {
		"ExpFloat64": true, "Float32": true, "Float64": true,
		"Int": true, "IntN": true, "New": true, "NormFloat64": true,
		"Perm": true, "Shuffle": true,
		"Uint32": true, "Uint32N": true, "Uint64": true, "Uint64N": true,
	},
	"crypto/rand": {
		"Int": true, "Prime": true, "Read": true, "Text": true,
	},
}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// ── Pass 1: collect the positions of all step-callback FuncLits ──────────
	stepCallbacks := make(map[token.Pos]bool)

	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		funcName, pkgPath, ok := resolveFunc(pass.TypesInfo, call)
		if !ok || pkgPath != durablePkg {
			return
		}

		// Direct FuncLit callback.
		if argIdx, found := callbackArgIndex[funcName]; found {
			if argIdx < len(call.Args) {
				if fn, ok := call.Args[argIdx].(*ast.FuncLit); ok {
					stepCallbacks[fn.Pos()] = true
				}
			}
		}

		// Parallel: the branches argument is a slice literal of FuncLits.
		if funcName == "Parallel" && parallelBranchesArg < len(call.Args) {
			if lit, ok := call.Args[parallelBranchesArg].(*ast.CompositeLit); ok {
				for _, elt := range lit.Elts {
					if fn, ok := elt.(*ast.FuncLit); ok {
						stepCallbacks[fn.Pos()] = true
					}
				}
			}
		}
	})

	// ── Pass 2: walk with stack, track step-callback depth ───────────────────
	// depth > 0 means we are currently inside at least one step callback body.
	depth := 0

	nodeFilter := []ast.Node{
		(*ast.FuncLit)(nil),
		(*ast.CallExpr)(nil),
	}

	insp.WithStack(nodeFilter, func(n ast.Node, push bool, _ []ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			if stepCallbacks[v.Pos()] {
				if push {
					depth++
				} else {
					depth--
				}
			}
		case *ast.CallExpr:
			if push && depth == 0 {
				name, pkg, ok := resolveFunc(pass.TypesInfo, v)
				if !ok {
					return true
				}
				// Check against known non-deterministic function list.
				if fns, found := nonDeterministicFns[pkg]; found && fns[name] {
					pass.Reportf(v.Pos(),
						"non-deterministic call %q must be inside a Step callback for replay consistency",
						pkg+"."+name)
					return true
				}
				// Heuristic: any call from a package whose import path contains "uuid".
				if strings.Contains(strings.ToLower(pkg), "uuid") {
					pass.Reportf(v.Pos(),
						"non-deterministic call %q (UUID generation) must be inside a Step callback for replay consistency",
						pkg+"."+name)
				}
			}
		}
		return true
	})

	return nil, nil
}

// resolveFunc extracts the function name and package path from a CallExpr,
// unwrapping generic type-argument syntax (Step[T] or Map[T, U]).
func resolveFunc(info *types.Info, call *ast.CallExpr) (funcName, pkgPath string, ok bool) {
	expr := call.Fun
	// Unwrap generic instantiation: e.g. operations.Step[string](...)
	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}

	var ident *ast.Ident
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		ident = e.Sel
	case *ast.Ident:
		ident = e
	default:
		return "", "", false
	}

	obj, exists := info.Uses[ident]
	if !exists || obj == nil || obj.Pkg() == nil {
		return "", "", false
	}
	return obj.Name(), obj.Pkg().Path(), true
}
