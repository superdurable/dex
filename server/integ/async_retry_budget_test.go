// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package integ

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	workflowcommon "github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
	dexconverter "github.com/superdurable/dex/service/common/converter"
	"github.com/superdurable/dex/service/common/ptr"
	"go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.uber.org/cadence/.gen/go/shared"
	"go.uber.org/cadence/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const asyncRetryBudgetFlowType = "async_retry_budget"

type asyncRetryAttempt struct {
	attempt               int32
	firstAttemptTimestamp int64
	receivedTime          time.Time
}

type asyncRetryBudgetHandler struct {
	dexpb.UnimplementedWorkerServiceServer

	mutex                sync.Mutex
	waitAttempts         []asyncRetryAttempt
	executeAttempts      []asyncRetryAttempt
	waitSuccessOnAttempt int32
	failExecute          bool
}

func (h *asyncRetryBudgetHandler) InvokeWaitForMethod(
	ctx context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	h.mutex.Lock()
	h.waitAttempts = append(h.waitAttempts, asyncRetryAttempt{
		attempt:               request.GetContext().GetAttempt(),
		firstAttemptTimestamp: request.GetContext().GetFirstAttemptTimestamp(),
		receivedTime:          time.Now(),
	})
	h.mutex.Unlock()
	if h.waitSuccessOnAttempt < 0 {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, "retry budget test timeout")
	}
	if request.GetContext().GetAttempt() != h.waitSuccessOnAttempt {
		return nil, status.Error(codes.Unavailable, "retry budget test failure")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		},
	}, nil
}

func (h *asyncRetryBudgetHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	h.mutex.Lock()
	h.executeAttempts = append(h.executeAttempts, asyncRetryAttempt{
		attempt:               request.GetContext().GetAttempt(),
		firstAttemptTimestamp: request.GetContext().GetFirstAttemptTimestamp(),
		receivedTime:          time.Now(),
	})
	h.mutex.Unlock()
	if h.failExecute {
		return nil, status.Error(codes.Unavailable, "execute retry budget test failure")
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: workflowcommon.GracefulCompleteDecision(request.GetStepInput()),
		},
	}, nil
}

func (h *asyncRetryBudgetHandler) attempts() []asyncRetryAttempt {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]asyncRetryAttempt(nil), h.waitAttempts...)
}

func (h *asyncRetryBudgetHandler) recordedExecuteAttempts() []asyncRetryAttempt {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]asyncRetryAttempt(nil), h.executeAttempts...)
}

func TestAsyncRetryBudgetTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	testAsyncRetryBudget(t, service.BackendTypeTemporal)
}

func TestAsyncRetryBudgetCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	testAsyncRetryBudget(t, service.BackendTypeCadence)
}

func testAsyncRetryBudget(t *testing.T, backendType service.BackendType) {
	t.Run("fallback-carries-attempts-and-adjusts-policy", func(t *testing.T) {
		testAsyncRetryFallback(t, backendType)
	})
	t.Run("multiple-local-attempts-carry-cumulative-context", func(t *testing.T) {
		testAsyncMultipleLocalAttempts(t, backendType)
	})
	t.Run("attempt-budget-exhausted-locally", func(t *testing.T) {
		testAsyncAttemptBudgetExhausted(t, backendType)
	})
	t.Run("duration-budget-exhausted-locally", func(t *testing.T) {
		testAsyncDurationBudgetExhausted(t, backendType)
	})
	t.Run("duration-and-attempt-budgets-bound-regular", func(t *testing.T) {
		testAsyncCombinedRetryBudget(t, backendType)
	})
	t.Run("execute-attempt-budget-exhausted-locally", func(t *testing.T) {
		testAsyncExecuteAttemptBudgetExhausted(t, backendType)
	})
}

