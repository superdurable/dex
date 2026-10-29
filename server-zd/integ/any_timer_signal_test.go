// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	anytimersignal "github.com/superdurable/dex/server-zd/integ/workflow/any_timer_signal"
	"google.golang.org/protobuf/proto"
)

func TestAnyTimerSignalFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, nil)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, nil)
		smallWaitForFastTest()
	}
}

func TestAnyTimerSignalFlowTemporalContinueAsNew(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(
			t,

			minimumContinueAsNewAsyncDurabilityConfig(),
		)
		smallWaitForFastTest()
	}
}

func TestGreedyAnyTimerSignalFlowTemporalContinueAsNew(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestAnyTimerSignalFlow(t, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestAnyTimerSignalFlow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := anytimersignal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
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
