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
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/timer"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestTimerFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestTimerFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestTimerFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestTimerFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

// NOTE: greedy timers should have the same result in these timer tests
func TestGreedyTimerFlowTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeTemporal, nil)
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, nil)
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestTimerFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	workerHandler := timer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := timer.WorkflowType + "-" + uuid.NewString()
	nowTimestamp := time.Now().Unix()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowId,
		FlowType:           timer.WorkflowType,
		FlowTimeoutSeconds: 30,

		StartStepType: timer.State1,
		StepInput:     stringValue(strconv.FormatInt(nowTimestamp, 10)),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	assertions := assert.New(t)
	resetTimerStatus := dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	if isServerZDBinaryMode() {
		// Temporal reset reapplies later SkipTimer signals; server-zd reset discards them.
		resetTimerStatus = dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
	}
	timer2 := &dexpb.TimerInfo{
		ConditionId:                "timer-cmd-id-2",
		FiringUnixTimestampSeconds: nowTimestamp + 86400,
		Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	timer3 := &dexpb.TimerInfo{
		ConditionId:                "timer-cmd-id-3",
		FiringUnixTimestampSeconds: nowTimestamp + 86400*365,
		Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	expectedTimerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{
		StepExecutionCurrentTimerInfos: map[string]*dexpb.TimerInfoList{
			"S1-1": {
				Timers: []*dexpb.TimerInfo{
					{
						ConditionId:                "timer-cmd-id",
						FiringUnixTimestampSeconds: nowTimestamp + 10,
						Status:                     dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
					},
					timer2,
					timer3,
				},
			},
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
	duration := data["fired_at"].(int64) - data["scheduled_at"].(int64)
	require.Equal(t, "timer-cmd-id", data["timer_id"])
	require.True(t, duration >= 9 && duration <= 11, "duration=%d", duration)

	_, err = flowClient.ResetFlow(ctx, &dexpb.ResetFlowRequest{
		FlowId:    flowId,
		ResetType: dexpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
	})
	require.NoError(t, err)

	timer2.Status = resetTimerStatus
	timer3.Status = resetTimerStatus
	var timerInfos *dexpb.GetCurrentTimerInfosQueryResponse
	require.Eventually(t, func() bool {
		var queryErr error
		if timerInfos, queryErr = queryCurrentTimerInfos(ctx, runtime, flowId); queryErr != nil {
			return false
		}
		actualList := timerInfos.GetStepExecutionCurrentTimerInfos()["S1-1"]
		if actualList == nil || len(actualList.GetTimers()) != 3 {
			return false
		}
		return actualList.GetTimers()[0].GetStatus() == dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING &&
			actualList.GetTimers()[1].GetStatus() == resetTimerStatus &&
			actualList.GetTimers()[2].GetStatus() == resetTimerStatus
	}, 15*time.Second, 200*time.Millisecond)
	// Reset rebuilds firing times from workflow.Now; sync expected before compare.
	for stepExecutionId, expectedList := range expectedTimerInfos.GetStepExecutionCurrentTimerInfos() {
		actualList := timerInfos.GetStepExecutionCurrentTimerInfos()[stepExecutionId]
		require.NotNil(t, actualList)
		require.Equal(t, len(expectedList.GetTimers()), len(actualList.GetTimers()))
		for idx := range expectedList.GetTimers() {
			expectedList.GetTimers()[idx].FiringUnixTimestampSeconds =
				actualList.GetTimers()[idx].GetFiringUnixTimestampSeconds()
		}
	}
	assertTimerQueryResponseEqual(assertions, expectedTimerInfos, timerInfos)
	if isServerZDBinaryMode() {
		for _, timerConditionID := range []string{"timer-cmd-id-2", "timer-cmd-id-3"} {
			_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
				FlowId: flowId, StepExecutionId: "S1-1", TimerConditionId: timerConditionID,
			})
			require.NoError(t, err)
		}
	}

	resp, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

// queryCurrentTimerInfos reads Step timers through the Temporal query, or through GetFlowState in binary mode.
func queryCurrentTimerInfos(
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) (*dexpb.GetCurrentTimerInfosQueryResponse, error) {
	if isServerZDBinaryMode() {
		state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		if err != nil {
			return nil, err
		}
		timerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{
			StepExecutionCurrentTimerInfos: map[string]*dexpb.TimerInfoList{},
		}
		for _, step := range state.GetActiveStepExecutions() {
			timerInfos.StepExecutionCurrentTimerInfos[step.GetStepExecutionId()] = &dexpb.TimerInfoList{
				Timers: step.GetTimers(),
			}
		}
		return timerInfos, nil
	}
	timerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{}
	err := runtime.UnifiedClient.QueryWorkflow(ctx, timerInfos, flowID, "", service.GetCurrentTimerInfosQueryType)
	return timerInfos, err
}

// requireTimerInfosEventually polls Step timers until they match expected.
func requireTimerInfosEventually(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	expected *dexpb.GetCurrentTimerInfosQueryResponse,
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
	expected *dexpb.GetCurrentTimerInfosQueryResponse,
	actual *dexpb.GetCurrentTimerInfosQueryResponse,
) {
	for stepExecutionId, expectedList := range expected.GetStepExecutionCurrentTimerInfos() {
		actualList := actual.GetStepExecutionCurrentTimerInfos()[stepExecutionId]
		if !assertions.NotNil(actualList) {
			continue
		}
		if !assertions.Equal(len(expectedList.GetTimers()), len(actualList.GetTimers())) {
			continue
		}
		for idx, expectedInfo := range expectedList.GetTimers() {
			actualInfo := actualList.GetTimers()[idx]
			abs := math.Abs(float64(
				expectedInfo.GetFiringUnixTimestampSeconds() - actualInfo.GetFiringUnixTimestampSeconds(),
			))
			assertions.True(abs <= 1)
			// Compare a new value so polling never rewrites the expected firing time.
			assertions.Equal(&dexpb.TimerInfo{
				ConditionId:                expectedInfo.GetConditionId(),
				FiringUnixTimestampSeconds: actualInfo.GetFiringUnixTimestampSeconds(),
				Status:                     expectedInfo.GetStatus(),
			}, actualInfo)
		}
	}
}
