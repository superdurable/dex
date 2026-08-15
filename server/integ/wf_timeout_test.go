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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/integ/workflow/signal"
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/common/event"
	"github.com/superdurable/dex/service/common/ptr"
)

func TestFlowTimeoutTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowTimeoutValidation(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeTemporal, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeTemporal, minimumContinueAsNewSyncDurabilityConfig(), dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeTemporal, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL)
		smallWaitForFastTest()
		doTestFlowTimeoutRetry(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeoutRetryAfterContinueAsNew(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeoutHandler(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerDecisions(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeoutSubFlowReport(t, service.BackendTypeTemporal)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, service.BackendTypeTemporal, false)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, service.BackendTypeTemporal, true)
		smallWaitForFastTest()
	}
}

func TestFlowTimeoutCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowTimeoutValidation(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeCadence, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeCadence, minimumContinueAsNewSyncDurabilityConfig(), dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL)
		smallWaitForFastTest()
		doTestFlowTimeout(t, service.BackendTypeCadence, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL)
		smallWaitForFastTest()
		doTestFlowTimeoutRetry(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeoutRetryAfterContinueAsNew(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeoutHandler(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerDecisions(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeoutSubFlowReport(t, service.BackendTypeCadence)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, service.BackendTypeCadence, false)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, service.BackendTypeCadence, true)
		smallWaitForFastTest()
	}
}

func doTestFlowTimeoutValidation(t *testing.T, backendType service.BackendType) {
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:         newRequestID(),
		FlowId:            "wf-timeout-invalid-test-" + uuid.NewString(),
		FlowType:          signal.WorkflowType,
		FlowTimeoutPolicy: dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL,
	})
	require.ErrorContains(t, err, "flow timeout policy requires a positive timeout")

	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             "wf-timeout-unknown-policy-test-" + uuid.NewString(),
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 1,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy(100),
	})
	require.ErrorContains(t, err, "unknown flow timeout policy")
}

func doTestFlowTimeout(
	t *testing.T,
	backendType service.BackendType,
	flowConfig *dexpb.FlowConfig,
	timeoutPolicy dexpb.FlowTimeoutPolicy,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-test-" + uuid.NewString()
	terminalEvents := make(chan event.Event, 1)
	previousEventHandler := event.Handle
	event.SetHandleEventFunc(func(observed event.Event) {
		if observed.FlowId == flowID &&
			(observed.EventType == event.EventTypeFlowFail ||
				observed.EventType == event.EventTypeFlowCancel) {
			terminalEvents <- observed
		}
	})
	defer event.SetHandleEventFunc(previousEventHandler)
	startResp, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 1,
		FlowTimeoutPolicy:  timeoutPolicy,

		StartStepType: signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			FlowConfigOverride: flowConfig,
		}, workerTarget),
	})
	require.NoError(t, err)

	waitReq := &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
	}
	// Cadence GetWorkflow with empty runId is unreliable for some closed runs.
	// TODO: debug and remove this once Cadence is fixed.
	if backendType == service.BackendTypeCadence {
		waitReq.RunId = startResp.GetRunId()
	}
	resp, err := flowClient.WaitForFlow(ctx, waitReq)
	require.NoError(t, err)

	expectedEventType := event.EventTypeFlowFail
	if timeoutPolicy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, resp.GetFlowStatus())
		require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, resp.GetErrorType())
		expectedEventType = event.EventTypeFlowCancel
	} else {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
		require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT, resp.GetErrorType())
		require.Contains(t, resp.GetErrorMessage(), "timed out after 1 seconds")
	}
	select {
	case terminalEvent := <-terminalEvents:
		require.Equal(t, expectedEventType, terminalEvent.EventType)
	case <-ctx.Done():
		require.FailNow(t, "terminal metric was not reported", ctx.Err())
	}
}

