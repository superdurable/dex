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
	s3_init_data_attributes "github.com/superdurable/dex/integ/workflow/s3-init-data-attributes"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
	"google.golang.org/protobuf/proto"
)

func TestS3WorkflowInitDataAttributesTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3InitDataAttributes(t, service.BackendTypeTemporal, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func TestS3WorkflowInitDataAttributesCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3InitDataAttributes(t, service.BackendTypeCadence, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func doTestWorkflowWithS3InitDataAttributes(t *testing.T, backendType service.BackendType, lazyLoading bool) {
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
		LazyLoading:     ptr.Any(lazyLoading),
	})
	workerHandler := s3_init_data_attributes.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_init_data_attributes.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           s3_init_data_attributes.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType: s3_init_data_attributes.State1,
		StepInput:     objJSONValue(`"test"`),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{
				{
					Key:   s3_init_data_attributes.TestDataAttrKey1,
					Value: s3_init_data_attributes.TestDataAttributeVal1,
				},
				{
					Key:   s3_init_data_attributes.TestDataAttrKey2,
					Value: s3_init_data_attributes.TestDataAttributeVal2,
				},
				{
					Key:   s3_init_data_attributes.TestDataAttrKey3,
					Value: s3_init_data_attributes.TestDataAttributeVal3,
				},
			},
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, true, history["S1_waitFor_attr1_found"])
	require.Equal(t, true, history["S1_waitFor_attr2_found"])
	require.Equal(t, true, history["S1_waitFor_attr3_found"])
	require.Equal(t, 3, history["S1_waitFor_total_attrs"])
	require.Equal(t, s3_init_data_attributes.LargeDataContent1, history["S1_waitFor_attr1_data"])
	require.Equal(t, s3_init_data_attributes.LargeDataContent2, history["S1_waitFor_attr2_data"])
	require.Equal(t, s3_init_data_attributes.SmallDataContent3, history["S1_waitFor_attr3_data"])
	require.Equal(t, true, history["S1_waitFor_validation_pass"])
	require.Equal(t, int64(1), history["S1_execute"])
	require.Equal(t, int64(1), history["S2_waitFor"])
	require.Equal(t, int64(1), history["S2_execute"])
	require.Equal(t, true, history["S2_execute_attr1_found"])
	require.Equal(t, true, history["S2_execute_attr2_found"])
	require.Equal(t, true, history["S2_execute_attr3_found"])
	require.Equal(t, 3, history["S2_execute_total_attrs"])
	require.Equal(t, true, history["S2_execute_validation_pass"])

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(2), objectCount)

	getResult, err := flowClient.GetAttributes(ctx, &dexpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			s3_init_data_attributes.TestDataAttrKey1,
			s3_init_data_attributes.TestDataAttrKey2,
			s3_init_data_attributes.TestDataAttrKey3,
		},
	})
	require.NoError(t, err)
	retrieved := attributeMap(getResult.GetAttributes())
	blobId1 := blobIdFromValue(retrieved[s3_init_data_attributes.TestDataAttrKey1])
	blobId2 := blobIdFromValue(retrieved[s3_init_data_attributes.TestDataAttrKey2])
	if lazyLoading {
		require.NotEmpty(t, blobId1)
		require.NotEmpty(t, blobId2)
		require.Empty(t, blobIdFromValue(retrieved[s3_init_data_attributes.TestDataAttrKey3]))
	} else {
		require.Empty(t, blobId1)
		require.Empty(t, blobId2)
		require.True(t, proto.Equal(s3_init_data_attributes.TestDataAttributeVal1, retrieved[s3_init_data_attributes.TestDataAttrKey1]))
		require.True(t, proto.Equal(s3_init_data_attributes.TestDataAttributeVal2, retrieved[s3_init_data_attributes.TestDataAttrKey2]))
		require.True(t, proto.Equal(s3_init_data_attributes.TestDataAttributeVal3, retrieved[s3_init_data_attributes.TestDataAttrKey3]))
	}

	if backendType == service.BackendTypeTemporal {
		historyBlobIds := []string{blobId1, blobId2}
		if !lazyLoading {
			historyBlobIds = []string{"s3-store-id|"}
		}
		requireTemporalHistoryStoresBlobIdsNotPayloads(
			t,
			ctx,
			runtime.UnifiedClient,
			flowId,
			historyBlobIds,
			[]string{
				s3_init_data_attributes.LargeDataContent1,
				s3_init_data_attributes.LargeDataContent2,
			},
		)
	}
}
