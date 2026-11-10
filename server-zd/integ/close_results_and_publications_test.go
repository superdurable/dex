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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	closeResultsFlowType       = "close-results"
	closeResultsFanOutStep     = "fan-out"
	closeResultsShipStep       = "ship"
	closeResultsPackStep       = "pack"
	closeResultsShipOutput     = "shipped"
	closeResultsFailureMessage = "packing failed"

	pendingClosePublishFlowType   = "pending-close-publish"
	pendingClosePublishFinishStep = "finish"
	pendingClosePublishHoldRPC    = "holdClose"
	pendingClosePublishChannel    = "orders"
)

// closeResultsHandler runs fan-out into ship and pack; ship completes gracefully with an output.
type closeResultsHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	shouldPackFail  bool
	packStartedOnce sync.Once
	packStarted     chan struct{}
	releasePack     chan struct{}
}

func newCloseResultsHandler(shouldPackFail bool) *closeResultsHandler {
	return &closeResultsHandler{
		shouldPackFail: shouldPackFail,
		packStarted:    make(chan struct{}),
		releasePack:    make(chan struct{}),
	}
}

func TestFailedFlowDropsGracefulOutputsTemporal(t *testing.T) {
	t.Run("ForceFail after GracefulComplete", func(t *testing.T) {
		testCloseResultsAfterGracefulOutput(t, true)
	})
	t.Run("completion after GracefulComplete", func(t *testing.T) {
		testCloseResultsAfterGracefulOutput(t, false)
	})
}

func testCloseResultsAfterGracefulOutput(t *testing.T, shouldPackFail bool) {
	handler := newCloseResultsHandler(shouldPackFail)
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := closeResultsFlowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           closeResultsFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      closeResultsFanOutStep,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	select {
	case <-handler.packStarted:
	case <-ctx.Done():
		require.FailNow(t, "pack did not start", ctx.Err())
	}
	require.Eventually(t, func() bool {
		state, stateErr := flowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
		return stateErr == nil && len(state.GetCompletedSteps()) == 1
	}, 10*time.Second, 50*time.Millisecond, "ship collects its output while pack runs")
	close(handler.releasePack)

	result, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		NeedsResults:    true,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	state, err := flowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	if shouldPackFail {
		require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_FAILED, result.GetFlowStatus())
		require.Equal(
			t,
			dexpb.FlowErrorType_FLOW_ERROR_TYPE_STEP_DECISION_FAILING_FLOW,
			result.GetErrorType(),
		)
		require.Equal(t, closeResultsFailureMessage, result.GetErrorMessage())
		require.Empty(t, result.GetResults(), "a failed Flow returns no outputs")
		require.Empty(t, state.GetCompletedSteps(), "GetFlowState shows no outputs")
		return
	}
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
	require.Len(t, result.GetResults(), 1)
	require.Equal(t, closeResultsShipStep, result.GetResults()[0].GetCompletedStepType())
	require.Equal(
		t,
		closeResultsShipOutput,
		result.GetResults()[0].GetCompletedStepOutput().GetStringValue(),
	)
	require.Len(t, state.GetCompletedSteps(), 1)
}

func (h *closeResultsHandler) InvokeWaitForMethod(
	context.Context,
	*dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "every close-results Step skips WaitFor")
}

func (h *closeResultsHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	switch request.GetStepType() {
	case closeResultsFanOutStep:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: closeResultsShipStep, StepOptions: &dexpb.StepOptions{SkipWaitFor: true}},
					{StepType: closeResultsPackStep, StepOptions: &dexpb.StepOptions{SkipWaitFor: true}},
				},
			},
		}, nil
	case closeResultsShipStep:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(&dexpb.Value{
					Kind: &dexpb.Value_StringValue{StringValue: closeResultsShipOutput},
				}),
			},
		}, nil
	case closeResultsPackStep:
		h.packStartedOnce.Do(func() { close(h.packStarted) })
		select {
		case <-h.releasePack:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if !h.shouldPackFail {
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{CloseDecision: common.DeadEndDecision()},
			}, nil
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.ForceFailDecision(&dexpb.Value{
					Kind: &dexpb.Value_StringValue{StringValue: closeResultsFailureMessage},
				}),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown close-results Step")
	}
}

// pendingClosePublishHandler holds a natural close with an admitted transactional RPC.
type pendingClosePublishHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	finishStartedOnce sync.Once
	finishStarted     chan struct{}
	releaseFinish     chan struct{}
	rpcStartedOnce    sync.Once
	rpcStarted        chan struct{}
	releaseRPC        chan struct{}
}

