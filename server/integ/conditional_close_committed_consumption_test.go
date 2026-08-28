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
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	conditionalCloseCommittedFlowType        = "conditional-close-committed-consumption"
	conditionalCloseCommittedRootStep        = "root"
	conditionalCloseCommittedCheckerStep     = "checker"
	conditionalCloseCommittedConsumerStep    = "consumer"
	conditionalCloseCommittedOrElseStep      = "or-else"
	conditionalCloseCommittedChannelName     = "messages"
	conditionalCloseCommittedMessage         = "only"
	conditionalCloseCommittedPrematureOutput = "premature"
	conditionalCloseCommittedOrElseOutput    = "or-else"
)

type conditionalCloseCommittedHandler struct {
	dexpb.UnimplementedWorkerServiceServer
	allowCheckerOnce       sync.Once
	releaseConsumerOnce    sync.Once
	consumerExecuteOnce    sync.Once
	allowChecker           chan struct{}
	releaseConsumer        chan struct{}
	consumerExecuteStarted chan *dexpb.InvokeExecuteMethodRequest
}

func newConditionalCloseCommittedHandler() *conditionalCloseCommittedHandler {
	return &conditionalCloseCommittedHandler{
		allowChecker:           make(chan struct{}),
		releaseConsumer:        make(chan struct{}),
		consumerExecuteStarted: make(chan *dexpb.InvokeExecuteMethodRequest, 1),
	}
}

func (h *conditionalCloseCommittedHandler) allowCheckerDecision() {
	h.allowCheckerOnce.Do(func() { close(h.allowChecker) })
}

func (h *conditionalCloseCommittedHandler) releaseConsumerExecute() {
	h.releaseConsumerOnce.Do(func() { close(h.releaseConsumer) })
}

func (h *conditionalCloseCommittedHandler) releaseAll() {
	h.allowCheckerDecision()
	h.releaseConsumerExecute()
}

func TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionTemporal(t *testing.T) {
	if !*temporalIntegTest {
		t.Skip()
	}
	for iteration := 0; iteration < *repeatIntegTest; iteration++ {
		doTestForceCompleteIfChannelsEmptyCountsCommittedConsumption(
			t,
			service.BackendTypeTemporal,
		)
	}
}

func TestForceCompleteIfChannelsEmptyCountsCommittedConsumptionCadence(t *testing.T) {
	if !*cadenceIntegTest {
		t.Skip()
	}
	for iteration := 0; iteration < *repeatIntegTest; iteration++ {
		doTestForceCompleteIfChannelsEmptyCountsCommittedConsumption(
			t,
			service.BackendTypeCadence,
		)
	}
}

