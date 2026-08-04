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

func doTestRpcWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	assertions := assert.New(t)

	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:    backendType,
		MemoEncryption: false,
	})
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
		}, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

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
	errResp := grpcErrorResponse(t, err)
	assertions.Equal(
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		errResp.GetSubStatus(),
	)
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
	workerFail := grpcErrorResponse(t, err)
	assertions.Equal(
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		workerFail.GetSubStatus(),
	)
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
	invalidArg := grpcErrorResponse(t, err)
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
