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

package reset

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/integ/workflow/common"
	"log"
	"strconv"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
* This test flow has 2 steps, using WorkerServiceServer to implement the flow directly.
* Step1:
*       - No WaitFor
*       - Execute moves to Step2
* Step2:
* 		- No WaitFor
*       - Execute loops through step2 5 times, then gracefully completes the flow.
* This test is used for testing reset by step type and step execution id without WaitFor
 */
const (
	WorkflowType = "reset"
	State1       = "S1"
	State2       = "S2"
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
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "No waitFor call is expected.")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	log.Println("start of execute")
	log.Println("received execute request, ", request)

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
					{
						StepType:  State2,
						StepInput: request.GetStepInput(),
						StepOptions: &dexpb.StepOptions{
							SkipWaitFor: true,
						},
					},
				},
			},
		}, nil
	case State2:
		counter, err := strconv.Atoi(stepInputString(request.GetStepInput()))
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if counter < 5 {
			updatedInput := &dexpb.Value{
				Kind: &dexpb.Value_ObjValue{
					ObjValue: &dexpb.EncodedObject{
						Encoding: "json",
						Payload:  []byte(fmt.Sprintf("%v", counter+1)),
					},
				},
			}
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{
							StepType:  State2,
							StepInput: updatedInput,
							StepOptions: &dexpb.StepOptions{
								SkipWaitFor: true,
							},
						},
					},
				},
			}, nil
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{
						StepType:  service.GracefulCompletingFlowStepType,
						StepInput: request.GetStepInput(),
					},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func stepInputString(stepInput *dexpb.Value) string {
	if stepInput == nil {
		return ""
	}
	if stringValue, ok := stepInput.Kind.(*dexpb.Value_StringValue); ok {
		return stringValue.StringValue
	}
	if objValue, ok := stepInput.Kind.(*dexpb.Value_ObjValue); ok {
		if objValue.ObjValue.GetEncoding() == "json" {
			return string(objValue.ObjValue.GetPayload())
		}
	}
	return ""
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
