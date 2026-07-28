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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	config2 "github.com/superdurable/iwf/config"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/signal"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestSignalWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSignalWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestSignalWorkflowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSignalWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func TestSignalWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSignalWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestSignalWorkflowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSignalWorkflow(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func doTestSignalWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	assertions := assert.New(t)

	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := signal.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 20,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	var debugDump iwfpb.DebugDumpResponse
	err = unifiedClient.QueryWorkflow(ctx, &debugDump, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	expectedConfig := proto.Clone(config2.DefaultWorkflowConfig).(*iwfpb.FlowConfig)
	if flowConfig != nil {
		expectedConfig = proto.Clone(flowConfig).(*iwfpb.FlowConfig)
	}
	assertions.True(proto.Equal(expectedConfig, debugDump.GetConfig()))

	_, err = flowClient.UpdateFlowConfig(ctx, &iwfpb.UpdateFlowConfigRequest{
		FlowId: flowId,
		FlowConfig: &iwfpb.FlowConfig{
			ContinueAsNewPageSizeInBytes: ptr.Any(int32(3000000)),
		},
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	err = unifiedClient.QueryWorkflow(ctx, &debugDump, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	expectedConfig.ContinueAsNewPageSizeInBytes = ptr.Any(int32(3000000))
	assertions.True(proto.Equal(expectedConfig, debugDump.GetConfig()))

	var unhandledSignalVals []*iwfpb.Value
	for i := 0; i < 10; i++ {
		signalVal := stringValue(fmt.Sprintf("test-data-%v", i))
		_, publishErr := flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: signal.UnhandledSignalName,
					Value:       signalVal,
				},
			},
		})
		if publishErr == nil {
			unhandledSignalVals = append(unhandledSignalVals, signalVal)
		}
		if *cadenceIntegTest {
			time.Sleep(100 * time.Millisecond)
		}

		rpcResp, rpcErr := flowClient.InvokeRPC(ctx, &iwfpb.InvokeRPCRequest{
			FlowId:  flowId,
			RpcName: signal.RPCNameGetSignalChannelInfo,
		})
		require.NoError(t, rpcErr)
		infos := channelInfosFromOutput(t, rpcResp.GetOutput())
		expectedInfos := map[string]*iwfpb.ChannelInfo{
			signal.UnhandledSignalName: {Size: int32(i + 1)},
		}
		if i > 0 {
			expectedInfos[signal.InternalChannelName] = &iwfpb.ChannelInfo{Size: int32(i)}
		}
		assertions.Equal(expectedInfos, infos)
	}
	if *cadenceIntegTest {
		time.Sleep(100 * time.Millisecond)
	}

	rpcResp, err := flowClient.InvokeRPC(ctx, &iwfpb.InvokeRPCRequest{
		FlowId:  flowId,
		RpcName: signal.RPCNameGetInternalChannelInfo,
	})
	require.NoError(t, err)
	infos := channelInfosFromOutput(t, rpcResp.GetOutput())
	assertions.Equal(
		map[string]*iwfpb.ChannelInfo{
			signal.UnhandledSignalName: {Size: 10},
			signal.InternalChannelName:  {Size: 10},
		},
		infos,
	)

	var signalVals []*iwfpb.Value
	for i := 0; i < 4; i++ {
		signalVal := stringValue(fmt.Sprintf("test-data-%v", i))
		signalVals = append(signalVals, signalVal)
		_, err = flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: signal.SignalName,
					Value:       signalVal,
				},
			},
		})
		require.NoError(t, err)
	}

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
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
		assertions.True(proto.Equal(signalVals[i], data[fmt.Sprintf("signalValue%v", i)].(*iwfpb.Value)))
	}

	var dump iwfpb.DebugDumpResponse
	err = unifiedClient.QueryWorkflow(ctx, &dump, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	received := dump.GetSnapshot().GetChannelReceived()[signal.UnhandledSignalName].GetValues()
	assertions.True(len(unhandledSignalVals) > 0)
	require.Len(t, received, len(unhandledSignalVals))
	for i, expected := range unhandledSignalVals {
		assertions.True(proto.Equal(expected, received[i]))
	}

	if flowConfig == nil {
		_, err = flowClient.ResetFlow(ctx, &iwfpb.ResetFlowRequest{
			FlowId:    flowId,
			ResetType: iwfpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)

		_, err = flowClient.ResetFlow(ctx, &iwfpb.ResetFlowRequest{
			FlowId:          flowId,
			ResetType:       iwfpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID,
			StepExecutionId: "S2-1",
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)

		_, err = flowClient.ResetFlow(ctx, &iwfpb.ResetFlowRequest{
			FlowId:    flowId,
			ResetType: iwfpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
			StepType:  "S2",
		})
		require.NoError(t, err)
		_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
			FlowId:          flowId,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)
	}
}

func channelInfosFromOutput(t *testing.T, output *iwfpb.Value) map[string]*iwfpb.ChannelInfo {
	t.Helper()
	var infos map[string]*iwfpb.ChannelInfo
	err := json.Unmarshal(output.GetObjValue().GetPayload(), &infos)
	require.NoError(t, err)
	return infos
}
