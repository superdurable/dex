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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/integ/workflow/rpc"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRpcWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalWithMemo(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalWithMemoAndEncryption(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalContinueAsNewWithMemo(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalContinueAsNewWithMemoAndEncryption(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestRpcLockingUnimplementedCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	runtime := startDexService(t, DexServiceTestConfig{BackendType: service.BackendTypeCadence})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:         newRequestID(),
		FlowId:            "cadence-locking-rpc-" + uuid.NewString(),
		RpcName:           rpc.RPCName,
		LockAttributeKeys: []string{"lock-key"},
	})
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func doTestRpcWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
	configureTest ...func(*DexServiceTestConfig),
) {
	assertions := assert.New(t)

	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	testConfig := DexServiceTestConfig{
		BackendType:    backendType,
		MemoEncryption: false,
	}
	for _, configure := range configureTest {
		configure(&testConfig)
	}
	runtime := startDexService(t, testConfig)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpc.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           rpc.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: rpc.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
			Attributes: []*dexpb.AttributeWrite{
				{Key: "ordinary", Value: stringValue("ordinary")},
				{Key: "selected-map/one", Value: stringValue("selected")},
				{Key: "other-map/two", Value: stringValue("other")},
			},
		}, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	testRPCSelectiveStateLoading(t, ctx, runtime, flowId, backendType, workerHandler)
	testInvalidRPCStateSelectors(t, ctx, flowClient, flowId)

	rpcRespReadOnly, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpc.RPCNameReadOnly,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)
	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpc.RPCNameError,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	assertions.Equal(rpc.WorkerApiErrorDetails, status.Convert(err).Message())
	errResp := grpcServiceErrorResponse(t, err)
	assertions.Equal(
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		errResp.GetSubStatus(),
	)
	assertions.Empty(errResp.GetDetail())
	assertions.Equal(rpc.WorkerApiErrorDetails, errResp.GetOriginalWorkerErrorDetail())
	assertions.Equal(rpc.WorkerApiErrorType, errResp.GetOriginalWorkerErrorType())
	assertions.Equal(int32(codes.Unavailable), errResp.GetOriginalWorkerErrorStatus())

	rpcResp, err := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpc.RPCName,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions.Equalf(map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 2,
		"S2_execute": 2,
	}, history, "rpc test fail, %v", history)

	assertions.True(proto.Equal(rpc.TestOutput, rpcResp.GetOutput()))
	assertions.True(proto.Equal(rpc.TestOutput, rpcRespReadOnly.GetOutput()))

	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCName+"-input"].(*dexpb.Value)))
	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCNameReadOnly+"-input"].(*dexpb.Value)))
	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCNameError+"-input"].(*dexpb.Value)))
	assertions.True(proto.Equal(
		rpc.TestInterstateChannelValue,
		data[rpc.TestInterStateChannelName].(*dexpb.Value),
	))

	attributesResp, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId:  flowId,
		AllKeys: true,
	})
	require.NoError(t, err)

	attributeMap := attributesToMap(attributesResp.GetAttributes())
	assertions.True(proto.Equal(rpc.TestDataAttributeVal2, attributeMap[rpc.TestDataAttributeKey]))
	assertions.Equal(int64(rpc.TestSearchAttributeIntValue2), attributeMap[rpc.TestSearchAttributeIntKey].GetIntValue())
	assertions.Equal(rpc.TestSearchAttributeKeywordValue2, attributeMap[rpc.TestSearchAttributeKeywordKey].GetStringValue())
	assertions.Equal(false, attributeMap[rpc.TestSearchAttributeBoolKey].GetBoolValue())
}

