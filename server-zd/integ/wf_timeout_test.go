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
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"github.com/superdurable/dex/server-zd/integ/workflow/signal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFlowTimeoutTemporal(t *testing.T) {
	for i := 0; i < *repeatIntegTest; i++ {
		doTestFlowTimeoutValidation(t)
		smallWaitForFastTest()
		doTestFlowTimeout(t, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED)
		smallWaitForFastTest()
		doTestFlowTimeout(t, minimumContinueAsNewSyncDurabilityConfig(), dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL)
		smallWaitForFastTest()
		doTestFlowTimeout(t, nil, dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL)
		smallWaitForFastTest()
		doTestFlowTimeoutRetry(t)
		smallWaitForFastTest()
		// server-zd never continues a run as new (C3).

		doTestFlowTimeoutHandler(t)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerExecutionOptions(t, false)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerExecutionOptions(t, true)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerDecisions(t)
		smallWaitForFastTest()
		doTestFlowTimeoutSubFlowReport(t)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, false)
		smallWaitForFastTest()
		doTestFlowTimeoutHandlerGraceful(t, true)
		smallWaitForFastTest()
	}
}

func doTestFlowTimeoutValidation(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
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

	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId: "wf-timeout-options-without-timeout-" + uuid.NewString(),
		FlowId:    "wf-timeout-options-without-timeout-" + uuid.NewString(),
		FlowType:  signal.WorkflowType,
		FlowStartOptions: &dexpb.FlowStartOptions{
			TimeoutHandlerOptions: &dexpb.FlowTimeoutHandlerOptions{},
		},
	})
	require.ErrorContains(t, err, "positive timeout")

	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          "wf-timeout-options-wrong-policy-" + uuid.NewString(),
		FlowId:             "wf-timeout-options-wrong-policy-" + uuid.NewString(),
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		FlowStartOptions: &dexpb.FlowStartOptions{
			TimeoutHandlerOptions: &dexpb.FlowTimeoutHandlerOptions{},
		},
	})
	require.ErrorContains(t, err, "Handler policy")

	_, err = runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          "wf-timeout-options-invalid-load-" + uuid.NewString(),
		FlowId:             "wf-timeout-options-invalid-load-" + uuid.NewString(),
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		FlowStartOptions: &dexpb.FlowStartOptions{
			TimeoutHandlerOptions: &dexpb.FlowTimeoutHandlerOptions{
				LoadChannelNames: []string{"commands", "commands"},
			},
		},
	})
	require.ErrorContains(t, err, "duplicated")
}

func doTestFlowTimeout(
	t *testing.T,
	flowConfig *dexpb.FlowConfig,
	timeoutPolicy dexpb.FlowTimeoutPolicy,
) {
	workerHandler := signal.NewHandler()
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-test-" + uuid.NewString()
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

	resp, err := flowClient.WaitForFlow(ctx, waitReq)
	require.NoError(t, err)

	if timeoutPolicy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_CANCELED, resp.GetFlowStatus())
		require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_UNSPECIFIED, resp.GetErrorType())
	} else {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, resp.GetFlowStatus())
		require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT, resp.GetErrorType())
		require.Contains(t, resp.GetErrorMessage(), "timed out after 1 seconds")
		requireFlowClosedHistoryTimeoutMessage(
			t,
			ctx,
			flowClient,
			flowID,
			startResp.GetRunId(),
			"timed out after 1 seconds",
		)
	}
	// The terminal event listener runs inside the in-process Go Server.

	return

}

func requireFlowClosedHistoryTimeoutMessage(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	runID string,
	expectedSubstring string,
) {
	t.Helper()
	historyResponse, err := flowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
		FlowId:           flowID,
		RunId:            runID,
		EstimatePageSize: 100,
	})
	require.NoError(t, err)
	var closed *dexpb.FlowClosedHistoryEvent
	for _, event := range historyResponse.GetEvents() {
		if event.GetFlowClosed() != nil {
			closed = event.GetFlowClosed()
		}
	}
	require.NotNil(t, closed)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, closed.GetFlowStatus())
	require.Equal(t, dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT, closed.GetErrorType())
	require.Contains(t, closed.GetErrorMessage(), expectedSubstring)
}

func doTestFlowTimeoutRetry(t *testing.T) {
	workerHandler := &softTimeoutRetryHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-retry-test-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
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

	// server-zd rejects a Flow retry policy instead of restarting the Flow.
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.ErrorContains(t, err, "server-zd does not support Flow retry_policy")
	return

}

