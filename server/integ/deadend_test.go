// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/deadend"
	"github.com/superdurable/dex/service"
	temporalcommon "go.temporal.io/api/common/v1"
	temporalenums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeadEndFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestDeadEndFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestSynchronousUpdateRequestIDTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSynchronousUpdateRequestID(t)
		smallWaitForFastTest()
	}
}

func TestDeadEndFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestDeadEndFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestDeadEndFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestDeadEndFlow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestDeadEndFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestDeadEndFlow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestSynchronousUpdateRequestID(t *testing.T) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeTemporal})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := deadend.WorkflowType + "-request-id-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 30,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:            flowId,
		RpcName:           deadend.RPCWriteData,
		LockAttributeKeys: []string{"any key"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "request ID is required", grpcErrorResponse(t, err).GetDetail())
	require.Zero(t, workerHandler.GetRPCInvokes())
	require.Eventually(t, func() bool {
		var dump dexpb.DebugDumpResponse
		queryErr := runtime.UnifiedClient.QueryWorkflow(
			ctx,
			&dump,
			flowId,
			startResponse.GetRunId(),
			service.DebugDumpQueryType,
		)
		return queryErr == nil &&
			dump.GetConfig().GetWorkerTarget().GetAddress() == workerTarget.GetAddress()
	}, 10*time.Second, 20*time.Millisecond)

	requestId := uuid.NewString()
	request := &dexpb.InvokeRPCRequest{
		FlowId:            flowId,
		RpcName:           deadend.RPCWriteData,
		LockAttributeKeys: []string{"any key"},
		RequestId:         requestId,
	}
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	requestErrors := make([]error, 2)
	responses := make([]*dexpb.InvokeRPCResponse, 2)
	for requestIndex := range requestErrors {
		go func() {
			defer waitGroup.Done()
			responses[requestIndex], requestErrors[requestIndex] = flowClient.InvokeRPC(ctx, request)
		}()
	}
	waitGroup.Wait()
	for _, requestErr := range requestErrors {
		require.NoError(t, requestErr)
	}
	require.Equal(t, responses[0], responses[1])

	retryResponse, err := flowClient.InvokeRPC(ctx, request)
	require.NoError(t, err)
	require.Equal(t, responses[0], retryResponse)
	require.Equal(t, int32(1), workerHandler.GetRPCInvokes())

	accepted, completed := countTemporalUpdateEvents(
		t,
		ctx,
		runtime,
		flowId,
		startResponse.GetRunId(),
		requestId,
	)
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, completed)

	request.RequestId = uuid.NewString()
	_, err = flowClient.InvokeRPC(ctx, request)
	require.NoError(t, err)
	require.Equal(t, int32(2), workerHandler.GetRPCInvokes())

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{FlowId: flowId})
	require.NoError(t, err)
}

func doTestDeadEndFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := deadend.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := deadend.WorkflowType + "-" + uuid.NewString()
	startResp, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           deadend.WorkflowType,
		FlowTimeoutSeconds: 100,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	for range 3 {
		time.Sleep(2 * time.Second)
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			RpcName:   deadend.RPCWriteData,
		})
		require.NoError(t, err)
	}

	if flowConfig != nil {
		descResp, err := runtime.UnifiedClient.DescribeWorkflowExecution(ctx, flowId, "", nil)
		require.NoError(t, err)
		require.NotEqual(t, startResp.GetRunId(), descResp.RunId)
	}

	for range 3 {
		time.Sleep(2 * time.Second)
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId: newRequestID(),
			FlowId:    flowId,
			RpcName:   deadend.RPCTriggerState,
		})
		require.NoError(t, err)
	}

	time.Sleep(2 * time.Second)

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S1_execute": 3,
	}, history, "deadend test fail, %v", history)
}

func countTemporalUpdateEvents(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowId string,
	runId string,
	requestId string,
) (accepted int, completed int) {
	t.Helper()
	api := runtime.UnifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	var nextPageToken []byte
	for {
		response, err := api.GetWorkflowExecutionHistory(
			ctx,
			&workflowservice.GetWorkflowExecutionHistoryRequest{
				Namespace: testNamespace,
				Execution: &temporalcommon.WorkflowExecution{
					WorkflowId: flowId,
					RunId:      runId,
				},
				MaximumPageSize: 1000,
				NextPageToken:   nextPageToken,
			},
		)
		require.NoError(t, err)
		for _, event := range response.GetHistory().GetEvents() {
			switch event.GetEventType() {
			case temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED:
				eventRequestId := event.GetWorkflowExecutionUpdateAcceptedEventAttributes().
					GetAcceptedRequest().
					GetMeta().
					GetUpdateId()
				if eventRequestId == requestId {
					accepted++
				}
			case temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_COMPLETED:
				eventRequestId := event.GetWorkflowExecutionUpdateCompletedEventAttributes().
					GetMeta().
					GetUpdateId()
				if eventRequestId == requestId {
					completed++
				}
			}
		}
		nextPageToken = response.GetNextPageToken()
		if len(nextPageToken) == 0 {
			return accepted, completed
		}
	}
}
