// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/request_id_dedup"
	"google.golang.org/protobuf/proto"
)

// requestIDDedupEnvironment shares one server and Worker across the request ID subtests.
type requestIDDedupEnvironment struct {
	runtime      *integRuntime
	handler      *request_id_dedup.Handler
	workerTarget *dexpb.WorkerTarget
}

// TestRequestIDDedupComparesOnlyTheRequestID checks that a repeated request ID returns the first
// reply and never applies the repeat, whatever the repeat contains.
func TestRequestIDDedupComparesOnlyTheRequestID(t *testing.T) {
	handler := request_id_dedup.NewHandler()
	environment := &requestIDDedupEnvironment{
		handler:      handler,
		workerTarget: startWorker(t, handler),
		runtime:      startDexService(t, serverZDTestConfig{}),
	}
	t.Run("SetAttributes", environment.testSetAttributesRequestIDReuse)
	t.Run("DeleteChannelMessage", environment.testDeleteChannelMessageRequestIDReuse)
	t.Run("InvokeRPC", environment.testInvokeRPCRequestIDReuse)
	t.Run("concurrent InvokeRPC calls", environment.testConcurrentInvokeRPCRequestIDReuse)
}

func (environment *requestIDDedupEnvironment) testSetAttributesRequestIDReuse(t *testing.T) {
	ctx, flowID := environment.startFlow(t, "set-attributes")
	flowClient := environment.runtime.FlowClient
	requestID := newRequestID()

	_, err := flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		FlowId:     flowID,
		RequestId:  requestID,
		Attributes: []*dexpb.AttributeWrite{{Key: "status", Value: stringValue("first")}},
	})
	require.NoError(t, err)
	_, err = flowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		FlowId:    flowID,
		RequestId: requestID,
		Attributes: []*dexpb.AttributeWrite{
			{Key: "status", Value: stringValue("second")},
			{Key: "extra", Value: stringValue("second")},
		},
	})
	require.NoError(t, err, "a repeated request ID returns the recorded success")

	attributes := environment.readAttributes(t, ctx, flowID, "status", "extra")
	require.Equal(t, map[string]string{"status": "first"}, attributes)
}

func (environment *requestIDDedupEnvironment) testDeleteChannelMessageRequestIDReuse(t *testing.T) {
	ctx, flowID := environment.startFlow(t, "delete-channel-message")
	flowClient := environment.runtime.FlowClient
	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{
			{ChannelName: request_id_dedup.PendingChannelName, Value: stringValue("first")},
			{ChannelName: request_id_dedup.PendingChannelName, Value: stringValue("second")},
		},
	})
	require.NoError(t, err)
	messages := waitForChannelMessages(t, ctx, environment.runtime, flowID, request_id_dedup.PendingChannelName, 2)
	requestID := newRequestID()

	_, err = flowClient.DeleteChannelMessage(ctx, &dexpb.DeleteChannelMessageRequest{
		FlowId:      flowID,
		ChannelName: request_id_dedup.PendingChannelName,
		MessageId:   messages[0].GetMessageId(),
		RequestId:   requestID,
	})
	require.NoError(t, err)
	_, err = flowClient.DeleteChannelMessage(ctx, &dexpb.DeleteChannelMessageRequest{
		FlowId:      flowID,
		ChannelName: request_id_dedup.PendingChannelName,
		MessageId:   messages[1].GetMessageId(),
		RequestId:   requestID,
	})
	require.NoError(t, err, "a repeated request ID returns the recorded success")

	remaining := waitForChannelMessages(t, ctx, environment.runtime, flowID, request_id_dedup.PendingChannelName, 1)
	require.Equal(t, messages[1].GetMessageId(), remaining[0].GetMessageId())
}

func (environment *requestIDDedupEnvironment) testInvokeRPCRequestIDReuse(t *testing.T) {
	ctx, flowID := environment.startFlow(t, "invoke-rpc")
	requestID := newRequestID()

	first := environment.invokeRecordInput(t, ctx, flowID, requestID, "first")
	repeated := environment.invokeRecordInput(t, ctx, flowID, requestID, "second")

	require.Equal(t, "first", first.GetOutput().GetStringValue())
	require.True(t, proto.Equal(first, repeated), "the repeat returns the first output: %v", repeated)
	require.Equal(t, int32(1), environment.handler.RPCRunCount(flowID))
	attributes := environment.readAttributes(t, ctx, flowID, request_id_dedup.RPCInputAttributeKey)
	require.Equal(t, map[string]string{request_id_dedup.RPCInputAttributeKey: "first"}, attributes)
}

