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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/rpc"
	"github.com/superdurable/iwf/service"
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
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
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
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func TestRpcWorkflowTemporalContinueAsNewWithMemoAndEncryption(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestRpcWorkflow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
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
		doTestRpcWorkflow(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func doTestRpcWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	assertions := assert.New(t)

	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:    backendType,
		MemoEncryption: false,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpc.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           rpc.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      rpc.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	rpcRespReadOnly, err := flowClient.InvokeRPC(ctx, &iwfpb.InvokeRPCRequest{
		FlowId:         flowId,
		RpcName:        rpc.RPCNameReadOnly,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	_, err = flowClient.InvokeRPC(ctx, &iwfpb.InvokeRPCRequest{
		FlowId:         flowId,
		RpcName:        rpc.RPCNameError,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	workerErr := workerErrorFromStatus(t, err)
	assertions.Equal(rpc.WorkerApiErrorDetails, workerErr.GetDetail())
	assertions.Equal(rpc.WorkerApiErrorType, workerErr.GetErrorType())

	rpcResp, err := flowClient.InvokeRPC(ctx, &iwfpb.InvokeRPCRequest{
		FlowId:         flowId,
		RpcName:        rpc.RPCName,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
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

	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCName+"-input"].(*iwfpb.Value)))
	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCNameReadOnly+"-input"].(*iwfpb.Value)))
	assertions.True(proto.Equal(rpc.TestInput, data[rpc.RPCNameError+"-input"].(*iwfpb.Value)))
	assertions.True(proto.Equal(
		rpc.TestInterstateChannelValue,
		data[rpc.TestInterStateChannelName].(*iwfpb.Value),
	))

	attributesResp, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
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

func workerErrorFromStatus(t *testing.T, err error) *iwfpb.WorkerErrorResponse {
	t.Helper()
	statusError, ok := status.FromError(err)
	require.True(t, ok)
	for _, detail := range statusError.Details() {
		if workerErr, ok := detail.(*iwfpb.WorkerErrorResponse); ok {
			return workerErr
		}
	}
	require.FailNow(t, "gRPC error has no WorkerErrorResponse details", err)
	return nil
}

func attributesToMap(attributes []*iwfpb.KV) map[string]*iwfpb.Value {
	result := make(map[string]*iwfpb.Value, len(attributes))
	for _, attribute := range attributes {
		result[attribute.GetKey()] = attribute.GetValue()
	}
	return result
}
