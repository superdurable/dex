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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/integ/workflow/rpc"
	"github.com/superdurable/dex/service"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/common/ptr"
	temporalcommon "go.temporal.io/api/common/v1"
	temporalenums "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type rpcBlobReadScenario struct {
	isLazyLoading bool
	isObjectInput bool
}

type rpcBlobTransportScenario struct {
	isLazyLoading      bool
	isBlobStoreEnabled bool
	blobThreshold      int
}

type rpcWorkerRequestLimitScenario struct {
	isLazyLoading   bool
	isTransactional bool
}

type rpcBlobTransportFixture struct {
	test           *testing.T
	runtime        *integRuntime
	worker         *rpcBlobTransportWorker
	ctx            context.Context
	flowID         string
	runID          string
	storeDirectory string
}

type rpcBlobTransportWorker struct {
	resultOnlyWaitForWorker
	resultOnlyExecuteWorker
	flowClient dexpb.FlowServiceClient
	mu         sync.Mutex
	request    *dexpb.InvokeWorkerRPCRequest
	callCount  int
}

func TestRpcReadBlobStoreUnavailableTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testRPCReadBlobStoreUnavailable(t, service.BackendTypeTemporal)
}

func TestRpcReadBlobStoreUnavailableCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testRPCReadBlobStoreUnavailable(t, service.BackendTypeCadence)
}

func testRPCReadBlobStoreUnavailable(t *testing.T, backendType service.BackendType) {
	for _, isLazyLoading := range []bool{true, false} {
		for _, isObjectInput := range []bool{false, true} {
			scenario := rpcBlobReadScenario{isLazyLoading, isObjectInput}
			t.Run(fmt.Sprintf("lazy=%v/object=%v", isLazyLoading, isObjectInput), func(t *testing.T) {
				testRPCReadWithRejectedBlobWrites(t, backendType, scenario)
			})
		}
	}
}

func testRPCReadWithRejectedBlobWrites(t *testing.T, backendType service.BackendType, scenario rpcBlobReadScenario) {
	fixture := newRPCBlobTransportFixture(t, DexServiceTestConfig{
		BackendType: backendType,
		LazyLoading: ptr.Any(scenario.isLazyLoading),
	})
	count, err := fixture.runtime.BlobStore.CountWorkflowObjectsForTesting(fixture.ctx, fixture.flowID)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, os.MkdirAll(fixture.storeDirectory, 0o700))
	availableDirectory := fixture.storeDirectory + "-available"
	require.NoError(t, os.Rename(fixture.storeDirectory, availableDirectory))
	require.NoError(t, os.WriteFile(fixture.storeDirectory, []byte("reject blob writes"), 0o600))
	_, _, err = fixture.runtime.BlobStore.WriteObject(fixture.ctx, fixture.flowID, "rejected-write", []byte("value"))
	require.Error(t, err)

	input := stringValue(strings.Repeat("input-", 200))
	if scenario.isObjectInput {
		input = objJSONValue(`"` + strings.Repeat("input-", 200) + `"`)
	}
	ctx, cancel := context.WithTimeout(fixture.ctx, 3*time.Second)
	defer cancel()
	response, err := fixture.runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload", Input: input,
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(input, response.GetOutput()))
	require.Empty(t, common.BlobIdFromValue(response.GetOutput()))
	request, callCount := fixture.worker.lastRequest()
	require.Empty(t, common.BlobIdFromValue(request.GetInput()))
	entries, err := os.ReadDir(availableDirectory)
	require.NoError(t, err)
	require.Empty(t, entries)
	fixture.assertNoRPCHistory()
	if backendType == service.BackendTypeTemporal {
		_, err = fixture.runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload", Input: input,
			IsTransactional: true,
		})
		require.Error(t, err)
		_, afterCallCount := fixture.worker.lastRequest()
		require.Equal(t, callCount, afterCallCount)
	}
}

func TestRpcInlineTransportTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testRPCInlineTransport(t, service.BackendTypeTemporal)
}

func TestRpcInlineTransportCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testRPCInlineTransport(t, service.BackendTypeCadence)
}

