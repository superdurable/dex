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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
		lazyLoadingValues := []bool{true}
		if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC {
			lazyLoadingValues = []bool{true, false}
		}
		for _, lazyLoading := range lazyLoadingValues {
			t.Run(fmt.Sprintf("%s-lazy-%t", durability, lazyLoading), func(t *testing.T) {
				testWebHistoryAndSummary(t, backendType, durability, lazyLoading)
			})
		}
	}
	for _, lazyLoading := range []bool{true, false} {
		t.Run(fmt.Sprintf("current-state-lazy-%t", lazyLoading), func(t *testing.T) {
			testWebCurrentState(t, backendType, lazyLoading)
		})
	}
	t.Run("set-attributes-history", func(t *testing.T) {
		testWebSetAttributesHistory(t, backendType)
	})
	for _, durability := range []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	} {
		for _, conditionType := range []string{"any", "all"} {
			t.Run(fmt.Sprintf("condition-results-%s-%s", durability, conditionType), func(t *testing.T) {
				testWebConditionResults(t, backendType, durability, conditionType)
			})
		}
	}
	t.Run("async-local-fallback", func(t *testing.T) {
		testWebAsyncLocalFallback(t, backendType)
	})
	t.Run("parallel-attribute-snapshots", func(t *testing.T) {
		testWebParallelAttributeSnapshots(t, backendType)
	})
	t.Run("parallel-same-type-failures", func(t *testing.T) {
		testWebParallelSameTypeFailures(t, backendType)
	})
	t.Run("force-closed-pending-step", func(t *testing.T) {
		testWebForceClosedPendingStep(t, backendType)
	})
	t.Run("force-closed-pending-wait-for", func(t *testing.T) {
		testWebForceClosedPendingWaitFor(t, backendType)
	})
	for _, method := range []string{"wait-for", "execute"} {
		t.Run("sync-last-failure-"+method, func(t *testing.T) {
			testWebSyncLastFailure(t, backendType, method, false)
		})
		t.Run("sync-terminal-failure-"+method, func(t *testing.T) {
			testWebSyncLastFailure(t, backendType, method, true)
		})
		t.Run("sync-timeout-"+method, func(t *testing.T) {
			testWebSyncTimeoutFailure(t, backendType, method)
		})
	}
	for _, durability := range []dexpb.StepDurability{
		dexpb.StepDurability_STEP_DURABILITY_SYNC,
		dexpb.StepDurability_STEP_DURABILITY_ASYNC,
	} {
		t.Run(fmt.Sprintf("storage-disabled-%s", durability), func(t *testing.T) {
			testWebStepInputWithoutStorage(t, backendType, durability)
		})
	}
}

func testWebForceClosedPendingStep(t *testing.T, backendType service.BackendType) {
	handler := newCooperativeStopHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "web-force-closed-pending-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           cooperativeStopFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      cooperativeStopStepType,
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.executeStarted:
	case <-ctx.Done():
		require.FailNow(t, "Execute did not start", ctx.Err())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, result.GetFlowStatus())

	events, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	var closed *dexpb.FlowClosedHistoryEvent
	var pendingEvent *dexpb.FlowHistoryEvent
	for _, event := range events {
		if event.GetFlowClosed() != nil {
			closed = event.GetFlowClosed()
		}
		if event.GetStepExecutePending() != nil {
			pendingEvent = event
		}
		require.Nil(t, event.GetStepExecuteCompleted())
		require.Nil(t, event.GetStepExecuteFailed())
	}
	require.NotNil(t, closed)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, closed.GetFlowStatus())
	require.NotNil(t, pendingEvent)
	require.Less(t, pendingEvent.GetEventId(), events[len(events)-1].GetEventId())
	require.NoError(t, pendingEvent.GetEventTime().CheckValid())
	pending := pendingEvent.GetStepExecutePending()
	require.Contains(t, []dexpb.PendingStepMethodPhase{
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_SCHEDULED,
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_STARTED,
	}, pending.GetPhase())
	require.NotNil(t, pending.GetInput())
	require.False(t, pending.GetInput().GetUnavailable())
	methodContext := pending.GetContext()
	require.Equal(t, cooperativeStopStepType+"-1", methodContext.GetStepExecutionId())
	require.Equal(t, service.StartingStepFromStepExecutionId, methodContext.GetFromStepExecutionId())
	require.Equal(t, cooperativeStopStepType, methodContext.GetStepType())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, methodContext.GetDurability())
	require.Equal(t, int32(1), methodContext.GetFinalAttempt())
	require.NoError(t, methodContext.GetStartedTime().CheckValid())
	require.NoError(t, methodContext.GetDuration().CheckValid())
	require.Equal(t, methodContext.GetStartedTime(), pendingEvent.GetEventTime())
}

type webPendingWaitForHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	waitForStartedOnce sync.Once
	waitForStarted     chan struct{}
}

func newWebPendingWaitForHandler() *webPendingWaitForHandler {
	return &webPendingWaitForHandler{waitForStarted: make(chan struct{})}
}

