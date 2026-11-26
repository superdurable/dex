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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	flowCountLimitFlowType       = "flow-count-limit"
	flowCountLimitFanOutStep     = "FanOut"
	flowCountLimitPublishStep    = "Publish"
	flowCountLimitHoldStep       = "Hold"
	flowCountLimitAddHoldsRPC    = "addHolds"
	flowCountLimitPublishRPC     = "publishOrders"
	flowCountLimitOrdersChannel  = "orders"
	flowCountLimitRefundsChannel = "refunds"
	flowCountLimitMaxSteps       = 3
	flowCountLimitMaxMessages    = 2
	flowCountLimitStepLimitKey   = "limits.maxConcurrentStepsPerFlow"
	flowCountLimitChannelKey     = "limits.maxChannelMessages"
)

// flowCountLimitHandler starts or publishes as many items as a Step input or RPC input names.
type flowCountLimitHandler struct{}

func (h *flowCountLimitHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != flowCountLimitFlowType || request.GetStepType() != flowCountLimitHoldStep {
		return nil, status.Error(codes.InvalidArgument, "unexpected WaitFor invocation")
	}
	// A Hold waits on a Channel that never receives a message, so it stays active.
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: "never"}},
		},
	}, nil
}

func (h *flowCountLimitHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != flowCountLimitFlowType {
		return nil, status.Error(codes.InvalidArgument, "unexpected Flow type")
	}
	count := int(request.GetStepInput().GetIntValue())
	switch request.GetStepType() {
	case flowCountLimitFanOutStep:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: flowCountLimitHolds(count)},
		}, nil
	case flowCountLimitPublishStep:
		return &dexpb.InvokeExecuteMethodResponse{
			PublishToChannel: flowCountLimitMessages(flowCountLimitOrdersChannel, count),
			StepDecision:     &dexpb.StepDecision{NextSteps: flowCountLimitHolds(1)},
		}, nil
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unexpected Step type %q", request.GetStepType())
	}
}

func (h *flowCountLimitHandler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	count := int(request.GetInput().GetIntValue())
	switch request.GetRpcName() {
	case flowCountLimitAddHoldsRPC:
		return &dexpb.InvokeWorkerRPCResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: flowCountLimitHolds(count)},
		}, nil
	case flowCountLimitPublishRPC:
		return &dexpb.InvokeWorkerRPCResponse{
			PublishToChannel: flowCountLimitMessages(flowCountLimitOrdersChannel, count),
		}, nil
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unexpected RPC %q", request.GetRpcName())
	}
}

func TestFlowCountLimitsTemporal(t *testing.T) {
	for iteration := 0; iteration < *repeatIntegTest; iteration++ {
		t.Run(fmt.Sprintf("step-limit-step-decision-%d", iteration), testStepLimitFailsAStepDecisionOverIt)
		t.Run(fmt.Sprintf("step-limit-rpc-%d", iteration), testStepLimitRejectsAnRPCOverIt)
		t.Run(fmt.Sprintf("channel-limit-client-%d", iteration), testChannelLimitRejectsAClientPublishOverIt)
		t.Run(fmt.Sprintf("channel-limit-rpc-%d", iteration), testChannelLimitRejectsAnRPCOverIt)
		t.Run(fmt.Sprintf("channel-limit-step-%d", iteration), testChannelLimitFailsAStepThatPublishesOverIt)
	}
}

func startFlowCountLimitService(t *testing.T) (*integRuntime, *dexpb.WorkerTarget) {
	t.Helper()
	workerTarget := startWorker(t, &flowCountLimitHandler{})
	runtime := startDexService(t, serverZDTestConfig{
		ServerZDMaxConcurrentStepsPerFlow: flowCountLimitMaxSteps,
		ServerZDMaxChannelMessages:        flowCountLimitMaxMessages,
	})
	return runtime, workerTarget
}

// testStepLimitFailsAStepDecisionOverIt fans out to the limit, then one Step over it.
func testStepLimitFailsAStepDecisionOverIt(t *testing.T) {
	runtime, workerTarget := startFlowCountLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	withinLimitFlowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitFanOutStep, flowCountLimitMaxSteps)
	require.Eventually(t, func() bool {
		return countActiveHolds(t, ctx, runtime, withinLimitFlowID) == flowCountLimitMaxSteps
	}, 30*time.Second, 100*time.Millisecond, "the fan-out within the limit did not start every Hold")
	requireFlowStatus(t, ctx, runtime, withinLimitFlowID, dexpb.FlowStatus_FLOW_STATUS_RUNNING)

	overLimitFlowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitFanOutStep, flowCountLimitMaxSteps+1)
	result := waitForFlowCountLimitResult(t, ctx, runtime, overLimitFlowID)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus(), result.GetErrorMessage())
	require.Contains(t, result.GetErrorMessage(), flowCountLimitStepLimitKey)
}

// testStepLimitRejectsAnRPCOverIt adds Holds through RPCs until one would pass the limit.
func testStepLimitRejectsAnRPCOverIt(t *testing.T) {
	runtime, workerTarget := startFlowCountLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitHoldStep, 0)

	_, err := invokeFlowCountLimitRPC(ctx, runtime, flowID, flowCountLimitAddHoldsRPC, flowCountLimitMaxSteps-1)
	require.NoError(t, err)
	_, err = invokeFlowCountLimitRPC(ctx, runtime, flowID, flowCountLimitAddHoldsRPC, 1)
	requireResourceExhausted(t, err, flowCountLimitStepLimitKey)
	require.Eventually(t, func() bool {
		return countActiveHolds(t, ctx, runtime, flowID) == flowCountLimitMaxSteps
	}, 30*time.Second, 100*time.Millisecond, "the accepted RPC did not start its Holds")
	requireFlowStatus(t, ctx, runtime, flowID, dexpb.FlowStatus_FLOW_STATUS_RUNNING)
}

