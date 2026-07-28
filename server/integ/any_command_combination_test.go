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
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	anycommandcombination "github.com/superdurable/iwf/integ/workflow/any_command_combination"
	"github.com/superdurable/iwf/service"
	"google.golang.org/protobuf/proto"
)

func TestAnyCommandCombinationFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCombinationFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCombinationFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		// TODO not sure why using ASYNC durability will fail
		doTestAnyCommandCombinationFlow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCombinationFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCombinationFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCombinationFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCombinationFlow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewConfig(iwfpb.StepDurability_STEP_DURABILITY_SYNC),
		)
		smallWaitForFastTest()
	}
}

func doTestAnyCommandCombinationFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := anycommandcombination.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := anycommandcombination.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           anycommandcombination.WorkflowType,
		FlowTimeoutSeconds: 40,
		WorkerTarget:       workerTarget,
		StartStepType:      anycommandcombination.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	signalValue := encodedObjectValue("json", []byte("test-data-1"))
	publishSignal := func() {
		_, publishErr := flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: anycommandcombination.SignalNameAndId1,
					Value:       signalValue,
				},
			},
		})
		require.NoError(t, publishErr)
	}

	publishSignal()
	publishSignal()

	time.Sleep(5 * time.Second)
	_, err = flowClient.SkipTimer(ctx, &iwfpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  "S1-1",
		TimerConditionId: anycommandcombination.TimerId1,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	publishSignal()

	descResp, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_RUNNING, descResp.Status)

	_, err = flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*iwfpb.ChannelMessage{
			{
				ChannelName: anycommandcombination.SignalNameAndId3,
				Value:       signalValue,
			},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*iwfpb.ChannelMessage{
			{
				ChannelName: anycommandcombination.SignalNameAndId2,
				Value:       signalValue,
			},
		},
	})
	require.NoError(t, err)

	if flowConfig == nil {
		time.Sleep(time.Second)
		descResp, err = runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
		require.NoError(t, err)
		require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, descResp.Status)
	} else {
		respWait, waitErr := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 30,
		})
		require.NoError(t, waitErr)
		require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, respWait.GetFlowStatus())
	}

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	require.Equalf(t, map[string]int64{
		"S1_waitFor": 2,
		"S1_execute": 1,
		"S2_waitFor": 2,
		"S2_execute": 1,
	}, history, "anycommandcombination test fail, %v", history)

	expectedData := map[string]interface{}{
		"s1_commandResults": expectedS1ConditionResults(signalValue),
		"s2_commandResults": expectedS2ConditionResults(signalValue),
	}
	require.True(t, proto.Equal(
		expectedData["s1_commandResults"].(*iwfpb.ConditionResults),
		data["s1_commandResults"].(*iwfpb.ConditionResults),
	))
	require.True(t, proto.Equal(
		expectedData["s2_commandResults"].(*iwfpb.ConditionResults),
		data["s2_commandResults"].(*iwfpb.ConditionResults),
	))
}

func expectedS1ConditionResults(signalValue *iwfpb.Value) *iwfpb.ConditionResults {
	return &iwfpb.ConditionResults{
		ChannelResults: []*iwfpb.ChannelResult{
			{
				ConditionId:     anycommandcombination.SignalCond1a,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*iwfpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalCond1b,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*iwfpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId2,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId2,
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId3,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId3,
			},
		},
		TimerResults: []*iwfpb.TimerResult{
			{
				ConditionId:     anycommandcombination.TimerId1,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
			},
		},
	}
}

func expectedS2ConditionResults(signalValue *iwfpb.Value) *iwfpb.ConditionResults {
	return &iwfpb.ConditionResults{
		ChannelResults: []*iwfpb.ChannelResult{
			{
				ConditionId:     anycommandcombination.SignalCond1a,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*iwfpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalCond1b,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId1,
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId2,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId2,
				Values:          []*iwfpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId3,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId3,
			},
		},
		TimerResults: []*iwfpb.TimerResult{
			{
				ConditionId:     anycommandcombination.TimerId1,
				ConditionStatus: iwfpb.ConditionStatus_CONDITION_STATUS_WAITING,
			},
		},
	}
}