func testRPCSelectiveStateLoading(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	backendType service.BackendType,
	workerHandler interface{ GetTestResult() common.TestResult },
) {
	flowClient := runtime.FlowClient
	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{
			{ChannelName: "selected-channel", Value: stringValue("first")},
			{ChannelName: "selected-channel", Value: stringValue("second")},
			{ChannelName: "other-channel", Value: stringValue("other")},
			{ChannelName: "selected-channel-map/one", Value: stringValue("map-one")},
			{ChannelName: "selected-channel-map/two", Value: stringValue("map-two")},
			{ChannelName: "other-channel-map/one", Value: stringValue("other-map")},
		},
	})
	require.NoError(t, err)
	waitForChannelMessages(t, ctx, runtime, flowID, "selected-channel", 2)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:             newRequestID(),
		FlowId:                flowID,
		RpcName:               rpc.RPCNameSnapshot,
		TimeoutSeconds:        2,
		LoadAttributeMapNames: []string{"selected-map", "empty-map"},
		LoadChannelNames:      []string{"selected-empty", "selected-channel"},
		LoadChannelMapNames:   []string{"selected-empty-map", "selected-channel-map"},
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowID,
		RpcName:        rpc.RPCNameSnapshotDefault,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	if backendType == service.BackendTypeTemporal {
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId:       newRequestID(),
			FlowId:          flowID,
			RpcName:         rpc.RPCNameSnapshotTransactional,
			TimeoutSeconds:  2,
			IsTransactional: true,
		})
		require.NoError(t, err)
		_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId:         newRequestID(),
			FlowId:            flowID,
			RpcName:           rpc.RPCNameSnapshotLocked,
			TimeoutSeconds:    2,
			LockAttributeKeys: []string{"ordinary"},
		})
		require.NoError(t, err)
	}

	data := workerHandler.GetTestResult().InvokeData
	selected := data[rpc.RPCNameSnapshot+"-request"].(*dexpb.InvokeWorkerRPCRequest)
	require.Equal(t, []string{"empty-map", "selected-map"}, selected.GetLoadedAttributeMapNames())
	require.Equal(t, []string{"selected-channel", "selected-empty"}, selected.GetLoadedChannelNames())
	require.Equal(t, []string{"selected-channel-map", "selected-empty-map"}, selected.GetLoadedChannelMapNames())
	selectedAttributes := attributesToMap(selected.GetAttributes())
	require.Equal(t, "ordinary", selectedAttributes["ordinary"].GetStringValue())
	require.Equal(t, "selected", selectedAttributes["selected-map/one"].GetStringValue())
	require.NotContains(t, selectedAttributes, "other-map/two")
	require.Contains(t, selected.GetChannelInfos(), "selected-channel")
	require.Contains(t, selected.GetChannelInfos(), "other-channel")
	require.Equal(t, int32(2), selected.GetChannelInfos()["selected-channel"].GetSize())
	require.Equal(t, int32(1), selected.GetChannelInfos()["other-channel"].GetSize())

	loaded := selected.GetLoadedChannelMessages()
	require.Contains(t, loaded, "selected-empty")
	require.Empty(t, loaded["selected-empty"].GetMessages())
	require.NotContains(t, loaded, "other-channel")
	require.NotContains(t, loaded, "other-channel-map/one")
	require.Equal(t, []string{"first", "second"}, channelStrings(loaded["selected-channel"]))
	require.Equal(t, []string{"map-one"}, channelStrings(loaded["selected-channel-map/one"]))
	require.Equal(t, []string{"map-two"}, channelStrings(loaded["selected-channel-map/two"]))
	for _, values := range loaded {
		for _, message := range values.GetMessages() {
			require.NotEmpty(t, message.GetMessageId())
		}
	}

	defaultRequest := data[rpc.RPCNameSnapshotDefault+"-request"].(*dexpb.InvokeWorkerRPCRequest)
	defaultAttributes := attributesToMap(defaultRequest.GetAttributes())
	require.Equal(t, "ordinary", defaultAttributes["ordinary"].GetStringValue())
	require.NotContains(t, defaultAttributes, "selected-map/one")
	require.NotContains(t, defaultAttributes, "other-map/two")
	require.Empty(t, defaultRequest.GetLoadedChannelMessages())
	require.Contains(t, defaultRequest.GetChannelInfos(), "selected-channel")

	if backendType == service.BackendTypeTemporal {
		transactional := data[rpc.RPCNameSnapshotTransactional+"-request"].(*dexpb.InvokeWorkerRPCRequest)
		locked := data[rpc.RPCNameSnapshotLocked+"-request"].(*dexpb.InvokeWorkerRPCRequest)
		require.NotContains(t, attributesToMap(transactional.GetAttributes()), "selected-map/one")
		require.NotContains(t, attributesToMap(locked.GetAttributes()), "selected-map/one")
		require.Empty(t, transactional.GetLoadedChannelMessages())
		require.Empty(t, locked.GetLoadedChannelMessages())
	}
}

