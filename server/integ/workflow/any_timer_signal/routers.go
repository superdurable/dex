// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package anytimersignal

import (
	"context"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, using WorkerServiceServer to implement the flow directly.
 *
 * State1:
 *		- WaitFor will wait for the signals to trigger.
 *      - Execute method will trigger signals and retry State1 once, then trigger signals and move the State2
 * State2:
 *		- Waits on nothing. Will execute momentarily
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType = "any_timer_signal"
	State1       = "S1"
	State2       = "S2"
	SignalName   = "test-signal-name"
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

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
		}

		if request.GetStepType() == State1 {
			var timerConditions []*dexpb.TimerCondition
			stepContext := request.GetContext()

			if stepContext.GetStepExecutionId() == State1+"-"+"1" {
				timerConditions = []*dexpb.TimerCondition{
					{
						DurationSeconds: 1,
					},
				}
			}

			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					ChannelConditions: []*dexpb.ChannelCondition{
						{
							ConditionId: "signal-cmd-id",
							ChannelName: SignalName,
						},
					},
					TimerConditions:      timerConditions,
					WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
				},
			}, nil
		}

		if request.GetStepType() == State2 {
			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				},
			}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
		}

		if request.GetStepType() == State1 {
			channelResults := request.GetConditionResults().GetChannelResults()
			var nextSteps []*dexpb.StepMovement

			stepContext := request.GetContext()
			if stepContext.GetStepExecutionId() == State1+"-"+"1" {
				h.invokeData.Store("signalChannelName1", channelResults[0].GetChannelName())
				h.invokeData.Store("signalCommandId1", channelResults[0].GetConditionId())
				h.invokeData.Store("signalStatus1", channelResults[0].GetConditionStatus())
				nextSteps = []*dexpb.StepMovement{{StepType: State1}}
			} else {
				h.invokeData.Store("signalChannelName2", channelResults[0].GetChannelName())
				h.invokeData.Store("signalCommandId2", channelResults[0].GetConditionId())
				h.invokeData.Store("signalStatus2", channelResults[0].GetConditionStatus())
				h.invokeData.Store("signalValue2", channelResults[0].GetValues())
				nextSteps = []*dexpb.StepMovement{{StepType: State2}}
			}

			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: nextSteps,
				},
			}, nil
		} else if request.GetStepType() == State2 {
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					CloseDecision: common.GracefulCompleteDecision(nil),
				},
			}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
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
