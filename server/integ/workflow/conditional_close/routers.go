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

package conditional_close

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 1 step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor will proceed when the channel or signal is published to
 *      - Execute method will continuously retry Step1 until the 3rd attempt which will send a message to the channel or
 *        signal, making the step empty and force-complete.
 */
const (
	WorkflowType              = "conditional_close"
	RpcPublishInternalChannel = "publish_internal_channel"

	TestChannelName = "test-channel-name"

	State1 = "S1"
)

var TestInput = &dexpb.Value{
	Kind: &dexpb.Value_ObjValue{
		ObjValue: &dexpb.EncodedObject{
			Encoding: "json",
			Payload:  []byte("test-data"),
		},
	},
}

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
		invokeData:    sync.Map{},
	}
}

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	common.LogRequest("received worker rpc request, ", request)
	if value, ok := h.invokeHistory.Load(request.GetRpcName()); ok {
		h.invokeHistory.Store(request.GetRpcName(), value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetRpcName(), int64(1))
	}

	return &dexpb.InvokeWorkerRPCResponse{
		PublishToChannel: []*dexpb.ChannelMessage{
			{ChannelName: TestChannelName},
		},
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType || request.GetStepType() != State1 {
		return nil, status.Error(codes.InvalidArgument, "error request")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
	}

	waitingCondition := &dexpb.WaitingCondition{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		ChannelConditions: []*dexpb.ChannelCondition{
			{ChannelName: TestChannelName},
		},
	}
	if stepInputString(request.GetStepInput()) == "use-signal-channel" {
		waitingCondition = &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{ChannelName: TestChannelName},
			},
		}
	}

	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: waitingCondition,
	}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType || request.GetStepType() != State1 {
		return nil, status.Error(codes.InvalidArgument, "error request")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
	}

	var publishToChannel []*dexpb.ChannelMessage
	if stepContext.GetStepExecutionId() == "S1-1" {
		time.Sleep(time.Second * 3)
	}

	conditionalClose := &dexpb.FlowConditionalClose{
		ConditionalCloseType: dexpb.FlowConditionalCloseType_FLOW_CONDITIONAL_CLOSE_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY,
		ChannelNames:         []string{TestChannelName},
		CloseInput:           TestInput,
	}

	return &dexpb.InvokeExecuteMethodResponse{
		PublishToChannel: publishToChannel,
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:  State1,
					StepInput: request.GetStepInput(),
				},
			},
			ConditionalClose: conditionalClose,
		},
	}, nil
}

func stepInputString(stepInput *dexpb.Value) string {
	if stepInput == nil {
		return ""
	}
	if stringValue, ok := stepInput.Kind.(*dexpb.Value_StringValue); ok {
		return stringValue.StringValue
	}
	if objValue, ok := stepInput.Kind.(*dexpb.Value_ObjValue); ok {
		if objValue.ObjValue.GetEncoding() == "json" {
			return string(objValue.ObjValue.GetPayload())
		}
	}
	return ""
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	invokeData := make(map[string]interface{})
	h.invokeData.Range(func(key, value interface{}) bool {
		invokeData[key.(string)] = value
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory, InvokeData: invokeData}
}
