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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/protobuf/proto"
)

func TestSignalWorkflowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSignalWorkflow(t, nil)
		smallWaitForFastTest()
	}
}

func doTestSignalWorkflow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	assertions := assert.New(t)

	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := signal.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType: signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	startedConfig, err := readFlowConfig(ctx, runtime, flowId)
	require.NoError(t, err)
	expectedConfig := copyFlowConfigForMutation(runtime.defaultFlowConfig)
	if flowConfig != nil {
		expectedConfig = copyFlowConfigForMutation(flowConfig)
	}
	expectedConfig.WorkerTarget = workerTarget

	// GetFlowState returns the resolved FlowConfig, which includes server-zd defaults.
	assertions.True(proto.Equal(expectedConfig.GetWorkerTarget(), startedConfig.GetWorkerTarget()))
	assertions.Equal(expectedConfig.GetStepDurability(), startedConfig.GetStepDurability())
	expectedConfig = copyFlowConfigForMutation(startedConfig)

	_, err = flowClient.UpdateFlowConfig(ctx, &dexpb.UpdateFlowConfigRequest{
		FlowId: flowId,
		FlowConfig: &dexpb.FlowConfig{
			ContinueAsNewPageSizeInBytes: ptr.Any(int32(3000000)),
		},
	})
	require.NoError(t, err)

	expectedConfig.ContinueAsNewPageSizeInBytes = ptr.Any(int32(3000000))
	require.Eventually(t, func() bool {
		currentConfig, queryErr := readFlowConfig(ctx, runtime, flowId)
		return queryErr == nil && proto.Equal(expectedConfig, currentConfig)
	}, 2*time.Second, 50*time.Millisecond)

	var unhandledSignalVals []*dexpb.Value
	for i := 0; i < 10; i++ {
		signalVal := stringValue(fmt.Sprintf("test-data-%v", i))
		_, publishErr := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*dexpb.ChannelMessage{
				{
					ChannelName: signal.UnhandledSignalName,
					Value:       signalVal,
				},
			},
		})
		if publishErr == nil {
			unhandledSignalVals = append(unhandledSignalVals, signalVal)
		}

		rpcResp, rpcErr := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			RpcName:   signal.RPCNameGetSignalChannelInfo,
		})
		require.NoError(t, rpcErr)
		infos := channelInfosFromOutput(t, rpcResp.GetOutput())
		expectedInfos := map[string]*dexpb.ChannelInfo{
			signal.UnhandledSignalName: {Size: int32(i + 1)},
		}
		if i > 0 {
			expectedInfos[signal.InternalChannelName] = &dexpb.ChannelInfo{Size: int32(i)}
		}
		assertions.Equal(expectedInfos, infos)
	}

	rpcResp, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		RpcName:   signal.RPCNameGetInternalChannelInfo,
	})
	require.NoError(t, err)
	infos := channelInfosFromOutput(t, rpcResp.GetOutput())
	assertions.Equal(
		map[string]*dexpb.ChannelInfo{
			signal.UnhandledSignalName: {Size: 10},
			signal.InternalChannelName: {Size: 10},
		},
		infos,
	)

	var signalVals []*dexpb.Value
	for i := 0; i < 4; i++ {
		signalVal := stringValue(fmt.Sprintf("test-data-%v", i))
		signalVals = append(signalVals, signalVal)
		_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*dexpb.ChannelMessage{
				{
					ChannelName: signal.SignalName,
					Value:       signalVal,
				},
			},
		})
		require.NoError(t, err)
	}

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions.Equalf(map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "signal test fail, %v", history)

	assertions.Equal("signal-cmd-id0", data["signalId0"])
	assertions.Equal("signal-cmd-id1", data["signalId1"])
	assertions.Equal("", data["signalId2"])
	assertions.Equal("", data["signalId3"])
	for i := 0; i < 4; i++ {
		assertions.True(proto.Equal(signalVals[i], data[fmt.Sprintf("signalValue%v", i)].(*dexpb.Value)))
	}

	received := channelReceivedValues(t, runtime, flowId, signal.UnhandledSignalName)
	assertions.True(len(unhandledSignalVals) > 0)
	require.Len(t, received, len(unhandledSignalVals))
	for i, expected := range unhandledSignalVals {
		assertions.True(proto.Equal(expected, received[i]))
	}

	if flowConfig == nil {
		_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
			FlowId:    flowId,
			ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)

		_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
			FlowId:          flowId,
			ResetType:       dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID,
			StepExecutionId: "S2-1",
			StepMethod:      dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_EXECUTE,
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)

		_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
			FlowId:    flowId,
			ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
			StepType:  "S2",
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)
	}
}

func channelInfosFromOutput(t *testing.T, output *dexpb.Value) map[string]*dexpb.ChannelInfo {
	t.Helper()
	var infos map[string]*dexpb.ChannelInfo
	err := json.Unmarshal(output.GetObjValue().GetPayload(), &infos)
	require.NoError(t, err)
	return infos
}
