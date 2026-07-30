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
	s3_start_input "github.com/superdurable/dex/integ/workflow/s3-start-input"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/blobstore"
)

func TestS3CleanupTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3Cleanup(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestS3CleanupCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3Cleanup(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func doTestWorkflowWithS3Cleanup(t *testing.T, backendType service.BackendType) {
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
	})
	workerHandler := s3_start_input.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	flowIds := make([]string, 0, 24)
	for i := 0; i < 12; i++ {
		flowId := fmt.Sprintf("test-user-wf-%d-%s", i, uuid.NewString())
		flowIds = append(flowIds, flowId)
		_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			RequestId:          newRequestID(),
			FlowId:             flowId,
			FlowType:           s3_start_input.WorkflowType,
			FlowTimeoutSeconds: 100,

			StartStepType:    s3_start_input.State1,
			StepInput:        objJSONValue(`"12345678901"`),
			FlowStartOptions: withWorkerTarget(nil, workerTarget),
		})
		require.NoError(t, err)
	}
	for i := 0; i < 12; i++ {
		flowIds = append(flowIds, fmt.Sprintf("test-user-wf-%d-%s", 12+i, uuid.NewString()))
	}

	const storeId = "s3-store-id"
	objectCounts := make([]int, len(flowIds))
	for i, flowId := range flowIds {
		objectCount := 1
		if i == len(flowIds)-1 {
			objectCount = 1001
		}
		objectCounts[i] = objectCount
		for j := 0; j < objectCount; j++ {
			_, _, err := globalBlobStore.WriteObject(
				ctx,
				flowId,
				fmt.Sprintf("cleanup-%d", j),
				[]byte(fmt.Sprintf("test-data-workflow-%d-object-%d", i, j)),
			)
			require.NoError(t, err)
		}
	}

	for i, flowId := range flowIds {
		expectedCount := int64(objectCounts[i])
		if i < 12 {
			expectedCount++
		}
		if expectedCount > 1000 {
			expectedCount = 1000
		}
		actualCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
		require.NoError(t, err)
		require.Equal(t, expectedCount, actualCount)
	}

	allWorkflowPaths := make([]string, 0)
	var continuationToken *string
	for {
		output, err := globalBlobStore.ListWorkflowPaths(ctx, blobstore.ListObjectPathsInput{
			StoreId:           storeId,
			ContinuationToken: continuationToken,
		})
		require.NoError(t, err)
		allWorkflowPaths = append(allWorkflowPaths, output.WorkflowPaths...)
		if output.ContinuationToken == nil {
			break
		}
		continuationToken = output.ContinuationToken
	}

	todayPrefix := time.Now().UTC().Format("20060102")
	foundCount := 0
	for _, flowId := range flowIds {
		expectedPath := fmt.Sprintf("%s$%s", todayPrefix, flowId)
		for _, path := range allWorkflowPaths {
			if path == expectedPath {
				foundCount++
				break
			}
		}
	}
	require.GreaterOrEqual(t, foundCount, len(flowIds))

	cleanupWorkflowId := "test-cleanup-" + uuid.NewString()
	err := unifiedClient.StartBlobStoreCleanupWorkflow(
		ctx,
		service.TaskQueue,
		cleanupWorkflowId,
		"",
		storeId,
	)
	require.NoError(t, err)
	_, _, err = unifiedClient.GetWorkflowResult(ctx, nil, cleanupWorkflowId, "")
	require.NoError(t, err)

	for i, flowId := range flowIds {
		count, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
		require.NoError(t, err)
		if i < 12 {
			expectedCount := int64(objectCounts[i]) + 1
			if expectedCount > 1000 {
				expectedCount = 1000
			}
			require.Equal(t, expectedCount, count)
		} else {
			require.Equal(t, int64(0), count)
		}
	}
}
