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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/interstate"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestInterStateWorkflowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestInterStateWorkflow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
		doTestInterStateWorkflow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestInterStateWorkflowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestInterStateWorkflow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
		doTestInterStateWorkflow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestInterStateWorkflow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := interstate.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := interstate.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           interstate.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: interstate.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	response, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowId,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	assertions := assert.New(t)
	assertions.Equalf(map[string]int64{
		"S1_waitFor":  1,
		"S1_execute":  1,
		"S21_waitFor": 1,
		"S21_execute": 1,
		"S22_waitFor": 1,
		"S22_execute": 1,
		"S31_waitFor": 1,
		"S31_execute": 1,
	}, history, "interstate test fail, %v", history)

	assertions.Equal(dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	assertions.Equal(0, len(response.GetResults()))
	assertions.True(proto.Equal(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: interstate.TestVal1}},
		data[interstate.State21+"received"].(*dexpb.Value),
	))
	assertions.True(proto.Equal(
		&dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: interstate.TestVal2}},
		data[interstate.State31+"received"].(*dexpb.Value),
	))
}