func (h *webPendingWaitForHandler) InvokeWaitForMethod(
	ctx context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	h.waitForStartedOnce.Do(func() { close(h.waitForStarted) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (h *webPendingWaitForHandler) InvokeExecuteMethod(
	context.Context,
	*dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	return nil, status.Error(codes.Internal, "Execute should not start")
}

func testWebForceClosedPendingWaitFor(t *testing.T, backendType service.BackendType) {
	handler := newWebPendingWaitForHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := "web-force-closed-pending-wait-for-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           "pending-wait-for",
		FlowTimeoutSeconds: 20,
		StartStepType:      "waiting-step",
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.waitForStarted:
	case <-ctx.Done():
		require.FailNow(t, "WaitFor did not start", ctx.Err())
	}
	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
		StopType: dexpb.StopType_STOP_TYPE_TERMINATE,
	})
	require.NoError(t, err)
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId: flowID,
		RunId:  startResponse.GetRunId(),
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, result.GetFlowStatus())

	events, _ := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	var closed *dexpb.FlowClosedHistoryEvent
	var pendingEvent *dexpb.FlowHistoryEvent
	for _, event := range events {
		if event.GetFlowClosed() != nil {
			closed = event.GetFlowClosed()
		}
		if event.GetStepWaitForPending() != nil {
			pendingEvent = event
		}
		require.Nil(t, event.GetStepWaitForCompleted())
		require.Nil(t, event.GetStepWaitForFailed())
		require.Nil(t, event.GetStepExecutePending())
	}
	require.NotNil(t, closed)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_TERMINATED, closed.GetFlowStatus())
	require.NotNil(t, pendingEvent)
	require.Less(t, pendingEvent.GetEventId(), events[len(events)-1].GetEventId())
	require.NoError(t, pendingEvent.GetEventTime().CheckValid())
	pending := pendingEvent.GetStepWaitForPending()
	require.Contains(t, []dexpb.PendingStepMethodPhase{
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_SCHEDULED,
		dexpb.PendingStepMethodPhase_PENDING_STEP_METHOD_PHASE_STARTED,
	}, pending.GetPhase())
	require.NotNil(t, pending.GetInput())
	require.False(t, pending.GetInput().GetUnavailable())
	methodContext := pending.GetContext()
	require.Equal(t, "waiting-step-1", methodContext.GetStepExecutionId())
	require.Equal(t, service.StartingStepFromStepExecutionId, methodContext.GetFromStepExecutionId())
	require.Equal(t, "waiting-step", methodContext.GetStepType())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, methodContext.GetDurability())
	require.Equal(t, int32(1), methodContext.GetFinalAttempt())
	require.NoError(t, methodContext.GetStartedTime().CheckValid())
	require.NoError(t, methodContext.GetDuration().CheckValid())
	require.Equal(t, methodContext.GetStartedTime(), pendingEvent.GetEventTime())
}

func testWebParallelSameTypeFailures(t *testing.T, backendType service.BackendType) {
	handler := &webParallelFailureHandler{
		flowType:        "web-parallel-same-type-failures",
		failureObserved: make(chan struct{}),
	}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "root",
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.failureObserved:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}

	var activeSteps []*dexpb.ActiveStepExecutionState
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		})
		if stateErr != nil {
			return false
		}
		activeUserSteps := userActiveStepExecutions(state)
		if len(activeUserSteps) != 2 {
			return false
		}
		for _, activeStep := range activeUserSteps {
			if activeStep.GetStepType() != "branch" || activeStep.GetLastFailureInfo() == nil {
				return false
			}
		}
		activeSteps = activeUserSteps
		return true
	}, 2*time.Second, 50*time.Millisecond)
	for _, activeStep := range activeSteps {
		failure := activeStep.GetLastFailureInfo()
		require.Equal(t, int32(1), failure.GetAttempt())
		require.Equal(
			t,
			activeStep.GetStepExecutionId(),
			failure.GetDetails().GetOriginalWorkerErrorDetail(),
		)
	}

	result, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
}

type webParallelFailureHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType        string
	failureCount    atomic.Int32
	failureObserved chan struct{}
}

func (h *webParallelFailureHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	if request.GetStepType() == "root" {
		options := webParallelFailureStepOptions()
		return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{StepType: "branch", StepOptions: options},
				{StepType: "branch", StepOptions: options},
			},
		}}, nil
	}
	if request.GetStepType() != "branch" {
		return nil, fmt.Errorf("unexpected step type %q", request.GetStepType())
	}
	if request.GetContext().GetAttempt() == 1 {
		if h.failureCount.Add(1) == 2 {
			close(h.failureObserved)
		}
		return nil, webParallelFailureError(request.GetContext().GetStepExecutionId())
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
		},
	}}, nil
}

func webParallelFailureStepOptions() *dexpb.StepOptions {
	return &dexpb.StepOptions{
		SkipWaitFor: true,
		ExecuteRetryPolicy: &dexpb.RetryPolicy{
			InitialIntervalSeconds: 3,
			BackoffCoefficient:     1,
			MaximumIntervalSeconds: 3,
			MaximumAttempts:        2,
		},
	}
}

func webParallelFailureError(stepExecutionID string) error {
	workerStatus, err := status.New(
		codes.Internal,
		"parallel branch failure",
	).WithDetails(&dexpb.WorkerErrorResponse{
		Detail:     stepExecutionID,
		ErrorType:  "ParallelBranchFailure",
		StackTrace: "parallel worker stack " + stepExecutionID,
	})
	if err != nil {
		return err
	}
	return workerStatus.Err()
}

