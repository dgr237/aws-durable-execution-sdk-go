package closuremutation_test

import (
	"testing"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/analysis/closuremutation"
	"golang.org/x/tools/go/analysis/analysistest"
)

func TestClosureMutation(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), closuremutation.Analyzer, "cm")
}
