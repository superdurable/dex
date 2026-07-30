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

package anycommandcombination

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
 *		- WaitFor will fail its first attempt and then retry which will proceed when a combination is completed
 *      - Execute method will invoke the combination and move the State2
 * State2:
 *		- WaitFor will fail its first attempt and then retry which will proceed when a combination is completed
 *      - Execute method will invoke the combination and gracefully complete flow
 */
const (
	WorkflowType     = "any_command_combination"
	State1           = "S1"
	State2           = "S2"
	TimerId1         = "test-timer-1"
	SignalNameAndId1 = "test-signal-name1"
	SignalNameAndId2 = "test-signal-name2"
	SignalNameAndId3 = "test-signal-name3"
	// Two waits on the same channel need distinct condition ids.
	SignalCond1a = "test-signal-cond1a"
	SignalCond1b = "test-signal-cond1b"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
	//we want to confirm that the interpreter workflow activity will fail when the commandId is empty with ANY_COMMAND_COMBINATION_COMPLETED
	hasS1RetriedForInvalidCommandId bool
	hasS2RetriedForInvalidCommandId bool
}

func NewHandler() *handler {
	return &handler{
		invokeHistory:                   sync.Map{},
		invokeData:                      sync.Map{},
		hasS1RetriedForInvalidCommandId: false,
		hasS2RetriedForInvalidCommandId: false,
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	invalidTimerConditions := []*dexpb.TimerCondition{
		{
			DurationSeconds: 86400 * 365,
		},
	}
	validTimerConditions := []*dexpb.TimerCondition{
		{
			ConditionId:     TimerId1,
			DurationSeconds: 86400 * 365,
		},
	}
	invalidChannelConditions := []*dexpb.ChannelCondition{
		{
			ChannelName: SignalNameAndId1,
		},
		{
			ConditionId: SignalNameAndId2,
			ChannelName: SignalNameAndId2,
		},
	}
	validChannelConditions := []*dexpb.ChannelCondition{
		{
			ConditionId: SignalCond1a,
			ChannelName: SignalNameAndId1,
		},
		{
			ConditionId: SignalCond1b,
			ChannelName: SignalNameAndId1,
		},
		{
			ConditionId: SignalNameAndId2,
			ChannelName: SignalNameAndId2,
		},
		{
			ConditionId: SignalNameAndId3,
			ChannelName: SignalNameAndId3,
		},
	}

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
		}

		if request.GetStepType() == State1 {
			if h.hasS1RetriedForInvalidCommandId {
				return &dexpb.InvokeWaitForMethodResponse{
					WaitingCondition: &dexpb.WaitingCondition{
						ChannelConditions:    validChannelConditions,
						TimerConditions:      validTimerConditions,
						WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
						ConditionCombinations: []*dexpb.ConditionCombination{
							{
								ConditionIds: []string{
									TimerId1, SignalCond1a, SignalCond1b,
								},
							},
							{
								ConditionIds: []string{
									TimerId1, SignalCond1a, SignalNameAndId2,
								},
							},
						},
					},
				}, nil
			}

			h.hasS1RetriedForInvalidCommandId = true
			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					ChannelConditions:    validChannelConditions,
					TimerConditions:      invalidTimerConditions,
					WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				},
			}, nil
		}

		if request.GetStepType() == State2 {
			if h.hasS2RetriedForInvalidCommandId {
				return &dexpb.InvokeWaitForMethodResponse{
					WaitingCondition: &dexpb.WaitingCondition{
						ChannelConditions:    validChannelConditions,
						TimerConditions:      validTimerConditions,
						WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
						ConditionCombinations: []*dexpb.ConditionCombination{
							{
								ConditionIds: []string{
									SignalNameAndId2, SignalCond1a,
								},
							},
							{
								ConditionIds: []string{
									TimerId1, SignalCond1a, SignalNameAndId2,
								},
							},
						},
					},
				}, nil
			}

			h.hasS2RetriedForInvalidCommandId = true
			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					ChannelConditions:    invalidChannelConditions,
					TimerConditions:      validTimerConditions,
					WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
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
			h.invokeData.Store("s1_commandResults", request.GetConditionResults())

			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{StepType: State2},
					},
				},
			}, nil
		} else if request.GetStepType() == State2 {
			h.invokeData.Store("s2_commandResults", request.GetConditionResults())

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