func testWebSyncLastFailure(
	t *testing.T,
	backendType service.BackendType,
	method string,
	terminal bool,
) {
	handler := &webSyncRetryHandler{
		flowType:        "web-sync-last-failure-" + method,
		method:          method,
		terminal:        terminal,
		failureObserved: make(chan struct{}),
	}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "retry-step",
		StepOptions:        webSyncRetryStepOptions(method),
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	select {
	case <-handler.failureObserved:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	var activeFailure *dexpb.StepMethodFailure
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		})
		if stateErr != nil {
			return false
		}
		activeUserSteps := userActiveStepExecutions(state)
		if len(activeUserSteps) != 1 {
			return false
		}
		activeFailure = activeUserSteps[0].GetLastFailureInfo()
		return activeFailure != nil
	}, 2*time.Second, 50*time.Millisecond)
	require.Equal(t, int32(1), activeFailure.GetAttempt())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		activeFailure.GetBackendError(),
	)
	require.Empty(t, activeFailure.GetDetails().GetDetail())
	require.Equal(t, "WorkerRetryError", activeFailure.GetDetails().GetOriginalWorkerErrorType())
	require.Equal(
		t,
		"retryable worker failure",
		activeFailure.GetDetails().GetOriginalWorkerErrorDetail(),
	)
	require.Equal(
		t,
		int32(codes.Unavailable),
		activeFailure.GetDetails().GetOriginalWorkerErrorStatus(),
	)
	require.Equal(t, "java worker stack", activeFailure.GetDetails().GetOriginalWorkerErrorStackTrace())
	requireStepMethodFailureJSON(t, activeFailure)
	flowResult, err := runtime.FlowClient.WaitForFlow(
		ctx,
		&dexpb.WaitForFlowRequest{FlowId: flowID},
	)
	require.NoError(t, err)
	if terminal {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, flowResult.GetFlowStatus())
	} else {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, flowResult.GetFlowStatus())
	}

	events, nextInternalEventID := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	eventContext, terminalFailure := syncRetryStepEvent(events, method, terminal)
	require.NotNil(t, eventContext)
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_SYNC, eventContext.GetDurability())
	require.Equal(t, int32(2), eventContext.GetFinalAttempt())
	lastFailure := eventContext.GetLastFailureInfo()
	require.NotNil(t, lastFailure)
	require.Equal(t, int32(1), lastFailure.GetAttempt())
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		lastFailure.GetBackendError(),
	)
	require.Empty(t, lastFailure.GetDetails().GetDetail())
	require.Equal(t, "WorkerRetryError", lastFailure.GetDetails().GetOriginalWorkerErrorType())
	require.Equal(t, "retryable worker failure", lastFailure.GetDetails().GetOriginalWorkerErrorDetail())
	require.Equal(t, int32(codes.Unavailable), lastFailure.GetDetails().GetOriginalWorkerErrorStatus())
	require.Equal(t, "java worker stack", lastFailure.GetDetails().GetOriginalWorkerErrorStackTrace())
	requireStepMethodFailureJSON(t, lastFailure)
	if terminal {
		require.NotNil(t, terminalFailure)
		require.Equal(t, int32(2), terminalFailure.GetAttempt())
		require.Empty(t, terminalFailure.GetDetails().GetDetail())
		require.Equal(
			t,
			"retryable worker failure",
			terminalFailure.GetDetails().GetOriginalWorkerErrorDetail(),
		)
		requireStepMethodFailureJSON(t, terminalFailure)
	} else {
		require.Nil(t, terminalFailure)
	}
}

func testWebSyncTimeoutFailure(
	t *testing.T,
	backendType service.BackendType,
	method string,
) {
	release := make(chan struct{})
	defer close(release)
	handler := &webSyncTimeoutHandler{
		flowType: "web-sync-timeout-" + method,
		method:   method,
		release:  release,
	}
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "timeout-step",
		StepOptions:        webSyncTimeoutStepOptions(method),
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_SYNC),
			WorkerTarget:   workerTarget,
		}},
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
	eventContext, terminalFailure := syncRetryStepEvent(events, method, true)
	require.NotNil(t, eventContext)
	require.Equal(t, int32(1), eventContext.GetFinalAttempt())
	require.Nil(t, eventContext.GetLastFailureInfo())
	require.NotNil(t, terminalFailure)
	require.Equal(t, int32(1), terminalFailure.GetAttempt())
	requireSyncTimeoutFailure(t, backendType, terminalFailure)
	requireStepMethodFailureJSON(t, terminalFailure)
}

func requireSyncTimeoutFailure(
	t *testing.T,
	backendType service.BackendType,
	failure *dexpb.StepMethodFailure,
) {
	t.Helper()
	expectedBackendError := "START_TO_CLOSE"
	switch backendType {
	case service.BackendTypeTemporal:
		expectedBackendError = "StartToClose"
	case service.BackendTypeCadence:
	default:
		t.Fatalf("unexpected backend type %v", backendType)
	}
	if failure.GetBackendError() == expectedBackendError {
		require.Nil(t, failure.GetDetails())
		return
	}
	require.Equal(
		t,
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_WORKER_API_FAIL.String(),
		failure.GetBackendError(),
	)
	details := failure.GetDetails()
	require.NotNil(t, details)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_WORKER_API_ERROR,
		details.GetSubStatus(),
	)
	workerStatus := codes.Code(details.GetOriginalWorkerErrorStatus())
	workerDetail := details.GetDetail()
	if workerStatus == codes.DeadlineExceeded {
		if strings.Contains(workerDetail, "context deadline exceeded") ||
			strings.Contains(workerDetail, "RST_STREAM") {
			return
		}
	}
	if backendType == service.BackendTypeCadence && workerStatus == codes.Internal {
		require.Contains(t, workerDetail, "RST_STREAM")
		return
	}
	t.Fatalf("unexpected worker status %v: %q", workerStatus, workerDetail)
}

func requireStepMethodFailureJSON(t *testing.T, failure *dexpb.StepMethodFailure) {
	t.Helper()
	encoded, err := protojson.Marshal(failure)
	require.NoError(t, err)
	jsonValue := string(encoded)
	require.Contains(t, jsonValue, `"backendError"`)
	for _, removedField := range []string{"message", "errorType", "stackTrace", "retryState"} {
		require.NotContains(t, jsonValue, `"`+removedField+`"`)
	}
}

func webSyncRetryStepOptions(method string) *dexpb.StepOptions {
	retryPolicy := &dexpb.RetryPolicy{
		InitialIntervalSeconds: 3,
		BackoffCoefficient:     1,
		MaximumIntervalSeconds: 3,
		MaximumAttempts:        2,
	}
	if method == "execute" {
		return &dexpb.StepOptions{
			SkipWaitFor:           true,
			ExecuteTimeoutSeconds: 5,
			ExecuteRetryPolicy:    retryPolicy,
		}
	}
	return &dexpb.StepOptions{
		WaitForTimeoutSeconds: 5,
		WaitForRetryPolicy:    retryPolicy,
	}
}

func webSyncTimeoutStepOptions(method string) *dexpb.StepOptions {
	retryPolicy := &dexpb.RetryPolicy{MaximumAttempts: 1}
	if method == "execute" {
		return &dexpb.StepOptions{
			SkipWaitFor:           true,
			ExecuteTimeoutSeconds: 1,
			ExecuteRetryPolicy:    retryPolicy,
		}
	}
	return &dexpb.StepOptions{
		WaitForTimeoutSeconds: 1,
		WaitForRetryPolicy:    retryPolicy,
	}
}

