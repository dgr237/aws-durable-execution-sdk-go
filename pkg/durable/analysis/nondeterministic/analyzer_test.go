package nondeterministic_test

import (
	"testing"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/analysis/nondeterministic"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestNonDeterministic(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), nondeterministic.Analyzer, "nd")
}
