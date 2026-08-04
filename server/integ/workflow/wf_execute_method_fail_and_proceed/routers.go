// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package wf_execute_method_fail_and_proceed

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has one step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method is skipped
 *      - Execute method will intentionally fail
 * StepRecover:
 *		- Execute method will gracefully complete flow
 */
const (
	FlowType          = "wf_execute_method_fail_and_proceed"
	Step1             = "S1"
	StepRecover       = "Recover"
	InputData         = "test-data"
	InputDataEncoding = "test-encoding"
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
	panic("should not get here")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if request.GetFlowType() != FlowType {
		panic("should not get here")
	}

	if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
		h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
	} else {
		h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
	}

	stepInput := request.GetStepInput()
	encoding, data := objValueParts(stepInput)
	if data != InputData || encoding != InputDataEncoding {
		panic("input is not correct: " + data + ", " + encoding)
	}

	if request.GetStepType() == Step1 {
		return nil, status.Error(codes.InvalidArgument, "test-error")
	}
	if request.GetStepType() == StepRecover {
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	}

	panic("should not get here")
}

func objValueParts(value *dexpb.Value) (encoding string, data string) {
	obj := value.GetObjValue()
	if obj == nil {
		return "", ""
	}
	return obj.GetEncoding(), string(obj.GetPayload())
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}
