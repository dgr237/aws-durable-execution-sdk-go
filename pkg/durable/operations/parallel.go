package operations

import (
	"context"
	"fmt"
	"sync"

	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// ParallelRunner
// ---------------------------------------------------------------------------

type ParallelRunner[TOut any] struct {
	d                types.DurableContext
	name             string
	namePtr          *string
	branches         []func(ctx context.Context, dc types.DurableContext) (TOut, error)
	serdes           types.Serdes
	maxConcurrency   int
	completionConfig *types.BatchCompletionConfig
	outerStepID      string
}

func newParallelRunner[TOut any](
	d types.DurableContext,
	name string,
	branches []func(ctx context.Context, dc types.DurableContext) (TOut, error),
	opts []ParallelOption[TOut],
) *ParallelRunner[TOut] {
	r := &ParallelRunner[TOut]{
		d:           d,
		name:        name,
		namePtr:     stringPtr(name),
		branches:    branches,
		serdes:      utils.DefaultSerdes,
		outerStepID: d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// Parallel — public entry point
// ---------------------------------------------------------------------------

func Parallel[TOut any](
	dc types.DurableContext,
	name string,
	branches []func(ctx context.Context, dc types.DurableContext) (TOut, error),
	opts ...ParallelOption[TOut],
) (types.BatchResult[TOut], error) {
	r := newParallelRunner[TOut](dc, name, branches, opts)

	stored := r.d.GetStepData(r.outerStepID)

	subType := types.OperationSubTypeParallel
	if err := durableCtx.ValidateReplayConsistency(r.outerStepID, types.OperationTypeContext, r.namePtr, &subType, stored); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonContextValidationError,
			Error:   err,
			Message: err.Error(),
		})
		return types.BatchResult[TOut]{}, err
	}

	switch {
	case stored != nil && stored.Status == types.OperationStatusSucceeded:
		return r.replaySucceeded(stored)
	case stored != nil && stored.Status == types.OperationStatusFailed:
		return r.replayFailed(stored)
	case stored != nil && stored.Status == types.OperationStatusStarted:
		return r.resumeStarted()
	default:
		return r.startFresh()
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *ParallelRunner[TOut]) replaySucceeded(stored *types.Operation) (types.BatchResult[TOut], error) {
	var resultPtr *string
	if stored.ContextDetails != nil {
		resultPtr = stored.ContextDetails.Result
	} else if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	raw, err := utils.SafeDeserialize[[]TOut](r.serdes, resultPtr, r.outerStepID, r.d.DurableExecutionArn())
	if err != nil {
		return types.BatchResult[TOut]{}, err
	}
	batch := types.BatchResult[TOut]{CompletionReason: "ALL_SUCCEEDED"}
	for i, v := range raw {
		batch.Items = append(batch.Items, types.BatchResultItem[TOut]{Value: v, Index: i})
	}
	return batch, nil
}

func (r *ParallelRunner[TOut]) replayFailed(stored *types.Operation) (types.BatchResult[TOut], error) {
	r.d.Logger().Info(fmt.Sprintf("Replaying parallel %s as FAILED", r.outerStepID))
	var cause error
	if stored.ContextDetails != nil && stored.ContextDetails.Error != nil {
		cause = utils.ErrorFromErrorObject(stored.ContextDetails.Error)
	} else if stored.Error != nil {
		cause = utils.ErrorFromErrorObject(stored.Error)
	}
	return types.BatchResult[TOut]{}, durableErrors.NewParallelError(r.outerStepID, r.namePtr, cause)
}

// resumeStarted handles a Parallel whose outer operation is already STARTED.
// Branches that already have a stored terminal state are collected directly;
// any that are still STARTED or missing are re-executed, then the whole
// batch is finalized.
func (r *ParallelRunner[TOut]) resumeStarted() (types.BatchResult[TOut], error) {
	r.d.Logger().Info(fmt.Sprintf("Parallel %s already started, checking branch status", r.outerStepID))

	results := make([]TOut, len(r.branches))
	errs := make([]error, len(r.branches))

	pendingIndices := r.collectCompletedBranches(results, errs)
	reason := "ALL_SUCCEEDED"
	if len(pendingIndices) > 0 {
		reason = r.executePendingBranches(pendingIndices, results, errs)
	} else {
		for _, e := range errs {
			if e != nil {
				reason = "COMPLETED_WITH_ERRORS"
				break
			}
		}
	}

	return r.finalize(results, errs, reason)
}

// startFresh checkpoints the Parallel START, executes all branches, then finalizes.
func (r *ParallelRunner[TOut]) startFresh() (types.BatchResult[TOut], error) {
	if err := r.d.Checkpoint(r.outerStepID, types.OperationUpdate{
		Id:      r.outerStepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeContext,
		SubType: subTypePtr(types.OperationSubTypeParallel),
		Name:    r.namePtr,
	}); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   err,
			Message: fmt.Sprintf("failed to checkpoint START for parallel %s: %v", r.outerStepID, err),
		})
		return types.BatchResult[TOut]{}, err
	}

	results, errs, reason := r.executeBranches()
	return r.finalize(results, errs, reason)
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