func channelStrings(values *dexpb.ChannelValues) []string {
	result := make([]string, 0, len(values.GetMessages()))
	for _, message := range values.GetMessages() {
		result = append(result, message.GetValue().GetStringValue())
	}
	return result
}

func testInvalidRPCStateSelectors(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) {
	testCases := []struct {
		name    string
		request *dexpb.InvokeRPCRequest
	}{
		{
			name: "empty",
			request: &dexpb.InvokeRPCRequest{
				LoadAttributeMapNames: []string{" "},
			},
		},
		{
			name: "physical-channel-name",
			request: &dexpb.InvokeRPCRequest{
				LoadChannelMapNames: []string{"mapped/one"},
			},
		},
		{
			name: "duplicate",
			request: &dexpb.InvokeRPCRequest{
				LoadChannelNames: []string{"queued", "queued"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.request.RequestId = newRequestID()
			testCase.request.FlowId = flowID
			testCase.request.RpcName = rpc.RPCNameSnapshot
			testCase.request.TimeoutSeconds = 2
			_, err := flowClient.InvokeRPC(ctx, testCase.request)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestRpcLockingErrorMappingTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcLockingErrorMapping(t)
		smallWaitForFastTest()
	}
}

func doTestRpcLockingErrorMapping(t *testing.T) {
	assertions := assert.New(t)

	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: service.BackendTypeTemporal,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpc.WorkflowType + "-locking-err-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           rpc.WorkflowType,
		FlowTimeoutSeconds: 20,

		StartStepType:    rpc.State1,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:            flowId,
		RpcName:           rpc.RPCNameError,
		Input:             rpc.TestInput,
		TimeoutSeconds:    2,
		RequestId:         uuid.NewString(),
		LockAttributeKeys: []string{"unused-lock-key"},
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	assertions.Equal(rpc.WorkerApiErrorDetails, status.Convert(err).Message())
	workerFail := grpcServiceErrorResponse(t, err)
	assertions.Equal(
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		workerFail.GetSubStatus(),
	)
	assertions.Empty(workerFail.GetDetail())
	assertions.Equal(rpc.WorkerApiErrorDetails, workerFail.GetOriginalWorkerErrorDetail())
	assertions.Equal(rpc.WorkerApiErrorType, workerFail.GetOriginalWorkerErrorType())
	assertions.Equal(int32(codes.Unavailable), workerFail.GetOriginalWorkerErrorStatus())

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		FlowId:            flowId,
		RpcName:           rpc.RPCNameReadOnly,
		Input:             rpc.TestInput,
		TimeoutSeconds:    2,
		RequestId:         uuid.NewString(),
		LockAttributeKeys: []string{""},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	invalidArg := grpcServiceErrorResponse(t, err)
	assertions.Equal(
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED,
		invalidArg.GetSubStatus(),
	)
	assertions.Equal("lock attribute key is empty", invalidArg.GetDetail())

	_, err = flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
}

func attributesToMap(attributes []*dexpb.KV) map[string]*dexpb.Value {
	result := make(map[string]*dexpb.Value, len(attributes))
	for _, attribute := range attributes {
		result[attribute.GetKey()] = attribute.GetValue()
	}
	return result
}
