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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/channel_cancel_requeue"
)

func TestChannelCancelRequeueTemporal(t *testing.T) {
	t.Run("a Step decision publishes and cancels the waiting Worker", testStepPublishesAndCancelsWorker)
	t.Run("an RPC cancels a Worker that consumed a message", testRpcCancelsConsumingWorker)
	t.Run("a Step decision cancels a Worker that consumed a message", testStepCancelsConsumingWorker)
}

func testStepCancelsConsumingWorker(t *testing.T) {
	handler := channel_cancel_requeue.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := channel_cancel_requeue.FlowType + "-step-consumed-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           channel_cancel_requeue.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      channel_cancel_requeue.Root,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	waitForWaitingSteps(t, ctx, runtime, flowID, start.GetRunId(),
		channel_cancel_requeue.FirstWorker, channel_cancel_requeue.FirstController)

	publishToChannel(t, ctx, runtime, flowID, channel_cancel_requeue.WorkChannel, "job-from-client")
	require.Eventually(t, handler.HasFirstWorkerExecuted, 10*time.Second, 50*time.Millisecond)
	publishToChannel(t, ctx, runtime, flowID, channel_cancel_requeue.GoChannel, "go")

	// The canceled Worker's message returns to the front; the Controller's message follows it.
	require.Equal(t,
		[]string{"job-from-client", channel_cancel_requeue.StepPublishedJob},
		waitForReceivedWork(t, ctx, runtime, flowID),
	)
	require.Eventually(t, handler.IsFirstWorkerExecuteCanceled, 5*time.Second, 50*time.Millisecond)
}

func publishToChannel(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	channelName string,
	text string,
) {
	t.Helper()
	_, err := runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: []*dexpb.ChannelMessage{{ChannelName: channelName, Value: stringValue(text)}},
	})
	require.NoError(t, err)
}

func testStepPublishesAndCancelsWorker(t *testing.T) {
	handler := channel_cancel_requeue.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := channel_cancel_requeue.FlowType + "-step-" + uuid.NewString()
	start, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           channel_cancel_requeue.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      channel_cancel_requeue.Root,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	waitForWaitingSteps(t, ctx, runtime, flowID, start.GetRunId(),
		channel_cancel_requeue.FirstWorker, channel_cancel_requeue.FirstController)

	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{{
			ChannelName: channel_cancel_requeue.GoChannel,
			Value:       stringValue("go"),
		}},
	})
	require.NoError(t, err)

	require.Equal(t, []string{channel_cancel_requeue.StepPublishedJob}, waitForReceivedWork(t, ctx, runtime, flowID))
	require.False(t, handler.HasFirstWorkerExecuted(), "the canceled Worker never takes the message")
	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, start.GetRunId())
	assertNoExecuteHistory(t, events, channel_cancel_requeue.FirstWorker)
}

func testRpcCancelsConsumingWorker(t *testing.T) {
	handler := channel_cancel_requeue.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := channel_cancel_requeue.FlowType + "-rpc-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           channel_cancel_requeue.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      channel_cancel_requeue.Worker,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{{
			ChannelName: channel_cancel_requeue.WorkChannel,
			Value:       stringValue("job-from-client"),
		}},
	})
	require.NoError(t, err)
	require.Eventually(t, handler.HasFirstWorkerExecuted, 10*time.Second, 50*time.Millisecond)

	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowID,
		RpcName:   channel_cancel_requeue.ReplaceWorkerRpc,
	})
	require.NoError(t, err)

	// The canceled Worker's message returns to the front; the RPC's own message follows it.
	require.Equal(t,
		[]string{"job-from-client", channel_cancel_requeue.RpcPublishedJob},
		waitForReceivedWork(t, ctx, runtime, flowID),
	)
	require.Eventually(t, handler.IsFirstWorkerExecuteCanceled, 5*time.Second, 50*time.Millisecond)
}

func waitForWaitingSteps(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
	stepExecutionIDs ...string,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  runID,
		})
		if err != nil {
			return false
		}
		waiting := map[string]bool{}
		for _, execution := range userActiveStepExecutions(state) {
			if execution.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING {
				waiting[execution.GetStepExecutionId()] = true
			}
		}
		for _, stepExecutionID := range stepExecutionIDs {
			if !waiting[stepExecutionID] {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond)
}

// waitForReceivedWork waits for the Flow to complete and returns the work the last Worker received.
func waitForReceivedWork(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) []string {
	t.Helper()
	response, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
		NeedsResults:    true,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	received := response.GetResults()[0].GetCompletedStepOutput().GetStringValue()
	return strings.Split(received, channel_cancel_requeue.ReceivedSeparator)
}
