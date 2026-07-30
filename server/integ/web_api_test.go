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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/basic"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/integ/workflow/wf_state_api_fail"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestWebAPITemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testWebAPI(t, service.BackendTypeTemporal)
}

func TestWebAPICadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testWebAPI(t, service.BackendTypeCadence)
}

func testWebAPI(t *testing.T, backendType service.BackendType) {
	for _, durability := range []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	} {
		t.Run(durability.String(), func(t *testing.T) {
			testWebHistoryAndSummary(t, backendType, durability)
		})
	}
	t.Run("current-state", func(t *testing.T) {
		testWebCurrentState(t, backendType)
	})
	t.Run("async-local-fallback", func(t *testing.T) {
		testWebAsyncLocalFallback(t, backendType)
	})
}

func testWebHistoryAndSummary(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := fmt.Sprintf("%s-web-%s-%s", basic.FlowType, durability, uuid.NewString())
	requestID := newRequestID()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          requestID,
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      basic.Step1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				ContinueAsNewThreshold: ptr.Any(int32(1)),
				StepDurability:         ptr.Any(durability),
				WorkerTarget:           workerTarget,
			},
		},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, flowID, summary.GetFlowExecutionId().GetFlowId())
	require.Equal(t, startResponse.GetRunId(), summary.GetFlowExecutionId().GetRunId())
	require.Equal(t, requestID, summary.GetRequestId())
	require.Equal(t, basic.FlowType, summary.GetFlowType())
	require.NotNil(t, summary.GetStartTime())

	firstRunEvents, nextEventID := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	require.NotEmpty(t, firstRunEvents)
	require.NotNil(t, firstRunEvents[0].GetFlowStartedOrContinued().GetInitialStart())
	firstStep := firstStepEvent(firstRunEvents)
	require.NotNil(t, firstStep)
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		firstStep.GetExecution().GetFromStepExecutionId(),
	)

	continuedToRunID := continuedToRunID(firstRunEvents)
	require.NotEmpty(t, continuedToRunID)
	continuedEvents, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		continuedToRunID,
	)
	require.NotEmpty(t, continuedEvents)
	continuedStart := continuedEvents[0].GetFlowStartedOrContinued().GetContinuedStart()
	require.NotNil(t, continuedStart)
	require.Equal(t, startResponse.GetRunId(), continuedStart.GetPreviousRunId())
	require.True(
		t,
		len(continuedStart.GetStepsToStart()) > 0 ||
			len(continuedStart.GetStepsToResume()) > 0 ||
			len(continuedStart.GetCompletedSteps()) > 0,
	)

	waitResponse, err := runtime.FlowClient.WaitForHistoryEvent(
		ctx,
		&dexpb.WaitForHistoryEventRequest{
			FlowId:              flowID,
			RunId:               startResponse.GetRunId(),
			NextInternalEventId: nextEventID,
		},
	)
	require.NoError(t, err)
	require.False(t, waitResponse.GetEventAvailable())
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CONTINUED_AS_NEW, waitResponse.GetFlowStatus())
}

func testWebCurrentState(t *testing.T, backendType service.BackendType) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowID := signal.WorkflowType + "-web-state-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      signal.State1,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{WorkerTarget: workerTarget},
		},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(
			ctx,
			&dexpb.GetFlowStateRequest{
				FlowId: flowID,
				RunId:  startResponse.GetRunId(),
			},
		)
		if stateErr != nil || len(state.GetActiveStepExecutions()) != 1 {
			return false
		}
		active := state.GetActiveStepExecutions()[0]
		return active.GetStepExecutionId() == signal.State1+"-1" &&
			active.GetFromStepExecutionId() == service.StartingStepFromStepExecutionId &&
			active.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING
	}, 30*time.Second, 50*time.Millisecond)

	messages := make([]*dexpb.ChannelMessage, 4)
	for index := range messages {
		messages[index] = &dexpb.ChannelMessage{ChannelName: signal.SignalName}
	}
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: messages,
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
}

func testWebAsyncLocalFallback(t *testing.T, backendType service.BackendType) {
	workerTarget := startWorker(t, wf_state_api_fail.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := wf_state_api_fail.FlowType + "-web-fallback-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           wf_state_api_fail.FlowType,
		FlowTimeoutSeconds: 10,
		StartStepType:      wf_state_api_fail.Step1,
		StepOptions: &dexpb.StepOptions{
			WaitForRetryPolicy: &dexpb.RetryPolicy{TotalDurationSeconds: 1},
		},
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	flowResult, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, flowResult.GetFlowStatus())

	events, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	var failedEvent *dexpb.StepWaitForFailedEvent
	for _, event := range events {
		if event.GetStepWaitForFailed() != nil {
			failedEvent = event.GetStepWaitForFailed()
			break
		}
	}
	require.NotNil(t, failedEvent)
	require.Equal(
		t,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
		failedEvent.GetExecution().GetDurability(),
	)
	require.NotEmpty(t, failedEvent.GetExecution().GetPreviousAttemptFailures())
	previousAttempt := failedEvent.GetExecution().GetPreviousAttemptFailures()
	require.Equal(
		t,
		previousAttempt[len(previousAttempt)-1].GetAttempt()+1,
		failedEvent.GetExecution().GetFinalAttempt(),
	)
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		failedEvent.GetExecution().GetFromStepExecutionId(),
	)
}

func getAllWebHistoryEvents(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
) ([]*dexpb.FlowHistoryEvent, int64) {
	t.Helper()
	var events []*dexpb.FlowHistoryEvent
	var pageToken []byte
	nextEventID := int64(1)
	for {
		response, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
			FlowId:               flowID,
			RunId:                runID,
			StartInternalEventId: nextEventID,
			EstimatePageSize:     1,
			NextPageToken:        pageToken,
		})
		require.NoError(t, err)
		events = append(events, response.GetEvents()...)
		nextEventID = response.GetNextInternalEventId()
		pageToken = response.GetNextPageToken()
		if len(pageToken) == 0 {
			return events, nextEventID
		}
	}
}

func firstStepEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepWaitForCompletedEvent {
	for _, event := range events {
		if event.GetStepWaitForCompleted() != nil {
			return event.GetStepWaitForCompleted()
		}
	}
	return nil
}

func continuedToRunID(events []*dexpb.FlowHistoryEvent) string {
	for _, event := range events {
		if event.GetFlowClosed().GetContinuedToRunId() != "" {
			return event.GetFlowClosed().GetContinuedToRunId()
		}
	}
	return ""
}