func testAsyncRetryFallback(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{waitSuccessOnAttempt: 2}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID, runID := startAsyncRetryFlow(t, ctx, runtime, &dexpb.RetryPolicy{
		InitialIntervalSeconds: 8,
		BackoffCoefficient:     2,
		MaximumIntervalSeconds: 10,
		MaximumAttempts:        3,
		TotalDurationSeconds:   30,
	})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())

	attempts := handler.attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, int32(1), attempts[0].attempt)
	require.Equal(t, int32(2), attempts[1].attempt)
	require.Equal(t, attempts[0].firstAttemptTimestamp, attempts[1].firstAttemptTimestamp)
	require.Less(t, attempts[1].receivedTime.Sub(attempts[0].receivedTime), 4*time.Second)

	regularPolicy := regularFallbackRetryPolicy(t, ctx, runtime, backendType, flowID, runID)
	require.Equal(t, int32(10), regularPolicy.GetInitialIntervalSeconds())
	require.Equal(t, float32(2), regularPolicy.GetBackoffCoefficient())
	require.Equal(t, int32(10), regularPolicy.GetMaximumIntervalSeconds())
	require.Equal(t, int32(2), regularPolicy.GetMaximumAttempts())
	require.Equal(t, int32(30), regularPolicy.GetTotalDurationSeconds())

	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, runID)
	waitEvent := completedWaitForEvent(events)
	require.NotNil(t, waitEvent)
	require.Equal(t, int32(2), waitEvent.GetContext().GetFinalAttempt())
	require.Equal(t, int32(3), waitEvent.GetContext().GetMethodOptions().GetRetryPolicy().GetMaximumAttempts())
}

func testAsyncMultipleLocalAttempts(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID, runID := startAsyncRetryFlow(t, ctx, runtime, &dexpb.RetryPolicy{
		InitialIntervalSeconds: 1,
		BackoffCoefficient:     2,
		MaximumIntervalSeconds: 10,
		MaximumAttempts:        4,
		TotalDurationSeconds:   30,
	})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())

	attempts := handler.attempts()
	require.Len(t, attempts, 4)
	for index, attempt := range attempts {
		require.Equal(t, int32(index+1), attempt.attempt)
		if backendType == service.BackendTypeTemporal {
			require.Equal(t, attempts[0].firstAttemptTimestamp, attempt.firstAttemptTimestamp)
		}
	}
	require.Less(t, attempts[3].receivedTime.Sub(attempts[2].receivedTime), 2*time.Second)

	regularPolicy := regularFallbackRetryPolicy(t, ctx, runtime, backendType, flowID, runID)
	require.Equal(t, int32(8), regularPolicy.GetInitialIntervalSeconds())
	require.Equal(t, int32(1), regularPolicy.GetMaximumAttempts())
	require.GreaterOrEqual(t, regularPolicy.GetTotalDurationSeconds(), int32(26))
	require.LessOrEqual(t, regularPolicy.GetTotalDurationSeconds(), int32(27))

	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, runID)
	failedEvent := failedWaitForEvent(events)
	require.NotNil(t, failedEvent)
	require.Equal(t, int32(4), failedEvent.GetContext().GetFinalAttempt())
	require.Equal(t, int32(4), failedEvent.GetOutput().GetFailure().GetAttempt())
}

func testAsyncAttemptBudgetExhausted(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flowID, runID := startAsyncRetryFlow(t, ctx, runtime, &dexpb.RetryPolicy{MaximumAttempts: 1})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Contains(t, result.GetErrorMessage(), "retry budget test failure")
	require.Len(t, handler.attempts(), 1)

	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, runID)
	failedEvent := failedWaitForEvent(events)
	require.NotNil(t, failedEvent)
	require.NotNil(t, failedEvent.GetInput())
	require.True(t, failedEvent.GetInput().GetUnavailable())
	require.Equal(t, int32(1), failedEvent.GetContext().GetFinalAttempt())
	require.Equal(t, int32(1), failedEvent.GetOutput().GetFailure().GetAttempt())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, failedEvent.GetContext().GetDurability())
}

func testAsyncDurationBudgetExhausted(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{waitSuccessOnAttempt: -1}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flowID, _ := startAsyncRetryFlow(t, ctx, runtime, &dexpb.RetryPolicy{
		MaximumAttempts:      10,
		TotalDurationSeconds: 1,
	})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Len(t, handler.attempts(), 1)
}