func testRPCInlineTransport(t *testing.T, backendType service.BackendType) {
	for _, isLazyLoading := range []bool{true, false} {
		for _, configuration := range []struct {
			isBlobStoreEnabled bool
			blobThreshold      int
		}{
			{true, 100}, {true, 4096}, {false, 100}, {false, 4096},
		} {
			scenario := rpcBlobTransportScenario{isLazyLoading, configuration.isBlobStoreEnabled, configuration.blobThreshold}
			t.Run(fmt.Sprintf("lazy=%v/blob=%v/threshold=%d", isLazyLoading, scenario.isBlobStoreEnabled, scenario.blobThreshold), func(t *testing.T) {
				fixture := newRPCBlobTransportFixture(t, DexServiceTestConfig{
					BackendType: backendType, GrpcMaxMessageBytes: 2048, LocalBlobThreshold: scenario.blobThreshold,
					LazyLoading: ptr.Any(scenario.isLazyLoading), BlobStoreEnabled: ptr.Any(scenario.isBlobStoreEnabled),
				})
				_, err := fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, &dexpb.InvokeRPCRequest{
					FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload", Input: stringValue("probe"),
				})
				require.NoError(t, err)
				workerRequest, callCount := fixture.worker.lastRequest()
				inputSize := 2048 - proto.Size(workerRequest) + proto.Size(workerRequest.GetInput()) + 32
				input := stringValue(strings.Repeat("x", inputSize))
				request := &dexpb.InvokeRPCRequest{
					FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload", Input: input,
				}
				require.Less(t, proto.Size(request), 2048)
				_, err = fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, request)
				require.Equal(t, codes.FailedPrecondition, status.Code(err))
				errorResponse := grpcServiceErrorResponse(t, err)
				require.Equal(t, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, errorResponse.GetSubStatus())
				require.Equal(t, int32(codes.ResourceExhausted), errorResponse.GetOriginalWorkerErrorStatus())
				_, afterCallCount := fixture.worker.lastRequest()
				require.Equal(t, callCount, afterCallCount)
				if scenario.isBlobStoreEnabled {
					count, err := fixture.runtime.BlobStore.CountWorkflowObjectsForTesting(fixture.ctx, fixture.flowID)
					require.NoError(t, err)
					require.Zero(t, count)
				}
				fixture.assertNoRPCHistory()
			})
		}
	}
}

func TestRpcTransactionalRequestTooLargeTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, isLazyLoading := range []bool{true, false} {
		for _, transactionMode := range []string{"explicit", "locking", "synchronous-update"} {
			t.Run(fmt.Sprintf("lazy=%v/transaction=%s", isLazyLoading, transactionMode), func(t *testing.T) {
				fixture := newRPCBlobTransportFixture(t, DexServiceTestConfig{
					BackendType: service.BackendTypeTemporal, GrpcMaxMessageBytes: 2048, LocalBlobThreshold: 4096,
					LazyLoading:                            ptr.Any(isLazyLoading),
					UseTemporalSynchronousUpdateForAllRPCs: transactionMode == "synchronous-update",
				})
				_, err := fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, &dexpb.InvokeRPCRequest{
					FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload", Input: stringValue("probe"),
				})
				require.NoError(t, err)
				workerRequest, callCount := fixture.worker.lastRequest()
				inputSize := 2048 - proto.Size(workerRequest) + proto.Size(workerRequest.GetInput()) + 32
				request := &dexpb.InvokeRPCRequest{
					FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload",
					Input: stringValue(strings.Repeat("x", inputSize)), IsTransactional: transactionMode == "explicit",
				}
				if transactionMode == "locking" {
					request.LockAttributeKeys = []string{rpc.TestDataAttributeKey}
				}
				require.Less(t, proto.Size(request), 2048)
				_, err = fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, request)
				t.Logf("transactional oversized request: %v", err)
				fixture.assertSingleNonRetryableWorkerRequestFailure()
				require.Equal(t, codes.FailedPrecondition, status.Code(err))
				errorResponse := grpcServiceErrorResponse(t, err)
				require.Equal(t, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, errorResponse.GetSubStatus())
				require.Equal(t, int32(codes.ResourceExhausted), errorResponse.GetOriginalWorkerErrorStatus())
				_, afterCallCount := fixture.worker.lastRequest()
				require.Equal(t, callCount, afterCallCount)
				count, err := fixture.runtime.BlobStore.CountWorkflowObjectsForTesting(fixture.ctx, fixture.flowID)
				require.NoError(t, err)
				require.Zero(t, count)
				request.Input = stringValue("after-failure")
				request.RequestId = newRequestID()
				response, err := fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, request)
				require.NoError(t, err)
				require.True(t, proto.Equal(request.GetInput(), response.GetOutput()))
			})
		}
	}
}

func TestRpcWorkerRequestTooLargeTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testRPCWorkerRequestTooLarge(t, service.BackendTypeTemporal)
}

func TestRpcWorkerRequestTooLargeCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testRPCWorkerRequestTooLarge(t, service.BackendTypeCadence)
}

func testRPCWorkerRequestTooLarge(t *testing.T, backendType service.BackendType) {
	transactionModes := []bool{false}
	if backendType == service.BackendTypeTemporal {
		transactionModes = append(transactionModes, true)
	}
	for _, isLazyLoading := range []bool{true, false} {
		for _, isTransactional := range transactionModes {
			scenario := rpcWorkerRequestLimitScenario{isLazyLoading, isTransactional}
			t.Run(fmt.Sprintf("lazy=%v/transaction=%v", isLazyLoading, isTransactional), func(t *testing.T) {
				testRPCWorkerRequestWithReceiveLimit(t, backendType, scenario)
			})
		}
	}
}

func testRPCWorkerRequestWithReceiveLimit(t *testing.T, backendType service.BackendType, scenario rpcWorkerRequestLimitScenario) {
	t.Helper()
	fixture := newRPCBlobTransportFixture(t, DexServiceTestConfig{
		BackendType: backendType, GrpcMaxMessageBytes: 4096, LocalBlobThreshold: 4096,
		LazyLoading: ptr.Any(scenario.isLazyLoading),
	}, grpc.MaxRecvMsgSize(2048))
	request := &dexpb.InvokeRPCRequest{
		FlowId: fixture.flowID, RequestId: newRequestID(), RpcName: "getLargePayload",
		Input: stringValue("probe"), IsTransactional: scenario.isTransactional,
	}
	_, err := fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, request)
	require.NoError(t, err)
	workerRequest, callCount := fixture.worker.lastRequest()
	inputSize := 2048 - proto.Size(workerRequest) + proto.Size(workerRequest.GetInput()) + 32
	request.Input = stringValue(strings.Repeat("x", inputSize))
	request.RequestId = newRequestID()
	workerRequest.Input = request.GetInput()
	require.Greater(t, proto.Size(workerRequest), 2048)
	require.Less(t, proto.Size(workerRequest), 4096)
	_, err = fixture.runtime.FlowClient.InvokeRPC(fixture.ctx, request)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	errorResponse := grpcServiceErrorResponse(t, err)
	require.Equal(t, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR, errorResponse.GetSubStatus())
	require.Equal(t, int32(codes.ResourceExhausted), errorResponse.GetOriginalWorkerErrorStatus())
	_, afterCallCount := fixture.worker.lastRequest()
	require.Equal(t, callCount, afterCallCount)
	count, err := fixture.runtime.BlobStore.CountWorkflowObjectsForTesting(fixture.ctx, fixture.flowID)
	require.NoError(t, err)
	require.Zero(t, count)
	if !scenario.isTransactional {
		fixture.assertNoRPCHistory()
	}
}

