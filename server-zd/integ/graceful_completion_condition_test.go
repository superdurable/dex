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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	gracefulConditionFlowType        = "graceful-condition"
	gracefulConditionAnyOfStepType   = "any-of-waiter"
	gracefulConditionAllOfStepType   = "all-of-waiter"
	gracefulConditionPlainStepType   = "plain-waiter"
	gracefulConditionBlockedStepType = "blocked-waiter"
	gracefulConditionFanOutStepType  = "fan-out"
	gracefulConditionCloserStepType  = "closer"
	gracefulConditionMessageChannel  = "messages"
	gracefulConditionCloseChannel    = "close-now"
	gracefulConditionID              = "graceful"
	gracefulConditionMessageID       = "message"
)

func TestGracefulStopWakesAStepWaitingOnTheGracefulCompletionCondition(t *testing.T) {
	runtime, handler, workerTarget := startGracefulConditionWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startGracefulConditionFlow(t, ctx, runtime.FlowClient, workerTarget, gracefulConditionAnyOfStepType, false)
	waiting := awaitGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionAnyOfStepType)
	require.Equal(t, gracefulConditionID, waiting.GetWaitingCondition().GetGracefulCompletionConditions()[0].GetConditionId())

	stopFlowGracefully(t, ctx, runtime.FlowClient, flowID)

	execution := awaitGracefulConditionExecution(t, ctx, handler)
	require.Equal(t, gracefulConditionAnyOfStepType, execution.stepType)
	requireGracefulResult(t, execution.results, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_WAITING, execution.results.GetChannelResults()[0].GetConditionStatus())
	waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
}

func TestGracefulCompleteDecisionWakesASiblingWaitingOnTheGracefulCompletionCondition(t *testing.T) {
	runtime, handler, workerTarget := startGracefulConditionWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startGracefulConditionFlow(t, ctx, runtime.FlowClient, workerTarget, gracefulConditionFanOutStepType, true)
	awaitGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionAnyOfStepType)
	awaitGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionCloserStepType)

	publishGracefulConditionMessage(t, ctx, runtime.FlowClient, flowID, gracefulConditionCloseChannel)

	executions := map[string]gracefulConditionExecution{}
	for len(executions) < 2 {
		execution := awaitGracefulConditionExecution(t, ctx, handler)
		executions[execution.stepType] = execution
	}
	require.Contains(t, executions, gracefulConditionCloserStepType)
	requireGracefulResult(t, executions[gracefulConditionAnyOfStepType].results, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED)
	waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
}

func TestGracefulCompletionConditionRegisteredAfterTheRequestCompletesAtOnce(t *testing.T) {
	runtime, handler, workerTarget := startGracefulConditionWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startGracefulConditionFlow(t, ctx, runtime.FlowClient, workerTarget, gracefulConditionBlockedStepType, false)
	select {
	case <-handler.blockedWaitForStarted:
	case <-ctx.Done():
		require.FailNow(t, "the blocked WaitFor did not start", ctx.Err())
	}

	stopFlowGracefully(t, ctx, runtime.FlowClient, flowID)
	requireFlowRunning(t, ctx, runtime.FlowClient, flowID)
	close(handler.releaseBlockedWaitFor)

	execution := awaitGracefulConditionExecution(t, ctx, handler)
	require.Equal(t, gracefulConditionBlockedStepType, execution.stepType)
	requireGracefulResult(t, execution.results, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED)
	waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
}

func TestGracefulStopDoesNotWakeAWaitWithoutTheGracefulCompletionCondition(t *testing.T) {
	runtime, handler, workerTarget := startGracefulConditionWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startGracefulConditionFlow(t, ctx, runtime.FlowClient, workerTarget, gracefulConditionPlainStepType, false)
	awaitGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionPlainStepType)

	stopFlowGracefully(t, ctx, runtime.FlowClient, flowID)

	// StopFlow returned after its commit, so a woken Step would have left waiting.
	requireFlowRunning(t, ctx, runtime.FlowClient, flowID)
	requireGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionPlainStepType)
	publishGracefulConditionMessage(t, ctx, runtime.FlowClient, flowID, gracefulConditionMessageChannel)

	execution := awaitGracefulConditionExecution(t, ctx, handler)
	require.Equal(t, gracefulConditionPlainStepType, execution.stepType)
	require.Empty(t, execution.results.GetGracefulCompletionResults())
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, execution.results.GetChannelResults()[0].GetConditionStatus())
	waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
}

