package durabletest

import "github.com/aws/durable-execution-sdk-go/pkg/durable/types"

// DurableOperation provides read access to a checkpointed operation and
// interaction methods for callbacks.
// All accessors return a fresh snapshot so they reflect the latest state.
type DurableOperation struct {
	hashedID string
	client   *testClient
}

func newDurableOperation(hashedID string, client *testClient) *DurableOperation {
	return &DurableOperation{hashedID: hashedID, client: client}
}

func (op *DurableOperation) snapshot() *types.Operation {
	op.client.mu.Lock()
	defer op.client.mu.Unlock()
	if o, ok := op.client.opsMap[op.hashedID]; ok {
		cp := *o
		return &cp
	}
	return nil
}

// ID returns the hashed operation ID as stored by the checkpoint backend.
func (op *DurableOperation) ID() string { return op.hashedID }

// Name returns the operation name, or empty string if unset.
func (op *DurableOperation) Name() string {
	if o := op.snapshot(); o != nil && o.Name != nil {
		return *o.Name
	}
	return ""
}

// Type returns the operation type (STEP, WAIT, CALLBACK, CONTEXT, …).
func (op *DurableOperation) Type() types.OperationType {
	if o := op.snapshot(); o != nil {
		return o.Type
	}
	return ""
}

// SubType returns the operation sub-type, or nil if unset.
func (op *DurableOperation) SubType() *types.OperationSubType {
	if o := op.snapshot(); o != nil {
		return o.SubType
	}
	return nil
}

// Status returns the current operation status.
func (op *DurableOperation) Status() types.OperationStatus {
	if o := op.snapshot(); o != nil {
		return o.Status
	}
	return ""
}

// StepDetails returns step result details, or nil if not applicable.
func (op *DurableOperation) StepDetails() *types.StepDetails {
	if o := op.snapshot(); o != nil {
		return o.StepDetails
	}
	return nil
}

// ContextDetails returns context result details, or nil if not applicable.
func (op *DurableOperation) ContextDetails() *types.ContextDetails {
	if o := op.snapshot(); o != nil {
		return o.ContextDetails
	}
	return nil
}

// CallbackDetails returns callback result details, or nil if not applicable.
func (op *DurableOperation) CallbackDetails() *types.CallbackDetails {
	if o := op.snapshot(); o != nil {
		return o.CallbackDetails
	}
	return nil
}

// WaitDetails returns wait scheduling details, or nil if not a wait operation.
func (op *DurableOperation) WaitDetails() *types.WaitDetails {
	if o := op.snapshot(); o != nil {
		return o.WaitDetails
	}
	return nil
}

// IsWaitForCallback returns true if this is a WaitForCallback operation.
func (op *DurableOperation) IsWaitForCallback() bool {
	st := op.SubType()
	return st != nil && *st == types.OperationSubTypeWaitForCallback
}

// IsWaitForCondition returns true if this is a WaitForCondition operation.
func (op *DurableOperation) IsWaitForCondition() bool {
	st := op.SubType()
	return st != nil && *st == types.OperationSubTypeWaitForCondition
}

// SendCallbackSuccess signals that the external system completed the callback
// successfully with the given result payload (JSON string).
// Calling this unblocks the orchestrator and triggers re-invocation of the handler.
func (op *DurableOperation) SendCallbackSuccess(result string) {
	op.client.completeCallback(op.hashedID, &result, nil)
}

// SendCallbackFailure signals that the external system failed the callback.
func (op *DurableOperation) SendCallbackFailure(errType, errMessage string) {
	op.client.completeCallback(op.hashedID, nil, &types.ErrorObject{
		ErrorType:    errType,
		ErrorMessage: errMessage,
	})
}
