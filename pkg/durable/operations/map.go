package operations

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	durableCtx "github.com/aws/durable-execution-sdk-go/pkg/durable/context"
	durableErrors "github.com/aws/durable-execution-sdk-go/pkg/durable/errors"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/types"
	"github.com/aws/durable-execution-sdk-go/pkg/durable/utils"
)

// ---------------------------------------------------------------------------
// MapRunner
// ---------------------------------------------------------------------------

type MapRunner[TIn, TOut any] struct {
	d                types.DurableContext
	name             string
	namePtr          *string
	items            []TIn
	mapFn            func(ctx context.Context, dc types.DurableContext, item TIn, index int, items []TIn) (TOut, error)
	serdes           types.Serdes
	maxConcurrency   int
	itemNamer        func(item TIn, index int) string
	outerStepID      string
	completionConfig *types.BatchCompletionConfig
}

func newMapRunner[TIn, TOut any](
	d types.DurableContext,
	name string,
	items []TIn,
	mapFn func(ctx context.Context, dc types.DurableContext, item TIn, index int, items []TIn) (TOut, error),
	opts []MapOption[TIn, TOut],
) *MapRunner[TIn, TOut] {
	r := &MapRunner[TIn, TOut]{
		d:           d,
		name:        name,
		namePtr:     stringPtr(name),
		items:       items,
		mapFn:       mapFn,
		serdes:      utils.DefaultSerdes,
		outerStepID: d.NextStepID(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// Map — public entry point
// ---------------------------------------------------------------------------

func Map[TIn, TOut any](
	dc types.DurableContext,
	name string,
	items []TIn,
	mapFn func(ctx context.Context, dc types.DurableContext, item TIn, index int, items []TIn) (TOut, error),
	opts ...MapOption[TIn, TOut],
) (types.BatchResult[TOut], error) {
	r := newMapRunner[TIn, TOut](dc, name, items, mapFn, opts)

	r.d.Logger().Info(fmt.Sprintf("Map function called for %s (stepID: %s)", r.name, r.outerStepID))

	stored := r.d.GetStepData(r.outerStepID)

	subType := types.OperationSubTypeMap
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
	case stored != nil && stored.Status == types.OperationStatusStarted:
		return r.resumeStarted()
	default:
		return r.startFresh()
	}
}

// ---------------------------------------------------------------------------
// Execution phases
// ---------------------------------------------------------------------------

func (r *MapRunner[TIn, TOut]) replaySucceeded(stored *types.Operation) (types.BatchResult[TOut], error) {
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
	batch := types.BatchResult[TOut]{}
	for i, v := range raw {
		batch.Items = append(batch.Items, types.BatchResultItem[TOut]{Value: v, Index: i})
	}
	return batch, nil
}

// resumeStarted handles a Map whose outer operation is already STARTED.
// It checks whether all iterations are done; if not, it suspends.
func (r *MapRunner[TIn, TOut]) resumeStarted() (types.BatchResult[TOut], error) {
	r.d.Logger().Info(fmt.Sprintf("Map %s already started, checking iteration status", r.outerStepID))

	if r.anyIterationsInProgress() {
		r.suspend()
	}

	results, errs := r.collectResultsFromInvokeChildren()
	reason := "ALL_SUCCEEDED"
	for _, e := range errs {
		if e != nil {
			reason = "COMPLETED_WITH_ERRORS"
			break
		}
	}
	return r.finalize(results, errs, reason)
}

// startFresh checkpoints the Map + all iteration STARTs, executes them, then finalizes.
func (r *MapRunner[TIn, TOut]) startFresh() (types.BatchResult[TOut], error) {
	results := make([]TOut, len(r.items))
	errs := make([]error, len(r.items))

	if err := r.checkpointMapStart(results, errs); err != nil {
		return types.BatchResult[TOut]{}, err
	}

	reason, err := r.executeIterations(results, errs)
	if err != nil {
		return types.BatchResult[TOut]{}, err
	}

	if r.anyIterationsInProgress() {
		r.suspend()
	}

	return r.finalize(results, errs, reason)
}

// ---------------------------------------------------------------------------
// Checkpointing
// ---------------------------------------------------------------------------

// checkpointMapStart sends the atomic batch: Map START + all new iteration STARTs.
// It also pre-populates results/errs for iterations that already have stored state.
func (r *MapRunner[TIn, TOut]) checkpointMapStart(results []TOut, errs []error) error {
	subType := types.OperationSubTypeMap
	iterSubType := types.OperationSubTypeMapIteration

	mapStartUpdate := types.OperationUpdate{
		Id:      r.outerStepID,
		Action:  types.OperationActionStart,
		Type:    types.OperationTypeContext,
		SubType: &subType,
		Name:    r.namePtr,
	}
	if r.d.ParentID() != "" {
		mapStartUpdate.ParentId = aws.String(r.d.ParentID())
	}

	batch := []types.OperationUpdate{mapStartUpdate}

	for i, item := range r.items {
		iterName := r.iterationName(i)
		iterStored := r.d.GetStepData(iterName)

		switch {
		case iterStored != nil && iterStored.Status == types.OperationStatusSucceeded:
			result, err := deserializeIterResult[TOut](r.d, r.serdes, iterStored, iterName)
			results[i], errs[i] = result, err
			continue
		case iterStored != nil && iterStored.Status == types.OperationStatusStarted:
			r.d.Logger().Info(fmt.Sprintf("Map iteration %s already started (exists in AWS), skipping checkpoint", iterName))
			continue
		case iterStored != nil && iterStored.Status == types.OperationStatusFailed:
			if iterStored.Error != nil {
				errs[i] = utils.ErrorFromErrorObject(iterStored.Error)
			} else {
				errs[i] = fmt.Errorf("map iteration %s failed with no error detail", iterName)
			}
			continue
		}

		outerStepIDCopy := r.outerStepID
		r.d.Logger().Info(fmt.Sprintf("Adding iteration %s START to batch with ParentId=%s", iterName, r.outerStepID))
		batch = append(batch, types.OperationUpdate{
			Id:       iterName,
			Action:   types.OperationActionStart,
			Type:     types.OperationTypeContext,
			SubType:  &iterSubType,
			Name:     r.itemName(item, i),
			ParentId: &outerStepIDCopy,
		})
	}

	r.d.Logger().Info(fmt.Sprintf("Sending atomic batch: Map START + %d iteration STARTs", len(batch)-1))
	if err := r.d.CheckpointBatch(batch); err != nil {
		return err
	}
	r.d.Logger().Info("Atomic batch committed, now executing map functions...")
	return nil
}

// checkpointIterationSuccess records a successful iteration result.
// Returns an error if serialization or the checkpoint call fails.
func (r *MapRunner[TIn, TOut]) checkpointIterationSuccess(iterName string, result TOut, item TIn, i int) error {
	iterSubType := types.OperationSubTypeMapIteration
	serialized, serErr := utils.SafeSerialize[TOut](r.serdes, result, iterName, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize map iteration result for %s: %v", iterName, serErr),
		})
		return &durableErrors.SerdesFailedError{Message: serErr.Error()}
	}
	var p *string
	if serialized != "" {
		p = &serialized
	}
	outerStepID := r.outerStepID
	return r.d.Checkpoint(iterName, types.OperationUpdate{
		Id:       iterName,
		Action:   types.OperationActionSucceed,
		Type:     types.OperationTypeContext,
		SubType:  &iterSubType,
		Name:     r.itemName(item, i),
		Payload:  p,
		ParentId: &outerStepID,
	})
}

