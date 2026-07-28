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
	s3GetSetDataAttributes "github.com/superdurable/iwf/integ/workflow/s3-get-set-data-attributes"
	"github.com/superdurable/iwf/service"
	"google.golang.org/protobuf/proto"
)

func TestS3GetSetDataAttributesTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestS3GetSetDataAttributes(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestS3GetSetDataAttributesCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestS3GetSetDataAttributes(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func TestS3GetSetDataAttributesWithInitialDataTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestS3GetSetDataAttributesWithInitialData(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func doTestS3GetSetDataAttributes(t *testing.T, backendType service.BackendType) {
	workerHandler := s3GetSetDataAttributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 50,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3GetSetDataAttributes.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           s3GetSetDataAttributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      s3GetSetDataAttributes.State1,
		StepInput:          objJSONValue(`"test-input"`),
	})
	require.NoError(t, err)

	testAttributes := []*iwfpb.AttributeWrite{
		{Key: s3GetSetDataAttributes.SmallDataKey, Value: s3GetSetDataAttributes.SmallDataValue},
		{Key: s3GetSetDataAttributes.LargeDataKey, Value: s3GetSetDataAttributes.LargeDataValue},
		{Key: s3GetSetDataAttributes.AnotherLargeDataKey, Value: s3GetSetDataAttributes.AnotherLargeDataValue},
	}
	_, err = flowClient.SetAttributes(ctx, &iwfpb.SetAttributesRequest{
		FlowId:     flowId,
		Attributes: testAttributes,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		getResult, getErr := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
			FlowId: flowId,
			Keys: []string{
				s3GetSetDataAttributes.SmallDataKey,
				s3GetSetDataAttributes.LargeDataKey,
				s3GetSetDataAttributes.AnotherLargeDataKey,
			},
		})
		if getErr != nil || len(getResult.GetAttributes()) != 3 {
			return false
		}
		retrieved := attributeMap(getResult.GetAttributes())
		return blobIdFromValue(retrieved[s3GetSetDataAttributes.LargeDataKey]) != "" &&
			blobIdFromValue(retrieved[s3GetSetDataAttributes.AnotherLargeDataKey]) != ""
	}, 10*time.Second, 100*time.Millisecond)

	getResult, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys: []string{
			s3GetSetDataAttributes.SmallDataKey,
			s3GetSetDataAttributes.LargeDataKey,
			s3GetSetDataAttributes.AnotherLargeDataKey,
		},
	})
	require.NoError(t, err)
	require.Len(t, getResult.GetAttributes(), 3)

	retrieved := attributeMap(getResult.GetAttributes())
	require.True(t, proto.Equal(s3GetSetDataAttributes.SmallDataValue, retrieved[s3GetSetDataAttributes.SmallDataKey]))
	largeBlobId := blobIdFromValue(retrieved[s3GetSetDataAttributes.LargeDataKey])
	anotherBlobId := blobIdFromValue(retrieved[s3GetSetDataAttributes.AnotherLargeDataKey])
	require.NotEmpty(t, largeBlobId)
	require.NotEmpty(t, anotherBlobId)
	require.Nil(t, retrieved[s3GetSetDataAttributes.LargeDataKey].GetObjValue())
	require.Nil(t, retrieved[s3GetSetDataAttributes.AnotherLargeDataKey].GetObjValue())

	requireLoadedBlobPayload(
		t,
		ctx,
		flowClient,
		largeBlobId,
		string(s3GetSetDataAttributes.LargeDataValue.GetObjValue().GetPayload()),
	)
	requireLoadedBlobPayload(
		t,
		ctx,
		flowClient,
		anotherBlobId,
		string(s3GetSetDataAttributes.AnotherLargeDataValue.GetObjValue().GetPayload()),
	)

	getSpecific, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   []string{s3GetSetDataAttributes.LargeDataKey},
	})
	require.NoError(t, err)
	require.Len(t, getSpecific.GetAttributes(), 1)
	require.Equal(t, s3GetSetDataAttributes.LargeDataKey, getSpecific.GetAttributes()[0].GetKey())
	require.Equal(t, largeBlobId, blobIdFromValue(getSpecific.GetAttributes()[0].GetValue()))

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(2), objectCount)

	if backendType == service.BackendTypeTemporal {
		requireTemporalHistoryStoresBlobIdsNotPayloads(
			t,
			ctx,
			runtime.UnifiedClient,
			flowId,
			[]string{largeBlobId, anotherBlobId},
			[]string{
				s3GetSetDataAttributes.LargeDataContent,
				s3GetSetDataAttributes.AnotherLargeDataContent,
			},
		)
	}

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)
}

