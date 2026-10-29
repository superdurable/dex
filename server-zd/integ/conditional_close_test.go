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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	conditionalClose "github.com/superdurable/dex/server-zd/integ/workflow/conditional_close"
	"google.golang.org/protobuf/proto"
)

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(t, nil)
		smallWaitForFastTest()
	}
}

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporalContinueAsNew(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(
			t,

			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	doTestConditionalForceCompleteOnChannelEmptyWorkflow(t, flowConfig, false)
	doTestConditionalForceCompleteOnChannelEmptyWorkflow(t, flowConfig, true)
}

func doTestConditionalForceCompleteOnChannelEmptyWorkflow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
	useSignalChannel bool,
) {
	assertions := assert.New(t)

	workerHandler := conditionalClose.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	channelType := "_internal_channel_"
	if useSignalChannel {
		channelType = "_signal_channel_"
	}
	flowId := conditionalClose.WorkflowType + channelType + uuid.NewString()

	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           conditionalClose.WorkflowType,
		FlowTimeoutSeconds: 20,

		StartStepType: conditionalClose.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	}
	if useSignalChannel {
		startRequest.StepInput = stringValue("use-signal-channel")
	}

	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	time.Sleep(time.Second)
	for i := 0; i < 3; i++ {
		if useSignalChannel {
			_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
				FlowId: flowId,
				Messages: []*dexpb.ChannelMessage{
					{ChannelName: conditionalClose.TestChannelName},
				},
			})
		} else {
			_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
				RequestId: newRequestID(),
				FlowId:    flowId,
				RpcName:   conditionalClose.RpcPublishInternalChannel,
			})
		}
		require.NoError(t, err)
		if i == 0 {
			time.Sleep(time.Second)
		}
	}

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:       flowId,
		NeedsResults: true,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory

	expectMap := map[string]int64{
		"S1_waitFor": 3,
		"S1_execute": 3,
	}
	if !useSignalChannel {
		expectMap[conditionalClose.RpcPublishInternalChannel] = 3
	}
	assertions.Equalf(expectMap, history, "conditional close test fail, %v", history)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	expectedOutput := &dexpb.StepCompletionOutput{
		CompletedStepType:        "S1",
		CompletedStepExecutionId: "S1-3",
		CompletedStepOutput:      conditionalClose.TestInput,
	}
	assertions.True(proto.Equal(expectedOutput, response.GetResults()[0]))
}
