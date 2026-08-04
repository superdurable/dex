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
	"github.com/superdurable/dex/integ/workflow/skipstart"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestSkipStartFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSkipStartFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestSkipStartFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSkipStartFlow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestSkipStartFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSkipStartFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestSkipStartFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestSkipStartFlow(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestSkipStartFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := skipstart.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := skipstart.WorkflowType + "-" + uuid.NewString()
	stepInput := encodedObjectValue("json", []byte("test data"))
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           skipstart.WorkflowType,
		FlowTimeoutSeconds: 100,

		StartStepType: skipstart.State1,
		StepInput:     stepInput,
		StepOptions: &dexpb.StepOptions{
			SkipWaitFor: true,
		},
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S1_execute": 1,
		"S2_execute": 1,
	}, history, "skipstart test fail, %v", history)

	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	result := response.GetResults()[0]
	require.Equal(t, skipstart.State2, result.GetCompletedStepType())
	require.Equal(t, skipstart.State2+"-1", result.GetCompletedStepExecutionId())
	require.True(t, proto.Equal(stepInput, result.GetCompletedStepOutput()))
}
