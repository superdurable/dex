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
	anycommandclose "github.com/superdurable/dex/integ/workflow/any_command_close"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestAnyCommandCloseFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCloseFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCloseFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCloseFlow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCloseFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCloseFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestAnyCommandCloseFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyCommandCloseFlow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func doTestAnyCommandCloseFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := anycommandclose.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowId := anycommandclose.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           anycommandclose.WorkflowType,
		FlowTimeoutSeconds: 10,

		StartStepType: anycommandclose.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	signalValue := encodedObjectValue("json", []byte("test-data-1"))
	_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*dexpb.ChannelMessage{
			{
				ChannelName: anycommandclose.SignalName2,
				Value:       signalValue,
			},
		},
	})
	require.NoError(t, err)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)

	result := workerHandler.GetTestResult()
	history := result.InvokeHistory
	data := result.InvokeData
	require.Equalf(t, map[string]int64{
		"S1_waitFor": 1,
		"S1_execute": 1,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "anycommandclose test fail, %v", history)

	require.Equal(t, anycommandclose.SignalName2, data["signalChannelName1"])
	require.Equal(t, "signal-cmd-id2", data["signalCommandId1"])
	requireProtoValuesEqual(t, []*dexpb.Value{signalValue}, data["signalValue1"])
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, data["signalStatus1"])

	require.Equal(t, anycommandclose.SignalName1, data["signalChannelName0"])
	require.Equal(t, "signal-cmd-id1", data["signalCommandId0"])
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, data["signalStatus0"])
}

func requireProtoValuesEqual(t *testing.T, expected []*dexpb.Value, actual any) {
	t.Helper()
	got, ok := actual.([]*dexpb.Value)
	require.True(t, ok, "expected []*dexpb.Value, got %T", actual)
	require.Len(t, got, len(expected))
	for i := range expected {
		require.True(t, proto.Equal(expected[i], got[i]), "value mismatch at index %d", i)
	}
}
