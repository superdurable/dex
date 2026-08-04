// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package anycommandclose

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, using WorkerServiceServer to implement the flow directly.
 *
 * State1:
 *		- WaitFor wait until a signal is received
 *      - Execute method will fire the signal and move the State2
 * State2:
 *		- Waits on nothing. Will execute momentarily
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType = "any_command_close"
	State1       = "S1"
	State2       = "S2"
	SignalName1  = "test-signal-name1"
	SignalName2  = "test-signal-name2"
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
			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					ChannelConditions: []*dexpb.ChannelCondition{
						{
							ConditionId: "signal-cmd-id1",
							ChannelName: SignalName1,
						},
						{
							ConditionId: "signal-cmd-id2",
							ChannelName: SignalName2,
						},
					},
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
			h.invokeData.Store("signalCommandResultsLength", len(channelResults))

			h.invokeData.Store("signalChannelName0", channelResults[0].GetChannelName())
			h.invokeData.Store("signalCommandId0", channelResults[0].GetConditionId())
			h.invokeData.Store("signalStatus0", channelResults[0].GetConditionStatus())

			h.invokeData.Store("signalChannelName1", channelResults[1].GetChannelName())
			h.invokeData.Store("signalCommandId1", channelResults[1].GetConditionId())
			h.invokeData.Store("signalStatus1", channelResults[1].GetConditionStatus())
			h.invokeData.Store("signalValue1", channelResults[1].GetValues())

			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{StepType: State2},
					},
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
