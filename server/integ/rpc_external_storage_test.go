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
		FlowId:             flowId,
		FlowType:           rpcStorage.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      rpcStorage.State1,
		StepInput:          objJSONValue(`"start-input"`),
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
