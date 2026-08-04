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
	"github.com/superdurable/dex/integ/workflow/wf_force_fail"
	"github.com/superdurable/dex/service"
)

func TestFlowForceFailTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowForceFail(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestFlowForceFail(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestFlowForceFailCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowForceFail(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestFlowForceFail(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestFlowForceFail(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := wf_force_fail.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-force-fail-test-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           wf_force_fail.FlowType,
		FlowTimeoutSeconds: 10,

		StartStepType: wf_force_fail.Step1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
		resp.GetErrorType(),
	)
	require.Equal(t, "test-data", resp.GetErrorMessage())
	require.Empty(t, resp.GetResults())
}