func syncRetryStepEvent(
	events []*dexpb.FlowHistoryEvent,
	method string,
	terminal bool,
) (*dexpb.StepMethodEventContext, *dexpb.StepMethodFailure) {
	for _, event := range events {
		switch {
		case method == "wait-for" && terminal && event.GetStepWaitForFailed() != nil:
			payload := event.GetStepWaitForFailed()
			return payload.GetContext(), payload.GetOutput().GetFailure()
		case method == "wait-for" && !terminal && event.GetStepWaitForCompleted() != nil:
			return event.GetStepWaitForCompleted().GetContext(), nil
		case method == "execute" && terminal && event.GetStepExecuteFailed() != nil:
			payload := event.GetStepExecuteFailed()
			return payload.GetContext(), payload.GetOutput().GetFailure()
		case method == "execute" && !terminal && event.GetStepExecuteCompleted() != nil:
			return event.GetStepExecuteCompleted().GetContext(), nil
		}
	}
	return nil, nil
}

type webSyncRetryHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType        string
	method          string
	terminal        bool
	waitCalls       atomic.Int32
	executeCalls    atomic.Int32
	failureObserved chan struct{}
}

func (h *webSyncRetryHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	attempt := h.waitCalls.Add(1)
	if h.method == "wait-for" && (attempt == 1 || h.terminal) {
		if attempt == 1 {
			close(h.failureObserved)
		}
		return nil, webSyncRetryError(h.method, attempt)
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *webSyncRetryHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	attempt := h.executeCalls.Add(1)
	if h.method == "execute" && (attempt == 1 || h.terminal) {
		if attempt == 1 {
			close(h.failureObserved)
		}
		return nil, webSyncRetryError(h.method, attempt)
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
		},
	}}, nil
}

func webSyncRetryError(method string, attempt int32) error {
	workerStatus, err := status.New(
		codes.Unavailable,
		fmt.Sprintf("%s failure %d", method, attempt),
	).WithDetails(&dexpb.WorkerErrorResponse{
		Detail:     "retryable worker failure",
		ErrorType:  "WorkerRetryError",
		StackTrace: "java worker stack",
	})
	if err != nil {
		return err
	}
	return workerStatus.Err()
}

type webSyncTimeoutHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType string
	method   string
	release  <-chan struct{}
}

func (h *webSyncTimeoutHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	if h.method == "wait-for" {
		<-h.release
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *webSyncTimeoutHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	if h.method == "execute" {
		<-h.release
	}
	return &dexpb.InvokeExecuteMethodResponse{}, nil
}

func testWebStepInputWithoutStorage(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:      backendType,
		BlobStoreEnabled: ptr.Any(false),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := fmt.Sprintf("web-storage-disabled-%s-%s", durability, uuid.NewString())
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      basic.Step1,
		StepInput:          &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "input"}},
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(durability),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId())
	waitForEvent := firstStepEvent(events)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, waitForEvent)
	require.NotNil(t, executeEvent)
	waitForInput := waitForEvent.GetInput()
	executeInput := executeEvent.GetInput()
	require.NotNil(t, waitForInput)
	require.NotNil(t, executeInput)
	expectedUnavailable := durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC
	require.Equal(t, expectedUnavailable, waitForInput.GetUnavailable())
	require.Equal(t, expectedUnavailable, executeInput.GetUnavailable())
}

func testWebParallelAttributeSnapshots(t *testing.T, backendType service.BackendType) {
	handler := newWebParallelSnapshotHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := handler.flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           handler.flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      handler.rootStep,
		StepInput: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue("parallel-root-input"),
		}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{webStringAttribute("snapshot", "initial")},
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)

	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	var executeEvents []*dexpb.StepExecuteCompletedEvent
	for _, event := range events {
		execute := event.GetStepExecuteCompleted()
		if execute == nil || execute.GetContext().GetStepType() == handler.rootStep {
			continue
		}
		executeEvents = append(executeEvents, execute)
	}
	require.Len(t, executeEvents, 2)
	values := make([]*dexpb.Value, 0, len(executeEvents))
	for _, executeEvent := range executeEvents {
		require.NotNil(t, executeEvent.GetInput())
		require.Len(t, executeEvent.GetInput().GetAttributes(), 1)
		require.Equal(t, "snapshot", executeEvent.GetInput().GetAttributes()[0].GetKey())
		values = append(values, executeEvent.GetInput().GetAttributes()[0].GetValue())
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	for _, executeEvent := range executeEvents {
		require.Equal(
			t,
			largeWebTestValue("root-snapshot"),
			resolvedWebStringValue(executeEvent.GetInput().GetAttributes()[0].GetValue(), loadedValues),
		)
		expected := handler.executeRequest(executeEvent.GetContext().GetStepExecutionId())
		require.NotNil(t, expected)
		require.True(t, proto.Equal(stepMethodEventInputFromExecuteRequest(expected), executeEvent.GetInput()))
		require.Equal(t, expected.GetStepType(), executeEvent.GetContext().GetStepType())
		require.Equal(
			t,
			expected.GetContext().GetFromStepExecutionId(),
			executeEvent.GetContext().GetFromStepExecutionId(),
		)
	}
}

func stepMethodEventInputFromExecuteRequest(
	request *dexpb.InvokeExecuteMethodRequest,
) *dexpb.StepMethodEventInput {
	return &dexpb.StepMethodEventInput{
		StepInput:           request.GetStepInput(),
		ConditionResults:    request.GetConditionResults(),
		Attributes:          request.GetAttributes(),
		StepExecutionLocals: request.GetStepExeLocals(),
	}
}

type webParallelSnapshotHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType        string
	rootStep        string
	leftStep        string
	rightStep       string
	requestsMutex   sync.Mutex
	executeRequests map[string][]byte
}

func newWebParallelSnapshotHandler() *webParallelSnapshotHandler {
	return &webParallelSnapshotHandler{
		flowType:        "web-parallel-snapshot",
		rootStep:        "root",
		leftStep:        "left",
		rightStep:       "right",
		executeRequests: map[string][]byte{},
	}
}

