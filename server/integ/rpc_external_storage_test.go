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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	rpcStorage "github.com/superdurable/iwf/integ/workflow/rpc-external-storage"
	"github.com/superdurable/iwf/service"
	"google.golang.org/protobuf/proto"
)

func TestRpcExternalStorageNonLockingTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	doTestRpcExternalStorage(t, service.BackendTypeTemporal, false)
}

func TestRpcExternalStorageSynchronousUpdateTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	doTestRpcExternalStorage(t, service.BackendTypeTemporal, true)
}

func TestRpcExternalStorageNonLockingCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	doTestRpcExternalStorage(t, service.BackendTypeCadence, false)
}

func doTestRpcExternalStorage(t *testing.T, backendType service.BackendType, useLocking bool) {
	workerHandler := rpcStorage.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 100,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpcStorage.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           rpcStorage.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      rpcStorage.State1,
		StepInput:          objJSONValue(`"start-input"`),
	})
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	rpcRequest := &iwfpb.InvokeRPCRequest{
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

	time.Sleep(100 * time.Millisecond)

	testData := workerHandler.GetTestResult().InvokeData
	rpcInputData, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-received-data"]
	require.True(t, exists)

	receivedAttributes, ok := rpcInputData.([]*iwfpb.KV)
	require.True(t, ok)

	receivedDataMap := make(map[string]*iwfpb.Value)
	for _, attribute := range receivedAttributes {
		receivedDataMap[attribute.GetKey()] = attribute.GetValue()
	}

	smallValue, exists := receivedDataMap[rpcStorage.SmallDataKey]
	require.True(t, exists)
	require.True(t, hasObjPayload(smallValue))

	largeValue, exists := receivedDataMap[rpcStorage.LargeDataKey]
	require.True(t, exists)
	require.True(t, hasObjPayload(largeValue))

	resp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 5,
	})
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

func hasObjPayload(value *iwfpb.Value) bool {
	if value == nil {
		return false
	}
	return value.GetObjValue() != nil && len(value.GetObjValue().GetPayload()) > 0
}