func testAsyncCombinedRetryBudget(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flowID, runID := startAsyncRetryFlow(t, ctx, runtime, &dexpb.RetryPolicy{
		InitialIntervalSeconds: 2,
		BackoffCoefficient:     2,
		MaximumIntervalSeconds: 10,
		MaximumAttempts:        3,
		TotalDurationSeconds:   1,
	})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())

	attempts := handler.attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, int32(1), attempts[0].attempt)
	require.Equal(t, int32(2), attempts[1].attempt)
	require.Equal(t, attempts[0].firstAttemptTimestamp, attempts[1].firstAttemptTimestamp)

	regularPolicy := regularFallbackRetryPolicy(t, ctx, runtime, backendType, flowID, runID)
	require.Equal(t, int32(4), regularPolicy.GetInitialIntervalSeconds())
	require.Equal(t, int32(2), regularPolicy.GetMaximumAttempts())
	require.Equal(t, int32(1), regularPolicy.GetTotalDurationSeconds())

	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, runID)
	failedEvent := failedWaitForEvent(events)
	require.NotNil(t, failedEvent)
	require.Equal(t, int32(2), failedEvent.GetContext().GetFinalAttempt())
	require.Equal(t, int32(2), failedEvent.GetOutput().GetFailure().GetAttempt())
}

func testAsyncExecuteAttemptBudgetExhausted(t *testing.T, backendType service.BackendType) {
	handler := &asyncRetryBudgetHandler{waitSuccessOnAttempt: 1, failExecute: true}
	runtime := startDexServiceWithRetryHandler(t, backendType, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	flowID, runID := startAsyncRetryFlowWithOptions(t, ctx, runtime, &dexpb.StepOptions{
		WaitForRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1},
		ExecuteRetryPolicy: &dexpb.RetryPolicy{MaximumAttempts: 1},
	})
	result, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
	require.Contains(t, result.GetErrorMessage(), "execute retry budget test failure")

	executeAttempts := handler.recordedExecuteAttempts()
	require.Len(t, executeAttempts, 1)
	require.Equal(t, int32(1), executeAttempts[0].attempt)
	require.NotZero(t, executeAttempts[0].firstAttemptTimestamp)

	events, _ := getAllWebHistoryEvents(t, ctx, runtime.FlowClient, flowID, runID)
	failedEvent := failedExecuteEvent(events)
	require.NotNil(t, failedEvent)
	require.NotNil(t, failedEvent.GetInput())
	require.True(t, failedEvent.GetInput().GetUnavailable())
	require.Equal(t, int32(1), failedEvent.GetContext().GetFinalAttempt())
	require.Equal(t, int32(1), failedEvent.GetOutput().GetFailure().GetAttempt())
	require.Equal(t, dexpb.StepDurability_STEP_DURABILITY_ASYNC, failedEvent.GetContext().GetDurability())
}

func startDexServiceWithRetryHandler(
	t *testing.T,
	backendType service.BackendType,
	handler *asyncRetryBudgetHandler,
) *integRuntime {
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	runtime.defaultFlowConfig.WorkerTarget = workerTarget
	return runtime
}

func startAsyncRetryFlow(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	retryPolicy *dexpb.RetryPolicy,
) (string, string) {
	t.Helper()
	return startAsyncRetryFlowWithOptions(t, ctx, runtime, &dexpb.StepOptions{
		WaitForRetryPolicy: retryPolicy,
	})
}

func startAsyncRetryFlowWithOptions(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	stepOptions *dexpb.StepOptions,
) (string, string) {
	t.Helper()
	flowID := asyncRetryBudgetFlowType + "-" + uuid.NewString()
	response, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           asyncRetryBudgetFlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      "step",
		StepOptions:        stepOptions,
		FlowStartOptions: &dexpb.FlowStartOptions{
			FlowConfigOverride: &dexpb.FlowConfig{
				StepDurability: ptr.Any(dexpb.StepDurability_STEP_DURABILITY_ASYNC),
				WorkerTarget:   runtime.defaultFlowConfig.GetWorkerTarget(),
			},
		},
	})
	require.NoError(t, err)
	return flowID, response.GetRunId()
}

func regularFallbackRetryPolicy(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	backendType service.BackendType,
	flowID string,
	runID string,
) *dexpb.RetryPolicy {
	t.Helper()
	switch backendType {
	case service.BackendTypeTemporal:
		return temporalRegularFallbackRetryPolicy(t, ctx, runtime, flowID, runID)
	case service.BackendTypeCadence:
		return cadenceRegularFallbackRetryPolicy(t, ctx, runtime, flowID, runID)
	default:
		require.FailNow(t, "unsupported backend", backendType)
		return nil
	}
}

