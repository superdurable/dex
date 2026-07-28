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
	s3_upsert_data_objects "github.com/superdurable/iwf/integ/workflow/s3-upsert-data-objects"
	"github.com/superdurable/iwf/service"
)

func TestS3WorkflowUpsertDataObjectsTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3UpsertDataObjects(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestS3WorkflowUpsertDataObjectsCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3UpsertDataObjects(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func doTestWorkflowWithS3UpsertDataObjects(t *testing.T, backendType service.BackendType) {
	workerHandler := s3_upsert_data_objects.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_upsert_data_objects.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           s3_upsert_data_objects.WorkflowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      s3_upsert_data_objects.State1,
		StepInput:          objJSONValue(`"test"`),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, int64(1), history["S1_execute"])
	require.Equal(t, int64(1), history["S2_waitFor"])
	require.Equal(t, int64(1), history["S2_execute"])
	require.Equal(t, true, history["S2_received_large_obj1"])
	require.Equal(t, true, history["S2_received_large_obj2"])
	require.Equal(t, true, history["S2_received_small_obj3"])
	require.Equal(t, s3_upsert_data_objects.LargeDataContent1, history["S2_large_obj1_data"])
	require.Equal(t, s3_upsert_data_objects.LargeDataContent2, history["S2_large_obj2_data"])
	require.Equal(t, s3_upsert_data_objects.SmallDataContent3, history["S2_small_obj3_data"])

	objectCount, err := globalBlobStore.CountWorkflowObjectsForTesting(ctx, flowId)
	require.NoError(t, err)
	require.Equal(t, int64(2), objectCount)
}
