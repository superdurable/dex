// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package channel_cancel_requeue

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/superdurable/dex/sdk-go/dex/ptr"
	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc"
)

const (
	FlowType          = "channel-cancel-requeue"
	Root              = "Root"
	Worker            = "Worker"
	Controller        = "Controller"
	FirstWorker       = Worker + "-1"
	FirstController   = Controller + "-1"
	WorkChannel       = "work"
	GoChannel         = "go"
	ReplaceWorkerRpc  = "replaceWorker"
	StepPublishedJob  = "job-from-controller"
	RpcPublishedJob   = "job-from-rpc"
	ReceivedSeparator = ","
)

// Handler runs a Controller or an RPC that publishes to the work Channel, cancels the Worker
// Step type, and starts a new Worker in the same commit.
type Handler struct {
	dexpb.UnimplementedWorkerServiceServer
	mu                           sync.Mutex
	hasFirstWorkerExecuted       bool
	isFirstWorkerExecuteCanceled bool
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if request.GetRpcName() != ReplaceWorkerRpc {
		return &dexpb.InvokeWorkerRPCResponse{}, nil
	}
	return &dexpb.InvokeWorkerRPCResponse{
		Output:           request.GetInput(),
		PublishToChannel: []*dexpb.ChannelMessage{workMessage(RpcPublishedJob)},
		StepDecision:     replaceWorker(),
	}, nil
}

func (h *Handler) InvokeWaitForMethod(
	request *dexpb.InvokeWaitForMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeWaitForMethodOutput],
) error {
	response := &dexpb.InvokeWaitForMethodResponse{}
	switch request.GetStepType() {
	case Controller:
		response = channelWait(GoChannel, ptr.Any(int32(1)), ptr.Any(int32(1)))
	case Worker:
		if request.GetContext().GetStepExecutionId() == FirstWorker {
			response = channelWait(WorkChannel, ptr.Any(int32(1)), ptr.Any(int32(1)))
		} else {
			// A later Worker takes every pending message, to show their order.
			response = channelWait(WorkChannel, ptr.Any(int32(1)), nil)
		}
	}
	return stream.Send(&dexpb.InvokeWaitForMethodOutput{
		Output: &dexpb.InvokeWaitForMethodOutput_Result{Result: response},
	})
}

func (h *Handler) InvokeExecuteMethod(
	request *dexpb.InvokeExecuteMethodRequest,
	stream grpc.ServerStreamingServer[dexpb.InvokeExecuteMethodOutput],
) error {
	if request.GetContext().GetStepExecutionId() == FirstWorker {
		h.markFirstWorkerExecuted()
		return h.streamHeartbeatsUntilCanceled(stream)
	}
	response := &dexpb.InvokeExecuteMethodResponse{}
	switch request.GetStepType() {
	case Root:
		response.StepDecision = &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{
			{StepType: Worker},
			{StepType: Controller},
		}}
	case Controller:
		response.PublishToChannel = []*dexpb.ChannelMessage{workMessage(StepPublishedJob)}
		response.StepDecision = replaceWorker()
	case Worker:
		response.StepDecision = &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_FORCE_COMPLETE,
			CloseInput:        stringValue(strings.Join(receivedWork(request), ReceivedSeparator)),
		}}
	}
	return stream.Send(&dexpb.InvokeExecuteMethodOutput{
		Output: &dexpb.InvokeExecuteMethodOutput_Result{Result: response},
	})
}

func (h *Handler) streamHeartbeatsUntilCanceled(
	stream grpc.ServerStreamingServer[dexpb.InvokeExecuteMethodOutput],
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := stream.Send(&dexpb.InvokeExecuteMethodOutput{
			Output: &dexpb.InvokeExecuteMethodOutput_Heartbeat{
				Heartbeat: &dexpb.StepMethodHeartbeat{},
			},
		}); err != nil {
			h.markFirstWorkerExecuteCanceled()
			return err
		}
		select {
		case <-stream.Context().Done():
			h.markFirstWorkerExecuteCanceled()
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

// HasFirstWorkerExecuted reports whether the first Worker's Execute was invoked.
func (h *Handler) HasFirstWorkerExecuted() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hasFirstWorkerExecuted
}

// IsFirstWorkerExecuteCanceled reports whether the first Worker's Execute saw its cancellation.
func (h *Handler) IsFirstWorkerExecuteCanceled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.isFirstWorkerExecuteCanceled
}

func (h *Handler) markFirstWorkerExecuted() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hasFirstWorkerExecuted = true
}

func (h *Handler) markFirstWorkerExecuteCanceled() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.isFirstWorkerExecuteCanceled = true
}

func replaceWorker() *dexpb.StepDecision {
	return &dexpb.StepDecision{
		CancelStepTypes: []string{Worker},
		NextSteps:       []*dexpb.StepMovement{{StepType: Worker}},
	}
}

func channelWait(channelName string, atLeast *int32, atMost *int32) *dexpb.InvokeWaitForMethodResponse {
	return &dexpb.InvokeWaitForMethodResponse{WaitingCondition: &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		ChannelConditions: []*dexpb.ChannelCondition{{
			ChannelName: channelName,
			AtLeast:     atLeast,
			AtMost:      atMost,
		}},
	}}
}

func receivedWork(request *dexpb.InvokeExecuteMethodRequest) []string {
	var received []string
	for _, result := range request.GetConditionResults().GetChannelResults() {
		for _, value := range result.GetValues() {
			received = append(received, value.GetStringValue())
		}
	}
	return received
}

func workMessage(text string) *dexpb.ChannelMessage {
	return &dexpb.ChannelMessage{ChannelName: WorkChannel, Value: stringValue(text)}
}

func stringValue(text string) *dexpb.Value {
	return &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: text}}
}
