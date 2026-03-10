// Package closuremutation provides a Go static analysis pass that reports
// mutations of outer-scope variables inside durable operation callbacks.
//
// During checkpoint-and-replay, the SDK skips step callbacks whose results
// are already checkpointed. If a step callback modifies a variable declared
// in the enclosing handler, that modification will NOT occur on replay,
// causing different behavior between the initial execution and replays.
//
// The following patterns are flagged:
//
//	counter := 0
//	operations.Step(dc, "s", func(sc types.StepContext) (any, error) {
//	    counter++        // BAD: outer variable modified, skipped on replay
//	    counter += 1    // BAD: compound assignment
//	    counter = 5     // BAD: direct assignment
//	    return nil, nil
//	})
//
// Reading outer variables is allowed; only writes are flagged.
// Variables declared inside the callback (including its parameters) are
// never flagged.
package closuremutation

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is the durableClosureMutation analysis pass.
var Analyzer = &analysis.Analyzer{
	Name:     "durableClosureMutation",
	Doc:      "reports mutations of outer-scope variables inside durable operation callbacks",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

const durablePkg = "github.com/aws/durable-execution-sdk-go/pkg/durable/operations"

var callbackArgIndex = map[string]int{
	"Step":              2,
	"RunInChildContext": 2,
	"WaitForCallback":   2,
	"WaitForCondition":  2,
	"Map":               3,
}

const parallelBranchesArg = 2

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// ── Pass 1: collect all step-callback FuncLits ───────────────────────────
	type callbackEntry struct {
		fn      *ast.FuncLit
		callPos token.Pos // position of the enclosing CallExpr
	}
	var callbacks []callbackEntry
	stepCallbackPos := make(map[token.Pos]bool) // for the nested-skip check

	insp.Preorder([]ast.Node{(*ast.CallExpr)(nil)}, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		funcName, pkgPath, ok := resolveFunc(pass.TypesInfo, call)
		if !ok || pkgPath != durablePkg {
			return
		}

		register := func(fn *ast.FuncLit) {
			stepCallbackPos[fn.Pos()] = true
			callbacks = append(callbacks, callbackEntry{fn: fn, callPos: call.Pos()})
		}

		if argIdx, found := callbackArgIndex[funcName]; found {
			if argIdx < len(call.Args) {
				if fn, ok := call.Args[argIdx].(*ast.FuncLit); ok {
					register(fn)
				}
			}
		}

		if funcName == "Parallel" && parallelBranchesArg < len(call.Args) {
			if lit, ok := call.Args[parallelBranchesArg].(*ast.CompositeLit); ok {
				for _, elt := range lit.Elts {
					if fn, ok := elt.(*ast.FuncLit); ok {
						register(fn)
					}
				}
			}
		}
	})

	// ── Pass 2: for each callback, check its body for outer mutations ─────────
	for _, cb := range callbacks {
		checkMutations(pass, cb.fn, stepCallbackPos)
	}

	return nil, nil
}

// checkMutations walks fn's body and reports assignments or inc/dec
// statements that target a variable declared before fn.Pos() (i.e., from
// an outer scope). It does not recurse into nested step-callback FuncLits —
// those are checked independently when their own callbackEntry is processed.
func checkMutations(pass *analysis.Pass, fn *ast.FuncLit, stepCallbackPos map[token.Pos]bool) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		// Don't recurse into nested step callbacks; they are checked on their
		// own turn and have their own outer-scope boundary.
		if lit, ok := n.(*ast.FuncLit); ok && lit != fn {
			if stepCallbackPos[lit.Pos()] {
				return false
			}
		}

		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range stmt.Lhs {
				reportIfOuter(pass, lhs, fn)
			}
		case *ast.IncDecStmt:
			reportIfOuter(pass, stmt.X, fn)
		}
		return true
	})
}

// reportIfOuter reports expr if it is an identifier whose declaration
// lies outside fn (i.e., from a surrounding scope).
func reportIfOuter(pass *analysis.Pass, expr ast.Expr, fn *ast.FuncLit) {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	obj := pass.TypesInfo.Uses[id]
	if obj == nil {
		return
	}
	declPos := obj.Pos()
	if !declPos.IsValid() {
		return
	}
	// If the declaration position falls inside fn, it is local — no problem.
	if declPos >= fn.Pos() && declPos <= fn.End() {
		return
	}
	pass.Reportf(id.Pos(),
		"variable %q from outer scope modified inside a durable callback; skipped during replay",
		id.Name)
}

// resolveFunc extracts the function name and package path from a CallExpr,
// unwrapping generic type-argument syntax (Step[T] or Map[T, U]).
func resolveFunc(info *types.Info, call *ast.CallExpr) (funcName, pkgPath string, ok bool) {
	expr := call.Fun
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