func newPendingClosePublishHandler() *pendingClosePublishHandler {
	return &pendingClosePublishHandler{
		finishStarted: make(chan struct{}),
		releaseFinish: make(chan struct{}),
		rpcStarted:    make(chan struct{}),
		releaseRPC:    make(chan struct{}),
	}
}

func TestPublicationDuringPendingCloseIsRejectedTemporal(t *testing.T) {
	handler := newPendingClosePublishHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, serverZDTestConfig{})
	flowClient := runtime.FlowClient
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	flowID := pendingClosePublishFlowType + "-" + uuid.NewString()
	_, err := flowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           pendingClosePublishFlowType,
		FlowTimeoutSeconds: 20,
		StartStepType:      pendingClosePublishFinishStep,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)
	select {
	case <-handler.finishStarted:
	case <-ctx.Done():
		require.FailNow(t, "finish did not start", ctx.Err())
	}
	rpcErrors := make(chan error, 1)
	go func() {
		_, rpcErr := flowClient.InvokeRPC(ctx, &dexpb.InvokeRPCRequest{
			RequestId:       newRequestID(),
			FlowId:          flowID,
			RpcName:         pendingClosePublishHoldRPC,
			IsTransactional: true,
		})
		rpcErrors <- rpcErr
	}()
	select {
	case <-handler.rpcStarted:
	case <-ctx.Done():
		require.FailNow(t, "the transactional RPC did not start", ctx.Err())
	}
	_, err = flowClient.PublishToChannel(ctx, publishOrder(flowID, "before-close"))
	require.NoError(t, err, "a running Flow accepts publications")

	close(handler.releaseFinish)
	require.Eventually(t, func() bool {
		_, waitErr := flowClient.WaitForAttribute(ctx, &dexpb.WaitForAttributeRequest{
			FlowId: flowID,
			Match: &dexpb.AttributeMatch{
				Key:      "status",
				Operator: dexpb.AttributeMatchOperator_ATTRIBUTE_MATCH_OPERATOR_EQUAL,
				Operand:  &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: "never"}},
			},
			RequestId:             newRequestID(),
			RequestTimeoutSeconds: 1,
		})
		return status.Code(waitErr) == codes.FailedPrecondition &&
			grpcServiceErrorResponse(t, waitErr).GetSubStatus() ==
				dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_CLOSING
	}, 10*time.Second, 50*time.Millisecond, "the admitted RPC holds the close")

	_, err = flowClient.PublishToChannel(ctx, publishOrder(flowID, "during-close"))
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	require.Equal(
		t,
		dexpb.ErrorSubStatus_ERROR_SUB_STATUS_FLOW_CLOSING,
		grpcServiceErrorResponse(t, err).GetSubStatus(),
	)
	state, err := flowClient.GetFlowState(ctx, &dexpb.GetFlowStateRequest{FlowId: flowID})
	require.NoError(t, err)
	pending := state.GetPendingChannelMessages()[pendingClosePublishChannel].GetMessages()
	require.Len(t, pending, 1, "the rejected publication stored nothing")
	require.Equal(t, "before-close", pending[0].GetValue().GetStringValue())

	close(handler.releaseRPC)
	require.NoError(t, <-rpcErrors)
	result, err := flowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 20,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, result.GetFlowStatus())
}

func publishOrder(flowID string, text string) *dexpb.PublishToChannelRequest {
	return &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{{
			ChannelName: pendingClosePublishChannel,
			Value:       &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}},
		}},
	}
}

func (h *pendingClosePublishHandler) InvokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if request.GetRpcName() != pendingClosePublishHoldRPC {
		return nil, status.Error(codes.InvalidArgument, "unknown pending-close RPC")
	}
	h.rpcStartedOnce.Do(func() { close(h.rpcStarted) })
	select {
	case <-h.releaseRPC:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &dexpb.InvokeWorkerRPCResponse{}, nil
}

func (h *pendingClosePublishHandler) InvokeWaitForMethod(
	context.Context,
	*dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "the finish Step skips WaitFor")
}

func (h *pendingClosePublishHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetStepType() != pendingClosePublishFinishStep {
		return nil, status.Error(codes.InvalidArgument, "unknown pending-close Step")
	}
	h.finishStartedOnce.Do(func() { close(h.finishStarted) })
	select {
	case <-h.releaseFinish:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{CloseDecision: common.ForceCompleteDecision(nil)},
	}, nil
}