func (h *webParallelSnapshotHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *webParallelSnapshotHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal received execute request: %w", err)
	}
	h.requestsMutex.Lock()
	h.executeRequests[request.GetContext().GetStepExecutionId()] = data
	h.requestsMutex.Unlock()

	switch request.GetStepType() {
	case h.rootStep:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{
				{StepType: h.leftStep, StepInput: request.GetStepInput()},
				{StepType: h.rightStep, StepInput: request.GetStepInput()},
			}},
			UpsertAttributes: []*dexpb.AttributeWrite{
				webStringAttribute("snapshot", "root-snapshot"),
			},
		}, nil
	case h.leftStep:
		time.Sleep(100 * time.Millisecond)
		return h.closeParallelBranch(request), nil
	case h.rightStep:
		return h.closeParallelBranch(request), nil
	default:
		return nil, fmt.Errorf("unexpected step type %q", request.GetStepType())
	}
}

func (h *webParallelSnapshotHandler) closeParallelBranch(
	request *dexpb.InvokeExecuteMethodRequest,
) *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			CloseInput:        request.GetStepInput(),
		}},
		UpsertAttributes: []*dexpb.AttributeWrite{
			webStringAttribute("snapshot", request.GetStepType()),
		},
	}
}

func (h *webParallelSnapshotHandler) executeRequest(stepExecutionID string) *dexpb.InvokeExecuteMethodRequest {
	h.requestsMutex.Lock()
	defer h.requestsMutex.Unlock()
	data := h.executeRequests[stepExecutionID]
	if len(data) == 0 {
		return nil
	}
	request := &dexpb.InvokeExecuteMethodRequest{}
	if err := proto.Unmarshal(data, request); err != nil {
		panic(err)
	}
	return request
}

func webStringAttribute(key string, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue(value),
		}},
	}
}

func testWebConditionResults(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
	conditionType string,
) {
	flowType := fmt.Sprintf("web-step-input-%s-%s", durability, conditionType)
	workerTarget := startWorker(t, &webStepInputHandler{flowType: flowType, conditionType: conditionType})
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(true),
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := flowType + "-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           flowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      "condition-step",
		StepOptions: &dexpb.StepOptions{
			WaitForTimeoutSeconds: 11,
			ExecuteTimeoutSeconds: 13,
			WaitForRetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 2,
				BackoffCoefficient:     3,
				MaximumIntervalSeconds: 4,
				MaximumAttempts:        5,
				TotalDurationSeconds:   60,
			},
			ExecuteRetryPolicy: &dexpb.RetryPolicy{
				InitialIntervalSeconds: 3,
				BackoffCoefficient:     4,
				MaximumIntervalSeconds: 5,
				MaximumAttempts:        6,
				TotalDurationSeconds:   70,
			},
		},
		StepInput: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: largeWebTestValue("condition-step-input"),
		}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{{
				Key: "condition-attribute",
				Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
					StringValue: largeWebTestValue("condition-attribute"),
				}},
			}},
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(durability),
				WorkerTarget:   workerTarget,
			},
		},
	})
	require.NoError(t, err)
	if conditionType == "any" {
		_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
			Messages: []*dexpb.ChannelMessage{{
				ChannelName: "condition-channel",
				Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
					StringValue: largeWebTestValue("condition-channel-value"),
				}},
			}},
		})
		require.NoError(t, err)
	}
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	waitForEvent := firstStepEvent(events)
	require.NotNil(t, waitForEvent)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, executeEvent)
	waitForInput := waitForEvent.GetInput()
	executeInput := executeEvent.GetInput()
	require.NotNil(t, waitForInput)
	require.NotNil(t, executeInput)
	assertStepMethodOptions(t, waitForEvent.GetContext().GetMethodOptions(), 11, 2, 3, 4, 5, 60)
	assertStepMethodOptions(t, executeEvent.GetContext().GetMethodOptions(), 13, 3, 4, 5, 6, 70)
	require.Nil(t, waitForEvent.GetContext().GetLastFailureInfo())
	require.Nil(t, executeEvent.GetContext().GetLastFailureInfo())
	require.Len(t, waitForInput.GetAttributes(), 1)
	require.Len(t, executeInput.GetAttributes(), 1)
	require.Len(t, executeInput.GetStepExecutionLocals(), 1)
	values := []*dexpb.Value{
		waitForInput.GetStepInput(),
		waitForInput.GetAttributes()[0].GetValue(),
		executeInput.GetStepInput(),
		executeInput.GetAttributes()[0].GetValue(),
		executeInput.GetStepExecutionLocals()[0].GetValue(),
	}
	for _, channelResult := range executeInput.GetConditionResults().GetChannelResults() {
		values = append(values, channelResult.GetValues()...)
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	require.Equal(
		t,
		largeWebTestValue("condition-step-input"),
		resolvedWebStringValue(waitForInput.GetStepInput(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-attribute"),
		resolvedWebStringValue(waitForInput.GetAttributes()[0].GetValue(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-step-input"),
		resolvedWebStringValue(executeInput.GetStepInput(), loadedValues),
	)
	require.Equal(
		t,
		largeWebTestValue("condition-attribute"),
		resolvedWebStringValue(executeInput.GetAttributes()[0].GetValue(), loadedValues),
	)
	require.Equal(t, "condition-local", executeInput.GetStepExecutionLocals()[0].GetKey())
	require.Equal(
		t,
		largeWebTestValue("condition-local"),
		resolvedWebStringValue(executeInput.GetStepExecutionLocals()[0].GetValue(), loadedValues),
	)
	if conditionType == "any" {
		require.Len(t, executeInput.GetConditionResults().GetChannelResults(), 1)
		require.Len(t, executeInput.GetConditionResults().GetTimerResults(), 2)
		require.Equal(
			t,
			largeWebTestValue("condition-channel-value"),
			resolvedWebStringValue(
				executeInput.GetConditionResults().GetChannelResults()[0].GetValues()[0],
				loadedValues,
			),
		)
		for _, result := range executeInput.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, result.GetConditionStatus())
		}
	} else {
		require.Empty(t, executeInput.GetConditionResults().GetChannelResults())
		require.Len(t, executeInput.GetConditionResults().GetTimerResults(), 2)
		for _, result := range executeInput.GetConditionResults().GetTimerResults() {
			require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		}
	}
}

func assertStepMethodOptions(
	t *testing.T,
	options *dexpb.StepMethodOptions,
	timeout int32,
	initialInterval int32,
	backoff float32,
	maximumInterval int32,
	maximumAttempts int32,
	totalDuration int32,
) {
	t.Helper()
	require.Equal(t, timeout, options.GetTimeoutSeconds())
	require.Equal(t, initialInterval, options.GetRetryPolicy().GetInitialIntervalSeconds())
	require.Equal(t, backoff, options.GetRetryPolicy().GetBackoffCoefficient())
	require.Equal(t, maximumInterval, options.GetRetryPolicy().GetMaximumIntervalSeconds())
	require.Equal(t, maximumAttempts, options.GetRetryPolicy().GetMaximumAttempts())
	require.Equal(t, totalDuration, options.GetRetryPolicy().GetTotalDurationSeconds())
}

type webStepInputHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowType      string
	conditionType string
}

func (h *webStepInputHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	condition := &dexpb.WaitingCondition{}
	if h.conditionType == "any" {
		condition.WaitingConditionType = dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED
		condition.ChannelConditions = []*dexpb.ChannelCondition{{
			ConditionId: "channel",
			ChannelName: "condition-channel",
		}}
		condition.TimerConditions = []*dexpb.TimerCondition{
			{ConditionId: "timer-1", DurationSeconds: 60},
			{ConditionId: "timer-2", DurationSeconds: 120},
		}
	} else {
		condition.WaitingConditionType = dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED
		condition.TimerConditions = []*dexpb.TimerCondition{
			{ConditionId: "timer-1", DurationSeconds: 1},
			{ConditionId: "timer-2", DurationSeconds: 1},
		}
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: condition,
		UpsertStepExeLocals: []*dexpb.KV{{
			Key: "condition-local",
			Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: largeWebTestValue("condition-local"),
			}},
		}},
	}, nil
}