func doTestFlowTimeoutRetry(t *testing.T, backendType service.BackendType) {
	workerHandler := &softTimeoutRetryHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-retry-test-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 1,
		StartStepType:      signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			RetryPolicy: &dexpb.FlowRetryPolicy{
				InitialIntervalSeconds: 1,
				BackoffCoefficient:     1,
				MaximumIntervalSeconds: 1,
				MaximumAttempts:        2,
			},
		}, workerTarget),
	})
	require.NoError(t, err)

	waitRequest := &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
	}
	if backendType == service.BackendTypeCadence {
		waitRequest.RunId = startResponse.GetRunId()
	}
	result, err := runtime.FlowClient.WaitForFlow(ctx, waitRequest)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT, result.GetErrorType())
	require.Equal(t, int32(2), workerHandler.waitForCalls.Load())
}

func doTestFlowTimeoutRetryAfterContinueAsNew(
	t *testing.T,
	backendType service.BackendType,
) {
	workerHandler := &softTimeoutRetryAfterContinueAsNewHandler{
		executions: make(chan time.Time, 2),
	}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-retry-after-continue-as-new-test-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 2,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			RetryPolicy: &dexpb.FlowRetryPolicy{
				InitialIntervalSeconds: 1,
				BackoffCoefficient:     1,
				MaximumIntervalSeconds: 1,
				MaximumAttempts:        2,
			},
		}, workerTarget),
	})
	require.NoError(t, err)

	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)
	deadline := flowTimeoutDeadline(t, ctx, runtime, flowID)
	_, err = runtime.FlowClient.TriggerContinueAsNew(ctx, &dexpb.TriggerContinueAsNewRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		currentRunID, queryErr := currentFlowRunID(ctx, runtime, flowID)
		return queryErr == nil && currentRunID != startResponse.GetRunId()
	}, 10*time.Second, 100*time.Millisecond)
	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)
	require.Equal(t, deadline, flowTimeoutDeadline(t, ctx, runtime, flowID))

	firstExecution := waitForTimeoutExecution(t, ctx, workerHandler.executions)
	secondExecution := waitForTimeoutExecution(t, ctx, workerHandler.executions)
	require.GreaterOrEqual(t, secondExecution.Sub(firstExecution), 2*time.Second)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
		result.GetErrorType(),
	)
}

func waitForTimeoutExecution(
	t *testing.T,
	ctx context.Context,
	executions <-chan time.Time,
) time.Time {
	t.Helper()
	select {
	case execution := <-executions:
		return execution
	case <-ctx.Done():
		require.FailNow(t, "timeout handler was not executed", ctx.Err())
		return time.Time{}
	}
}

func doTestFlowTimeoutHandler(t *testing.T, backendType service.BackendType) {
	workerHandler := &softTimeoutHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-handler-test-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 3600,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		StartStepType:      signal.State1,
		FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
	})
	require.NoError(t, err)

	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)
	assertFlowTimeoutDebugState(t, ctx, runtime, flowID)
	deadline := flowTimeoutDeadline(t, ctx, runtime, flowID)
	_, err = runtime.FlowClient.TriggerContinueAsNew(
		ctx,
		&dexpb.TriggerContinueAsNewRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		},
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		currentRunID, queryErr := currentFlowRunID(ctx, runtime, flowID)
		return queryErr == nil && currentRunID != startResponse.GetRunId()
	}, 10*time.Second, 100*time.Millisecond)
	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)
	require.Equal(t, deadline, flowTimeoutDeadline(t, ctx, runtime, flowID))

	_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:              flowID,
		StepExecutionId:     service.FlowTimeoutStepExecutionID,
		TimerConditionIndex: ptr.Any(int32(0)),
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	require.Len(t, result.GetResults(), 1)
	require.Equal(t, service.FlowTimeoutStepType, result.GetResults()[0].GetCompletedStepType())
	require.Equal(t, int64(42), result.GetResults()[0].GetCompletedStepOutput().GetIntValue())
	require.Equal(t, int32(0), workerHandler.timeoutWaitForCalls.Load())
	require.Equal(t, int32(1), workerHandler.timeoutExecuteCalls.Load())
}

