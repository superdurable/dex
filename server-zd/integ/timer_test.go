// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Sustainable Use License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package integ

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/timer"
)

func TestTimerFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, nil)
		smallWaitForFastTest()
	}
}

// NOTE: greedy timers should have the same result in these timer tests
func TestGreedyTimerFlowTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, nil)
		smallWaitForFastTest()
	}
}

func doTestTimerFlow(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := timer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := timer.WorkflowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           timer.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType: timer.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	timerScheduledTimestamp := readTimerWaitForCompletionTimestamp(t, ctx, flowClient, flowId)

	assertions := assert.New(t)
	const resetTimerStatus = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING

	timer2 := &dexpb.TimerInfo{
		ConditionId:                "timer-cmd-id-2",
		FiringUnixTimestampSeconds: timerScheduledTimestamp + 86400,
		Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	timer3 := &dexpb.TimerInfo{
		ConditionId:                "timer-cmd-id-3",
		FiringUnixTimestampSeconds: timerScheduledTimestamp + 86400*365,
		Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	expectedTimerInfos := map[string][]*dexpb.TimerInfo{
		"S1-1": {
			{
				ConditionId:                "timer-cmd-id",
				FiringUnixTimestampSeconds: timerScheduledTimestamp + 10,
				Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
			},
			timer2,
			timer3,
		},
	}
	requireTimerInfosEventually(t, ctx, runtime, flowId, expectedTimerInfos)

	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  "S1-1",
		TimerConditionId: "timer-cmd-id-2",
	})
	require.NoError(t, err)

	timer2.Status = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	requireTimerInfosEventually(t, ctx, runtime, flowId, expectedTimerInfos)

	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:              flowId,
		StepExecutionId:     "S1-1",
		TimerConditionIndex: ptr.Any(int32(2)),
	})
	require.NoError(t, err)

	timer3.Status = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	requireTimerInfosEventually(t, ctx, runtime, flowId, expectedTimerInfos)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
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
	}, history, "timer test fail, %v", history)
	duration := data["fired_at"].(int64) - timerScheduledTimestamp
	require.Equal(t, "timer-cmd-id", data["timer_id"])
	require.True(t, duration >= 9 && duration <= 11, "duration=%d", duration)

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId:    flowId,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
	})
	require.NoError(t, err)

	// Temporal reset reapplies later SkipTimer signals; server-zd reset discards them.
	timer2.Status = resetTimerStatus
	timer3.Status = resetTimerStatus
	var timerInfos map[string][]*dexpb.TimerInfo
	require.Eventually(t, func() bool {
		var queryErr error
		if timerInfos, queryErr = queryCurrentTimerInfos(ctx, runtime, flowId); queryErr != nil {
			return false
		}
		actualList := timerInfos["S1-1"]
		if len(actualList) != 3 {
			return false
		}
		return actualList[0].GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING &&
			actualList[1].GetStatus() == resetTimerStatus &&
			actualList[2].GetStatus() == resetTimerStatus
	}, 15*time.Second, 200*time.Millisecond)
	// Reset rebuilds firing times from workflow.Now; sync expected before compare.
	for stepExecutionId, expectedList := range expectedTimerInfos {
		actualList := timerInfos[stepExecutionId]
		require.NotNil(t, actualList)
		require.Equal(t, len(expectedList), len(actualList))
		for idx := range expectedList {
			expectedList[idx].FiringUnixTimestampSeconds = actualList[idx].GetFiringUnixTimestampSeconds()
		}
	}
	assertTimerQueryResponseEqual(assertions, expectedTimerInfos, timerInfos)

	for _, timerConditionID := range []string{"timer-cmd-id-2", "timer-cmd-id-3"} {
		_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
			FlowId: flowId, StepExecutionId: "S1-1", TimerConditionId: timerConditionID,
		})
		require.NoError(t, err)
	}

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

// queryCurrentTimerInfos reads Step timers through GetFlowState, keyed by Step execution ID.
func queryCurrentTimerInfos(
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) (map[string][]*dexpb.TimerInfo, error) {
	state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	if err != nil {
		return nil, err
	}
	timerInfos := map[string][]*dexpb.TimerInfo{}
	for _, step := range state.GetActiveStepExecutions() {
		timerInfos[step.GetStepExecutionId()] = step.GetTimers()
	}
	return timerInfos, nil

}

// requireTimerInfosEventually polls Step timers until they match expected.
func requireTimerInfosEventually(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	expected map[string][]*dexpb.TimerInfo,
) {
	t.Helper()
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		timerInfos, err := queryCurrentTimerInfos(ctx, runtime, flowID)
		if !assert.NoError(collect, err) {
			return
		}
		assertTimerQueryResponseEqual(assert.New(collect), expected, timerInfos)
	}, 10*time.Second, 100*time.Millisecond)
}

func assertTimerQueryResponseEqual(
	assertions *assert.Assertions,
	expected map[string][]*dexpb.TimerInfo,
	actual map[string][]*dexpb.TimerInfo,
) {
	for stepExecutionId, expectedList := range expected {
		actualList := actual[stepExecutionId]
		if !assertions.NotNil(actualList) {
			continue
		}
		if !assertions.Equal(len(expectedList), len(actualList)) {
			continue
		}
		for idx, expectedInfo := range expectedList {
			actualInfo := actualList[idx]
			abs := math.Abs(float64(
				expectedInfo.GetFiringUnixTimestampSeconds() - actualInfo.GetFiringUnixTimestampSeconds(),
			))
			assertions.True(abs <= 1, "timer %s expected timestamp %d, actual %d", expectedInfo.GetConditionId(), expectedInfo.GetFiringUnixTimestampSeconds(), actualInfo.GetFiringUnixTimestampSeconds())
			// Compare a new value so polling never rewrites the expected firing time.
			assertions.Equal(&dexpb.TimerInfo{
				ConditionId:                expectedInfo.GetConditionId(),
				FiringUnixTimestampSeconds: actualInfo.GetFiringUnixTimestampSeconds(),
				Status:                     expectedInfo.GetStatus(),
			}, actualInfo)
		}
	}
}

type timerWaitForHistoryProbe struct {
	ctx                context.Context
	flowClient         dexpb.FlowServiceClient
	flowID             string
	scheduledTimestamp int64
}

func readTimerWaitForCompletionTimestamp(t *testing.T, ctx context.Context, flowClient dexpb.FlowServiceClient, flowID string) int64 {
	t.Helper()
	probe := &timerWaitForHistoryProbe{ctx: ctx, flowClient: flowClient, flowID: flowID}
	require.EventuallyWithT(t, probe.requireCompletionTimestamp, 10*time.Second, 100*time.Millisecond)
	return probe.scheduledTimestamp
}

func (probe *timerWaitForHistoryProbe) requireCompletionTimestamp(collect *assert.CollectT) {
	history, err := probe.flowClient.GetHistoryEvents(probe.ctx, &dexpb.GetHistoryEventsRequest{FlowId: probe.flowID})
	if !assert.NoError(collect, err) {
		return
	}
	for _, event := range history.GetEvents() {
		if event.GetStepWaitForCompleted().GetContext().GetStepExecutionId() == "S1-1" {
			probe.scheduledTimestamp = event.GetEventTime().GetSeconds()
			break
		}
	}
	assert.NotZero(collect, probe.scheduledTimestamp, "timer WaitFor completion missing: %v", history)
}