func (h *webStepInputHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != h.flowType {
		return nil, fmt.Errorf("unexpected flow type %q", request.GetFlowType())
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
			CloseInput:        request.GetStepInput(),
		},
	}}, nil
}

func testWebHistoryAndSummary(
	t *testing.T,
	backendType service.BackendType,
	durability dexpb.StepDurability,
	lazyLoading bool,
) {
	workerTarget := startWorker(t, basic.NewHandler())
	blobDirectory := t.TempDir()
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(lazyLoading),
		LocalBlobDirectory: blobDirectory,
		LocalBlobThreshold: 10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	flowID := fmt.Sprintf("%s-web-%s-%s", basic.FlowType, durability, uuid.NewString())
	stepInput := largeWebTestValue("step-input")
	attributePayload := []byte(fmt.Sprintf(`{"source":%q}`, largeWebTestValue("web-step-event-input")))
	requestID := newRequestID()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          requestID,
		FlowId:             flowID,
		FlowType:           basic.FlowType,
		FlowTimeoutSeconds: 60,
		StartStepType:      basic.Step1,
		StepInput:          &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: stepInput}},
		FlowStartOptions: &dexpb.FlowStartOptions{
			Attributes: []*dexpb.AttributeWrite{{
				Key: "web-test-attribute",
				Value: &dexpb.Value{Kind: &dexpb.Value_ObjValue{ObjValue: &dexpb.EncodedObject{
					Encoding: "json",
					Payload:  attributePayload,
				}}},
			}},
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
	flowStarted := firstRunEvents[0].GetFlowStartedOrContinued()
	require.Equal(t, time.Minute, flowStarted.GetFlowTimeout().AsDuration())
	require.Equal(
		t,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		flowStarted.GetFlowTimeoutPolicy(),
	)
	initialStart := flowStarted.GetInitialStart()
	require.NotNil(t, initialStart)
	require.NotEmpty(t, initialStart.GetStepInput().GetInternalBlobIdForStringValue())
	require.Len(t, initialStart.GetInitialAttributes(), 1)
	require.NotEmpty(t, initialStart.GetInitialAttributes()[0].GetValue().GetInternalBlobIdForObjValue())
	loadedStartValues, err := runtime.FlowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: []*dexpb.Value{
		initialStart.GetStepInput(),
		initialStart.GetInitialAttributes()[0].GetValue(),
	}})
	require.NoError(t, err)
	require.Equal(t, stepInput, loadedStartValues.GetValues()[initialStart.GetStepInput().GetInternalBlobIdForStringValue()].GetStringValue())
	require.Equal(t, attributePayload, loadedStartValues.GetValues()[initialStart.GetInitialAttributes()[0].GetValue().GetInternalBlobIdForObjValue()].GetObjValue().GetPayload())
	firstStep := firstStepEvent(firstRunEvents)
	require.NotNil(t, firstStep)
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		firstStep.GetContext().GetFromStepExecutionId(),
	)
	firstExecute := firstExecuteEvent(firstRunEvents)
	require.NotNil(t, firstExecute)
	require.NotNil(t, firstStep.GetInput())
	require.NotNil(t, firstExecute.GetInput())
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		firstStep.GetInput().GetStepInput(),
		firstStep.GetInput().GetAttributes(),
		stepInput,
		attributePayload,
	)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		firstExecute.GetInput().GetStepInput(),
		firstExecute.GetInput().GetAttributes(),
		stepInput,
		attributePayload,
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
	continuedFlowStarted := continuedEvents[0].GetFlowStartedOrContinued()
	require.Equal(t, time.Minute, continuedFlowStarted.GetFlowTimeout().AsDuration())
	require.Equal(
		t,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		continuedFlowStarted.GetFlowTimeoutPolicy(),
	)
	continuedStart := continuedFlowStarted.GetContinuedStart()
	require.NotNil(t, continuedStart)
	require.Equal(t, startResponse.GetRunId(), continuedStart.GetPreviousRunId())
	require.True(
		t,
		len(continuedStart.GetStepsToStart()) > 0 ||
			len(continuedStart.GetStepsToResume()) > 0 ||
			len(continuedStart.GetCompletedSteps()) > 0,
	)
	continuedStep := firstStepEvent(continuedEvents)
	require.NotNil(t, continuedStep)
	continuedExecute := firstExecuteEvent(continuedEvents)
	require.NotNil(t, continuedExecute)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		continuedStep.GetInput().GetStepInput(),
		continuedStep.GetInput().GetAttributes(),
		stepInput,
		attributePayload,
	)
	assertStepMethodRequestValues(
		t,
		ctx,
		runtime.FlowClient,
		continuedExecute.GetInput().GetStepInput(),
		continuedExecute.GetInput().GetAttributes(),
		stepInput,
		attributePayload,
	)
	closeOutput := firstWebCloseOutput(continuedEvents)
	require.NotNil(t, closeOutput)
	require.NotEmpty(t, closeOutput.GetInternalBlobIdForStringValue())
	loadedCloseOutput, err := runtime.FlowClient.LoadBlobs(
		ctx,
		&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{closeOutput}},
	)
	require.NoError(t, err)
	require.Equal(
		t,
		stepInput,
		loadedCloseOutput.GetValues()[closeOutput.GetInternalBlobIdForStringValue()].GetStringValue(),
	)
	unknownStoreBlobID := "unknown-store|" + strings.SplitN(
		closeOutput.GetInternalBlobIdForStringValue(),
		"|",
		2,
	)[1]
	partiallyLoaded, err := runtime.FlowClient.LoadBlobs(
		ctx,
		&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{
			closeOutput,
			{Kind: &dexpb.Value_InternalBlobIdForStringValue{
				InternalBlobIdForStringValue: unknownStoreBlobID,
			}},
		}},
	)
	require.NoError(t, err)
	require.Len(t, partiallyLoaded.GetValues(), 1)
	require.Contains(t, partiallyLoaded.GetValues(), closeOutput.GetInternalBlobIdForStringValue())

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
	if durability == dexpb.StepDurability_STEP_DURABILITY_ASYNC && lazyLoading && *dexServerAddress == "" {
		stepInputBlobID := firstStep.GetInput().GetStepInput().GetInternalBlobIdForStringValue()
		require.NotEmpty(t, stepInputBlobID)
		stepInputObjectPath := strings.SplitN(stepInputBlobID, "|", 2)[1]
		require.NoError(t, os.Remove(filepath.Join(blobDirectory, "default", stepInputObjectPath)))
		valueMissingEvents, _ := getAllWebHistoryEvents(
			t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
		)
		require.False(t, firstStepEvent(valueMissingEvents).GetInput().GetUnavailable())
		missingValue, loadErr := runtime.FlowClient.LoadBlobs(
			ctx,
			&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{firstStep.GetInput().GetStepInput()}},
		)
		require.NoError(t, loadErr)
		require.Empty(t, missingValue.GetValues())

		require.NoError(t, os.RemoveAll(blobDirectory))
		unavailableBlobs, loadErr := runtime.FlowClient.LoadBlobs(
			ctx,
			&dexpb.LoadBlobsRequest{Values: []*dexpb.Value{closeOutput}},
		)
		require.NoError(t, loadErr)
		require.Empty(t, unavailableBlobs.GetValues())
		unavailableEvents, _ := getAllWebHistoryEvents(
			t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
		)
		require.True(t, firstStepEvent(unavailableEvents).GetInput().GetUnavailable())
		require.True(t, firstExecuteEvent(unavailableEvents).GetInput().GetUnavailable())
	}
}

