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

package parallel

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
	iwfpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
		}

		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
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

		var nextSteps []*iwfpb.StepMovement
		switch request.GetStepType() {
		case State1:
			time.Sleep(time.Second * 1)

			nextSteps = []*iwfpb.StepMovement{
				{StepType: State11},
				{StepType: State12},
				{StepType: State13},
			}
		case State11:
			time.Sleep(time.Second * 2)

			nextSteps = []*iwfpb.StepMovement{
				{StepType: State111},
				{StepType: State112},
			}
		case State12:
			time.Sleep(time.Second * 2)

			nextSteps = []*iwfpb.StepMovement{
				{StepType: State121},
				{StepType: State122},
			}
		case State13:
			time.Sleep(time.Second * 1)

			nextSteps = []*iwfpb.StepMovement{
				{
					StepType: service.GracefulCompletingFlowStepType,
					StepInput: &iwfpb.Value{
						Kind: &iwfpb.Value_ObjValue{
							ObjValue: &iwfpb.EncodedObject{
								Encoding: "json",
								Payload:  []byte("from " + request.GetStepType()),
							},
						},
					},
				},
			}
		case State111, State112, State121, State122:
			nextSteps = []*iwfpb.StepMovement{
				{
					StepType: service.GracefulCompletingFlowStepType,
					StepInput: &iwfpb.Value{
						Kind: &iwfpb.Value_ObjValue{
							ObjValue: &iwfpb.EncodedObject{
								Encoding: "json",
								Payload:  []byte("from " + request.GetStepType()),
							},
						},
					},
				},
			}
		default:
			nextSteps = []*iwfpb.StepMovement{
				{StepType: service.ForceFailingFlowStepType},
			}
		}

		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: nextSteps,
			},
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
