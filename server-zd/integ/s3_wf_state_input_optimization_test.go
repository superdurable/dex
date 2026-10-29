// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
	s3_state_input_optimization "github.com/superdurable/dex/server-zd/integ/workflow/s3-state-input-optimization"
)

func TestS3WorkflowStateInputOptimizationTemporal(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StateInputOptimization(t, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func doTestWorkflowWithS3StateInputOptimization(t *testing.T, lazyLoading bool) {
	runtime := startDexService(t, serverZDTestConfig{

		ObjectPayloadThresholdBytes: 10,
		LazyLoading:                 ptr.Any(lazyLoading),
	})
	workerHandler := s3_state_input_optimization.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_state_input_optimization.WorkflowType + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           s3_state_input_optimization.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType:    s3_state_input_optimization.State1,
		StepInput:        objJSONValue(`"this-is-a-large-input-that-exceeds-threshold"`),
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, int64(1), history["S1_execute"])
	require.Equal(t, int64(1), history["S2_waitFor"])
	require.Equal(t, int64(1), history["S2_execute"])
	require.Equal(t, int64(1), history["S3_waitFor"])
	require.Equal(t, int64(1), history["S3_execute"])

	expectedData := `"this-is-a-large-input-that-exceeds-threshold"`
	require.Equal(t, expectedData, history["S1_input_data"])
	require.Equal(t, expectedData, history["S2_input_data"])
	require.Equal(t, expectedData, history["S3_input_data"])

	requireServerZDValueReferences(t, history, lazyLoading, []string{"S1_input_is_reference"}, nil)

}