func newRPCBlobTransportFixture(t *testing.T, cfg DexServiceTestConfig, workerOptions ...grpc.ServerOption) *rpcBlobTransportFixture {
	t.Helper()
	cfg.LocalBlobDirectory = filepath.Join(t.TempDir(), "objects")
	if cfg.LocalBlobThreshold == 0 {
		cfg.LocalBlobThreshold = 100
	}
	cfg.AsyncStepInputSnapshotsEnabled = ptr.Any(false)
	fixture := &rpcBlobTransportFixture{test: t, storeDirectory: cfg.LocalBlobDirectory}
	fixture.runtime = startDexService(t, cfg)
	baseWorker := rpc.NewHandler()
	fixture.worker = &rpcBlobTransportWorker{
		resultOnlyWaitForWorker: baseWorker, resultOnlyExecuteWorker: baseWorker,
		flowClient: fixture.runtime.FlowClient,
	}
	workerTarget := startStreamingWorker(t, &resultOnlyWorkerAdapter{handler: fixture.worker}, workerOptions...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	fixture.ctx = ctx
	fixture.flowID = "rpc-inline-" + uuid.NewString()
	response, err := fixture.runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId: fixture.flowID, RequestId: newRequestID(), FlowType: rpc.WorkflowType,
		StartStepType: rpc.State1, FlowTimeoutSeconds: 60,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	fixture.runID = response.GetRunId()
	t.Cleanup(fixture.terminateFlow)
	require.Eventually(t, fixture.isReady, 15*time.Second, 50*time.Millisecond)
	return fixture
}

func (fixture *rpcBlobTransportFixture) assertNoRPCHistory() {
	t := fixture.test
	t.Helper()
	events, _ := getAllWebHistoryEvents(t, fixture.ctx, fixture.runtime.FlowClient, fixture.flowID, fixture.runID)
	for _, event := range events {
		require.Nil(t, event.GetRpcExecutionCompleted())
	}
	if fixture.runtime.UnifiedClient.GetBackendType() != service.BackendTypeTemporal {
		return
	}
	api := fixture.runtime.UnifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	request := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: testNamespace,
		Execution: &temporalcommon.WorkflowExecution{WorkflowId: fixture.flowID, RunId: fixture.runID},
	}
	for {
		response, err := api.GetWorkflowExecutionHistory(fixture.ctx, request)
		require.NoError(t, err)
		for _, event := range response.GetHistory().GetEvents() {
			require.NotEqual(t, temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED, event.GetEventType())
			require.NotEqual(t, temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_ACCEPTED, event.GetEventType())
			require.NotEqual(t, temporalenums.EVENT_TYPE_WORKFLOW_EXECUTION_UPDATE_COMPLETED, event.GetEventType())
		}
		request.NextPageToken = response.GetNextPageToken()
		if len(request.GetNextPageToken()) == 0 {
			return
		}
	}
}

func (fixture *rpcBlobTransportFixture) assertSingleNonRetryableWorkerRequestFailure() {
	t := fixture.test
	t.Helper()
	api := fixture.runtime.UnifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	request := &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: testNamespace,
		Execution: &temporalcommon.WorkflowExecution{WorkflowId: fixture.flowID, RunId: fixture.runID},
	}
	dataConverter := dexconverter.NewTemporalDataConverter()
	failureCount := 0
	for {
		response, err := api.GetWorkflowExecutionHistory(fixture.ctx, request)
		require.NoError(t, err)
		for _, event := range response.GetHistory().GetEvents() {
			attributes := event.GetMarkerRecordedEventAttributes()
			if attributes.GetMarkerName() != "LocalActivity" || attributes.GetFailure() == nil {
				continue
			}
			var marker struct {
				ActivityType string
				Attempt      int32
			}
			require.NoError(t, dataConverter.FromPayloads(attributes.GetDetails()["data"], &marker))
			if marker.ActivityType != "IWRPC" {
				continue
			}
			failureCount++
			failure := attributes.GetFailure().GetApplicationFailureInfo()
			t.Logf("IWRPC failure: attempt=%d, failure=%v", marker.Attempt, failure)
			require.Equal(t, int32(1), marker.Attempt)
			require.True(t, failure.GetNonRetryable())
			require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(), failure.GetType())
		}
		request.NextPageToken = response.GetNextPageToken()
		if len(request.GetNextPageToken()) == 0 {
			break
		}
	}
	require.Equal(t, 1, failureCount)
}

func (fixture *rpcBlobTransportFixture) terminateFlow() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(fixture.test, fixture.runtime.UnifiedClient.TerminateWorkflow(
		ctx, fixture.flowID, "", "RPC Blob Store integration completed",
	))
}

func (fixture *rpcBlobTransportFixture) isReady() bool {
	response, err := fixture.runtime.FlowClient.GetAttributes(fixture.ctx, &dexpb.GetAttributesRequest{
		FlowId: fixture.flowID, Keys: []string{rpc.TestDataAttributeKey},
	})
	return err == nil && len(response.GetAttributes()) == 1
}

func (worker *rpcBlobTransportWorker) InvokeWorkerRPC(ctx context.Context, request *dexpb.InvokeWorkerRPCRequest) (*dexpb.InvokeWorkerRPCResponse, error) {
	worker.mu.Lock()
	worker.request = request
	worker.callCount++
	worker.mu.Unlock()
	input, err := common.LoadBlobsValue(ctx, worker.flowClient, request.GetContext().GetFlowId(), request.GetInput())
	if err != nil {
		return nil, err
	}
	return &dexpb.InvokeWorkerRPCResponse{Output: input}, nil
}

func (worker *rpcBlobTransportWorker) lastRequest() (*dexpb.InvokeWorkerRPCRequest, int) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.request, worker.callCount
}
