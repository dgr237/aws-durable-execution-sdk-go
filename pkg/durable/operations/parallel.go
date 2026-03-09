package operations

import (
	"fmt"
	"sync"

	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// ParallelRunner
// ---------------------------------------------------------------------------

type ParallelRunner[TOut any] struct {
	d              types.DurableContext
	name           string
	namePtr        *string
	branches       []func(ctx types.DurableContext) (TOut, error)
	serdes         types.Serdes
	maxConcurrency int
	outerStepID    string
}

func newParallelRunner[TOut any](
	d types.DurableContext,
	name string,
	branches []func(ctx types.DurableContext) (TOut, error),
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
	d types.DurableContext,
	name string,
	branches []func(ctx types.DurableContext) (TOut, error),
	opts ...ParallelOption[TOut],
) (types.BatchResult[TOut], error) {
	r := newParallelRunner[TOut](d, name, branches, opts)

	stored := r.d.GetStepData(r.outerStepID)

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
	if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	raw, err := utils.SafeDeserialize[[]TOut](r.serdes, resultPtr, r.outerStepID, r.d.DurableExecutionArn())
	if err != nil {
		return types.BatchResult[TOut]{}, err
	}
	batch := types.BatchResult[TOut]{}
	for i, v := range raw {
		batch.Items = append(batch.Items, types.BatchResultItem[TOut]{Value: v, Index: i})
	}
	return batch, nil
}

func (r *ParallelRunner[TOut]) replayFailed(stored *types.Operation) (types.BatchResult[TOut], error) {
	r.d.Logger().Info(fmt.Sprintf("Replaying parallel %s as FAILED", r.outerStepID))
	var cause error
	if stored.Error != nil {
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
	if len(pendingIndices) > 0 {
		r.executePendingBranches(pendingIndices, results, errs)
	}

	return r.finalize(results, errs)
}

// startFresh checkpoints the Parallel START, executes all branches, then finalizes.
func (r *ParallelRunner[TOut]) startFresh() (types.BatchResult[TOut], error) {
	_ = r.d.Checkpoint(r.outerStepID, types.OperationUpdate{
		Id:      r.outerStepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: subTypePtr(types.OperationSubTypeParallel),
		Name:    r.namePtr,
	})

	results, errs := r.executeBranches()
	return r.finalize(results, errs)
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

func (r *ParallelRunner[TOut]) executeBranches() ([]TOut, []error) {
	results := make([]TOut, len(r.branches))
	errs := make([]error, len(r.branches))
	allIndices := make([]int, len(r.branches))
	for i := range allIndices {
		allIndices[i] = i
	}
	r.executePendingBranches(allIndices, results, errs)
	return results, errs
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
			}
		default:
			pending = append(pending, i)
		}
	}
	return pending
}

// executePendingBranches runs a specific subset of branches in parallel.
func (r *ParallelRunner[TOut]) executePendingBranches(indices []int, results []TOut, errs []error) {
	if len(indices) == 0 {
		return
	}

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
			for i := range workCh {
				results[i], errs[i] = r.executeBranch(i)
			}
		}()
	}
	wg.Wait()
}

// executeBranch runs a single branch, checkpointing its START and SUCCEED/FAIL.
func (r *ParallelRunner[TOut]) executeBranch(i int) (TOut, error) {
	var zero TOut
	branchName := r.branchName(i)
	childCtx := r.d.NewChildDurableContext(branchName, branchName, r.d.Mode())

	_ = r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:      branchName,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeStep,
		SubType: subTypePtr(types.OperationSubTypeParallelBranch),
		// TODO: ParentId should be &r.outerStepID
	})

	result, err := r.branches[i](childCtx)
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
	serialized, _ := utils.SafeSerialize(r.serdes, result, branchName, r.d.DurableExecutionArn())
	var p *string
	if serialized != "" {
		p = &serialized
	}
	_ = r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:      branchName,
		Action:  types.OperationActionSucceed,
		Type:    types.OperationTypeStep,
		SubType: subTypePtr(types.OperationSubTypeParallelBranch),
		Payload: p,
		// TODO: ParentId should be &r.outerStepID
	})
}

func (r *ParallelRunner[TOut]) checkpointBranchFailed(branchName string, err error) {
	_ = r.d.Checkpoint(branchName, types.OperationUpdate{
		Id:      branchName,
		Action:  types.OperationActionFail,
		Type:    types.OperationTypeStep,
		SubType: subTypePtr(types.OperationSubTypeParallelBranch),
		Error:   utils.SafeStringify(err),
		// TODO: ParentId should be &r.outerStepID
	})
}

func (r *ParallelRunner[TOut]) finalize(results []TOut, errs []error) (types.BatchResult[TOut], error) {
	batchResult := types.BatchResult[TOut]{Items: make([]types.BatchResultItem[TOut], len(r.branches))}
	values := make([]TOut, len(r.branches))
	for i := range r.branches {
		batchResult.Items[i] = types.BatchResultItem[TOut]{Value: results[i], Err: errs[i], Index: i}
		values[i] = results[i]
	}

	serialized, _ := utils.SafeSerialize(r.serdes, values, r.outerStepID, r.d.DurableExecutionArn())
	var payloadPtr *string
	if serialized != "" {
		payloadPtr = &serialized
	}
	_ = r.d.Checkpoint(r.outerStepID, types.OperationUpdate{
		Id:      r.outerStepID,
		Action:  types.OperationActionSucceed,
		Type:    types.OperationTypeStep,
		SubType: subTypePtr(types.OperationSubTypeParallel),
		Name:    r.namePtr,
		Payload: payloadPtr,
	})

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
