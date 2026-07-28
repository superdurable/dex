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
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"sync"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
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
)

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
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
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	invalidTimerConditions := []*iwfpb.TimerCondition{
		{
			FiringUnixTimestampSeconds: time.Now().Unix() + 86400*365,
		},
	}
	validTimerConditions := []*iwfpb.TimerCondition{
		{
			ConditionId:                TimerId1,
			FiringUnixTimestampSeconds: time.Now().Unix() + 86400*365,
		},
	}
	invalidChannelConditions := []*iwfpb.ChannelCondition{
		{
			ChannelName: SignalNameAndId1,
		},
		{
			ConditionId: SignalNameAndId2,
			ChannelName: SignalNameAndId2,
		},
	}
	validChannelConditions := []*iwfpb.ChannelCondition{
		{
			ConditionId: SignalNameAndId1,
			ChannelName: SignalNameAndId1,
		},
		{
			ConditionId: SignalNameAndId1,
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
				return &iwfpb.InvokeWaitForMethodResponse{
					WaitingCondition: &iwfpb.WaitingCondition{
						ChannelConditions:    validChannelConditions,
						TimerConditions:      validTimerConditions,
						WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
						ConditionCombinations: []*iwfpb.ConditionCombination{
							{
								ConditionIds: []string{
									TimerId1, SignalNameAndId1, SignalNameAndId1,
								},
							},
							{
								ConditionIds: []string{
									TimerId1, SignalNameAndId1, SignalNameAndId2,
								},
							},
						},
					},
				}, nil
			}

			h.hasS1RetriedForInvalidCommandId = true
			return &iwfpb.InvokeWaitForMethodResponse{
				WaitingCondition: &iwfpb.WaitingCondition{
					ChannelConditions:    validChannelConditions,
					TimerConditions:      invalidTimerConditions,
					WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				},
			}, nil
		}

		if request.GetStepType() == State2 {
			if h.hasS2RetriedForInvalidCommandId {
				return &iwfpb.InvokeWaitForMethodResponse{
					WaitingCondition: &iwfpb.WaitingCondition{
						ChannelConditions:    validChannelConditions,
						TimerConditions:      validTimerConditions,
						WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
						ConditionCombinations: []*iwfpb.ConditionCombination{
							{
								ConditionIds: []string{
									SignalNameAndId2, SignalNameAndId1,
								},
							},
							{
								ConditionIds: []string{
									TimerId1, SignalNameAndId1, SignalNameAndId2,
								},
							},
						},
					},
				}, nil
			}

			h.hasS2RetriedForInvalidCommandId = true
			return &iwfpb.InvokeWaitForMethodResponse{
				WaitingCondition: &iwfpb.WaitingCondition{
					ChannelConditions:    invalidChannelConditions,
					TimerConditions:      validTimerConditions,
					WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMBINATION_COMPLETED,
				},
			}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
		}

		if request.GetStepType() == State1 {
			h.invokeData.Store("s1_commandResults", request.GetConditionResults())

			return &iwfpb.InvokeExecuteMethodResponse{
				StepDecision: &iwfpb.StepDecision{
					NextSteps: []*iwfpb.StepMovement{
						{StepType: State2},
					},
				},
			}, nil
		} else if request.GetStepType() == State2 {
			h.invokeData.Store("s2_commandResults", request.GetConditionResults())

			return &iwfpb.InvokeExecuteMethodResponse{
				StepDecision: &iwfpb.StepDecision{
					NextSteps: []*iwfpb.StepMovement{
						{StepType: service.GracefulCompletingFlowStepType},
					},
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