func temporalRegularFallbackRetryPolicy(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
) *dexpb.RetryPolicy {
	t.Helper()
	api := runtime.UnifiedClient.GetApiService().(workflowservice.WorkflowServiceClient)
	response, err := api.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace: testNamespace,
		Execution: &common.WorkflowExecution{
			WorkflowId: flowID,
			RunId:      runID,
		},
		MaximumPageSize: 1000,
	})
	require.NoError(t, err)
	dataConverter := dexconverter.NewTemporalDataConverter()
	for _, historyEvent := range response.GetHistory().GetEvents() {
		if historyEvent.GetEventType() != enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
			continue
		}
		attributes := historyEvent.GetActivityTaskScheduledEventAttributes()
		if !strings.Contains(attributes.GetActivityType().GetName(), "InvokeWaitForMethod") {
			continue
		}
		var input dexpb.InvokeWaitForMethodActivityInput
		require.NoError(t, dataConverter.FromPayloads(attributes.GetInput(), &input))
		if input.GetRequest().GetContext().GetAttempt() == 0 {
			continue
		}
		policy := attributes.GetRetryPolicy()
		return &dexpb.RetryPolicy{
			InitialIntervalSeconds: int32(policy.GetInitialInterval().AsDuration() / time.Second),
			BackoffCoefficient:     float32(policy.GetBackoffCoefficient()),
			MaximumIntervalSeconds: int32(policy.GetMaximumInterval().AsDuration() / time.Second),
			MaximumAttempts:        policy.GetMaximumAttempts(),
			TotalDurationSeconds:   int32(attributes.GetScheduleToCloseTimeout().AsDuration() / time.Second),
		}
	}
	require.FailNow(t, "regular fallback activity not found")
	return nil
}

func cadenceRegularFallbackRetryPolicy(
	t *testing.T,
	ctx context.Context,
	runtime *integRuntime,
	flowID string,
	runID string,
) *dexpb.RetryPolicy {
	t.Helper()
	api := runtime.UnifiedClient.GetApiService().(client.Client)
	iterator := api.GetWorkflowHistory(ctx, flowID, runID, false, shared.HistoryEventFilterTypeAllEvent)
	dataConverter := dexconverter.NewCadenceDataConverter()
	for iterator.HasNext() {
		historyEvent, err := iterator.Next()
		require.NoError(t, err)
		if historyEvent.GetEventType() != shared.EventTypeActivityTaskScheduled {
			continue
		}
		attributes := historyEvent.GetActivityTaskScheduledEventAttributes()
		if !strings.Contains(attributes.GetActivityType().GetName(), "InvokeWaitForMethod") {
			continue
		}
		var input dexpb.InvokeWaitForMethodActivityInput
		var localInput *dexpb.InternalLocalActivityInput
		require.NoError(t, dataConverter.FromData(attributes.GetInput(), &input, &localInput))
		if input.GetRequest().GetContext().GetAttempt() == 0 {
			continue
		}
		policy := attributes.GetRetryPolicy()
		return &dexpb.RetryPolicy{
			InitialIntervalSeconds: policy.GetInitialIntervalInSeconds(),
			BackoffCoefficient:     float32(policy.GetBackoffCoefficient()),
			MaximumIntervalSeconds: policy.GetMaximumIntervalInSeconds(),
			MaximumAttempts:        policy.GetMaximumAttempts(),
			TotalDurationSeconds:   policy.GetExpirationIntervalInSeconds(),
		}
	}
	require.FailNow(t, "regular fallback activity not found")
	return nil
}

func completedWaitForEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepWaitForCompletedEvent {
	for _, historyEvent := range events {
		if historyEvent.GetStepWaitForCompleted() != nil {
			return historyEvent.GetStepWaitForCompleted()
		}
	}
	return nil
}

func failedWaitForEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepWaitForFailedEvent {
	for _, historyEvent := range events {
		if historyEvent.GetStepWaitForFailed() != nil {
			return historyEvent.GetStepWaitForFailed()
		}
	}
	return nil
}

func failedExecuteEvent(events []*dexpb.FlowHistoryEvent) *dexpb.StepExecuteFailedEvent {
	for _, historyEvent := range events {
		if historyEvent.GetStepExecuteFailed() != nil {
			return historyEvent.GetStepExecuteFailed()
		}
	}
	return nil
}