func (environment *requestIDDedupEnvironment) testConcurrentInvokeRPCRequestIDReuse(t *testing.T) {
	ctx, flowID := environment.startFlow(t, "concurrent-invoke-rpc")
	requestID := newRequestID()
	firstResult := make(chan *dexpb.InvokeRPCResponse, 1)
	secondResult := make(chan *dexpb.InvokeRPCResponse, 1)

	go environment.invokeRecordInputAsync(t, ctx, flowID, requestID, request_id_dedup.GatedInputPrefix+"first", firstResult)
	select {
	case enteredFlowID := <-environment.handler.RPCEntered():
		require.Equal(t, flowID, enteredFlowID)
	case <-ctx.Done():
		require.FailNow(t, "the first RPC never reached the Worker")
	}
	go environment.invokeRecordInputAsync(t, ctx, flowID, requestID, request_id_dedup.GatedInputPrefix+"second", secondResult)
	environment.handler.ReleaseGatedRPCs()

	first := receiveRPCResponse(t, ctx, firstResult)
	second := receiveRPCResponse(t, ctx, secondResult)
	require.Equal(t, request_id_dedup.GatedInputPrefix+"first", first.GetOutput().GetStringValue())
	// The second call may join the first in flight or replay its commit; both give the same reply.
	require.True(t, proto.Equal(first, second), "concurrent calls with the same request ID get the same reply: %v", second)
	require.Equal(t, int32(1), environment.handler.RPCRunCount(flowID), "concurrent calls run the handler once")
	attributes := environment.readAttributes(t, ctx, flowID, request_id_dedup.RPCInputAttributeKey)
	require.Equal(
		t,
		map[string]string{request_id_dedup.RPCInputAttributeKey: request_id_dedup.GatedInputPrefix + "first"},
		attributes,
	)
}

func (environment *requestIDDedupEnvironment) startFlow(t *testing.T, scope string) (context.Context, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	flowID := request_id_dedup.WorkflowType + "-" + scope + "-" + uuid.NewString()
	_, err := environment.runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           request_id_dedup.WorkflowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      request_id_dedup.HoldStep,
		FlowStartOptions:   withWorkerTarget(nil, environment.workerTarget),
	})
	require.NoError(t, err)
	return ctx, flowID
}

func (environment *requestIDDedupEnvironment) invokeRecordInputAsync(
	t *testing.T,
	ctx context.Context,
	flowID string,
	requestID string,
	input string,
	result chan<- *dexpb.InvokeRPCResponse,
) {
	response, err := environment.runtime.FlowClient.InvokeRPC(ctx, recordInputRequest(flowID, requestID, input))
	if err != nil {
		t.Errorf("InvokeRPC with input %q failed: %v", input, err)
		result <- nil
		return
	}
	result <- response
}

func (environment *requestIDDedupEnvironment) invokeRecordInput(
	t *testing.T,
	ctx context.Context,
	flowID string,
	requestID string,
	input string,
) *dexpb.InvokeRPCResponse {
	t.Helper()
	response, err := environment.runtime.FlowClient.InvokeRPC(ctx, recordInputRequest(flowID, requestID, input))
	require.NoError(t, err)
	return response
}

// readAttributes returns the string values of the requested keys that the run holds.
func (environment *requestIDDedupEnvironment) readAttributes(
	t *testing.T,
	ctx context.Context,
	flowID string,
	keys ...string,
) map[string]string {
	t.Helper()
	response, err := environment.runtime.FlowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowID,
		Keys:   keys,
	})
	require.NoError(t, err)
	values := make(map[string]string)
	for _, attribute := range response.GetAttributes() {
		if attribute.GetValue() != nil && attribute.GetValue().GetKind() != nil {
			values[attribute.GetKey()] = attribute.GetValue().GetStringValue()
		}
	}
	return values
}

func recordInputRequest(flowID string, requestID string, input string) *dexpb.InvokeRPCRequest {
	return &dexpb.InvokeRPCRequest{
		FlowId:          flowID,
		RpcName:         request_id_dedup.RPCName,
		Input:           stringValue(input),
		TimeoutSeconds:  20,
		RequestId:       requestID,
		IsTransactional: true,
	}
}

func receiveRPCResponse(
	t *testing.T,
	ctx context.Context,
	result <-chan *dexpb.InvokeRPCResponse,
) *dexpb.InvokeRPCResponse {
	t.Helper()
	select {
	case response := <-result:
		require.NotNil(t, response, "the RPC call failed")
		return response
	case <-ctx.Done():
		require.FailNow(t, "the RPC call did not return")
		return nil
	}
}