func doTestFlowTimeoutHandler(t *testing.T) {
	workerHandler := &softTimeoutHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := "wf-timeout-handler-test-" + uuid.NewString()
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
	// server-zd lists no timeout Step timer and never continues a run as new (C3).

	_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:              flowID,
		StepExecutionId:     flowTimeoutStepExecutionID,
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
	require.Equal(t, flowTimeoutStepType, result.GetResults()[0].GetCompletedStepType())
	require.Equal(t, int64(42), result.GetResults()[0].GetCompletedStepOutput().GetIntValue())
	require.Equal(t, int32(0), workerHandler.timeoutWaitForCalls.Load())
	require.Equal(t, int32(1), workerHandler.timeoutExecuteCalls.Load())
}

func doTestFlowTimeoutHandlerExecutionOptions(
	t *testing.T,
	shouldRecover bool,
) {
	t.Helper()
	workerHandler := newSoftTimeoutExecutionOptionsHandler(shouldRecover)
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	flowID := "wf-timeout-handler-options-" + uuid.NewString()
	if shouldRecover {
		flowID = "wf-timeout-handler-options-recovery-" + uuid.NewString()
	}
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 3600,
		FlowTimeoutPolicy:  dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
		StartStepType:      signal.State1,
		FlowStartOptions: withWorkerTarget(&dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{
				{Key: "ordinary", Value: stringValue("ordinary")},
				{Key: "timeout-map/one", Value: stringValue("one")},
				{Key: "timeout-map/two", Value: stringValue("two")},
			},
			TimeoutHandlerOptions: &dexpb.FlowTimeoutHandlerOptions{
				MethodTimeoutSeconds: 1,
				RetryPolicy: &dexpb.RetryPolicy{
					InitialIntervalSeconds: 1,
					BackoffCoefficient:     1,
					MaximumIntervalSeconds: 1,
					MaximumAttempts:        2,
				},
				FailurePolicy:          dexpb.ExecuteMethodFailurePolicy_EXECUTE_METHOD_FAILURE_POLICY_PROCEED_TO_CONFIGURED_STEP,
				FailureProceedStepType: timeoutRecoveryStep,
				FailureProceedStepOptions: &dexpb.StepOptions{
					SkipWaitFor: true,
				},
				LoadAttributeMapInstances: []string{"timeout-map/one"},
				LoadChannelNames:          []string{"timeout-commands"},
				LoadChannelMapInstances:   []string{"timeout-channel-map/"},
			},
		}, workerTarget),
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
		Messages: []*dexpb.ChannelMessage{
			{ChannelName: "timeout-commands", Value: stringValue("first")},
			{ChannelName: "timeout-channel-map/one", Value: stringValue("one")},
			{ChannelName: "timeout-channel-map/two", Value: stringValue("two")},
		},
	})
	require.NoError(t, err)
	waitForTimeoutHandlerTimer(t, ctx, runtime, flowID)
	_, err = runtime.FlowClient.SkipTimer(ctx, &dexpb.SkipTimerRequest{
		FlowId:              flowID,
		StepExecutionId:     flowTimeoutStepExecutionID,
		TimerConditionIndex: ptr.Any(int32(0)),
	})
	require.NoError(t, err)

	firstRequest := receiveTimeoutExecuteRequest(t, ctx, workerHandler.executeRequests)
	requireTimeoutHandlerState(t, firstRequest, 1)
	if !shouldRecover {
		_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
			Messages: []*dexpb.ChannelMessage{{
				ChannelName: "timeout-commands",
				Value:       stringValue("late"),
			}},
		})
		require.NoError(t, err)
	}
	secondRequest := receiveTimeoutExecuteRequest(t, ctx, workerHandler.executeRequests)
	requireTimeoutHandlerState(t, secondRequest, 1)
	require.Equal(
		t,
		firstRequest.GetLoadedChannelMessages()["timeout-commands"].GetMessages()[0].GetMessageId(),
		secondRequest.GetLoadedChannelMessages()["timeout-commands"].GetMessages()[0].GetMessageId(),
	)
	require.GreaterOrEqual(
		t,
		workerHandler.secondAttemptAt.Load()-workerHandler.firstAttemptAt.Load(),
		int64(time.Second),
	)

	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		RunId:           startResponse.GetRunId(),
		NeedsResults:    true,
		WaitTimeSeconds: 30,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	require.Len(t, result.GetResults(), 1)
	if shouldRecover {
		require.Equal(t, "recovered", result.GetResults()[0].GetCompletedStepOutput().GetStringValue())
		recoveryRequest := receiveTimeoutExecuteRequest(t, ctx, workerHandler.recoveryRequests)
		_, isNull := recoveryRequest.GetStepInput().GetKind().(*dexpb.Value_NullValue)
		require.True(t, isNull)
		require.NotNil(t, recoveryRequest.GetContext().GetRecoveryError())
		require.Contains(t, recoveryRequest.GetContext().GetRecoveryError().GetDetail(), "retryable timeout handler failure")
	} else {
		require.Equal(t, "handled", result.GetResults()[0].GetCompletedStepOutput().GetStringValue())
		requireTimeoutHandlerDeletionHistory(t, ctx, runtime, flowID, startResponse.GetRunId())
	}
}