func doTestS3GetSetDataAttributesWithInitialData(t *testing.T, backendType service.BackendType) {
	workerHandler := s3GetSetDataAttributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 50,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3GetSetDataAttributes.WorkflowType + uuid.NewString() + "-initial"
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           s3GetSetDataAttributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      s3GetSetDataAttributes.State1,
		StepInput:          objJSONValue(`"test-input"`),
		FlowStartOptions: &iwfpb.FlowStartOptions{
			Attributes: []*iwfpb.AttributeWrite{
				{Key: "initial-small", Value: s3GetSetDataAttributes.SmallDataValue},
				{Key: "initial-large", Value: s3GetSetDataAttributes.LargeDataValue},
			},
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		getResult, getErr := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
			FlowId: flowId,
			Keys:   []string{"initial-small", "initial-large"},
		})
		if getErr != nil || len(getResult.GetAttributes()) != 2 {
			return false
		}
		retrieved := attributeMap(getResult.GetAttributes())
		return blobIdFromValue(retrieved["initial-large"]) != ""
	}, 10*time.Second, 100*time.Millisecond)

	getResult, err := flowClient.GetAttributes(ctx, &iwfpb.GetAttributesRequest{
		FlowId: flowId,
		Keys:   []string{"initial-small", "initial-large"},
	})
	require.NoError(t, err)
	require.Len(t, getResult.GetAttributes(), 2)

	retrieved := attributeMap(getResult.GetAttributes())
	require.True(t, proto.Equal(s3GetSetDataAttributes.SmallDataValue, retrieved["initial-small"]))
	largeBlobId := blobIdFromValue(retrieved["initial-large"])
	require.NotEmpty(t, largeBlobId)
	require.Nil(t, retrieved["initial-large"].GetObjValue())
	requireLoadedBlobPayload(
		t,
		ctx,
		flowClient,
		largeBlobId,
		string(s3GetSetDataAttributes.LargeDataValue.GetObjValue().GetPayload()),
	)

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(1), objectCount)

	if backendType == service.BackendTypeTemporal {
		requireTemporalHistoryStoresBlobIdsNotPayloads(
			t,
			ctx,
			runtime.UnifiedClient,
			flowId,
			[]string{largeBlobId},
			[]string{s3GetSetDataAttributes.LargeDataContent},
		)
	}

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)
}

func attributeMap(attributes []*iwfpb.KV) map[string]*iwfpb.Value {
	result := make(map[string]*iwfpb.Value, len(attributes))
	for _, attribute := range attributes {
		result[attribute.GetKey()] = attribute.GetValue()
	}
	return result
}

func blobIdFromValue(value *iwfpb.Value) string {
	if value == nil {
		return ""
	}
	if blobId := value.GetInternalBlobIdForObjValue(); blobId != "" {
		return blobId
	}
	return value.GetInternalBlobIdForStringValue()
}

func requireLoadedBlobPayload(
	t *testing.T,
	ctx context.Context,
	flowClient iwfpb.FlowServiceClient,
	blobId string,
	expectedPayload string,
) {
	t.Helper()
	loadResult, err := flowClient.LoadBlobs(ctx, &iwfpb.LoadBlobsRequest{
		BlobIds: []string{blobId},
	})
	require.NoError(t, err)
	loaded := loadResult.GetValues()[blobId]
	require.NotNil(t, loaded)
	// LoadBlobs hydrates via the string arm; compare raw stored bytes.
	require.Equal(t, expectedPayload, loaded.GetStringValue())
}
