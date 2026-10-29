// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	anycommandcombination "github.com/superdurable/dex/server-zd/integ/workflow/any_command_combination"
	"google.golang.org/protobuf/proto"
)

func TestAnyCommandCombinationFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCombinationFlow(t, nil)
		smallWaitForFastTest()
	}
}

func doTestAnyCommandCombinationFlow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := anycommandcombination.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := anycommandcombination.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           anycommandcombination.WorkflowType,
		FlowTimeoutSeconds: 40,

		StartStepType: anycommandcombination.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	signalValue := encodedObjectValue("json", []byte("test-data-1"))
	publishSignal := func() {
		_, publishErr := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*dexpb.ChannelMessage{
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
	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  "S1-1",
		TimerConditionId: anycommandcombination.TimerId1,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	publishSignal()

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, describeFlowStatus(t, ctx, runtime, flowId))

	_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*dexpb.ChannelMessage{
			{
				ChannelName: anycommandcombination.SignalNameAndId3,
				Value:       signalValue,
			},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*dexpb.ChannelMessage{
			{
				ChannelName: anycommandcombination.SignalNameAndId2,
				Value:       signalValue,
			},
		},
	})
	require.NoError(t, err)

	if flowConfig == nil {
		require.Eventually(t, func() bool {
			return describeFlowStatus(t, ctx, runtime, flowId) == dexpb.FlowStatus_FLOW_STATUS_COMPLETED
		}, 10*time.Second, 100*time.Millisecond)
	} else {
		respWait, waitErr := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 30,
		})
		require.NoError(t, waitErr)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, respWait.GetFlowStatus())
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
		expectedData["s1_commandResults"].(*dexpb.ConditionResults),
		data["s1_commandResults"].(*dexpb.ConditionResults),
	))
	require.True(t, proto.Equal(
		expectedData["s2_commandResults"].(*dexpb.ConditionResults),
		data["s2_commandResults"].(*dexpb.ConditionResults),
	))
}

func expectedS1ConditionResults(signalValue *dexpb.Value) *dexpb.ConditionResults {
	return &dexpb.ConditionResults{
		ChannelResults: []*dexpb.ChannelResult{
			{
				ConditionId:     anycommandcombination.SignalCond1a,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*dexpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalCond1b,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*dexpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId2,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId2,
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId3,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId3,
			},
		},
		TimerResults: []*dexpb.TimerResult{
			{
				ConditionId:     anycommandcombination.TimerId1,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
			},
		},
	}
}

func expectedS2ConditionResults(signalValue *dexpb.Value) *dexpb.ConditionResults {
	return &dexpb.ConditionResults{
		ChannelResults: []*dexpb.ChannelResult{
			{
				ConditionId:     anycommandcombination.SignalCond1a,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId1,
				Values:          []*dexpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalCond1b,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId1,
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId2,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
				ChannelName:     anycommandcombination.SignalNameAndId2,
				Values:          []*dexpb.Value{signalValue},
			},
			{
				ConditionId:     anycommandcombination.SignalNameAndId3,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
				ChannelName:     anycommandcombination.SignalNameAndId3,
			},
		},
		TimerResults: []*dexpb.TimerResult{
			{
				ConditionId:     anycommandcombination.TimerId1,
				ConditionStatus: dexpb.ConditionStatus_CONDITION_STATUS_WAITING,
			},
		},
	}
}
