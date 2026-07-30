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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/locking"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestLockingWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLockingWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestLockingWorkflowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLockingWorkflow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestLockingWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLockingWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestLockingWorkflowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLockingWorkflow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestLockingWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := locking.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	flowId := locking.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           locking.WorkflowType,
		FlowTimeoutSeconds: 300,

		StartStepType: locking.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:            flowId,
		RpcName:           locking.RPCName,
		Input:             objJSONValue("data"),
		LockAttributeKeys: []string{locking.TestSearchAttributeIntKey},
	})
	if backendType == service.BackendTypeCadence {
		require.Equal(t, codes.Unimplemented, status.Code(err))
	} else {
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Equal(
			t,
			"request ID is required for locking RPC",
			grpcErrorResponse(t, err).GetDetail(),
		)
	}

	for i := 0; i < locking.NumUnusedSignals; i++ {
		_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*dexpb.ChannelMessage{
				{ChannelName: locking.UnusedSignalChannelName},
			},
		})
		require.NoError(t, err)
	}

	assertions := assert.New(t)

	if flowConfig != nil && backendType == service.BackendTypeTemporal {
		time.Sleep(locking.InParallelS2 * time.Second)
	}

	rpcIncrease := 0
	rpcLockingFailure := 0
	if backendType == service.BackendTypeTemporal {
		for i := 0; i < 25; i++ {
			time.Sleep(2 * time.Second)
			rpcResp, rpcErr := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
				FlowId:         flowId,
				RpcName:        locking.RPCName,
				Input:          objJSONValue("data"),
				TimeoutSeconds: 2,
				RequestId:      uuid.NewString(),
				LockAttributeKeys: []string{
					locking.TestSearchAttributeIntKey,
					locking.TestDataAttributeKey1,
				},
			})
			if rpcErr != nil {
				if status.Code(rpcErr) == codes.Aborted {
					errResp := grpcErrorResponse(t, rpcErr)
					assertions.Equal("one or more attribute keys are locked", errResp.GetDetail())
					assertions.Equal(
						dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
						errResp.GetSubStatus(),
					)
					rpcLockingFailure++
					continue
				}
				require.NoError(t, rpcErr)
			}
			rpcIncrease++
			assertions.True(proto.Equal(objJSONValue("data"), rpcResp.GetOutput()))
		}
		assertions.True(rpcIncrease > 0)
		assertions.True(rpcLockingFailure > 0)
	}

	time.Sleep(time.Second)
	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		RpcName:   locking.RPCName,
		Input:     objJSONValue(locking.ShouldUnblockStateWaiting),
	})
	require.NoError(t, err)

	time.Sleep(20 * time.Second)
	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	s2StartsDecides := locking.InParallelS2 + rpcIncrease
	finalCounterValue := int64(locking.InParallelS2 + 2*rpcIncrease)
	history := workerHandler.GetTestResult().InvokeHistory
	assertions.Equalf(map[string]int64{
		"S1_waitFor":           1,
		"S1_execute":           1,
		"StateWaiting_waitFor": 1,
		"StateWaiting_execute": 1,
		"S2_waitFor":           int64(s2StartsDecides),
		"S2_execute":           int64(s2StartsDecides),
	}, history, "locking.test fail, %v", history)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	assertions.Equal(0, len(response.GetResults()))

	attributesResp, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   []string{locking.TestSearchAttributeIntKey, locking.TestDataAttributeKey1},
	})
	require.NoError(t, err)
	attributeMap := attributesToMap(attributesResp.GetAttributes())
	assertions.Equal(finalCounterValue, attributeMap[locking.TestSearchAttributeIntKey].GetIntValue())
	assertions.Equal(
		fmt.Sprintf("%v", finalCounterValue),
		string(attributeMap[locking.TestDataAttributeKey1].GetObjValue().GetPayload()),
	)

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId:                flowId,
		ResetType:             dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
		StepType:              locking.StateWaiting,
		SkipLockingRpcReapply: true,
	})
	require.NoError(t, err)

	time.Sleep(20 * time.Second)
	resetResponse, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resetResponse.GetFlowStatus())
}
