// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/wf_state_api_fail"
)

func TestStateApiFailTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFail(t, nil, dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestStateApiFail(
			t,

			minimumContinueAsNewAsyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_UNSPECIFIED,
		)
		smallWaitForFastTest()
		doTestStateApiFail(
			t,

			asyncDurabilityConfig(),
			dexpb.StepDurability_STEP_DURABILITY_SYNC,
		)
		smallWaitForFastTest()
	}
}

func doTestStateApiFail(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
	waitForDurabilityOverride dexpb.StepDurability,
) {
	workerHandler := wf_state_api_fail.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_api_fail.FlowType + uuid.NewString()
	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
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
	// C2: server-zd ASYNC Steps have no local phase with its own retry attempt.

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