// ---------------------------------------------------------------------------
// Execution
// ---------------------------------------------------------------------------

// executeIterations runs the mapFn for each pending iteration in parallel.
// Returns the completion reason and the first fatal (non-termination) error encountered.
func (r *MapRunner[TIn, TOut]) executeIterations(results []TOut, errs []error) (string, error) {
	workCh := r.pendingIterationsChannel()

	concurrency := r.maxConcurrency
	if concurrency <= 0 || concurrency > len(r.items) {
		concurrency = len(r.items)
	}

	tracker := newCompletionTracker(len(r.items), r.completionConfig)
	fatalErrCh := make(chan error, concurrency)

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
					item := r.items[i]
					iterName := r.iterationName(i)
					childDc := r.d.NewChildDurableContext(r.d.Context(), iterName, iterName, r.d.Mode())

					result, err := r.mapFn(childDc.Context(), childDc, item, i, r.items)
					errs[i] = err
					if err == nil {
						results[i] = result
						if checkpointErr := r.checkpointIterationSuccess(iterName, result, item, i); checkpointErr != nil {
							errs[i] = checkpointErr
							fatalErrCh <- checkpointErr
						}
					} else {
						var terminatedError *durableErrors.TerminatedError
						if errors.As(err, &terminatedError) {
							r.d.Logger().Info(fmt.Sprintf("Map iteration %s suspended due to checkpoint", iterName))
						} else {
							fatalErrCh <- err
						}
					}
					if tracker.Record(errs[i]) {
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(fatalErrCh)

	for err := range fatalErrCh {
		if err != nil {
			return "", err
		}
	}
	return tracker.CompletionReason(), nil
}

// ---------------------------------------------------------------------------
// Result collection
// ---------------------------------------------------------------------------

// collectResultsFromInvokeChildren reads results/errors from the ChainedInvoke child
// operations (stepID-iteration-N-1) when the Map was already started.
func (r *MapRunner[TIn, TOut]) collectResultsFromInvokeChildren() ([]TOut, []error) {
	count := len(r.items)
	results := make([]TOut, count)
	errs := make([]error, count)

	for i := range count {
		invokeID := r.invokeChildID(i)
		invokeStored := r.d.GetStepData(invokeID)
		if invokeStored == nil {
			continue
		}
		switch invokeStored.Status {
		case types.OperationStatusSucceeded:
			result, err := deserializeInvokeResult[TOut](r.d, r.serdes, invokeStored, invokeID)
			results[i], errs[i] = result, err
		case types.OperationStatusFailed:
			errs[i] = errorFromInvokeStored(invokeStored)
		}
	}

	r.d.Logger().Info("Map already started, iterations already exist in AWS, skipping iteration checkpoints")
	return results, errs
}

// finalize checkpoints the outer Map as SUCCEEDED and returns the BatchResult.
func (r *MapRunner[TIn, TOut]) finalize(results []TOut, errs []error, reason string) (types.BatchResult[TOut], error) {
	subType := types.OperationSubTypeMap

	batchResult := types.BatchResult[TOut]{
		Items:            make([]types.BatchResultItem[TOut], len(r.items)),
		CompletionReason: reason,
	}
	values := make([]TOut, len(r.items))
	for i := range r.items {
		batchResult.Items[i] = types.BatchResultItem[TOut]{Value: results[i], Err: errs[i], Index: i}
		values[i] = results[i]
	}

	serialized, serErr := utils.SafeSerialize[[]TOut](r.serdes, values, r.outerStepID, r.d.DurableExecutionArn())
	if serErr != nil {
		r.d.Terminate(types.TerminationResult{
			Reason:  types.TerminationReasonSerdesFailed,
			Message: fmt.Sprintf("failed to serialize map result for %s: %v", r.outerStepID, serErr),
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
		SubType: &subType,
		Name:    r.namePtr,
		Payload: payloadPtr,
	}); err != nil {
		return types.BatchResult[TOut]{}, err
	}

	return batchResult, nil
}

// ---------------------------------------------------------------------------
// State inspection helpers
// ---------------------------------------------------------------------------

func (r *MapRunner[TIn, TOut]) anyIterationsInProgress() bool {
	for i := range r.items {
		invokeStored := r.d.GetStepData(r.invokeChildID(i))
		if invokeStored == nil || invokeStored.Status == types.OperationStatusStarted {
			return true
		}
	}
	return false
}

func (r *MapRunner[TIn, TOut]) pendingIterationsChannel() <-chan int {
	ch := make(chan int, len(r.items))
	for i := range r.items {
		invokeStored := r.d.GetStepData(r.invokeChildID(i))
		if invokeStored == nil ||
			(invokeStored.Status != types.OperationStatusSucceeded &&
				invokeStored.Status != types.OperationStatusFailed) {
			ch <- i
		}
	}
	close(ch)
	return ch
}

func (r *MapRunner[TIn, TOut]) suspend() {
	r.d.Logger().Info(fmt.Sprintf("Map %s has iterations still in progress, suspending", r.outerStepID))
	r.d.Terminate(types.TerminationResult{
		Reason:  types.TerminationReasonCheckpointTerminating,
		Message: fmt.Sprintf("waiting for map %s iterations to complete", r.name),
	})
	select {}
}

// ---------------------------------------------------------------------------
// ID / name helpers
// ---------------------------------------------------------------------------

func (r *MapRunner[TIn, TOut]) iterationName(i int) string {
	return fmt.Sprintf("%s-iteration-%d", r.outerStepID, i)
}

func (r *MapRunner[TIn, TOut]) invokeChildID(i int) string {
	return fmt.Sprintf("%s-iteration-%d-1", r.outerStepID, i)
}

func (r *MapRunner[TIn, TOut]) itemName(item TIn, i int) *string {
	if r.itemNamer != nil {
		n := r.itemNamer(item, i)
		return &n
	}
	return nil
}

// ---------------------------------------------------------------------------
// Package-level helpers
// ---------------------------------------------------------------------------

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deserializeIterResult[TOut any](
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

func deserializeInvokeResult[TOut any](
	d types.DurableContext,
	serdes types.Serdes,
	stored *types.Operation,
	id string,
) (TOut, error) {
	var resultPtr *string
	if stored.ChainedInvokeDetails != nil {
		resultPtr = stored.ChainedInvokeDetails.Result
	} else if stored.StepDetails != nil {
		resultPtr = stored.StepDetails.Result
	}
	return utils.SafeDeserialize[TOut](serdes, resultPtr, id, d.DurableExecutionArn())
}

func errorFromInvokeStored(stored *types.Operation) error {
	if stored.ChainedInvokeDetails != nil && stored.ChainedInvokeDetails.Error != nil {
		return utils.ErrorFromErrorObject(stored.ChainedInvokeDetails.Error)
	}
	if stored.Error != nil {
		return utils.ErrorFromErrorObject(stored.Error)
	}
	return nil
}
