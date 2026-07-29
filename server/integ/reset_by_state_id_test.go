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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/reset"
	"github.com/superdurable/dex/service"
)

func TestResetByStateIdWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestResetByStatIdWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestResetByStateIdWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestResetByStatIdWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func doTestResetByStatIdWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := reset.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := reset.WorkflowType + uuid.NewString()
	startResponse, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           reset.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType: reset.State1,
		StepInput:     stringValue("1"),
		StepOptions: &dexpb.StepOptions{
			SkipWaitFor: true,
		},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
			IdReusePolicy:      dexpb.IdReusePolicy_ID_REUSE_POLICY_DISALLOW_REUSE,
		}, workerTarget),
	})
	require.NoError(t, err)

	assertions := assert.New(t)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:       flowId,
		NeedsResults: true,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	assertions.Equalf(map[string]int64{
		"S1_execute": 1,
		"S2_execute": 5,
	}, history, "reset test fail, %v", history)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	assertions.Equal("S2", response.GetResults()[0].GetCompletedStepType())
	assertions.Equal("S2-5", response.GetResults()[0].GetCompletedStepExecutionId())
	assertions.Equal("5", string(response.GetResults()[0].GetCompletedStepOutput().GetObjValue().GetPayload()))

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		RunId:     startResponse.GetRunId(),
		FlowId:    flowId,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_TYPE,
		StepType:  reset.State2,
	})
	require.NoError(t, err)

	response, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:       flowId,
		NeedsResults: true,
	})
	require.NoError(t, err)

	resetHistory := workerHandler.GetTestResult().InvokeHistory
	assertions.Equalf(map[string]int64{
		"S1_execute": 1,
		"S2_execute": 10,
	}, resetHistory, "reset test fail, %v", resetHistory)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	assertions.Equal("S2", response.GetResults()[0].GetCompletedStepType())
	assertions.Equal("S2-5", response.GetResults()[0].GetCompletedStepExecutionId())
	assertions.Equal("5", string(response.GetResults()[0].GetCompletedStepOutput().GetObjValue().GetPayload()))

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		RunId:           startResponse.GetRunId(),
		FlowId:          flowId,
		ResetType:       dexpb.FlowResetType_FLOW_RESET_TYPE_STEP_EXECUTION_ID,
		StepExecutionId: reset.State2 + "-4",
	})
	require.NoError(t, err)

	response, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:       flowId,
		NeedsResults: true,
	})
	require.NoError(t, err)

	reset2History := workerHandler.GetTestResult().InvokeHistory
	assertions.Equalf(map[string]int64{
		"S1_execute": 1,
		"S2_execute": 12,
	}, reset2History, "reset test fail, %v", reset2History)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	assertions.Equal("S2", response.GetResults()[0].GetCompletedStepType())
	assertions.Equal("S2-5", response.GetResults()[0].GetCompletedStepExecutionId())
	assertions.Equal("5", string(response.GetResults()[0].GetCompletedStepOutput().GetObjValue().GetPayload()))
}
