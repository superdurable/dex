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
	anytimersignal "github.com/superdurable/dex/integ/workflow/any_timer_signal"
	"github.com/superdurable/dex/service"
	"google.golang.org/protobuf/proto"
)

func TestAnyTimerSignalFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestAnyTimerSignalFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestAnyTimerSignalFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(
			t,
			service.BackendTypeTemporal,
			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestAnyTimerSignalFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(
			t,
			service.BackendTypeCadence,
			minimumContinueAsNewSyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestAnyTimerSignalFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := anytimersignal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowId := anytimersignal.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           anytimersignal.WorkflowType,
		FlowTimeoutSeconds: 20,

		StartStepType: anytimersignal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(3 * time.Second)
	signalValue := encodedObjectValue("json", []byte("test-data-1"))
	_, err = flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowId,
		Messages: []*dexpb.ChannelMessage{
			{
				ChannelName: anytimersignal.SignalName,
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
		"S1_waitFor": 2,
		"S1_execute": 2,
		"S2_waitFor": 1,
		"S2_execute": 1,
	}, history, "anytimersignal test fail, %v", history)

	require.Equal(t, anytimersignal.SignalName, data["signalChannelName1"])
	require.Equal(t, "signal-cmd-id", data["signalCommandId1"])
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, data["signalStatus1"])

	require.Equal(t, anytimersignal.SignalName, data["signalChannelName2"])
	require.Equal(t, "signal-cmd-id", data["signalCommandId2"])
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, data["signalStatus2"])
	actualValues, ok := data["signalValue2"].([]*dexpb.Value)
	require.True(t, ok)
	require.Len(t, actualValues, 1)
	require.True(t, proto.Equal(signalValue, actualValues[0]))
}
