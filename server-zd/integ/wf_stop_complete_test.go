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
	completeStopFlowType         = "complete-stop"
	completeStopBlockingStepType = "blocking-step"
	completeStopIdleStepType     = "idle-step"
	completeStopSelfStepType     = "self-stop-step"
	completeStopOutput           = "completed by StopFlow"
)

func TestStopFlowForceCompleteCancelsActiveSteps(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newCompleteStopHandler(runtime.FlowClient)
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startCompleteStopFlow(t, ctx, runtime.FlowClient, workerTarget, completeStopBlockingStepType)
	awaitExecuteStarted(t, ctx, handler, flowID)

	_, err := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_FORCE_COMPLETE,
		Reason:   "operator closed the session",
		Output:   completeStopOutputValue(),
	})
	require.NoError(t, err)

	result := waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID)
	close(handler.releaseExecute)
	state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Empty(t, userActiveStepExecutions(state))
	assertOnlyStopOutput(t, result)
	require.Empty(t, result.GetErrorMessage())
}

func TestStopFlowGracefulCompleteWaitsForActiveStep(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newCompleteStopHandler(runtime.FlowClient)
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startCompleteStopFlow(t, ctx, runtime.FlowClient, workerTarget, completeStopBlockingStepType)
	awaitExecuteStarted(t, ctx, handler, flowID)

	_, err := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_GRACEFUL_COMPLETE,
		Output:   completeStopOutputValue(),
	})
	require.NoError(t, err)

	// StopFlow returned after its commit, so the Flow waits for the blocked Step.
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, summary.GetFlowStatus())
	state, err := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Len(t, userActiveStepExecutions(state), 1)

	close(handler.releaseExecute)
	assertOnlyStopOutput(t, waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID))
}

func TestStopFlowGracefulCompleteWithoutActiveStepCompletesAtOnce(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newCompleteStopHandler(runtime.FlowClient)
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startCompleteStopFlow(t, ctx, runtime.FlowClient, workerTarget, completeStopIdleStepType)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		state, stateErr := runtime.FlowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		if !assert.NoError(collect, stateErr) {
			return
		}
		assert.Empty(collect, userActiveStepExecutions(state))
	}, 10*time.Second, 50*time.Millisecond)
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, summary.GetFlowStatus())

	_, err = runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_GRACEFUL_COMPLETE,
		Output:   completeStopOutputValue(),
	})
	require.NoError(t, err)

	assertOnlyStopOutput(t, waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID))
}

func TestStopFlowGracefulCompleteFromTheFlowsOwnStep(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newCompleteStopHandler(runtime.FlowClient)
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := startCompleteStopFlow(t, ctx, runtime.FlowClient, workerTarget, completeStopSelfStepType)

	assertOnlyStopOutput(t, waitForCompleteStopResult(t, ctx, runtime.FlowClient, flowID))
}

func TestStopFlowRejectsAnOutputForNonCompleteStopTypes(t *testing.T) {
	runtime := startDexService(t, serverZDTestConfig{})
	handler := newCompleteStopHandler(runtime.FlowClient)
	workerTarget := startWorker(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flowID := startCompleteStopFlow(t, ctx, runtime.FlowClient, workerTarget, completeStopIdleStepType)

	_, err := runtime.FlowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
		FlowId:   flowID,
		StopType: dexpb.StopType_STOP_TYPE_CANCEL,
		Output:   completeStopOutputValue(),
	})

	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, dexpb.ErrorSubStatus_ERROR_SUB_STATUS_UNCATEGORIZED, grpcServiceErrorResponse(t, err).GetSubStatus())
	summary, err := runtime.FlowClient.GetFlowSummary(ctx, &dexpb.GetFlowSummaryRequest{FlowId: flowID})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_RUNNING, summary.GetFlowStatus())
}

func startCompleteStopFlow(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	workerTarget *dexpb.WorkerTarget,
	startStepType string,
) string {
	t.Helper()
	flowID := completeStopFlowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           completeStopFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      startStepType,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	return flowID
}

func awaitExecuteStarted(t *testing.T, ctx context.Context, handler *completeStopHandler, flowID string) {
	t.Helper()
	select {
	case startedFlowID := <-handler.executeStarted:
		require.Equal(t, flowID, startedFlowID)
	case <-ctx.Done():
		require.FailNow(t, "blocking step did not start", ctx.Err())
	}
}

func waitForCompleteStopResult(
	t *testing.T,
	ctx context.Context,
	flowClient dexpb.FlowServiceClient,
	flowID string,
) *dexpb.FlowResult {
	t.Helper()
	result, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:       flowID,
		NeedsResults: true,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	return result
}

func assertOnlyStopOutput(t *testing.T, result *dexpb.FlowResult) {
	t.Helper()
	require.Len(t, result.GetResults(), 1)
	completion := result.GetResults()[0]
	require.Empty(t, completion.GetCompletedStepType())
	require.Empty(t, completion.GetCompletedStepExecutionId())
	require.Equal(t, completeStopOutput, completion.GetCompletedStepOutput().GetStringValue())
}

func completeStopOutputValue() *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: completeStopOutput}}
}

type completeStopHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowClient     dexpb.FlowServiceClient
	executeStarted chan string
	releaseExecute chan struct{}
}

func newCompleteStopHandler(flowClient dexpb.FlowServiceClient) *completeStopHandler {
	return &completeStopHandler{
		flowClient:     flowClient,
		executeStarted: make(chan string, 1),
		releaseExecute: make(chan struct{}),
	}
}

func (h *completeStopHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	flowID := request.GetContext().GetFlowId()
	switch request.GetStepType() {
	case completeStopBlockingStepType:
		h.executeStarted <- flowID
		select {
		case <-h.releaseExecute:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case completeStopIdleStepType:
	case completeStopSelfStepType:
		_, err := h.flowClient.StopFlow(ctx, &dexpb.StopFlowRequest{
			FlowId:   flowID,
			StopType: dexpb.StopType_STOP_TYPE_GRACEFUL_COMPLETE,
			Output:   completeStopOutputValue(),
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown complete stop step")
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
			},
		},
	}, nil
}
