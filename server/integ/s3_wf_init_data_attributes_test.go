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
	s3_init_data_attributes "github.com/superdurable/iwf/integ/workflow/s3-init-data-attributes"
	"github.com/superdurable/iwf/service"
)

func TestS3WorkflowInitDataAttributesTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3InitDataAttributes(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestS3WorkflowInitDataAttributesCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowWithS3InitDataAttributes(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func doTestWorkflowWithS3InitDataAttributes(t *testing.T, backendType service.BackendType) {
	workerHandler := s3_init_data_attributes.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{
		BackendType:     backendType,
		S3TestThreshold: 10,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_init_data_attributes.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           s3_init_data_attributes.WorkflowType,
		FlowTimeoutSeconds: 100,
		WorkerTarget:       workerTarget,
		StartStepType:      s3_init_data_attributes.State1,
		StepInput:          objJSONValue(`"test"`),
		FlowStartOptions: &iwfpb.FlowStartOptions{
			Attributes: []*iwfpb.AttributeWrite{
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
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{FlowId: flowId})
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
}
