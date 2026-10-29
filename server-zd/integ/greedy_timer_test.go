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
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/greedy_timer"
)

func TestGreedyTimerFlowBaseTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestGreedyTimerFlow(t)
		smallWaitForFastTest()
	}
}

func doTestGreedyTimerFlow(t *testing.T) {
	doTestGreedyTimerFlowCustomConfig(t, nil)
}

func doTestGreedyTimerFlowCustomConfig(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := greedy_timer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	durations := []int64{15, 30}
	input := greedy_timer.Input{Durations: durations}
	flowId := greedy_timer.WorkflowType + "-" + uuid.NewString()
	inputData, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           greedy_timer.WorkflowType,
		FlowTimeoutSeconds: 300,

		StartStepType: greedy_timer.ScheduleTimerState,
		StepInput:     encodedObjectValue("json", inputData),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	firstStepExecutionID := greedy_timer.ScheduleTimerState + "-1"
	pendingTimerStatus := dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
	requireStepTimerStatusEventually(t, ctx, runtime, flowId, firstStepExecutionID, "duration-30", pendingTimerStatus)

	// Greedy timers limit Temporal timers; server-zd keeps timers in its own log, so binary mode skips these counts.

	scheduleTimerAndAwaitPending(t, runtime, flowId, greedy_timer.ScheduleTimerState+"-2", 20)

	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  greedy_timer.ScheduleTimerState + "-1",
		TimerConditionId: "duration-15",
	})
	require.NoError(t, err)

	scheduleTimerAndAwaitPending(t, runtime, flowId, greedy_timer.ScheduleTimerState+"-3", 5)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	require.Equalf(t, map[string]int64{
		"schedule_waitFor": 3,
		"schedule_execute": 1,
	}, history, "history does not match expected")
}

func scheduleTimerAndAwaitPending(
	t *testing.T,
	runtime *integRuntime,
	flowId string,
	stepExecutionID string,
	duration int64,
) {
	input := greedy_timer.Input{Durations: []int64{duration}}
	inputData, err := json.Marshal(input)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = runtime.FlowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        greedy_timer.SubmitDurationsRPC,
		Input:          encodedObjectValue("json", inputData),
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	requireStepTimerStatusEventually(
		t, ctx, runtime, flowId, stepExecutionID, "duration-"+strconv.FormatInt(duration, 10),
		dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	)

	return

}

// requireStepTimerStatusEventually polls Step timers until the timer condition reports the status.
func requireStepTimerStatusEventually(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	stepExecutionID string,
	timerConditionID string,
	expectedStatus dexpb.InternalTimerStatus,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		timerInfos, err := queryCurrentTimerInfos(ctx, runtime, flowID)
		if err != nil {
			return false
		}
		for _, timer := range timerInfos.GetStepExecutionCurrentTimerInfos()[stepExecutionID].GetTimers() {
			if timer.GetConditionId() == timerConditionID {
				return timer.GetStatus() == expectedStatus
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
}
