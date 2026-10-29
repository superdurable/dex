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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/locking"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestLockingWorkflowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLockingWorkflow(t, nil)
		smallWaitForFastTest()
	}
}

func doTestLockingWorkflow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := locking.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
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
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "request ID is required", grpcServiceErrorResponse(t, err).GetDetail())

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

	if flowConfig != nil {
		time.Sleep(locking.InParallelS2 * time.Second)
	}

	rpcIncrease := 0
	rpcLockingFailure := 0

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
				errResp := grpcServiceErrorResponse(t, rpcErr)
				// zd-bug #1002: server-zd names the locked key in the lock-conflict detail.
				expectedDetail := "RPC lock conflict: attribute key " + locking.TestSearchAttributeIntKey +
					" is locked by a running Step"

				assertions.Equal(expectedDetail, errResp.GetDetail())
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

	s2StartsDecides := locking.InParallelS2 + rpcIncrease
	finalCounterValue := int64(locking.InParallelS2 + 2*rpcIncrease)
	initialStateWaitingExecutes := int64(0)
	resetSourceRunID := ""

	require.Eventually(t, func() bool {
		initialHistory := workerHandler.GetTestResult().InvokeHistory
		return initialHistory["S2_waitFor"] == int64(s2StartsDecides) &&
			initialHistory["S2_execute"] == int64(s2StartsDecides)
	}, 60*time.Second, 100*time.Millisecond)
	resetSourceRunID = describeFlowRunID(t, ctx, runtime, flowId)
	require.Eventually(t, func() bool {
		isCompleted, historyErr := hasStepExecuteCompletedHistory(
			ctx,
			flowClient,
			flowId,
			resetSourceRunID,
			fmt.Sprintf("%s-%d", locking.State2, s2StartsDecides),
		)
		return historyErr == nil && isCompleted
	}, 60*time.Second, 100*time.Millisecond)

	history := workerHandler.GetTestResult().InvokeHistory
	expectedInitialHistory := map[string]int64{
		"S1_waitFor":           1,
		"S1_execute":           1,
		"StateWaiting_waitFor": 1,
		"S2_waitFor":           int64(s2StartsDecides),
		"S2_execute":           int64(s2StartsDecides),
	}
	if initialStateWaitingExecutes > 0 {
		expectedInitialHistory["StateWaiting_execute"] = initialStateWaitingExecutes
	}
	assertions.Equalf(expectedInitialHistory, history, "locking.test fail, %v", history)

	assertions.Equal(int32(rpcIncrease), workerHandler.GetRPCInvokeCount())

	resetRequest := &dexpb.ResetFlowRequest{
		FlowId:    flowId,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
		StepType:  locking.StateWaiting,
	}
	expectedRPCInvokes := int32(2 * rpcIncrease)
	expectedS2Invokes := int64(2 * s2StartsDecides)
	expectedStateWaitingWaitForInvokes := int64(2)
	// Temporal reset reapplies RPC writes made after the reset point; server-zd reset discards them.

	resetRequest.ResetType = dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID
	resetRequest.RunId = resetSourceRunID
	resetRequest.StepType = ""
	resetRequest.StepExecutionId = fmt.Sprintf("%s-%d", locking.State2, s2StartsDecides)
	resetRequest.StepMethod = dexpb.FlowResetStepMethod_FLOW_RESET_STEP_METHOD_WAIT_FOR
	expectedRPCInvokes = int32(rpcIncrease)
	expectedS2Invokes = int64(s2StartsDecides + 1)
	expectedStateWaitingWaitForInvokes = 1

	resetRequest.SkipWritesReapply = true

	_, err = flowClient.ResetFlow(ctx, resetRequest)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return workerHandler.GetRPCInvokeCount() == expectedRPCInvokes
	}, 30*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		resetHistory := workerHandler.GetTestResult().InvokeHistory
		return resetHistory["S2_waitFor"] == expectedS2Invokes &&
			resetHistory["S2_execute"] == expectedS2Invokes
	}, 60*time.Second, 100*time.Millisecond)
	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId: newRequestID(),
		FlowId:    flowId,
		RpcName:   locking.RPCName,
		Input:     objJSONValue(locking.ShouldUnblockStateWaiting),
	})
	require.NoError(t, err)

	resetWaitResponse, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resetWaitResponse.GetFlowStatus())

	resetHistory := workerHandler.GetTestResult().InvokeHistory
	finalStateWaitingExecutes := int64(1)

	assertions.Equalf(map[string]int64{
		"S1_waitFor":           1,
		"S1_execute":           1,
		"StateWaiting_waitFor": expectedStateWaitingWaitForInvokes,
		"StateWaiting_execute": finalStateWaitingExecutes,
		"S2_waitFor":           expectedS2Invokes,
		"S2_execute":           expectedS2Invokes,
	}, resetHistory, "locking reset reapply failed, %v", resetHistory)
	assertions.Equal(expectedRPCInvokes, workerHandler.GetRPCInvokeCount())

	resetAttributesResp, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   []string{locking.TestSearchAttributeIntKey, locking.TestDataAttributeKey1},
	})
	require.NoError(t, err)
	resetAttributeMap := attributesToMap(resetAttributesResp.GetAttributes())
	assertions.Equal(
		finalCounterValue,
		resetAttributeMap[locking.TestSearchAttributeIntKey].GetIntValue(),
	)
	assertions.Equal(
		fmt.Sprintf("%v", finalCounterValue),
		string(resetAttributeMap[locking.TestDataAttributeKey1].GetObjValue().GetPayload()),
	)
}

func hasStepExecuteCompletedHistory(
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
	stepExecutionID string,
) (bool, error) {
	var pageToken []byte
	nextEventID := int64(1)
	for {
		response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId:               flowID,
			RunId:                runID,
			StartInternalEventId: nextEventID,
			EstimatePageSize:     100,
			NextPageToken:        pageToken,
		})
		if err != nil {
			return false, err
		}
		for _, event := range response.GetEvents() {
			executeEvent := event.GetStepExecuteCompleted()
			if executeEvent != nil && executeEvent.GetContext().GetStepExecutionId() == stepExecutionID {
				return true, nil
			}
		}
		nextEventID = response.GetNextInternalEventId()
		pageToken = response.GetNextPageToken()
		if len(pageToken) == 0 {
			return false, nil
		}
	}
}

func assertReappliedLockingRPCResult(t *testing.T, rpcResults []locking.RPCResult) {
	t.Helper()
	require.Len(t, rpcResults, 1)
	rpcResult := rpcResults[0]
	require.Equal(t, "data", rpcResult.InputPayload)
	require.Equal(t, "data", rpcResult.OutputPayload)
	require.Equal(t, 4, rpcResult.UpsertAttributeCount)
	require.Equal(t, 1, rpcResult.RecordEventCount)
	require.Equal(t, 1, rpcResult.PublishedMessageCount)
	require.Equal(t, 1, rpcResult.NextStepCount)
	require.Equal(t, locking.State2, rpcResult.NextStepType)
}
