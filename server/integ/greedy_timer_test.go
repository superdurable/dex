// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

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
		FlowTimeoutSeconds: 30,

		StartStepType: greedy_timer.ScheduleTimerState,
		StepInput:     encodedObjectValue("json", inputData),
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	debug := &dexpb.DebugDumpResponse{}
	err = unifiedClient.QueryWorkflow(ctx, debug, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	assertions.Equal(1, len(debug.GetFiringTimersUnixTimestamps()))
	singleTimerScheduled := debug.GetFiringTimersUnixTimestamps()[0]

	scheduleTimerAndAssertExpectedScheduled(t, flowClient, unifiedClient, flowId, 20, 1)

	_, err = flowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  greedy_timer.ScheduleTimerState + "-1",
		TimerConditionId: "duration-15",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)

	err = unifiedClient.QueryWorkflow(ctx, debug, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	assertions.Equal(1, len(debug.GetFiringTimersUnixTimestamps()))
	assertions.LessOrEqual(singleTimerScheduled, debug.GetFiringTimersUnixTimestamps()[0])
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

	debug := &dexpb.DebugDumpResponse{}
	err = unifiedClient.QueryWorkflow(ctx, debug, flowId, "", service.DebugDumpQueryType)
	require.NoError(t, err)
	assertions.LessOrEqual(len(debug.GetFiringTimersUnixTimestamps()), noMoreThan)
}
