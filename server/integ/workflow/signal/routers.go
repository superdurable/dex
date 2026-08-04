// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
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
	if request.GetRpcName() == RPCNameGetSignalChannelInfo {
		data, err := json.Marshal(request.GetChannelInfos())
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return &dexpb.InvokeWorkerRPCResponse{
			PublishToChannel: []*dexpb.ChannelMessage{
				{ChannelName: InternalChannelName},
			},
			Output: &dexpb.Value{
				Kind: &dexpb.Value_ObjValue{
					ObjValue: &dexpb.EncodedObject{
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
		return &dexpb.InvokeWorkerRPCResponse{
			Output: &dexpb.Value{
				Kind: &dexpb.Value_ObjValue{
					ObjValue: &dexpb.EncodedObject{
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
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ConditionId: "signal-cmd-id0", ChannelName: SignalName},
					{ConditionId: "signal-cmd-id1", ChannelName: SignalName},
					{ChannelName: SignalName},
					{ChannelName: SignalName},
				},
			},
		}, nil
	case State2:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
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
			var signalValue *dexpb.Value
			if values := channelResults[i].GetValues(); len(values) > 0 {
				signalValue = values[0]
			}
			h.invokeData.Store(fmt.Sprintf("signalId%v", i), signalId)
			h.invokeData.Store(fmt.Sprintf("signalValue%v", i), signalValue)
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State2},
				},
			},
		}, nil
	case State2:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
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