func doTestForceCompleteIfChannelsEmptyCountsCommittedConsumption(
	t *testing.T,
	backendType service.BackendType,
) {
	t.Helper()
	handler := newConditionalCloseCommittedHandler()
	workerTarget := startWorker(t, handler)
	runtime := startDexService(t, DexServiceTestConfig{BackendType: backendType})
	t.Cleanup(handler.releaseAll)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	flowID := conditionalCloseCommittedFlowType + "-" + uuid.NewString()
	_, err := runtime.FlowClient.StartFlow(ctx, &dexpb.StartFlowRequest{
		RequestId:          newRequestID(),
		FlowId:             flowID,
		FlowType:           conditionalCloseCommittedFlowType,
		FlowTimeoutSeconds: 30,
		StartStepType:      conditionalCloseCommittedRootStep,
		StepOptions:        &dexpb.StepOptions{SkipWaitFor: true},
		FlowStartOptions:   withWorkerTarget(nil, workerTarget),
	})
	require.NoError(t, err)

	_, err = runtime.FlowClient.PublishToChannel(ctx, &dexpb.PublishToChannelRequest{
		FlowId: flowID,
		Messages: []*dexpb.ChannelMessage{{
			ChannelName: conditionalCloseCommittedChannelName,
			Value:       stringValue(conditionalCloseCommittedMessage),
		}},
	})
	require.NoError(t, err)

	var consumerRequest *dexpb.InvokeExecuteMethodRequest
	select {
	case consumerRequest = <-handler.consumerExecuteStarted:
	case <-ctx.Done():
		require.FailNow(t, "consumer Execute did not start", ctx.Err())
	}
	channelResults := consumerRequest.GetConditionResults().GetChannelResults()
	require.Len(t, channelResults, 1)
	require.Equal(t, "message", channelResults[0].GetConditionId())
	require.Equal(t, conditionalCloseCommittedChannelName, channelResults[0].GetChannelName())
	require.Equal(
		t,
		dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED,
		channelResults[0].GetConditionStatus(),
	)
	require.Len(t, channelResults[0].GetValues(), 1)
	require.Equal(
		t,
		conditionalCloseCommittedMessage,
		channelResults[0].GetValues()[0].GetStringValue(),
	)

	handler.allowCheckerDecision()
	response, err := runtime.FlowClient.WaitForFlow(ctx, &dexpb.WaitForFlowRequest{
		FlowId:          flowID,
		WaitTimeSeconds: 30,
		NeedsResults:    true,
	})
	require.NoError(t, err)
	require.Equal(t, dexpb.FlowStatus_FLOW_STATUS_COMPLETED, response.GetFlowStatus())
	require.Len(t, response.GetResults(), 1)
	require.Equal(
		t,
		conditionalCloseCommittedOrElseStep,
		response.GetResults()[0].GetCompletedStepType(),
	)
	require.Equal(
		t,
		conditionalCloseCommittedOrElseOutput,
		response.GetResults()[0].GetCompletedStepOutput().GetStringValue(),
	)
}

func (h *conditionalCloseCommittedHandler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != conditionalCloseCommittedFlowType ||
		request.GetStepType() != conditionalCloseCommittedConsumerStep {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"unexpected WaitFor %q/%q",
			request.GetFlowType(),
			request.GetStepType(),
		)
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{{
				ConditionId: "message",
				ChannelName: conditionalCloseCommittedChannelName,
			}},
		},
	}, nil
}

func (h *conditionalCloseCommittedHandler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != conditionalCloseCommittedFlowType {
		return nil, status.Errorf(
			codes.InvalidArgument,
			"unexpected Execute Flow type %q",
			request.GetFlowType(),
		)
	}
	switch request.GetStepType() {
	case conditionalCloseCommittedRootStep:
		return conditionalCloseCommittedRootResponse(), nil
	case conditionalCloseCommittedCheckerStep:
		select {
		case <-h.allowChecker:
		case <-ctx.Done():
			return nil, status.Error(codes.Canceled, "checker Execute canceled")
		}
		return conditionalCloseCommittedCheckerResponse(), nil
	case conditionalCloseCommittedConsumerStep:
		h.consumerExecuteOnce.Do(func() { h.consumerExecuteStarted <- request })
		select {
		case <-h.releaseConsumer:
		case <-ctx.Done():
			return nil, status.Error(codes.Canceled, "consumer Execute canceled")
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.DeadEndDecision(),
			},
		}, nil
	case conditionalCloseCommittedOrElseStep:
		h.releaseConsumerExecute()
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(
					stringValue(conditionalCloseCommittedOrElseOutput),
				),
			},
		}, nil
	default:
		return nil, status.Errorf(
			codes.InvalidArgument,
			"unexpected Execute Step type %q",
			request.GetStepType(),
		)
	}
}

func conditionalCloseCommittedRootResponse() *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:    conditionalCloseCommittedCheckerStep,
					StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
				},
				{StepType: conditionalCloseCommittedConsumerStep},
			},
		},
	}
}

func conditionalCloseCommittedCheckerResponse() *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{{
				StepType:    conditionalCloseCommittedOrElseStep,
				StepOptions: &dexpb.StepOptions{SkipWaitFor: true},
			}},
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
				ConditionalChannelNames: []string{
					conditionalCloseCommittedChannelName,
				},
				CloseInput: stringValue(conditionalCloseCommittedPrematureOutput),
			},
		},
	}
}
