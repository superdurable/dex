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
	"github.com/superdurable/dex/integ/workflow/headers"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestHeadersFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}

	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowWithHeaders(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()

		doTestFlowWithHeaders(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()

		doTestFlowWithHeaders(t, service.BackendTypeTemporal, &dexpb.FlowConfig{
			ActiveStepSearchMode: ptr.Any(
				dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_DISABLED,
			),
		})
		smallWaitForFastTest()
	}
}

func TestHeadersFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowWithHeaders(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestFlowWithHeaders(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestFlowWithHeaders(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := headers.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType: backendType,
		DefaultHeaders: map[string]string{
			headers.TestHeaderKey: headers.TestHeaderValue,
		},
	})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := headers.WorkflowType + "-" + uuid.NewString()
	stepInput := encodedObjectValue("json", []byte("test data"))
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           headers.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType: headers.State1,
		StepInput:     stepInput,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	if flowConfig != nil && flowConfig.ContinueAsNewThreshold != nil {
		require.Eventually(t, func() bool {
			return len(runtime.internalDumpCapture.snapshot()) > 0
		}, 30*time.Second, 100*time.Millisecond, "expected DumpFlowForContinueAsNew call")
		runtime.requireInternalDumpHeaders(
			t,
			headers.TestHeaderKey,
			headers.TestHeaderValue,
		)
	}

	expectedHistory := map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}
	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, expectedHistory, history, "headers test fail, %v", history)
}
