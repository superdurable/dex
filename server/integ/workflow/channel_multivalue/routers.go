// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package channel_multivalue

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service/common/ptr"
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
	ScenarioAtMostEmpty      = "at_most_empty"
	ScenarioAtMostCapped     = "at_most_capped"
	ScenarioRange            = "range"
	ScenarioSameChannelExact = "same_channel_exact2x2"
	ScenarioExact2PlusZero   = "exact2_plus_zero_to_all"
	ScenarioAnyNoPremature   = "any_no_premature"
	ScenarioInvalidBounds    = "invalid_bounds"
	ScenarioCanBuffered      = "can_buffered"
	ScenarioCanMatchBoundary = "can_match_boundary"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler() *handler {
	return &handler{}
}

func (h *handler) InvokeWorkerRPC(
	context.Context,
	*dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	return nil, status.Error(codes.Unimplemented, "no RPC")
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	log.Println("channel_multivalue waitFor", request.GetStepType(), request.GetStepInput())
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	bump(&h.invokeHistory, request.GetStepType()+"_waitFor")

	scenario := stepInputString(request.GetStepInput())
	switch request.GetStepType() {
	case State1:
		response := &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: waitingConditionForScenario(scenario),
		}
		if scenario == ScenarioAtMostCapped {
			response.PublishToChannel = stringChannelMessages(
				ChannelName,
				"m0",
				"m1",
				"m2",
				"m3",
				"m4",
			)
		}
		return response, nil
	case State2:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
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
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
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
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{StepType: State2, StepInput: request.GetStepInput()},
					},
				},
			}, nil
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	case State2:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
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

func waitingConditionForScenario(scenario string) *dexpb.WaitingCondition {
	switch scenario {
	case ScenarioExactN, ScenarioCanBuffered, ScenarioCanMatchBoundary:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(3)),
					AtMost:      ptr.Any(int32(3)),
				},
			},
		}
	case ScenarioOneToAll:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(1)),
				},
			},
		}
	case ScenarioZeroToAllEmpty:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(0)),
				},
			},
		}
	case ScenarioAtMostEmpty, ScenarioAtMostCapped:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtMost:      ptr.Any(int32(3)),
				},
			},
		}
	case ScenarioRange:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "c1",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(2)),
					AtMost:      ptr.Any(int32(4)),
				},
			},
		}
	case ScenarioSameChannelExact:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
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
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
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
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
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
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{
					ConditionId: "bad",
					ChannelName: ChannelName,
					AtLeast:     ptr.Any(int32(3)),
					AtMost:      ptr.Any(int32(1)),
				},
			},
		}
	default:
		return &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		}
	}
}

func stepInputString(stepInput *dexpb.Value) string {
	if stepInput == nil {
		return ""
	}
	if stringValue, ok := stepInput.Kind.(*dexpb.Value_StringValue); ok {
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

func cloneChannelResults(results []*dexpb.ChannelResult) []*dexpb.ChannelResult {
	out := make([]*dexpb.ChannelResult, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		cloned := &dexpb.ChannelResult{
			ConditionId:     result.GetConditionId(),
			ConditionStatus: result.GetConditionStatus(),
			ChannelName:     result.GetChannelName(),
			Values:          append([]*dexpb.Value{}, result.GetValues()...),
		}
		out = append(out, cloned)
	}
	return out
}

func stringChannelMessages(channelName string, values ...string) []*dexpb.ChannelMessage {
	messages := make([]*dexpb.ChannelMessage, 0, len(values))
	for _, value := range values {
		messages = append(messages, &dexpb.ChannelMessage{
			ChannelName: channelName,
			Value: &dexpb.Value{
				Kind: &dexpb.Value_StringValue{StringValue: value},
			},
		})
	}
	return messages
}
