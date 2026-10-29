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
	s3_start_input "github.com/superdurable/dex/server-zd/integ/workflow/s3-start-input"
	"google.golang.org/protobuf/proto"
)

func TestS3WorkflowStartInputTemporal(t *testing.T) {
	for _, lazyLoading := range []bool{true, false} {
		for i := 0; i < *repeatIntegTest; i++ {
			t.Run(fmt.Sprintf("lazy=%v-%d", lazyLoading, i), func(t *testing.T) {
				doTestWorkflowWithS3StartInput(t, lazyLoading)
				smallWaitForFastTest()
			})
		}
	}
}

func doTestWorkflowWithS3StartInput(t *testing.T, lazyLoading bool) {
	runtime := startDexService(t, serverZDTestConfig{

		ObjectPayloadThresholdBytes: 10,
		LazyLoading:                 ptr.Any(lazyLoading),
	})
	workerHandler := s3_start_input.NewHandler(runtime.FlowClient)
	workerTarget := startWorker(t, workerHandler)
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := s3_start_input.WorkflowType + uuid.NewString()
	const largeInputPayload = `"12345678901"`
	stepInput := objJSONValue(largeInputPayload)

	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           s3_start_input.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType:    s3_start_input.State1,
		StepInput:        stepInput,
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowId})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeData
	s1WaitForInput := history["S1_waitFor_input"].(*dexpb.Value)
	s1ExecuteInput := history["S1_execute_input"].(*dexpb.Value)

	require.True(t, proto.Equal(stepInput, s1WaitForInput))
	require.True(t, proto.Equal(stepInput, s1ExecuteInput))
	require.Equal(t, int64(1), history["S1_waitFor"])
	require.Equal(t, int64(1), history["S1_execute"])

	requireServerZDValueReferences(t, history, lazyLoading, []string{"S1_execute_input_is_reference"}, nil)

}
