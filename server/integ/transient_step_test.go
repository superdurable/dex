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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/transient_step"
	"github.com/superdurable/dex/service"
)

func TestTransientStepTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for index := 0; index < *repeatIntegTest; index++ {
		doTestTransientStep(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
	}
}

func TestTransientStepCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for index := 0; index < *repeatIntegTest; index++ {
		doTestTransientStep(t, service.BackendTypeCadence)
		smallWaitForFastTest()
	}
}

func doTestTransientStep(t *testing.T, backendType service.BackendType) {
	handler := transient_step.NewHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := transient_step.FlowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           transient_step.FlowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      transient_step.SourceStep,
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	select {
	case <-handler.TransientStarted():
	case <-ctx.Done():
		require.FailNow(t, "transient Execute did not start", ctx.Err())
	}

	_, err = runtime.FlowClient.TriggerContinueAsNew(
		ctx,
		&dexpb.TriggerContinueAsNewRequest{FlowId: flowID},
	)
	require.NoError(t, err)

	var blockedTimers dexpb.GetCurrentTimerInfosQueryResponse
	err = runtime.UnifiedClient.QueryWorkflow(
		ctx,
		&blockedTimers,
		flowID,
		"",
		service.GetCurrentTimerInfosQueryType,
	)
	require.NoError(t, err)
	require.NotContains(
		t,
		blockedTimers.GetStepExecutionCurrentTimerInfos(),
		transient_step.SourceStep+"-1",
	)

	var blockedDump dexpb.DebugDumpResponse
	err = runtime.UnifiedClient.QueryWorkflow(
		ctx,
		&blockedDump,
		flowID,
		"",
		service.DebugDumpQueryType,
	)
	require.NoError(t, err)
	require.Empty(t, blockedDump.GetSnapshot().GetStepExecutionsToResume())
	require.Equal(
		t,
		int32(2),
		blockedDump.GetSnapshot().GetCounterInfo().GetTotalCurrentlyExecutingCount(),
	)
	require.Equal(
		t,
		int32(1),
		blockedDump.GetSnapshot().GetCounterInfo().
			GetStepTypeStartedCount()[transient_step.TransientStep],
	)

	blockedDescription, err := runtime.UnifiedClient.DescribeWorkflowExecution(
		ctx,
		flowID,
		"",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, startResponse.GetRunId(), blockedDescription.RunId)

	handler.ReleaseTransient()

	currentRunID := ""
	require.Eventually(t, func() bool {
		description, describeErr := runtime.UnifiedClient.DescribeWorkflowExecution(
			ctx,
			flowID,
			"",
			nil,
		)
		if describeErr != nil || description.RunId == startResponse.GetRunId() {
			return false
		}
		currentRunID = description.RunId
		return true
	}, 30*time.Second, 50*time.Millisecond)

	var timerInfo *dexpb.TimerInfo
	require.Eventually(t, func() bool {
		var timerResponse dexpb.GetCurrentTimerInfosQueryResponse
		queryErr := runtime.UnifiedClient.QueryWorkflow(
			ctx,
			&timerResponse,
			flowID,
			currentRunID,
			service.GetCurrentTimerInfosQueryType,
		)
		if queryErr != nil {
			return false
		}
		timerList := timerResponse.GetStepExecutionCurrentTimerInfos()[transient_step.SourceStep+"-1"]
		if len(timerList.GetTimers()) != 1 {
			return false
		}
		timerInfo = timerList.GetTimers()[0]
		return timerInfo.GetStatus() ==
			dexpb.InternalTimerStatus_INTERNAL_TIMER_STATUS_PENDING
	}, 30*time.Second, 50*time.Millisecond)

	result := handler.GetResult()
	require.GreaterOrEqual(
		t,
		timerInfo.GetFiringUnixTimestampSeconds(),
		result.TransientCompletedUnix+transient_step.TimerDurationSeconds,
	)

	var resumedDump dexpb.DebugDumpResponse
	err = runtime.UnifiedClient.QueryWorkflow(
		ctx,
		&resumedDump,
		flowID,
		currentRunID,
		service.DebugDumpQueryType,
	)
	require.NoError(t, err)
	resumeInfos := resumedDump.GetSnapshot().GetStepExecutionsToResume()
	require.Len(t, resumeInfos, 1)
	require.Equal(t, transient_step.SourceStep+"-1", resumeInfos[0].GetStepExecutionId())
	require.Equal(
		t,
		int32(1),
		resumedDump.GetSnapshot().GetCounterInfo().GetTotalCurrentlyExecutingCount(),
	)
	require.NotContains(
		t,
		resumedDump.GetSnapshot().GetCounterInfo().GetStepActiveExecutionNums(),
		transient_step.TransientStep,
	)
	require.Equal(
		t,
		int32(1),
		resumedDump.GetSnapshot().GetCounterInfo().
			GetStepTypeStartedCount()[transient_step.TransientStep],
	)

	_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:           flowID,
		RunId:            currentRunID,
		StepExecutionId:  transient_step.SourceStep + "-1",
		TimerConditionId: transient_step.TimerConditionID,
	})
	require.NoError(t, err)

	flowResponse, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, flowResponse.GetFlowStatus())
	require.Equal(t, []string{
		transient_step.SourceWaitCall,
		transient_step.TransientExecuteCall,
		transient_step.SourceExecuteCall,
	}, handler.GetResult().Calls)
}
