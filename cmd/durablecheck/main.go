// Command durablecheck is a standalone vet tool that bundles all AWS Durable
// Execution SDK static analysis passes.
//
// Install:
//
//	go install github.com/aws/durable-execution-sdk-go/cmd/durablecheck@latest
//
// Run via go vet:
//
//	go vet -vettool=$(which durablecheck) ./...
//
// Or invoke directly:
//
//	durablecheck ./...
//
// Passes included:
//
//   - durableNonDeterministic  — reports time.Now, rand.*, etc. outside Step callbacks
//   - durableClosureMutation   — reports outer-variable mutations inside Step callbacks
package main

import (
	"github.com/aws/durable-execution-sdk-go/pkg/durable/analysis/closuremutation"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/analysis/nondeterministic"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	multichecker.Main(
		nondeterministic.Analyzer,
		closuremutation.Analyzer,
	)
}