// testChannelLimitRejectsAClientPublishOverIt fills one Channel, then publishes once more.
func testChannelLimitRejectsAClientPublishOverIt(t *testing.T) {
	runtime, workerTarget := startFlowCountLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitHoldStep, 0)

	// One request over the limit is rejected whole.
	err := publishFlowCountLimitMessages(ctx, runtime, flowID, flowCountLimitOrdersChannel, flowCountLimitMaxMessages+1)
	requireResourceExhausted(t, err, flowCountLimitChannelKey)
	require.NoError(t, publishFlowCountLimitMessages(ctx, runtime, flowID, flowCountLimitOrdersChannel, flowCountLimitMaxMessages))
	err = publishFlowCountLimitMessages(ctx, runtime, flowID, flowCountLimitOrdersChannel, 1)
	requireResourceExhausted(t, err, flowCountLimitChannelKey)
	// The limit counts each Channel on its own.
	require.NoError(t, publishFlowCountLimitMessages(ctx, runtime, flowID, flowCountLimitRefundsChannel, 1))

	state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Len(t, state.GetPendingChannelMessages()[flowCountLimitOrdersChannel].GetMessages(), flowCountLimitMaxMessages)
	requireFlowStatus(t, ctx, runtime, flowID, dexpb.FlowStatus_FLOW_STATUS_RUNNING)
}

func testChannelLimitRejectsAnRPCOverIt(t *testing.T) {
	runtime, workerTarget := startFlowCountLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitHoldStep, 0)

	_, err := invokeFlowCountLimitRPC(ctx, runtime, flowID, flowCountLimitPublishRPC, flowCountLimitMaxMessages)
	require.NoError(t, err)
	_, err = invokeFlowCountLimitRPC(ctx, runtime, flowID, flowCountLimitPublishRPC, 1)
	requireResourceExhausted(t, err, flowCountLimitChannelKey)
	requireFlowStatus(t, ctx, runtime, flowID, dexpb.FlowStatus_FLOW_STATUS_RUNNING)
}

// testChannelLimitFailsAStepThatPublishesOverIt publishes one message over the limit from a Step result.
func testChannelLimitFailsAStepThatPublishesOverIt(t *testing.T) {
	runtime, workerTarget := startFlowCountLimitService(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flowID := startFlowCountLimitFlow(t, ctx, runtime, workerTarget, flowCountLimitPublishStep, flowCountLimitMaxMessages+1)
	result := waitForFlowCountLimitResult(t, ctx, runtime, flowID)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus(), result.GetErrorMessage())
	require.Contains(t, result.GetErrorMessage(), flowCountLimitChannelKey)
}

func startFlowCountLimitFlow(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	workerTarget *dexpb.WorkerTarget,
	startStepType string,
	count int64,
) string {
	t.Helper()
	flowID := flowCountLimitFlowType + "-" + uuid.NewString()
	stepOptions := &dexpb.StepOptions{SkipWaitFor: true}
	if startStepType == flowCountLimitHoldStep {
		stepOptions = nil
	}
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           flowCountLimitFlowType,
		FlowTimeoutSeconds: 120,
		StartStepType:      startStepType,
		StepInput:          intValue(count),
		StepOptions:        stepOptions,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	return flowID
}

func waitForFlowCountLimitResult(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) *dexpb.FlowResult {
	t.Helper()
	// A wait ends with DeadlineExceeded at the harness wait cap, so wait again until the run closes.
	for {
		require.NoError(t, ctx.Err(), "the run did not close")
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowID,
			WaitTimeSeconds: 10,
		})
		if status.Code(err) == codes.DeadlineExceeded {
			continue
		}
		require.NoError(t, err)
		return result
	}
}

func invokeFlowCountLimitRPC(
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	rpcName string,
	count int64,
) (*dexpb.InvokeRPCResponse, error) {
	return runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:    flowID,
		RpcName:   rpcName,
		Input:     intValue(count),
		RequestId: newRequestID(),
	})
}

func publishFlowCountLimitMessages(
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	channelName string,
	count int,
) error {
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: flowCountLimitMessages(channelName, count),
	})
	return err
}

func countActiveHolds(t *testing.T, ctx context.Context, runtime *integRuntime, flowID string) int {
	t.Helper()
	state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	holdCount := 0
	for _, step := range state.GetActiveStepExecutions() {
		if step.GetStepType() == flowCountLimitHoldStep {
			holdCount++
		}
	}
	return holdCount
}

func requireFlowStatus(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	expected dexpb.FlowStatus,
) {
	t.Helper()
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, expected, summary.GetFlowStatus())
}

func requireResourceExhausted(t *testing.T, err error, limitKey string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, codes.ResourceExhausted, status.Code(err), err.Error())
	require.Contains(t, status.Convert(err).Message(), limitKey)
}

func flowCountLimitHolds(count int) []*dexpb.StepMovement {
	movements := make([]*dexpb.StepMovement, 0, count)
	for range count {
		movements = append(movements, &dexpb.StepMovement{StepType: flowCountLimitHoldStep})
	}
	return movements
}

func flowCountLimitMessages(channelName string, count int) []*dexpb.ChannelMessage {
	messages := make([]*dexpb.ChannelMessage, 0, count)
	for index := range count {
		messages = append(messages, &dexpb.ChannelMessage{
			ChannelName: channelName,
			Value:       stringValue(fmt.Sprintf("%s-%d", channelName, index)),
		})
	}
	return messages
}
