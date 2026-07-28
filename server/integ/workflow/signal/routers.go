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

package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has two steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor waits until 4 signals are received
 * 		- Execute method publishes the 4 signals & moves to Step2
 * Step2:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType                  = "signal"
	State1                        = "S1"
	State2                        = "S2"
	SignalName                    = "test-signal-name"
	InternalChannelName           = "test-internal-channel-name"
	UnhandledSignalName           = "test-unhandled-signal-name"
	RPCNameGetSignalChannelInfo   = "RPCNameGetSignalChannelInfo"
	RPCNameGetInternalChannelInfo = "RPCNameGetInternalChannelInfo"
)

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
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
	request *iwfpb.InvokeWorkerRPCRequest,
) (*iwfpb.InvokeWorkerRPCResponse, error) {
	if request.GetRpcName() == RPCNameGetSignalChannelInfo {
		data, err := json.Marshal(request.GetChannelInfos())
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return &iwfpb.InvokeWorkerRPCResponse{
			PublishToChannel: []*iwfpb.ChannelMessage{
				{ChannelName: InternalChannelName},
			},
			Output: &iwfpb.Value{
				Kind: &iwfpb.Value_ObjValue{
					ObjValue: &iwfpb.EncodedObject{
						Encoding: "json",
						Payload:  data,
					},
				},
			},
		}, nil
	}
	if request.GetRpcName() == RPCNameGetInternalChannelInfo {
		data, err := json.Marshal(request.GetChannelInfos())
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return &iwfpb.InvokeWorkerRPCResponse{
			Output: &iwfpb.Value{
				Kind: &iwfpb.Value_ObjValue{
					ObjValue: &iwfpb.EncodedObject{
						Encoding: "json",
						Payload:  data,
					},
				},
			},
		}, nil
	}
	return nil, status.Error(codes.InvalidArgument, "unknown rpc name")
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
	}

	switch request.GetStepType() {
	case State1:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ConditionId: "signal-cmd-id0", ChannelName: SignalName},
					{ConditionId: "signal-cmd-id1", ChannelName: SignalName},
					{ChannelName: SignalName},
					{ChannelName: SignalName},
				},
			},
		}, nil
	case State2:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
	}

	switch request.GetStepType() {
	case State1:
		channelResults := request.GetConditionResults().GetChannelResults()
		for i := 0; i < 4; i++ {
			signalId := channelResults[i].GetConditionId()
			var signalValue *iwfpb.Value
			if values := channelResults[i].GetValues(); len(values) > 0 {
				signalValue = values[0]
			}
			h.invokeData.Store(fmt.Sprintf("signalId%v", i), signalId)
			h.invokeData.Store(fmt.Sprintf("signalValue%v", i), signalValue)
		}
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: State2},
				},
			},
		}, nil
	case State2:
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
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