func doTestFlowTimeoutHandlerDecisions(t *testing.T, backendType service.BackendType) {
	workerHandler := &softTimeoutHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testCases := []struct {
		name              string
		startStepType     string
		expectedStatus    dexpb.FlowStatus
		expectedErrorType dexpb.FlowErrorType
		expectedOutput    int64
	}{
		{
			name:              "force-fail",
			expectedStatus:    dexpb.FlowStatus_FLOW_STATUS_FAILED,
			expectedErrorType: dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
		},
		{
			name:           "go-to",
			expectedStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
			expectedOutput: 84,
		},
		{
			name:           "dead-end",
			startStepType:  signal.State1,
			expectedStatus: dexpb.FlowStatus_FLOW_STATUS_COMPLETED,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			flowID := "wf-timeout-handler-" + testCase.name + "-" + uuid.NewString()
			_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
				RequestId:          newRequestID(),
				FlowId:             flowID,
				FlowType:           signal.WorkflowType,
				FlowTimeoutSeconds: 3600,
				FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
				StartStepType:      testCase.startStepType,
				FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
			})
			require.NoError(t, err)
			waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)

			executeCalls := workerHandler.timeoutExecuteCalls.Load()
			_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
				FlowId:              flowID,
				StepExecutionId:     service.FlowTimeoutStepExecutionID,
				TimerConditionIndex: ptr.Any(int32(0)),
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				return workerHandler.timeoutExecuteCalls.Load() == executeCalls+1
			}, 10*time.Second, 100*time.Millisecond)

			if testCase.name == "dead-end" {
				_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
					FlowId: flowID,
					Messages: []*dexpb.ChannelMessage{
						{ChannelName: signal.SignalName, Value: stringValue("complete")},
					},
				})
				require.NoError(t, err)
			}
			result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
				FlowId:          flowID,
				NeedsResults:    testCase.expectedOutput != 0,
				WaitTimeSeconds: 20,
			})
			require.NoError(t, err)
			require.Equal(t, testCase.expectedStatus, result.GetFlowStatus())
			require.Equal(t, testCase.expectedErrorType, result.GetErrorType())
			if testCase.expectedOutput != 0 {
				require.Len(t, result.GetResults(), 1)
				require.Equal(
					t,
					testCase.expectedOutput,
					result.GetResults()[0].GetCompletedStepOutput().GetIntValue(),
				)
			}
		})
	}
}

func doTestFlowTimeoutSubFlowReport(t *testing.T, backendType service.BackendType) {
	workerHandler := &softTimeoutSubFlowHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, timeoutPolicy := range []dexpb.FlowTimeoutPolicy{
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL,
	} {
		flowID := "wf-timeout-subflow-report-" + timeoutPolicy.String() + "-" + uuid.NewString()
		_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
			RequestId:          newRequestID(),
			FlowId:             flowID,
			FlowType:           timeoutSubFlowParentType,
			FlowTimeoutSeconds: 30,
			StartStepType:      timeoutSubFlowParentStep,
			StepInput:          intValue(int64(timeoutPolicy)),
			FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
		})
		require.NoError(t, err)
		result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
			FlowId:          flowID,
			WaitTimeSeconds: 20,
		})
		require.NoError(t, err)
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	}
}

func doTestFlowTimeoutHandlerGraceful(
	t *testing.T,
	backendType service.BackendType,
	isHandlerExecuting bool,
) {
	workerHandler := &softTimeoutHandler{
		timeoutExecuteRelease: make(chan struct{}),
	}
	workerHandler.blockTimeoutExecute.Store(isHandlerExecuting)
	if isHandlerExecuting {
		defer close(workerHandler.timeoutExecuteRelease)
	}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-handler-graceful-test-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 3600,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		StartStepType:      signal.State1,
		FlowStartOptions:   withWorkerTarget(&dexpb.FlowStartOptions{}, workerTarget),
	})
	require.NoError(t, err)
	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)

	if isHandlerExecuting {
		_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
			FlowId:              flowID,
			StepExecutionId:     service.FlowTimeoutStepExecutionID,
			TimerConditionIndex: ptr.Any(int32(0)),
		})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return workerHandler.timeoutExecuteCalls.Load() == 1
		}, 10*time.Second, 100*time.Millisecond)
	}

	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{
			{ChannelName: signal.SignalName, Value: stringValue("complete")},
		},
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	if isHandlerExecuting {
		require.Equal(t, int32(1), workerHandler.timeoutExecuteCalls.Load())
	} else {
		require.Equal(t, int32(0), workerHandler.timeoutExecuteCalls.Load())
	}
}