func TestGracefulCompletionConditionInAllOfWaitsForTheOtherCondition(t *testing.T) {
	runtime, handler, workerTarget := startGracefulConditionWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startGracefulConditionFlow(t, ctx, runtime.FlowClient, workerTarget, gracefulConditionAllOfStepType, false)
	awaitGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionAllOfStepType)

	stopFlowGracefully(t, ctx, runtime.FlowClient, flowID)

	requireFlowRunning(t, ctx, runtime.FlowClient, flowID)
	requireGracefulConditionStepWaiting(t, ctx, runtime.FlowClient, flowID, gracefulConditionAllOfStepType)
	publishGracefulConditionMessage(t, ctx, runtime.FlowClient, flowID, gracefulConditionMessageChannel)

	execution := awaitGracefulConditionExecution(t, ctx, handler)
	require.Equal(t, gracefulConditionAllOfStepType, execution.stepType)
	requireGracefulResult(t, execution.results, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED)
	require.Equal(t, dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED, execution.results.GetChannelResults()[0].GetConditionStatus())
	waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
}

func startGracefulConditionWorker(t *testing.T) (*integRuntime, *gracefulConditionHandler, *dexpb.WorkerTarget) {
	t.Helper()
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newGracefulConditionHandler()
	return runtime, handler, startWorker(t, handler)
}

func startGracefulConditionFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	workerTarget *dexpb.WorkerTarget,
	startStepType string,
	shouldSkipWaitFor bool,
) string {
	t.Helper()
	flowID := gracefulConditionFlowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           gracefulConditionFlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      startStepType,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: shouldSkipWaitFor},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	return flowID
}

func stopFlowGracefully(t *testing.T, ctx context.Context, flowClient dexpb.FlowServiceClient, flowID string) {
	t.Helper()
	_, err := flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_GRACEFUL_COMPLETE,
	})
	require.NoError(t, err)
}

func publishGracefulConditionMessage(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	channelName string,
) {
	t.Helper()
	_, err := flowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId:   flowID,
		Messages: []*dexpb.ChannelMessage{{ChannelName: channelName, Value: stringValue("next")}},
	})
	require.NoError(t, err)
}

func awaitGracefulConditionStepWaiting(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	stepType string,
) *dexpb.ActiveStepExecutionState {
	t.Helper()
	var waiting *dexpb.ActiveStepExecutionState
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		waiting = findWaitingStep(collect, ctx, flowClient, flowID, stepType)
		assert.NotNil(collect, waiting)
	}, 10*time.Second, 50*time.Millisecond)
	return waiting
}

func requireGracefulConditionStepWaiting(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	stepType string,
) {
	t.Helper()
	state, err := flowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	for _, execution := range userActiveStepExecutions(state) {
		if execution.GetStepType() == stepType {
			require.Equal(t, dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING, execution.GetPhase())
			return
		}
	}
	require.FailNow(t, "the Step is not active", stepType)
}

func findWaitingStep(
	collect *assert.CollectT,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
	stepType string,
) *dexpb.ActiveStepExecutionState {
	state, err := flowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	if !assert.NoError(collect, err) {
		return nil
	}
	for _, execution := range userActiveStepExecutions(state) {
		if execution.GetStepType() == stepType &&
			execution.GetPhase() == dexpb.ActiveStepPhase_ACTIVE_STEP_PHASE_WAITING {
			return execution
		}
	}
	return nil
}

func requireFlowRunning(t *testing.T, ctx context.Context, flowClient dexpb.FlowServiceClient, flowID string) {
	t.Helper()
	summary, err := flowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, summary.GetFlowStatus())
}

