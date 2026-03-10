package operations

import (
	"sync"

	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
)

// completionTracker monitors batch execution progress and signals early
// termination when a BatchCompletionConfig policy is satisfied.
// It is safe to call Record concurrently from multiple goroutines.
type completionTracker struct {
	mu        sync.Mutex
	succeeded int
	failed    int
	total     int
	config    *types.BatchCompletionConfig
	doneCh    chan struct{}
	once      sync.Once
	Reason    string // set when a policy fires; empty if no early stop
}

func newCompletionTracker(total int, cfg *types.BatchCompletionConfig) *completionTracker {
	return &completionTracker{
		total:  total,
		config: cfg,
		doneCh: make(chan struct{}),
	}
}

// Record notes one completion (success when err==nil, failure otherwise).
// Returns true and closes Done() if a completion policy is now satisfied.
func (t *completionTracker) Record(err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.succeeded++
	} else {
		t.failed++
	}
	return t.checkPolicies()
}

// Done returns a channel that is closed the moment a completion policy fires.
// Workers should select on this alongside their work channel to exit early.
func (t *completionTracker) Done() <-chan struct{} { return t.doneCh }

// CompletionReason returns the string reason appropriate for BatchResult.CompletionReason.
// Call this after all workers have exited.
func (t *completionTracker) CompletionReason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Reason != "" {
		return t.Reason
	}
	if t.failed > 0 {
		return "COMPLETED_WITH_ERRORS"
	}
	return "ALL_SUCCEEDED"
}

func (t *completionTracker) checkPolicies() bool {
	if t.config == nil {
		return false
	}
	if t.config.MinSuccessful != nil && t.succeeded >= *t.config.MinSuccessful {
		t.signal("MIN_SUCCESSFUL_MET")
		return true
	}
	if t.config.ToleratedFailureCount != nil && t.failed > *t.config.ToleratedFailureCount {
		t.signal("MAX_FAILURES_REACHED")
		return true
	}
	if t.config.ToleratedFailurePercentage != nil && t.total > 0 {
		pct := float64(t.failed) / float64(t.total) * 100
		if pct > *t.config.ToleratedFailurePercentage {
			t.signal("MAX_FAILURE_PERCENTAGE_REACHED")
			return true
		}
	}
	return false
}

func (t *completionTracker) signal(reason string) {
	t.once.Do(func() {
		t.Reason = reason
		close(t.doneCh)
	})
}
