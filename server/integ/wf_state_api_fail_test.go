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
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/wf_state_api_fail"
	"github.com/superdurable/dex/service"
)

func TestStateApiFailTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFail(t, service.BackendTypeTemporal, nil, dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestStateApiFail(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
		doTestStateApiFail(
			t,
			service.BackendTypeTemporal,
			asyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_SYNC,
		)
		smallWaitForFastTest()
	}
}

func TestStateApiFailCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFail(t, service.BackendTypeCadence, nil, dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestStateApiFail(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
	}
}

func doTestStateApiFail(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
	waitForDurabilityOverride dexpb.StepDurability,
) {
	workerHandler := wf_state_api_fail.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_api_fail.FlowType + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           wf_state_api_fail.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType: wf_state_api_fail.Step1,
		StepOptions: &dexpb.StepOptions{
			WaitForDurabilityOverride: waitForDurabilityOverride,
			WaitForRetryPolicy: &dexpb.RetryPolicy{
				TotalDurationSeconds: 1,
			},
		},
		FlowStartOptions: withWorkerTarget(nil, workerTarget),
	}
	if flowConfig != nil {
		startRequest.FlowStartOptions = withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget)
	}
	_, err := flowClient.StartFlow(ctx, startRequest)
	require.NoError(t, err)

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	expectedWaitFor := int64(1)
	if flowConfig != nil &&
		flowConfig.GetStepDurability() == dexpb.StepDurability_STEP_DURABILITY_ASYNC &&
		waitForDurabilityOverride != dexpb.StepDurability_STEP_DURABILITY_SYNC {
		expectedWaitFor = 4
	}
	require.Equal(t, map[string]int64{
		"S1_waitFor": expectedWaitFor,
	}, history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL,
		resp.GetErrorType(),
	)
	require.Contains(t, resp.GetErrorMessage(), "waitFor method failure")
}