func awaitGracefulConditionExecution(
	t *testing.T,
	ctx context.Context,
	handler *gracefulConditionHandler,
) gracefulConditionExecution {
	t.Helper()
	select {
	case execution := <-handler.executions:
		return execution
	case <-ctx.Done():
		require.FailNow(t, "no Execute arrived", ctx.Err())
		return gracefulConditionExecution{}
	}
}

func requireGracefulResult(t *testing.T, results *dexpb.ConditionResults, conditionStatus dexpb.ConditionStatus) {
	t.Helper()
	require.Len(t, results.GetGracefulCompletionResults(), 1)
	graceful := results.GetGracefulCompletionResults()[0]
	require.Equal(t, gracefulConditionID, graceful.GetConditionId())
	require.Equal(t, conditionStatus, graceful.GetConditionStatus())
}

type gracefulConditionExecution struct {
	stepType string
	results  *dexpb.ConditionResults
}

type gracefulConditionHandler struct {
	blockedWaitForStarted chan struct{}
	releaseBlockedWaitFor chan struct{}
	executions            chan gracefulConditionExecution
}

func newGracefulConditionHandler() *gracefulConditionHandler {
	return &gracefulConditionHandler{
		blockedWaitForStarted: make(chan struct{}, 1),
		releaseBlockedWaitFor: make(chan struct{}),
		executions:            make(chan gracefulConditionExecution, 4),
	}
}

func (h *gracefulConditionHandler) InvokeWaitForMethod(
	ctx context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	var waiting *dexpb.WaitingCondition
	switch request.GetStepType() {
	case gracefulConditionAnyOfStepType:
		waiting = messageOrGracefulCondition(dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED)
	case gracefulConditionAllOfStepType:
		waiting = messageOrGracefulCondition(dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED)
	case gracefulConditionBlockedStepType:
		h.blockedWaitForStarted <- struct{}{}
		select {
		case <-h.releaseBlockedWaitFor:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		waiting = messageOrGracefulCondition(dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED)
	case gracefulConditionPlainStepType:
		waiting = channelOnlyCondition(gracefulConditionMessageChannel)
	case gracefulConditionCloserStepType:
		waiting = channelOnlyCondition(gracefulConditionCloseChannel)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown graceful condition WaitFor Step")
	}
	return &dexpb.InvokeWaitForMethodResponse{WaitingCondition: waiting}, nil
}

func (h *gracefulConditionHandler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	switch request.GetStepType() {
	case gracefulConditionFanOutStepType:
		return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{StepType: gracefulConditionAnyOfStepType},
				{StepType: gracefulConditionCloserStepType},
			},
		}}, nil
	case gracefulConditionCloserStepType:
		h.executions <- gracefulConditionExecution{stepType: request.GetStepType(), results: request.GetConditionResults()}
		return closeDecisionResponse(dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE), nil
	case gracefulConditionAnyOfStepType, gracefulConditionAllOfStepType,
		gracefulConditionBlockedStepType, gracefulConditionPlainStepType:
		h.executions <- gracefulConditionExecution{stepType: request.GetStepType(), results: request.GetConditionResults()}
		return closeDecisionResponse(dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END), nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown graceful condition Execute Step")
	}
}

func messageOrGracefulCondition(waitingConditionType dexpb.WaitingConditionType) *dexpb.WaitingCondition {
	return &dexpb.WaitingCondition{
		WaitingConditionType: waitingConditionType,
		ChannelConditions: []*dexpb.ChannelCondition{{
			ConditionId: gracefulConditionMessageID,
			ChannelName: gracefulConditionMessageChannel,
		}},
		GracefulCompletionConditions: []*dexpb.GracefulCompletionCondition{{ConditionId: gracefulConditionID}},
	}
}

func channelOnlyCondition(channelName string) *dexpb.WaitingCondition {
	return &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: channelName}},
	}
}

func closeDecisionResponse(closeDecisionType dexpb.CloseDecisionType) *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		CloseDecision: &dexpb.CloseDecision{CloseDecisionType: closeDecisionType},
	}}
}
