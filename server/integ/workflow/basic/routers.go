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

package basic

import (
	"context"
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- Waits on nothing. Will execute momentarily
 *      - Execute method will move to Step2
 * Step2:
 *		- Waits on nothing. Will execute momentarily
 *      - Execute method will gracefully complete flow
 */
const (
	FlowType = "basic"
	Step1    = "S1"
	Step2    = "S2"
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

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() == FlowType {
		// Basic flow goes straight to execute methods without any conditions
		if request.GetStepType() == Step1 || request.GetStepType() == Step2 {
			if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
				h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
			} else {
				h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
			}
			return &iwfpb.InvokeWaitForMethodResponse{}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	stepContext := request.GetContext()
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}

	if request.GetFlowType() == FlowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
		}

		if request.GetStepType() == Step1 {
			// Move to next step
			return &iwfpb.InvokeExecuteMethodResponse{
				StepDecision: &iwfpb.StepDecision{
					NextSteps: []*iwfpb.StepMovement{
						{
							StepType:  Step2,
							StepInput: request.GetStepInput(),
							StepOptions: &iwfpb.StepOptions{
								WaitForTimeoutSeconds: 14,
								ExecuteTimeoutSeconds: 15,
								WaitForRetryPolicy: &iwfpb.RetryPolicy{
									InitialIntervalSeconds: 14,
									BackoffCoefficient:     14,
									MaximumAttempts:        14,
									MaximumIntervalSeconds: 14,
								},
								ExecuteRetryPolicy: &iwfpb.RetryPolicy{
									InitialIntervalSeconds: 15,
									BackoffCoefficient:     15,
									MaximumAttempts:        15,
									MaximumIntervalSeconds: 15,
								},
							},
						},
					},
				},
			}, nil
		} else if request.GetStepType() == Step2 {
			// Move to completion
			return &iwfpb.InvokeExecuteMethodResponse{
				StepDecision: &iwfpb.StepDecision{
					NextSteps: []*iwfpb.StepMovement{
						{
							StepType:  service.GracefulCompletingFlowStepType,
							StepInput: request.GetStepInput(),
						},
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
	return common.TestResult{InvokeHistory: invokeHistory}
}