type timeoutHandlerTimerProbe struct {
	ctx     context.Context
	runtime *integRuntime
	flowID  string
}

func waitForTimeoutHandlerTimer(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) {
	t.Helper()
	probe := timeoutHandlerTimerProbe{ctx: ctx, runtime: runtime, flowID: flowID}
	require.Eventually(t, probe.isReady, 10*time.Second, 100*time.Millisecond)
}

func flowTimeoutDeadline(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) int64 {
	t.Helper()
	timerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{}
	require.NoError(t, runtime.UnifiedClient.QueryWorkflow(
		ctx,
		timerInfos,
		flowID,
		"",
		service.GetCurrentTimerInfosQueryType,
	))
	timers := timerInfos.GetStepExecutionCurrentTimerInfos()[service.FlowTimeoutStepExecutionID]
	require.NotNil(t, timers)
	require.Len(t, timers.GetTimers(), 1)
	return timers.GetTimers()[0].GetFiringUnixTimestampSeconds()
}

func currentFlowRunID(
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) (string, error) {
	preparation := &dexpb.PrepareRpcQueryResponse{}
	if err := runtime.UnifiedClient.QueryWorkflow(
		ctx,
		preparation,
		flowID,
		"",
		service.PrepareRpcQueryType,
		&dexpb.PrepareRpcQueryRequest{},
	); err != nil {
		return "", err
	}
	return preparation.GetRunId(), nil
}

func assertFlowTimeoutDebugState(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) {
	t.Helper()
	debugDump := &dexpb.DebugDumpResponse{}
	require.NoError(t, runtime.UnifiedClient.QueryWorkflow(
		ctx,
		debugDump,
		flowID,
		"",
		service.DebugDumpQueryType,
	))
	counterInfo := debugDump.GetSnapshot().GetCounterInfo()
	require.NotContains(t, counterInfo.GetStepTypeStartedCount(), service.FlowTimeoutStepType)
	require.NotContains(t, counterInfo.GetStepTypeCurrentlyExecutingCount(), service.FlowTimeoutStepType)
	require.NotContains(t, counterInfo.GetStepActiveExecutionNums(), service.FlowTimeoutStepType)

	for _, activeStep := range debugDump.GetActiveStepExecutions() {
		if activeStep.GetStepExecutionId() != service.FlowTimeoutStepExecutionID {
			continue
		}
		require.Equal(t, service.FlowTimeoutStepType, activeStep.GetStepType())
		require.Len(t, activeStep.GetTimers(), 1)
		require.Empty(t, activeStep.GetTimers()[0].GetConditionId())
		return
	}
	require.Fail(t, "Flow timeout Step missing from debug state")
}

func (p timeoutHandlerTimerProbe) isReady() bool {
	timerInfos := &dexpb.GetCurrentTimerInfosQueryResponse{}
	queryErr := p.runtime.UnifiedClient.QueryWorkflow(
		p.ctx,
		timerInfos,
		p.flowID,
		"",
		service.GetCurrentTimerInfosQueryType,
	)
	if queryErr != nil {
		return false
	}
	timers := timerInfos.GetStepExecutionCurrentTimerInfos()[service.FlowTimeoutStepExecutionID]
	return timers != nil && len(timers.GetTimers()) == 1 &&
		timers.GetTimers()[0].GetConditionId() == ""
}

type softTimeoutHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	timeoutWaitForCalls   atomic.Int32
	timeoutExecuteCalls   atomic.Int32
	blockTimeoutExecute   atomic.Bool
	timeoutExecuteRelease chan struct{}
}

func (h *softTimeoutHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetStepType() == service.FlowTimeoutStepType {
		h.timeoutWaitForCalls.Add(1)
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{ChannelName: signal.SignalName},
			},
		},
	}, nil
}

