// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	conditionalClose "github.com/superdurable/dex/integ/workflow/conditional_close"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewConfig(dexpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func TestConditionalForceCompleteOnInternalChannelEmptyWorkflowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewConfig(dexpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func doTestConditionalForceCompleteOnInternalChannelEmptyWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	doTestConditionalForceCompleteOnChannelEmptyWorkflow(t, backendType, flowConfig, false)
	doTestConditionalForceCompleteOnChannelEmptyWorkflow(t, backendType, flowConfig, true)
}

func doTestConditionalForceCompleteOnChannelEmptyWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
	useSignalChannel bool,
) {
	assertions := assert.New(t)

	workerHandler := conditionalClose.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	channelType := "_internal_channel_"
	if useSignalChannel {
		channelType = "_signal_channel_"
	}
	flowId := conditionalClose.WorkflowType + channelType + uuid.NewString()

	startRequest := &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           conditionalClose.WorkflowType,
		FlowTimeoutSeconds: 20,
		WorkerTarget:       workerTarget,
		StartStepType:      conditionalClose.State1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
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
				FlowId:  flowId,
				RpcName: conditionalClose.RpcPublishInternalChannel,
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