func (r *ParallelRunner[TOut]) executeBranches() ([]TOut, []error, string) {
	results := make([]TOut, len(r.branches))
	errs := make([]error, len(r.branches))
	allIndices := make([]int, len(r.branches))
	for i := range allIndices {
		allIndices[i] = i
	}
	reason := r.executePendingBranches(allIndices, results, errs)
	return results, errs, reason
}

// collectCompletedBranches pre-populates results/errs from already-terminal branch
// steps and returns the indices of branches that still need execution.
func (r *ParallelRunner[TOut]) collectCompletedBranches(results []TOut, errs []error) []int {
	var pending []int
	for i := range r.branches {
		branchName := r.branchName(i)
		stored := r.d.GetStepData(branchName)
		switch {
		case stored != nil && stored.Status == types.OperationStatusSucceeded:
			r.d.Logger().Info(fmt.Sprintf("Parallel branch %s already succeeded, skipping execution", branchName))
			result, err := deserializeBranchResult[TOut](r.d, r.serdes, stored, branchName)
			results[i], errs[i] = result, err
		case stored != nil && stored.Status == types.OperationStatusFailed:
			r.d.Logger().Info(fmt.Sprintf("Parallel branch %s already failed, skipping execution", branchName))
			if stored.Error != nil {
				errs[i] = utils.ErrorFromErrorObject(stored.Error)
			} else {
				errs[i] = fmt.Errorf("parallel branch %s failed with no error detail", branchName)
			}
		default:
			pending = append(pending, i)
		}
	}
	return pending
}

// executePendingBranches runs a specific subset of branches in parallel,
// honouring the completionConfig policy for early termination.
// Returns the CompletionReason string.
func (r *ParallelRunner[TOut]) executePendingBranches(indices []int, results []TOut, errs []error) string {
	if len(indices) == 0 {
		return "ALL_SUCCEEDED"
	}

	tracker := newCompletionTracker(len(r.branches), r.completionConfig)

	workCh := make(chan int, len(indices))
	for _, i := range indices {
		workCh <- i
	}
	close(workCh)

	concurrency := r.maxConcurrency
	if concurrency <= 0 || concurrency > len(indices) {
		concurrency = len(indices)
	}

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-tracker.Done():
					return
				case i, ok := <-workCh:
					if !ok {
						return
					}
					results[i], errs[i] = r.executeBranch(i)
					if tracker.Record(errs[i]) {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	return tracker.CompletionReason()
}

// executeBranch runs a single branch, checkpointing its START and SUCCEED/FAIL.
func (r *ParallelRunner[TOut]) executeBranch(i int) (TOut, error) {
	var zero TOut
	branchName := r.branchName(i)
	childDc := r.d.NewChildDurableContext(r.d.Context(), branchName, branchName, r.d.Mode())

	outerID := r.outerStepID
	if err := r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:       branchName,
		Action:   types.OperationActionStart,
		Type:     types.OperationTypeContext,
		SubType:  subTypePtr(types.OperationSubTypeParallelBranch),
		ParentId: &outerID,
	}); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   err,
			Message: fmt.Sprintf("failed to checkpoint START for branch %s: %v", branchName, err),
		})
		return zero, err
	}

	result, err := r.branches[i](childDc.Context(), childDc)
	if err != nil {
		r.checkpointBranchFailed(branchName, err)
		return zero, err
	}

	r.checkpointBranchSucceeded(branchName, result)
	return result, nil
}

