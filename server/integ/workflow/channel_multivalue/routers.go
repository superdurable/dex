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

package channel_multivalue

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/common"
	"github.com/superdurable/iwf/service"
	"github.com/superdurable/iwf/service/common/ptr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	WorkflowType = "channel_multivalue"
	State1       = "S1"
	State2       = "S2"
	ChannelName  = "mv-channel"
	ChannelB     = "mv-channel-b"

	ScenarioExactN           = "exact_n"
	ScenarioOneToAll         = "one_to_all"
	ScenarioZeroToAllEmpty   = "zero_to_all_empty"
	ScenarioRange            = "range"
	ScenarioSameChannelExact = "same_channel_exact2x2"
	ScenarioExact2PlusZero   = "exact2_plus_zero_to_all"
	ScenarioAnyNoPremature   = "any_no_premature"
	ScenarioInvalidBounds    = "invalid_bounds"
	ScenarioCanBuffered      = "can_buffered"
	ScenarioCanMatchBoundary = "can_match_boundary"
)

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler() *handler {
	return &handler{}
}

func (h *handler) InvokeWorkerRPC(
	context.Context,
	*iwfpb.InvokeWorkerRPCRequest,
) (*iwfpb.InvokeWorkerRPCResponse, error) {
	return nil, status.Error(codes.Unimplemented, "no RPC")
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("channel_multivalue waitFor", request.GetStepType(), request.GetStepInput())
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	bump(&h.invokeHistory, request.GetStepType()+"_waitFor")

	scenario := stepInputString(request.GetStepInput())
	switch request.GetStepType() {
	case State1:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: waitingConditionForScenario(scenario),
		}, nil
	case State2:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*iwfpb.ChannelCondition{
					{
						ConditionId: "s2",
						ChannelName: ChannelName,
						AtLeast:     ptr.Any(int32(1)),
						AtMost:      ptr.Any(int32(1)),
					},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid step")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("channel_multivalue execute", request.GetStepType())
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	bump(&h.invokeHistory, request.GetStepType()+"_execute")

	scenario := stepInputString(request.GetStepInput())
	if request.GetStepType() == State1 {
		results := request.GetConditionResults().GetChannelResults()
		h.invokeData.Store(scenario+"-results", cloneChannelResults(results))
	}

	switch request.GetStepType() {
	case State1:
		if scenario == ScenarioCanBuffered {
			return &iwfpb.InvokeExecuteMethodResponse{
				StepDecision: &iwfpb.StepDecision{
					NextSteps: []*iwfpb.StepMovement{
						{StepType: State2, StepInput: request.GetStepInput()},
					},
				},
			}, nil
		}
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
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
		return nil, status.Error(codes.InvalidArgument, "invalid step")
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

func waitingConditionForScenario(scenario string) *iwfpb.WaitingCondition {
	switch scenario {
	case ScenarioExactN, ScenarioCanBuffered, ScenarioCanMatchBoundary:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(3)),
					AtMost:      ptr.Any(int32(3)),
				},
			},
		}
	case ScenarioOneToAll:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(1)),
				},
			},
		}
	case ScenarioZeroToAllEmpty:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(0)),
				},
			},
		}
	case ScenarioRange:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(2)),
					AtMost:      ptr.Any(int32(4)),
				},
			},
		}
	case ScenarioSameChannelExact:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(2)),
					AtMost:      ptr.Any(int32(2)),
				},
				{
					ConditionId: "c2",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(2)),
					AtMost:      ptr.Any(int32(2)),
				},
			},
		}
	case ScenarioExact2PlusZero:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "exact",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(2)),
					AtMost:      ptr.Any(int32(2)),
				},
				{
					ConditionId: "zero",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(0)),
				},
			},
		}
	case ScenarioAnyNoPremature:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "a",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(5)),
					AtMost:      ptr.Any(int32(5)),
				},
				{
					ConditionId: "b",
					ChannelName: ChannelB,
					AtLeast:     ptr.Any(int32(1)),
					AtMost:      ptr.Any(int32(1)),
				},
			},
		}
	case ScenarioInvalidBounds:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*iwfpb.ChannelCondition{
				{
					ConditionId: "bad",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(3)),
					AtMost:      ptr.Any(int32(1)),
				},
			},
		}
	default:
		return &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		}
	}
}

func stepInputString(stepInput *iwfpb.Value) string {
	if stepInput == nil {
		return ""
	}
	if stringValue, ok := stepInput.Kind.(*iwfpb.Value_StringValue); ok {
		return stringValue.StringValue
	}
	return ""
}

func bump(history *sync.Map, key string) {
	if value, ok := history.Load(key); ok {
		history.Store(key, value.(int64)+1)
		return
	}
	history.Store(key, int64(1))
}

func cloneChannelResults(results []*iwfpb.ChannelResult) []*iwfpb.ChannelResult {
	out := make([]*iwfpb.ChannelResult, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		cloned := &iwfpb.ChannelResult{
			ConditionId:     result.GetConditionId(),
			ConditionStatus: result.GetConditionStatus(),
			ChannelName:     result.GetChannelName(),
			Values:          append([]*iwfpb.Value{}, result.GetValues()...),
		}
		out = append(out, cloned)
	}
	return out
}
