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
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/signal"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
)

func TestLargeDataAttributesTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestLargeQueryAttributes(t, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func doTestLargeQueryAttributes(t *testing.T, flowConfig *iwfpb.FlowConfig) {
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("lazy=%v", lazyLoading), func(t *testing.T) {
			doTestLargeQueryAttributesLazy(t, flowConfig, lazyLoading)
		})
	}
}

func doTestLargeQueryAttributesLazy(t *testing.T, flowConfig *iwfpb.FlowConfig, lazyLoading bool) {
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     service.BackendTypeTemporal,
		S3TestThreshold: 100 * 1024,
		LazyLoading:     ptr.Any(lazyLoading),
	})
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := signal.WorkflowType + uuid.NewString()
	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 86400,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	const size = 1024 * 1024
	oneMbPayload := strings.Repeat("a", size)

	keys := make([]string, 5)
	for i := 0; i < 5; i++ {
		keys[i] = "large-data-attribute-" + fmt.Sprintf("%d", i)
		_, err = flowClient.SetAttributes(ctx, &iwfpb.SetAttributesRequest{
			FlowId: flowId,
			Attributes: []*iwfpb.AttributeWrite{
				dataObjectAttribute(keys[i], `"`+oneMbPayload+`"`),
			},
		})
		require.NoError(t, err)
	}

	for i := 0; i < 4; i++ {
		_, err = flowClient.PublishToChannel(ctx, &iwfpb.PublishToChannelRequest{
			FlowId: flowId,
			Messages: []*iwfpb.ChannelMessage{
				{
					ChannelName: signal.SignalName,
					Value:       objJSONValue(`"` + fmt.Sprintf("test-data-%v", i) + `"`),
				},
			},
		})
		require.NoError(t, err)
	}

	resp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())

	getResult, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   keys,
	})
	require.NoError(t, err)
	require.Len(t, getResult.GetAttributes(), 5)

	retrieved := attributeMap(getResult.GetAttributes())
	blobIds := make([]string, 0, 5)
	for _, key := range keys {
		if lazyLoading {
			blobId := blobIdFromValue(retrieved[key])
			require.NotEmpty(t, blobId, key)
			blobIds = append(blobIds, blobId)
		} else {
			require.Empty(t, blobIdFromValue(retrieved[key]), key)
			require.NotNil(t, retrieved[key].GetObjValue())
		}
	}

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(5), objectCount)

	historyBlobIds := blobIds
	if !lazyLoading {
		historyBlobIds = []string{"s3-store-id|"}
	}
	requireTemporalHistoryStoresBlobIdsNotPayloads(
		t,
		ctx,
		runtime.UnifiedClient,
		flowId,
		historyBlobIds,
		[]string{oneMbPayload},
	)
}
