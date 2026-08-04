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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	rpcStorage "github.com/superdurable/dex/integ/workflow/rpc-external-storage"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestRpcExternalStorageNonLockingTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestRpcExternalStorage(t, service.BackendTypeTemporal, false, lazyLoading)
		})
	}
}

func TestRpcExternalStorageSynchronousUpdateTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestRpcExternalStorage(t, service.BackendTypeTemporal, true, lazyLoading)
		})
	}
}

func TestRpcExternalStorageNonLockingCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestRpcExternalStorage(t, service.BackendTypeCadence, false, lazyLoading)
		})
	}
}

func doTestRpcExternalStorage(t *testing.T, backendType service.BackendType, useLocking bool, lazyLoading bool) {
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 100,
		LazyLoading:     ptr.Any(lazyLoading),
	})
	workerHandler := rpcStorage.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpcStorage.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           rpcStorage.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType:    rpcStorage.State1,
		StepInput:        objJSONValue(`"start-input"`),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		getResult, getErr := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
			FlowId: flowId,
			Keys:   []string{rpcStorage.SmallDataKey, rpcStorage.LargeDataKey},
		})
		return getErr == nil && len(getResult.GetAttributes()) == 2
	}, 5*time.Second, 50*time.Millisecond)

	rpcRequest := &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpcStorage.UpdateDataAttributesRPC,
		Input:          objJSONValue(`"rpc-input"`),
		TimeoutSeconds: 10,
	}
	if useLocking {
		rpcRequest.LockAttributeKeys = []string{
			rpcStorage.SmallDataKey,
			rpcStorage.LargeDataKey,
		}
		rpcRequest.RequestId = uuid.NewString()
	}

	rpcResp, err := flowClient.InvokeRPC(ctx, rpcRequest)
	require.NoError(t, err)
	require.True(t, proto.Equal(rpcStorage.TestOutput, rpcResp.GetOutput()))

	require.Eventually(t, func() bool {
		testData := workerHandler.GetTestResult().InvokeData
		_, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-received-data"]
		return exists
	}, 2*time.Second, 50*time.Millisecond)

	testData := workerHandler.GetTestResult().InvokeData
	rpcInputData, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-received-data"]
	require.True(t, exists)

	receivedAttributes, ok := rpcInputData.([]*dexpb.KV)
	require.True(t, ok)

	receivedDataMap := make(map[string]*dexpb.Value)
	for _, attribute := range receivedAttributes {
		receivedDataMap[attribute.GetKey()] = attribute.GetValue()
	}

	smallValue, exists := receivedDataMap[rpcStorage.SmallDataKey]
	require.True(t, exists)
	require.True(t, hasObjPayload(smallValue))

	largeValue, exists := receivedDataMap[rpcStorage.LargeDataKey]
	require.True(t, exists)
	require.True(t, hasObjPayload(largeValue))

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 5,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

func hasObjPayload(value *dexpb.Value) bool {
	if value == nil {
		return false
	}
	return value.GetObjValue() != nil && len(value.GetObjValue().GetPayload()) > 0
}
