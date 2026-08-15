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
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/greedy_timer"
	"github.com/superdurable/dex/service"
	uclient "github.com/superdurable/dex/service/client"
)

func TestGreedyTimerFlowBaseTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestGreedyTimerFlow(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowBaseCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestGreedyTimerFlow(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowBaseTemporalContinueAsNew(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestGreedyTimerFlowCustomConfig(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowBaseCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestGreedyTimerFlowCustomConfig(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig())
		smallWaitForFastTest()
	}
}

func doTestGreedyTimerFlow(t *testing.T, backendType service.BackendType) {
	doTestGreedyTimerFlowCustomConfig(t, backendType, nil)
}

func doTestGreedyTimerFlowCustomConfig(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
) {
	assertions := assert.New(t)
	workerHandler := greedy_timer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

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

	time.Sleep(time.Second)

	firingUserTimers := queryFiringUserTimers(t, ctx, unifiedClient, flowId)
	assertions.Len(firingUserTimers, 1)
	singleTimerScheduled := firingUserTimers[0]

	scheduleTimerAndAssertExpectedScheduled(t, flowClient, unifiedClient, flowId, 20, 1)

	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  greedy_timer.ScheduleTimerState + "-1",
		TimerConditionId: "duration-15",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	firingUserTimers = queryFiringUserTimers(t, ctx, unifiedClient, flowId)
	assertions.Len(firingUserTimers, 1)
	assertions.LessOrEqual(singleTimerScheduled, firingUserTimers[0])
	scheduleTimerAndAssertExpectedScheduled(t, flowClient, unifiedClient, flowId, 5, 2)

	_, err = flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)

	history := workerHandler.GetTestResult().InvokeHistory
	assertions.Equalf(map[string]int64{
		"schedule_waitFor": 3,
		"schedule_execute": 1,
	}, history, "history does not match expected")
}

func scheduleTimerAndAssertExpectedScheduled(
	t *testing.T,
	flowClient dexpb.FlowServiceClient,
	unifiedClient uclient.UnifiedClient,
	flowId string,
	duration int64,
	noMoreThan int,
) {
	assertions := assert.New(t)
	input := greedy_timer.Input{Durations: []int64{duration}}
	inputData, err := json.Marshal(input)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
		RequestId:      newRequestID(),
		FlowId:         flowId,
		RpcName:        greedy_timer.SubmitDurationsRPC,
		Input:          encodedObjectValue("json", inputData),
		TimeoutSeconds: 2,
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	firingUserTimers := queryFiringUserTimers(t, ctx, unifiedClient, flowId)
	assertions.LessOrEqual(len(firingUserTimers), noMoreThan)
}

func queryFiringUserTimers(
	t *testing.T,
	ctx context.Context,
	unifiedClient uclient.UnifiedClient,
	flowID string,
) []int64 {
	debug := &dexpb.DebugDumpResponse{}
	err := unifiedClient.QueryWorkflow(ctx, debug, flowID, "", service.DebugDumpQueryType)
	require.NoError(t, err)

	timerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{}
	err = unifiedClient.QueryWorkflow(ctx, timerInfos, flowID, "", service.GetCurrentTimerInfosQueryType)
	require.NoError(t, err)
	timeoutTimers := timerInfos.GetStepExecutionCurrentTimerInfos()[service.FlowTimeoutStepExecutionID]
	require.NotNil(t, timeoutTimers)
	require.Len(t, timeoutTimers.GetTimers(), 1)
	timeoutFiringTimestamp := timeoutTimers.GetTimers()[0].GetFiringUnixTimestampSeconds()

	userTimers := make([]int64, 0, len(debug.GetFiringTimersUnixTimestamps()))
	hasRemovedTimeout := false
	for _, firingTimestamp := range debug.GetFiringTimersUnixTimestamps() {
		if !hasRemovedTimeout && firingTimestamp == timeoutFiringTimestamp {
			hasRemovedTimeout = true
			continue
		}
		userTimers = append(userTimers, firingTimestamp)
	}
	return userTimers
}
