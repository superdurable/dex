// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/wf_state_api_fail_and_proceed"
	"github.com/superdurable/dex/service"
)

func TestStateApiFailAndProceedTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFailAndProceed(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestStateApiFailAndProceed(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestStateApiFailAndProceedCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestStateApiFailAndProceed(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestStateApiFailAndProceed(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestStateApiFailAndProceed(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := wf_state_api_fail_and_proceed.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := wf_state_api_fail_and_proceed.FlowType + uuid.NewString()
	stepOptions := &dexpb.StepOptions{
		WaitForRetryPolicy: &dexpb.RetryPolicy{
			MaximumAttempts: 1,
		},
		WaitForFailurePolicy: dexpb.WaitForMethodFailurePolicy_WAIT_FOR_METHOD_FAILURE_POLICY_PROCEED_ON_FAILURE,
	}

	startRequest := &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_state_api_fail_and_proceed.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType:    wf_state_api_fail_and_proceed.Step1,
		StepOptions:      stepOptions,
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
	require.Equal(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
	}, history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
	require.Empty(t, resp.GetResults())
}
