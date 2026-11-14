// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
)

var (
	requestIDCounterTotal = dex.DefineAttribute[int]("request-id-counter-total")
	requestIDCounterHold  = dex.DefineChannel[dex.None]("request-id-counter-hold")
)

// requestIDHandlerRuns counts counter RPC handler runs per Flow ID; the Worker runs in this process.
var requestIDHandlerRuns sync.Map

// requestIDCounterFlow keeps a counter that RPCs increment while its Step waits indefinitely.
type requestIDCounterFlow struct {
	dex.FlowDefaults
}

func (requestIDCounterFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{dex.DefineStartStep(requestIDCounterHoldStep{})}
}

func (flow requestIDCounterFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.IncrementCounter, &dex.RPCOptions{IsTransactional: true}),
		dex.DefineRPC(flow.IncrementCounterWithoutTransaction, nil),
		dex.DefineRPC(flow.GetCounterTotal, nil),
	}
}

func (requestIDCounterFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{requestIDCounterTotal},
		Channels:   []dex.ChannelDef{requestIDCounterHold},
	}
}

func (requestIDCounterFlow) IncrementCounter(ctx dex.Context, delta int) (*dex.RPCResult[int], error) {
	recordRequestIDHandlerRun(ctx.FlowID())
	return addToRequestIDCounter(ctx, delta)
}

func (requestIDCounterFlow) IncrementCounterWithoutTransaction(
	ctx dex.Context,
	delta int,
) (*dex.RPCResult[int], error) {
	recordRequestIDHandlerRun(ctx.FlowID())
	return addToRequestIDCounter(ctx, delta)
}

func (requestIDCounterFlow) GetCounterTotal(ctx dex.Context, _ dex.None) (*dex.RPCResult[int], error) {
	total, err := requestIDCounterTotal.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: total}, nil
}

type requestIDCounterHoldStep struct {
	dex.StepDefaults
}

func (requestIDCounterHoldStep) WaitFor(dex.Context, dex.None) (*dex.Wait, error) {
	return dex.AllOf(requestIDCounterHold.ForOne()), nil
}

func (requestIDCounterHoldStep) Execute(dex.Context, dex.None) (*dex.StepDecision, error) {
	return dex.GracefulComplete(nil), nil
}

// TestRPCRequestIDRetryAppliesTransactionalRPCOnce shows a stable request ID makes a retry idempotent.
func TestRPCRequestIDRetryAppliesTransactionalRPCOnce(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)
	requestID := "event-" + uuid.NewString() + "/IncrementCounter"

	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, &requestID))
	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, &requestID))

	require.Equal(t, 5, readRequestIDCounterTotal(t, flowID))
	// A recorded request ID is answered before dispatch, so the handler ran once.
	require.Equal(t, int32(1), requestIDHandlerRunCount(flowID))
}

// TestRPCRequestIDReusedForDifferentInputFails shows the Server rejects a changed request under a recorded ID.
func TestRPCRequestIDReusedForDifferentInputFails(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)
	requestID := "event-" + uuid.NewString()

	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, &requestID))
	var total int
	err := integClient.InvokeRPCWithOptions(
		integrationContext(t),
		flowID,
		flow.IncrementCounter,
		7,
		&total,
		dex.RPCInvokeOptions{RequestID: &requestID},
	)
	var serviceError *dex.ServiceError
	require.ErrorAs(t, err, &serviceError)
	require.Equal(t, codes.InvalidArgument, serviceError.Code)
	require.Equal(t, dex.ErrorSubStatusUncategorized, serviceError.SubStatus)
	require.Contains(t, serviceError.Detail, "was already used for a different request")

	require.Equal(t, 5, readRequestIDCounterTotal(t, flowID))
}

// TestRPCWithoutRequestIDAppliesEveryCall shows the generated default keeps every call a new request.
func TestRPCWithoutRequestIDAppliesEveryCall(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)

	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, nil))
	require.Equal(t, 10, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, nil))

	require.Equal(t, 10, readRequestIDCounterTotal(t, flowID))
	require.Equal(t, int32(2), requestIDHandlerRunCount(flowID))
}

