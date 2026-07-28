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
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/timer"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
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
		doTestTimerFlow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func TestTimerFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
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
		doTestTimerFlow(t, service.BackendTypeTemporal, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func TestGreedyTimerFlowCadenceContinueAsNew(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestTimerFlow(t, service.BackendTypeCadence, minimumContinueAsNewConfigV0())
		smallWaitForFastTest()
	}
}

func doTestTimerFlow(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *iwfpb.FlowConfig,
) {
	workerHandler := timer.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startIwfService(t, IwfServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient
	unifiedClient := runtime.UnifiedClient

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	flowId := timer.WorkflowType + "-" + uuid.NewString()
	nowTimestamp := time.Now().Unix()
	_, err := flowClient.StartFlow(ctx, &iwfpb.StartFlowRequest{
		FlowId:             flowId,
		FlowType:           timer.WorkflowType,
		FlowTimeoutSeconds: 30,
		WorkerTarget:       workerTarget,
		StartStepType:      timer.State1,
		StepInput:          stringValue(strconv.FormatInt(nowTimestamp, 10)),
		FlowStartOptions: &iwfpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		},
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	timerInfos := &iwfpb.GetCurrentTimerInfosQueryResponse{}
	err = unifiedClient.QueryWorkflow(
		ctx,
		timerInfos,
		flowId,
		"",
		service.GetCurrentTimerInfosQueryType,
	)
	require.NoError(t, err)

	assertions := assert.New(t)
	timer2 := &iwfpb.TimerInfo{
		ConditionId:                "timer-cmd-id-2",
		FiringUnixTimestampSeconds: nowTimestamp + 86400,
		Status:                     iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	timer3 := &iwfpb.TimerInfo{
		ConditionId:                "timer-cmd-id-3",
		FiringUnixTimestampSeconds: nowTimestamp + 86400*365,
		Status:                     iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
	}
	expectedTimerInfos := &iwfpb.GetCurrentTimerInfosQueryResponse{
		StepExecutionCurrentTimerInfos: map[string]*iwfpb.TimerInfoList{
			"S1-1": {
				Timers: []*iwfpb.TimerInfo{
					{
						ConditionId:                "timer-cmd-id",
						FiringUnixTimestampSeconds: nowTimestamp + 10,
						Status:                     iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING,
					},
					timer2,
					timer3,
				},
			},
		},
	}
	assertTimerQueryResponseEqual(assertions, expectedTimerInfos, timerInfos)

	_, err = flowClient.SkipTimer(ctx, &iwfpb.SkipTimerRequest{
		FlowId:           flowId,
		StepExecutionId:  "S1-1",
		TimerConditionId: "timer-cmd-id-2",
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	timerInfos = &iwfpb.GetCurrentTimerInfosQueryResponse{}
	err = unifiedClient.QueryWorkflow(
		ctx,
		timerInfos,
		flowId,
		"",
		service.GetCurrentTimerInfosQueryType,
	)
	require.NoError(t, err)
	timer2.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	assertTimerQueryResponseEqual(assertions, expectedTimerInfos, timerInfos)

	_, err = flowClient.SkipTimer(ctx, &iwfpb.SkipTimerRequest{
		FlowId:              flowId,
		StepExecutionId:     "S1-1",
		TimerConditionIndex: ptr.Any(int32(2)),
	})
	require.NoError(t, err)

	time.Sleep(time.Second)
	timerInfos = &iwfpb.GetCurrentTimerInfosQueryResponse{}
	err = unifiedClient.QueryWorkflow(
		ctx,
		timerInfos,
		flowId,
		"",
		service.GetCurrentTimerInfosQueryType,
	)
	require.NoError(t, err)
	timer3.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	assertTimerQueryResponseEqual(assertions, expectedTimerInfos, timerInfos)

	_, err = flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
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

	_, err = flowClient.ResetFlow(ctx, &iwfpb.ResetFlowRequest{
		FlowId:    flowId,
		ResetType: iwfpb.FlowResetType_FLOW_RESET_TYPE_BEGINNING,
	})
	require.NoError(t, err)

	timer2.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	timer3.Status = iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
	require.Eventually(t, func() bool {
		timerInfos = &iwfpb.GetCurrentTimerInfosQueryResponse{}
		if queryErr := unifiedClient.QueryWorkflow(
			ctx,
			timerInfos,
			flowId,
			"",
			service.GetCurrentTimerInfosQueryType,
		); queryErr != nil {
			return false
		}
		actualList := timerInfos.GetStepExecutionCurrentTimerInfos()["S1-1"]
		if actualList == nil || len(actualList.GetTimers()) != 3 {
			return false
		}
		return actualList.GetTimers()[0].GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING &&
			actualList.GetTimers()[1].GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED &&
			actualList.GetTimers()[2].GetStatus() == iwfpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_SKIPPED
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

	resp, err := flowClient.WaitForFlow(ctx, &iwfpb.WaitForFlowRequest{
		FlowId:          flowId,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, iwfpb.FlowStatus_FLOW_STATUS_COMPLETED, resp.GetFlowStatus())
}

func assertTimerQueryResponseEqual(
	assertions *assert.Assertions,
	expected *iwfpb.GetCurrentTimerInfosQueryResponse,
	actual *iwfpb.GetCurrentTimerInfosQueryResponse,
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
			expectedInfo.FiringUnixTimestampSeconds = actualInfo.GetFiringUnixTimestampSeconds()
			assertions.Equal(expectedInfo, actualInfo)
		}
	}
}
