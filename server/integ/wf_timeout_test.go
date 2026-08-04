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
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
)

func TestFlowTimeoutTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowTimeout(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestFlowTimeoutCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowTimeout(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestFlowTimeout(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := "wf-timeout-test-" + uuid.NewString()
	startResp, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 1,

		StartStepType: signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	waitReq := &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	}
	// Cadence GetWorkflow with empty runId is unreliable for timed-out closed runs.
	// TODO: debug and remove this once Cadence is fixed.
	if backendType == service.BackendTypeCadence {
		waitReq.RunId = startResp.GetRunId()
	}
	resp, err := flowClient.WaitForFlow(ctx, waitReq)
	require.NoError(t, err)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TIMEOUT, resp.GetFlowStatus())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, resp.GetErrorType())
	require.Empty(t, resp.GetErrorMessage())
}