func (h *softTimeoutHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetStepType() != service.FlowTimeoutStepType {
		closeDecision := common.GracefulCompleteDecision(nil)
		if request.GetStepType() == timeoutRecoveryStep {
			closeDecision = common.ForceCompleteDecision(intValue(84))
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: closeDecision,
			},
		}, nil
	}
	h.timeoutExecuteCalls.Add(1)
	if h.blockTimeoutExecute.Load() {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout handler canceled: %w", ctx.Err())
		case <-h.timeoutExecuteRelease:
		}
	}
	flowID := request.GetContext().GetFlowId()
	switch {
	case strings.Contains(flowID, "-force-fail-"):
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.ForceFailDecision(stringValue("timeout handler failed")),
			},
		}, nil
	case strings.Contains(flowID, "-go-to-"):
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{{
					StepType:    timeoutRecoveryStep,
					StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
				}},
			},
		}, nil
	case strings.Contains(flowID, "-dead-end-"):
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.DeadEndDecision(),
			},
		}, nil
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.ForceCompleteDecision(intValue(42)),
		},
	}, nil
}

type softTimeoutRetryHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	waitForCalls atomic.Int32
}

func (h *softTimeoutRetryHandler) InvokeWaitForMethod(
	_ context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	h.waitForCalls.Add(1)
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{ChannelName: "never-published"},
			},
		},
	}, nil
}

type softTimeoutRetryAfterContinueAsNewHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	executions chan time.Time
}

func (h *softTimeoutRetryAfterContinueAsNewHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetStepType() != service.FlowTimeoutStepType {
		return nil, fmt.Errorf("unexpected Step type %q", request.GetStepType())
	}
	h.executions <- time.Now()
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.ForceFailDecision(stringValue("retry timeout")),
		},
	}, nil
}

const (
	timeoutRecoveryStep      = "TimeoutRecovery"
	timeoutSubFlowParentType = "timeout-subflow-parent"
	timeoutSubFlowChildType  = "timeout-subflow-child"
	timeoutSubFlowParentStep = "TimeoutParent"
	timeoutSubFlowChildStep  = "TimeoutChild"
)

type softTimeoutSubFlowHandler struct {
	dexpb.UnimplementedWorkerServiceServer
}

func (h *softTimeoutSubFlowHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	switch request.GetFlowType() {
	case timeoutSubFlowParentType:
		timeoutPolicy := dexpb.FlowTimeoutPolicy(request.GetStepInput().GetIntValue())
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				SubFlowConditions: []*dexpb.SubFlowCondition{{
					SubFlowType:   timeoutSubFlowChildType,
					StartStepType: timeoutSubFlowChildStep,
					Options: &dexpb.SubFlowOptions{
						FlowTimeoutSeconds: 1,
						FlowTimeoutPolicy:  timeoutPolicy,
					},
				}},
			},
		}, nil
	case timeoutSubFlowChildType:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ChannelName: "never-published"},
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected timeout SubFlow type %q", request.GetFlowType())
	}
}

func (h *softTimeoutSubFlowHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != timeoutSubFlowParentType {
		return nil, fmt.Errorf("unexpected timeout SubFlow Execute type %q", request.GetFlowType())
	}
	results := request.GetConditionResults().GetSubFlowResults()
	if len(results) != 1 {
		return nil, fmt.Errorf("expected one timeout SubFlow result")
	}
	timeoutPolicy := dexpb.FlowTimeoutPolicy(request.GetStepInput().GetIntValue())
	expectedStatus := dexpb.FlowStatus_FLOW_STATUS_FAILED
	expectedErrorType := dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT
	if timeoutPolicy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL {
		expectedStatus = dexpb.FlowStatus_FLOW_STATUS_CANCELED
		expectedErrorType = dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED
	}
	if results[0].GetFlowStatus() != expectedStatus ||
		results[0].GetErrorType() != expectedErrorType {
		return nil, fmt.Errorf(
			"unexpected timeout SubFlow result: status=%s error=%s",
			results[0].GetFlowStatus(),
			results[0].GetErrorType(),
		)
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.ForceCompleteDecision(nil),
		},
	}, nil
}
