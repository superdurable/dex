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
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	integcommon "github.com/superdurable/dex/server-zd/integ/workflow/common"
	rpcStorage "github.com/superdurable/dex/server-zd/integ/workflow/rpc-external-storage"
	"google.golang.org/protobuf/proto"
)

func TestRpcExternalStorageNonLockingTemporal(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestRpcExternalStorage(
				t, false, false, lazyLoading,
			)
		})
	}
}

func TestRpcExternalStorageLockingTemporal(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestRpcExternalStorage(
				t, true, false, lazyLoading,
			)
		})
	}
}

func TestRpcSelectedChannelExternalStorageTemporal(t *testing.T) {
	testRPCSelectedChannelExternalStorage(t)
}

func testRPCSelectedChannelExternalStorage(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			runtime := startDexService(t, serverZDTestConfig{

				ObjectPayloadThresholdBytes: 100,
				LazyLoading:                 ptr.Any(lazyLoading),
			})
			workerHandler := rpcStorage.NewHandler(runtime.FlowClient)
			workerTarget := startWorker(t, workerHandler)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			flowID := rpcStorage.WorkflowType + "-selected-channel-" + uuid.NewString()
			_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
				RequestId:          newRequestID(),
				FlowId:             flowID,
				FlowType:           rpcStorage.WorkflowType,
				FlowTimeoutSeconds: 30,
				StartStepType:      rpcStorage.State1,
				StepInput:          objJSONValue(`"start-input"`),
				FlowStartOptions:   withWorkerTarget(nil, workerTarget),
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				response, getErr := runtime.FlowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
					FlowId: flowID,
					Keys:   []string{rpcStorage.LargeDataKey},
				})
				return getErr == nil && len(response.GetAttributes()) == 1
			}, 5*time.Second, 50*time.Millisecond)

			_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
				FlowId: flowID,
				Messages: []*dexpb.ChannelMessage{{
					ChannelName: rpcStorage.LargeChannel,
					Value:       rpcStorage.InitialLargeData,
				}},
			})
			require.NoError(t, err)
			waitForChannelMessages(t, ctx, runtime, flowID, rpcStorage.LargeChannel, 1)
			_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
				RequestId:        newRequestID(),
				FlowId:           flowID,
				RpcName:          rpcStorage.UpdateDataAttributesRPC,
				Input:            rpcStorage.TestInput,
				TimeoutSeconds:   10,
				LoadChannelNames: []string{rpcStorage.LargeChannel},
			})
			require.NoError(t, err)

			testData := workerHandler.GetTestResult().InvokeData
			rawChannels := testData[rpcStorage.UpdateDataAttributesRPC+"-raw-channels"].(map[string]*dexpb.ChannelValues)
			rawMessage := rawChannels[rpcStorage.LargeChannel].GetMessages()[0]
			require.NotEmpty(t, rawMessage.GetMessageId())
			require.Equal(t, lazyLoading, integcommon.BlobIdFromValue(rawMessage.GetValue()) != "")
			resolvedChannels := testData[rpcStorage.UpdateDataAttributesRPC+"-received-channels"].(map[string]*dexpb.ChannelValues)
			resolvedMessage := resolvedChannels[rpcStorage.LargeChannel].GetMessages()[0]
			require.Equal(t, rawMessage.GetMessageId(), resolvedMessage.GetMessageId())
			require.True(t, proto.Equal(rpcStorage.InitialLargeData, resolvedMessage.GetValue()))
			_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
				FlowId:          flowID,
				WaitTimeSeconds: 5,
			})
			require.NoError(t, err)
		})
	}
}

func doTestRpcExternalStorage(
	t *testing.T,
	useLocking bool,
	useSynchronousUpdate bool,
	lazyLoading bool,
) {
	testConfig := serverZDTestConfig{

		ObjectPayloadThresholdBytes: 100,
		LazyLoading:                 ptr.Any(lazyLoading),
	}
	// server-zd has no async Step input snapshots (C2, C5).

	runtime := startDexService(t, testConfig)
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
		Input:          rpcStorage.TestInput,
		TimeoutSeconds: 10,
	}
	if useLocking {
		rpcRequest.LockAttributeKeys = []string{
			rpcStorage.SmallDataKey,
			rpcStorage.LargeDataKey,
		}
		rpcRequest.RequestId = uuid.NewString()
	}
	// server-zd has no Go Blob Store to count (C5).

	rpcResp, err := flowClient.InvokeRPC(ctx, rpcRequest)
	require.NoError(t, err)
	rpcOutput := rpcResp.GetOutput()
	// server-zd returns the RPC output to the client inline.
	require.Equal(
		t,
		false,
		integcommon.BlobIdFromValue(rpcOutput) != "",
	)
	rpcOutput, err = integcommon.LoadBlobsValue(ctx, flowClient, flowId, rpcOutput)
	require.NoError(t, err)
	require.True(t, proto.Equal(rpcStorage.TestOutput, rpcOutput))

	require.Eventually(t, func() bool {
		testData := workerHandler.GetTestResult().InvokeData
		_, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-received-data"]
		return exists
	}, 2*time.Second, 50*time.Millisecond)

	testData := workerHandler.GetTestResult().InvokeData
	rawInput, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-raw-input"]
	require.True(t, exists)
	// server-zd passes the client's RPC input to the Worker inline.
	require.Equal(
		t,
		false,
		integcommon.BlobIdFromValue(rawInput.(*dexpb.Value)) != "",
	)
	receivedInput, exists := testData[rpcStorage.UpdateDataAttributesRPC+"-input"]
	require.True(t, exists)
	require.True(t, proto.Equal(rpcStorage.TestInput, receivedInput.(*dexpb.Value)))
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
	// server-zd keeps large values in its log, and its history events return them inline (C5).

	// server-zd records every RPC with effects as an RpcExecutionCompleted event and has no Temporal history.

	return

}

func hasObjPayload(value *dexpb.Value) bool {
	if value == nil {
		return false
	}
	return value.GetObjValue() != nil && len(value.GetObjValue().GetPayload()) > 0
}
