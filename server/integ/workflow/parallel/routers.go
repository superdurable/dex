// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package parallel

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
 * This test flow has eight steps, using WorkerServiceServer to implement the flow directly.
 *
 * State1:
 *		- WaitFor method does nothing
 * 		- Execute method delays 1s then moves to State11, State12, & State13
 * State11:
 *		- WaitFor method does nothing
 * 		- Execute method delays 2s then moves to State111 & State112
 * State12:
 *		- WaitFor method does nothing
 * 		- Execute method delays 2s then moves to State121 & State122
 * State13:
 *		- WaitFor method does nothing
 *      - Execute method will delay 1s then gracefully complete flow
 * State111:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 * State112:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 * State121:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 * State122:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType = "parallel"
	State1       = "S1"
	State11      = "S11"
	State12      = "S12"
	State13      = "S13"
	State111     = "S111"
	State112     = "S112"
	State121     = "S121"
	State122     = "S122"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
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

		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
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

		var nextSteps []*dexpb.StepMovement
		var closeDecision *dexpb.CloseDecision
		switch request.GetStepType() {
		case State1:
			time.Sleep(time.Second * 1)

			nextSteps = []*dexpb.StepMovement{
				{StepType: State11},
				{StepType: State12},
				{StepType: State13},
			}
		case State11:
			time.Sleep(time.Second * 2)

			nextSteps = []*dexpb.StepMovement{
				{StepType: State111},
				{StepType: State112},
			}
		case State12:
			time.Sleep(time.Second * 2)

			nextSteps = []*dexpb.StepMovement{
				{StepType: State121},
				{StepType: State122},
			}
		case State13:
			time.Sleep(time.Second * 1)

			closeDecision = common.GracefulCompleteDecision(parallelCloseInput(request.GetStepType()))
		case State111, State112, State121, State122:
			closeDecision = common.GracefulCompleteDecision(parallelCloseInput(request.GetStepType()))
		default:
			closeDecision = common.ForceFailDecision(nil)
		}

		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps:     nextSteps,
				CloseDecision: closeDecision,
			},
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func parallelCloseInput(stepType string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte("from " + stepType),
			},
		},
	}
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
