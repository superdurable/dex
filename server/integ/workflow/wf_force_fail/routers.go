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

package wf_force_fail

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"log"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
)

/**
 * This test flow has one step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 *      - Execute method will intentionally force-fail
 */
const (
	FlowType = "wf_force_fail"
	Step1    = "S1"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

var testStepInput = &dexpb.Value{
	Kind: &dexpb.Value_StringValue{StringValue: "test-data"},
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
	log.Println("received waitFor request, ", request)

	if request.GetFlowType() != FlowType {
		panic("should not get here")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_waitFor"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_waitFor", int64(1))
	}

	if request.GetStepType() == Step1 {
		return &dexpb.InvokeWaitForMethodResponse{}, nil
	}

	panic("should not get here")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	if request.GetFlowType() != FlowType || request.GetStepType() != Step1 {
		panic("should not get here")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
	}

	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:  service.ForceFailingFlowStepType,
					StepInput: testStepInput,
				},
			},
		},
	}, nil
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
