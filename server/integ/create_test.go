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
	"github.com/superdurable/dex/integ/workflow/rpc"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestCreateFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestCreateFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestCreateFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestCreateFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestCreateWithoutStartingStep(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestCreateWithoutStartingStep(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := rpc.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := rpc.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           rpc.WorkflowType,
		FlowTimeoutSeconds: 10,

		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	debug := &dexpb.DebugDumpResponse{}
	err = unifiedClient.QueryWorkflow(ctx, debug, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	require.True(t, proto.Equal(&dexpb.StepExecutionCounterInfo{
		StepTypeStartedCount:            map[string]int32{},
		StepTypeCurrentlyExecutingCount: map[string]int32{},
		TotalCurrentlyExecutingCount:    0,
	}, debug.GetSnapshot().GetCounterInfo()))

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        rpc.RPCName,
		Input:          rpc.TestInput,
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	respWait, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, respWait.GetFlowStatus())

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "create test fail, %v", history)
}
