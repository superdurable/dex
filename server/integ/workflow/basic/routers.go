// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package basic

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
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
			return &dexpb.InvokeWaitForMethodResponse{}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

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
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{
							StepType:  Step2,
							StepInput: request.GetStepInput(),
							StepOptions: &dexpb.StepOptions{
								WaitForTimeoutSeconds: 14,
								ExecuteTimeoutSeconds: 15,
								WaitForRetryPolicy: &dexpb.RetryPolicy{
									InitialIntervalSeconds: 14,
									BackoffCoefficient:     14,
									MaximumAttempts:        14,
									MaximumIntervalSeconds: 14,
								},
								ExecuteRetryPolicy: &dexpb.RetryPolicy{
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
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					CloseDecision: common.GracefulCompleteDecision(request.GetStepInput()),
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