// ---------------------------------------------------------------------------
// Checkpointing
// ---------------------------------------------------------------------------

func (r *ParallelRunner[TOut]) checkpointBranchSucceeded(branchName string, result TOut) {
	serialized, serErr := utils.SafeSerialize(r.serdes, result, branchName, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize result for branch %s: %v", branchName, serErr),
		})
		return
	}
	var p *string
	if serialized != "" {
		p = &serialized
	}
	outerID := r.outerStepID
	if err := r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:       branchName,
		Action:   types.OperationActionSucceed,
		Type:     types.OperationTypeContext,
		SubType:  subTypePtr(types.OperationSubTypeParallelBranch),
		Payload:  p,
		ParentId: &outerID,
	}); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   err,
			Message: fmt.Sprintf("failed to checkpoint SUCCEED for branch %s: %v", branchName, err),
		})
	}
}

func (r *ParallelRunner[TOut]) checkpointBranchFailed(branchName string, err error) {
	outerID := r.outerStepID
	if cpErr := r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:       branchName,
		Action:   types.OperationActionFail,
		Type:     types.OperationTypeContext,
		SubType:  subTypePtr(types.OperationSubTypeParallelBranch),
		Error:    utils.SafeStringify(err),
		ParentId: &outerID,
	}); cpErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   cpErr,
			Message: fmt.Sprintf("failed to checkpoint FAIL for branch %s: %v", branchName, cpErr),
		})
	}
}

func (r *ParallelRunner[TOut]) finalize(results []TOut, errs []error, reason string) (types.BatchResult[TOut], error) {
	batchResult := types.BatchResult[TOut]{
		Items:            make([]types.BatchResultItem[TOut], len(r.branches)),
		CompletionReason: reason,
	}
	values := make([]TOut, len(r.branches))
	for i := range r.branches {
		batchResult.Items[i] = types.BatchResultItem[TOut]{Value: results[i], Err: errs[i], Index: i}
		values[i] = results[i]
	}

	serialized, serErr := utils.SafeSerialize(r.serdes, values, r.outerStepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize parallel result for %s: %v", r.outerStepID, serErr),
		})
		return types.BatchResult[TOut]{}, &durableErrors.SerdesFailedError{Message: serErr.Error()}
	}
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}
	if err := r.d.Checkpoint(r.outerStepID, types.OperationUpdate{
		Id:      r.outerStepID,
		Action:  types.OperationActionSucceed,
		Type:    types.OperationTypeContext,
		SubType: subTypePtr(types.OperationSubTypeParallel),
		Name:    r.namePtr,
		Payload: payloadPtr,
	}); err != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonCheckpointFailed,
			Error:   err,
			Message: fmt.Sprintf("failed to checkpoint SUCCEED for parallel %s: %v", r.outerStepID, err),
		})
		return types.BatchResult[TOut]{}, err
	}

	return batchResult, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (r *ParallelRunner[TOut]) branchName(i int) string {
	return fmt.Sprintf("%s-branch-%d", r.outerStepID, i)
}

func deserializeBranchResult[TOut any](
	d types.DurableContext,
	serdes types.Serdes,
	stored *types.Operation,
	id string,
) (TOut, error) {
	var resultPtr *string
	if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	return utils.SafeDeserialize[TOut](serdes, resultPtr, id, d.DurableExecutionArn())
}