// TestRPCRequestIDRetryCommitsNonTransactionalRPCOnce shows a non-transactional retry reruns the handler but commits once.
func TestRPCRequestIDRetryCommitsNonTransactionalRPCOnce(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)
	requestID := "event-" + uuid.NewString()

	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounterWithoutTransaction, 5, &requestID))
	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounterWithoutTransaction, 5, &requestID))

	require.Equal(t, 5, readRequestIDCounterTotal(t, flowID))
	require.Equal(t, int32(2), requestIDHandlerRunCount(flowID))
}

// TestRPCRequestIDReplaysRecordedReplyAfterFlowCloses shows a late retry still gets its reply after the Flow closed.
func TestRPCRequestIDReplaysRecordedReplyAfterFlowCloses(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)
	requestID := "event-" + uuid.NewString()
	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, &requestID))

	require.NoError(t, integClient.StopFlow(
		integrationContext(t),
		flowID,
		dex.StopOptions{Type: dex.TerminateFlow, Reason: "close before the retry"},
	))
	result := waitForUncompletedFlow(t, flowID, false)
	require.Equal(t, dex.FlowTerminated, result.Status)

	require.Equal(t, 5, invokeRequestIDCounter(t, flowID, flow.IncrementCounter, 5, &requestID))
	require.Equal(t, int32(1), requestIDHandlerRunCount(flowID))

	newRequestID := "event-" + uuid.NewString()
	var total int
	err := integClient.InvokeRPCWithOptions(
		integrationContext(t),
		flowID,
		flow.IncrementCounter,
		5,
		&total,
		dex.RPCInvokeOptions{RequestID: &newRequestID},
	)
	var inactive *dex.FlowNotActiveOrNotFoundError
	require.ErrorAs(t, err, &inactive)
}

// TestRPCRequestIDRejectsEmptyValue shows an empty override fails before the request is sent.
func TestRPCRequestIDRejectsEmptyValue(t *testing.T) {
	flow := requestIDCounterFlow{}
	flowID := startRequestIDCounterFlow(t)
	emptyRequestID := ""
	var total int
	err := integClient.InvokeRPCWithOptions(
		integrationContext(t),
		flowID,
		flow.IncrementCounter,
		5,
		&total,
		dex.RPCInvokeOptions{RequestID: &emptyRequestID},
	)
	require.EqualError(t, err, "dex: InvokeRPC request ID must not be empty")
	require.Equal(t, int32(0), requestIDHandlerRunCount(flowID))
}

func startRequestIDCounterFlow(t *testing.T) string {
	t.Helper()
	flowID := newFlowID(t, "rpc-request-id")
	initialTotal, err := dex.InitialAttribute(requestIDCounterTotal, 0)
	require.NoError(t, err)
	_, err = integClient.StartFlow(
		integrationContext(t),
		requestIDCounterFlow{},
		flowID,
		nil,
		dex.StartFlowOptions{Attributes: []dex.InitialAttributeDef{initialTotal}},
	)
	require.NoError(t, err)
	return flowID
}

func invokeRequestIDCounter(
	t *testing.T,
	flowID string,
	rpc any,
	delta int,
	requestID *string,
) int {
	t.Helper()
	var total int
	require.NoError(t, integClient.InvokeRPCWithOptions(
		integrationContext(t),
		flowID,
		rpc,
		delta,
		&total,
		dex.RPCInvokeOptions{RequestID: requestID},
	))
	return total
}

func readRequestIDCounterTotal(t *testing.T, flowID string) int {
	t.Helper()
	var total int
	require.NoError(t, integClient.InvokeRPC(
		integrationContext(t),
		flowID,
		requestIDCounterFlow{}.GetCounterTotal,
		nil,
		&total,
	))
	return total
}

func addToRequestIDCounter(ctx dex.Context, delta int) (*dex.RPCResult[int], error) {
	total, err := requestIDCounterTotal.Get(ctx)
	if err != nil {
		return nil, err
	}
	total += delta
	if err := requestIDCounterTotal.Set(ctx, total); err != nil {
		return nil, err
	}
	return &dex.RPCResult[int]{Output: total}, nil
}

func recordRequestIDHandlerRun(flowID string) {
	runs, _ := requestIDHandlerRuns.LoadOrStore(flowID, new(atomic.Int32))
	runs.(*atomic.Int32).Add(1)
}

func requestIDHandlerRunCount(flowID string) int32 {
	runs, isRecorded := requestIDHandlerRuns.Load(flowID)
	if !isRecorded {
		return 0
	}
	return runs.(*atomic.Int32).Load()
}