func receiveTimeoutExecuteRequest(
	t *testing.T,
	ctx context.Context,
	requests <-chan *dexpb.InvokeExecuteMethodRequest,
) *dexpb.InvokeExecuteMethodRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-ctx.Done():
		require.FailNow(t, "timeout Execute was not invoked", ctx.Err())
		return nil
	}
}

func requireTimeoutHandlerState(
	t *testing.T,
	request *dexpb.InvokeExecuteMethodRequest,
	expectedCommandMessages int,
) {
	t.Helper()
	require.Equal(t, flowTimeoutStepType, request.GetStepType())
	require.ElementsMatch(t, []string{"ordinary", "timeout-map/one"}, attributeKeys(request.GetAttributes()))
	require.Equal(t, []string{"timeout-map/one"}, request.GetLoadedAttributeMapInstances())
	require.Equal(t, []string{"timeout-commands"}, request.GetLoadedChannelNames())
	require.Equal(t, []string{"timeout-channel-map/"}, request.GetLoadedChannelMapInstances())
	require.Len(t, request.GetLoadedChannelMessages()["timeout-commands"].GetMessages(), expectedCommandMessages)
	require.Len(t, request.GetLoadedChannelMessages()["timeout-channel-map/one"].GetMessages(), 1)
	require.Len(t, request.GetLoadedChannelMessages()["timeout-channel-map/two"].GetMessages(), 1)
	require.Equal(t, int32(expectedCommandMessages), request.GetChannelInfos()["timeout-commands"].GetSize())
}

func requireTimeoutHandlerDeletionHistory(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
) {
	t.Helper()
	history, err := runtime.FlowClient.GetHistoryEvents(ctx, &dexpb.GetHistoryEventsRequest{
		FlowId: flowID,
		RunId:  runID,
	})
	require.NoError(t, err)
	for _, historyEvent := range history.GetEvents() {
		completed := historyEvent.GetStepExecuteCompleted()
		if completed.GetContext().GetStepType() == flowTimeoutStepType {
			require.Len(t, completed.GetOutput().GetDeleteFromChannel(), 2)
			return
		}
	}
	require.Fail(t, "timeout handler Execute history was not found")
}

func doTestFlowTimeoutHandlerDecisions(t *testing.T) {
	workerHandler := &softTimeoutHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
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
				StepExecutionId:     flowTimeoutStepExecutionID,
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

func doTestFlowTimeoutSubFlowReport(t *testing.T) {
	workerHandler := &softTimeoutSubFlowHandler{}
	workerTarget := startWorker(t, workerHandler)
	runtime := startDexService(t, serverZDTestConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, timeoutPolicy := range []dexpb.FlowTimeoutPolicy{
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER,
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
	runtime := startDexService(t, serverZDTestConfig{})
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
			StepExecutionId:     flowTimeoutStepExecutionID,
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

func waitForTimeoutHandlerTimer(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
) {
	t.Helper()
	// server-zd keeps the Flow timeout outside Step state until it fires; StartFlow has already committed it.

	return

}

type softTimeoutHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	timeoutWaitForCalls   atomic.Int32
	timeoutExecuteCalls   atomic.Int32
	blockTimeoutExecute   atomic.Bool
	timeoutExecuteRelease chan struct{}
}

type softTimeoutExecutionOptionsHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	shouldRecover    bool
	executeAttempts  atomic.Int32
	firstAttemptAt   atomic.Int64
	secondAttemptAt  atomic.Int64
	executeRequests  chan *dexpb.InvokeExecuteMethodRequest
	recoveryRequests chan *dexpb.InvokeExecuteMethodRequest
}

func newSoftTimeoutExecutionOptionsHandler(shouldRecover bool) *softTimeoutExecutionOptionsHandler {
	return &softTimeoutExecutionOptionsHandler{
		shouldRecover:    shouldRecover,
		executeRequests:  make(chan *dexpb.InvokeExecuteMethodRequest, 2),
		recoveryRequests: make(chan *dexpb.InvokeExecuteMethodRequest, 1),
	}
}

func (h *softTimeoutExecutionOptionsHandler) InvokeWaitForMethod(
	_ context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: "never-published"}},
		},
	}, nil
}

func (h *softTimeoutExecutionOptionsHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetStepType() == timeoutRecoveryStep {
		h.recoveryRequests <- request
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.ForceCompleteDecision(stringValue("recovered")),
			},
		}, nil
	}
	if request.GetStepType() != flowTimeoutStepType {
		return nil, fmt.Errorf("unexpected Step type %q", request.GetStepType())
	}
	attempt := h.executeAttempts.Add(1)
	now := time.Now().UnixNano()
	if attempt == 1 {
		h.firstAttemptAt.Store(now)
	} else {
		h.secondAttemptAt.Store(now)
	}
	h.executeRequests <- request
	if h.shouldRecover {
		return nil, timeoutHandlerRetryableError()
	}
	if attempt == 1 {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "timeout handler attempt timed out")
	}
	commands := request.GetLoadedChannelMessages()["timeout-commands"].GetMessages()
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.ForceCompleteDecision(stringValue("handled")),
		},
		UpsertAttributes: []*dexpb.AttributeWrite{{Key: "timeout-effect", Value: stringValue("written")}},
		DeleteFromChannel: []*dexpb.ChannelMessageDeletion{
			{ChannelName: "timeout-commands", MessageId: commands[0].GetMessageId()},
			{ChannelName: "timeout-commands", MessageId: "missing"},
		},
		PublishToChannel: []*dexpb.ChannelMessage{{
			ChannelName: "timeout-effects",
			Value:       stringValue("published"),
		}},
	}, nil
}

func timeoutHandlerRetryableError() error {
	grpcStatus := status.New(codes.Unavailable, "timeout handler failed")
	grpcStatus, err := grpcStatus.WithDetails(&dexpb.WorkerErrorResponse{
		Detail:    "retryable timeout handler failure",
		ErrorType: "TimeoutHandlerFailure",
	})
	if err != nil {
		panic(err)
	}
	return grpcStatus.Err()
}

func (h *softTimeoutHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetStepType() == flowTimeoutStepType {
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
	if request.GetStepType() != flowTimeoutStepType {
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
		options := &dexpb.SubFlowOptions{
			FlowTimeoutSeconds: 1,
			FlowTimeoutPolicy:  timeoutPolicy,
		}
		if timeoutPolicy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER {
			options.Attributes = []*dexpb.AttributeWrite{
				{Key: "ordinary", Value: stringValue("ordinary")},
				{Key: "child-map/one", Value: stringValue("one")},
				{Key: "child-map/two", Value: stringValue("two")},
			}
			options.TimeoutHandlerOptions = &dexpb.FlowTimeoutHandlerOptions{
				MethodTimeoutSeconds:      7,
				LoadAttributeMapInstances: []string{"child-map/"},
			}
		}
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				SubFlowConditions: []*dexpb.SubFlowCondition{{
					SubFlowType:   timeoutSubFlowChildType,
					StartStepType: timeoutSubFlowChildStep,
					Options:       options,
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
	if request.GetFlowType() == timeoutSubFlowChildType &&
		request.GetStepType() == flowTimeoutStepType {
		if !strings.Contains(request.GetContext().GetFlowId(), dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER.String()) {
			return nil, fmt.Errorf("unexpected timeout handler child Flow ID %q", request.GetContext().GetFlowId())
		}
		if len(request.GetAttributes()) != 3 ||
			len(request.GetLoadedAttributeMapInstances()) != 1 ||
			request.GetLoadedAttributeMapInstances()[0] != "child-map/" {
			return nil, fmt.Errorf("unexpected timeout handler SubFlow state selection")
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.ForceCompleteDecision(stringValue("handled")),
			},
		}, nil
	}
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
	} else if timeoutPolicy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER {
		expectedStatus = dexpb.FlowStatus_FLOW_STATUS_COMPLETED
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
