// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package greedy_timer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/superdurable/dex/integ/workflow/common"
	"strconv"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/*
*
This flow will accept an array of integers representing durations and execute a step that waits on a timer corresponding to each duration provided
*/
const (
	WorkflowType       = "greedy_timer"
	ScheduleTimerState = "schedule"
	SubmitDurationsRPC = "submitDurationsRPC"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

type Input struct {
	Durations []int64 `json:"durations"`
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

	flowContext := request.GetContext()
	if flowContext.GetFlowId() == "" || flowContext.GetRunId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid context in the request")
	}
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type: "+request.GetFlowType())
	}

	if request.GetRpcName() == SubmitDurationsRPC {
		return &dexpb.InvokeWorkerRPCResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{
						StepType:  ScheduleTimerState,
						StepInput: request.GetInput(),
					},
				},
			},
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
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

		if request.GetStepType() == ScheduleTimerState {
			var input Input
			stepInput := request.GetStepInput()
			payload := stepInput.GetObjValue().GetPayload()
			if len(payload) == 0 {
				payload = []byte(stepInput.GetStringValue())
			}
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}

			timers := make([]*dexpb.TimerCondition, len(input.Durations))
			for i, duration := range input.Durations {
				timers[i] = &dexpb.TimerCondition{
					ConditionId:     "duration-" + strconv.FormatInt(duration, 10),
					DurationSeconds: duration,
				}
			}

			return &dexpb.InvokeWaitForMethodResponse{
				WaitingCondition: &dexpb.WaitingCondition{
					TimerConditions:      timers,
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

		if request.GetStepType() == ScheduleTimerState {
			h.invokeData.Store("completed_state_id", request.GetContext().GetStepExecutionId())
			results := request.GetConditionResults()
			timerResults := results.GetTimerResults()
			h.invokeData.Store("completed_timer_id", timerResults[0].GetConditionId())

			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					CloseDecision: common.ForceCompleteDecision(nil),
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