func testWebCurrentState(t *testing.T, backendType service.BackendType, lazyLoading bool) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{
		BackendType:        backendType,
		LazyLoading:        ptr.Any(lazyLoading),
		LocalBlobDirectory: t.TempDir(),
		LocalBlobThreshold: 10,
	})
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
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   workerTarget,
			},
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
		if stateErr != nil {
			return false
		}
		activeUserSteps := userActiveStepExecutions(state)
		if len(activeUserSteps) != 1 {
			return false
		}
		active := activeUserSteps[0]
		return active.GetStepExecutionId() == signal.State1+"-1" &&
			active.GetFromStepExecutionId() == service.StartingStepFromStepExecutionId &&
			active.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING
	}, 30*time.Second, 50*time.Millisecond)

	messages := make([]*dexpb.ChannelMessage, 4)
	for index := range messages {
		messages[index] = &dexpb.ChannelMessage{
			ChannelName: signal.SignalName,
			Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
				StringValue: largeWebTestValue(fmt.Sprintf("channel-value-%d", index)),
			}},
		}
	}
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: messages,
	})
	require.NoError(t, err)
	_, err = runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	events, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
	executeEvent := firstExecuteEvent(events)
	require.NotNil(t, executeEvent)
	channelResults := executeEvent.GetInput().GetConditionResults().GetChannelResults()
	require.Len(t, channelResults, 4)
	values := make([]*dexpb.Value, 0, len(channelResults))
	for _, result := range channelResults {
		values = append(values, result.GetValues()...)
	}
	loadedValues := loadWebBlobValues(t, ctx, runtime.FlowClient, values)
	for index, result := range channelResults {
		require.Equal(t, signal.SignalName, result.GetChannelName())
		require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, result.GetConditionStatus())
		require.Len(t, result.GetValues(), 1)
		require.Equal(
			t,
			largeWebTestValue(fmt.Sprintf("channel-value-%d", index)),
			resolvedWebStringValue(result.GetValues()[0], loadedValues),
		)
	}
	assertExternalChannelValuesLoad(t, ctx, runtime.FlowClient, events)
}

