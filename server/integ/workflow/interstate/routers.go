// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package interstate

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
 * This test flow has four steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 * 		- Execute method will move to Step21 & Step22:
 * Step21:
 * 		- WaitFor will proceed once channel1 has been published to
 * 		- Execute method will move to Step31:
 * Step22:
 * 		- WaitFor will delay 2s then publish on channel1
 *      - Execute method will delay 2s then publish on channel2 & end in a dead-end
 * Step31:
 * 		- WaitFor will proceed once channel2 has been published to
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType = "interstate"
	State1       = "S1"
	State21      = "S21"
	State22      = "S22"
	State31      = "S31"

	channel1 = "channel1"
	channel2 = "channel2"
)

var TestVal1 = &dexpb.EncodedObject{
	Encoding: "json",
	Payload:  []byte("test-value1"),
}

var TestVal2 = &dexpb.EncodedObject{
	Encoding: "json",
	Payload:  []byte("test-value2"),
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
			},
		}, nil
	case State21:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ConditionId: "cmd-1", ChannelName: channel1},
				},
			},
		}, nil
	case State31:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ConditionId: "cmd-2", ChannelName: channel2},
				},
			},
		}, nil
	case State22:
		time.Sleep(time.Second * 2)
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
			PublishToChannel: []*dexpb.ChannelMessage{
				{
					ChannelName: channel1,
					Value: &dexpb.Value{
						Kind: &dexpb.Value_ObjValue{ObjValue: TestVal1},
					},
				},
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
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State21},
					{StepType: State22},
				},
			},
		}, nil
	case State21:
		results := request.GetConditionResults()
		channelResults := results.GetChannelResults()
		if len(channelResults) > 0 {
			values := channelResults[0].GetValues()
			if len(values) > 0 {
				h.invokeData.Store(State21+"received", values[0])
			}
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State31},
				},
			},
		}, nil
	case State31:
		results := request.GetConditionResults()
		channelResults := results.GetChannelResults()
		if len(channelResults) > 0 {
			values := channelResults[0].GetValues()
			if len(values) > 0 {
				h.invokeData.Store(State31+"received", values[0])
			}
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	case State22:
		time.Sleep(time.Second * 2)
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.DeadEndDecision(),
			},
			PublishToChannel: []*dexpb.ChannelMessage{
				{
					ChannelName: channel2,
					Value: &dexpb.Value{
						Kind: &dexpb.Value_ObjValue{ObjValue: TestVal2},
					},
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
