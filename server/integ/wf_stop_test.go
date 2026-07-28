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
	"github.com/superdurable/iwf/integ/workflow/signal"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowCanceledTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowCanceled(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowCanceled(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()

		doTestWorkflowTerminated(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowTerminated(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()

		doTestWorkflowFail(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestWorkflowFail(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func TestWorkflowCanceledCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestWorkflowCanceled(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowCanceled(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()

		doTestWorkflowTerminated(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowTerminated(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()

		doTestWorkflowFail(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestWorkflowFail(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func doTestWorkflowCanceled(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-cancel-test-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_CANCEL,
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(startResponse.GetRunId(), response.GetRunId())
	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_CANCELED, response.GetFlowStatus())
	assertions.Equal(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, response.GetErrorType())
	assertions.Empty(response.GetErrorMessage())
}

func doTestWorkflowTerminated(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-cancel-test-" + uuid.NewString()
	startRequest := &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	}
	startResponse, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	_, err = flowClient.StartFlow(ctx, startRequest)
	require.Equal(t, codes.AlreadyExists, status.Code(err))

	startRequest.FlowStartOptions.IdReusePolicy =
		iwfpb.IdReusePolicy_ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING
	startResponse, err = flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(startResponse.GetRunId(), response.GetRunId())
	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_TERMINATED, response.GetFlowStatus())
	assertions.Equal(iwfpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, response.GetErrorType())
	assertions.Empty(response.GetErrorMessage())
}

func doTestWorkflowFail(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-cancel-test-" + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 10,
		WorkerTarget:       workerTarget,
		StartStepType:      signal.State1,
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	_, err = flowClient.StopFlow(ctx, &iwfpb.StopFlowRequest{
		FlowId:   flowId,
		StopType: iwfpb.StopType_STOP_TYPE_FAIL,
		Reason:   "fail reason",
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	assertions.Equal(startResponse.GetRunId(), response.GetRunId())
	assertions.Equal(iwfpb.FlowStatus_FLOW_STATUS_FAILED, response.GetFlowStatus())
	assertions.Equal(
		iwfpb.FlowErrorType_FLOW_ERROR_TYPE_CLIENT_API_FAILING_FLOW,
		response.GetErrorType(),
	)
	assertions.Equal("fail reason", response.GetErrorMessage())
}