func testWebSetAttributesHistory(t *testing.T, backendType service.BackendType) {
	workerTarget := startWorker(t, signal.NewHandler())
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	flowID := signal.WorkflowType + "-web-set-attribute-" + uuid.NewString()
	startResponse, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           signal.WorkflowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      signal.State1,
		FlowStartOptions: &dexpb.FlowStartOptions{FlowConfigOverride: &dexpb.FlowConfig{
			StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
			WorkerTarget:   workerTarget,
		}},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{
			FlowId: flowID,
			RunId:  startResponse.GetRunId(),
		})
		return stateErr == nil && len(userActiveStepExecutions(state)) == 1
	}, 30*time.Second, 50*time.Millisecond)
	_, nextInternalEventID := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)

	attribute := &dexpb.AttributeWrite{
		Key: "web-set-attribute",
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{
			StringValue: "updated-from-web",
		}},
	}
	_, err = runtime.FlowClient.SetAttributes(ctx, &dexpb.SetAttributesRequest{
		FlowId:    flowID,
		RunId:     startResponse.GetRunId(),
		RequestId: newRequestID(),
		Attributes: []*dexpb.AttributeWrite{
			attribute,
		},
	})
	require.NoError(t, err)
	waitResponse, err := runtime.FlowClient.WaitForHistoryEvent(ctx, &dexpb.WaitForHistoryEventRequest{
		FlowId:              flowID,
		RunId:               startResponse.GetRunId(),
		NextInternalEventId: nextInternalEventID,
	})
	require.NoError(t, err)
	require.True(t, waitResponse.GetEventAvailable())
	events, _ := getAllWebHistoryEvents(
		t, ctx, runtime.FlowClient, flowID, startResponse.GetRunId(),
	)
	var rpcEvent *dexpb.RpcExecutionCompletedEvent
	for _, event := range events {
		candidate := event.GetRpcExecutionCompleted()
		if candidate.GetIsSetAttributeApi() {
			rpcEvent = candidate
			break
		}
	}
	require.NotNil(t, rpcEvent)
	require.Empty(t, rpcEvent.GetRpcName())
	require.Len(t, rpcEvent.GetUpsertAttributes(), 1)
	require.True(t, proto.Equal(attribute, rpcEvent.GetUpsertAttributes()[0]))
	require.Nil(t, rpcEvent.GetInput())
	require.Nil(t, rpcEvent.GetOutput())
	require.Nil(t, rpcEvent.GetStepDecision())

	messages := make([]*dexpb.ChannelMessage, 4)
	for index := range messages {
		messages[index] = &dexpb.ChannelMessage{ChannelName: signal.SignalName}
	}
	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		RunId:    startResponse.GetRunId(),
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

	events, nextInternalEventID := getAllWebHistoryEvents(
		t,
		ctx,
		runtime.FlowClient,
		flowID,
		startResponse.GetRunId(),
	)
	require.Positive(t, nextInternalEventID)
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
		failedEvent.GetContext().GetDurability(),
	)
	require.Positive(t, failedEvent.GetContext().GetFinalAttempt())
	require.Equal(
		t,
		service.StartingStepFromStepExecutionId,
		failedEvent.GetContext().GetFromStepExecutionId(),
	)
	require.NotNil(t, failedEvent.GetInput())
	require.False(t, failedEvent.GetInput().GetUnavailable())
	require.NotNil(t, failedEvent.GetOutput().GetFailure())
	require.Nil(t, failedEvent.GetContext().GetLastFailureInfo())
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

func firstExecuteEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepExecuteCompletedEvent {
	for _, event := range events {
		if event.GetStepExecuteCompleted() != nil {
			return event.GetStepExecuteCompleted()
		}
	}
	return nil
}

func firstWebCloseOutput(events []*dexpb.FlowHistoryEvent) *dexpb.Value {
	for _, event := range events {
		results := event.GetFlowClosed().GetResults()
		if len(results) > 0 {
			return results[0].GetCompletedStepOutput()
		}
	}
	return nil
}

func assertStepMethodRequestValues(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	stepInput *dexpb.Value,
	attributes []*dexpb.KV,
	expectedInput string,
	expectedAttribute []byte,
) {
	t.Helper()
	require.Len(t, attributes, 1)
	require.Equal(t, "web-test-attribute", attributes[0].GetKey())
	loadedValues := loadWebBlobValues(
		t,
		ctx,
		flowClient,
		[]*dexpb.Value{stepInput, attributes[0].GetValue()},
	)
	require.Equal(t, expectedInput, resolvedWebStringValue(stepInput, loadedValues))
	attribute := attributes[0].GetValue()
	if blobID := attribute.GetInternalBlobIdForObjValue(); blobID != "" {
		attribute = loadedValues[blobID]
	}
	require.Equal(t, expectedAttribute, attribute.GetObjValue().GetPayload())
}

func loadWebBlobValues(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	values []*dexpb.Value,
) map[string]*dexpb.Value {
	t.Helper()
	var blobValues []*dexpb.Value
	blobIDs := map[string]struct{}{}
	for _, value := range values {
		blobID := value.GetInternalBlobIdForStringValue()
		if blobID == "" {
			blobID = value.GetInternalBlobIdForObjValue()
		}
		if blobID == "" {
			continue
		}
		if _, exists := blobIDs[blobID]; !exists {
			blobIDs[blobID] = struct{}{}
			blobValues = append(blobValues, value)
		}
	}
	if len(blobValues) == 0 {
		return nil
	}
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: blobValues})
	require.NoError(t, err)
	require.Len(t, response.GetValues(), len(blobValues))
	return response.GetValues()
}

func resolvedWebStringValue(value *dexpb.Value, loadedValues map[string]*dexpb.Value) string {
	if blobID := value.GetInternalBlobIdForStringValue(); blobID != "" {
		return loadedValues[blobID].GetStringValue()
	}
	return value.GetStringValue()
}

func assertExternalChannelValuesLoad(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	events []*dexpb.FlowHistoryEvent,
) {
	t.Helper()
	var values []*dexpb.Value
	for _, event := range events {
		for _, message := range event.GetChannelExternalPublish().GetMessages() {
			values = append(values, message.GetValue())
		}
	}
	require.Len(t, values, 4)
	for _, value := range values {
		require.NotEmpty(t, value.GetInternalBlobIdForStringValue())
	}
	response, err := flowClient.LoadBlobs(ctx, &dexpb.LoadBlobsRequest{Values: values})
	require.NoError(t, err)
	for index, value := range values {
		loaded := response.GetValues()[value.GetInternalBlobIdForStringValue()]
		require.Equal(t, largeWebTestValue(fmt.Sprintf("channel-value-%d", index)), loaded.GetStringValue())
	}
}

func largeWebTestValue(prefix string) string {
	return prefix + "-" + strings.Repeat("value", 300)
}

func continuedToRunID(events []*dexpb.FlowHistoryEvent) string {
	for _, event := range events {
		if event.GetFlowClosed().GetContinuedToRunId() != "" {
			return event.GetFlowClosed().GetContinuedToRunId()
		}
	}
	return ""
}
