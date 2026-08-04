// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package headers

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, using WorkerServiceServer to implement the flow directly.
 *
 * State1:
 *		- WaitFor method does nothing
 *      - Execute method goes to State2
 * State2:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType = "headers"
	State1       = "S1"
	State2       = "S2"

	TestHeaderKey   = "integration-test-header"
	TestHeaderValue = "integration-test-value"
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

func validateTestHeader(ctx context.Context) error {
	incomingMetadata, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.InvalidArgument, "test header not found")
	}
	values := incomingMetadata.Get(TestHeaderKey)
	if len(values) == 0 || values[0] != TestHeaderValue {
		return status.Error(codes.InvalidArgument, "test header not found")
	}
	return nil
}

func (h *handler) InvokeWaitForMethod(
	ctx context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if err := validateTestHeader(ctx); err != nil {
		return nil, err
	}

	common.LogRequest("received waitFor request, ", request)

	if request.GetFlowType() == WorkflowType {
		if request.GetStepType() == State1 {
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
		if request.GetStepType() == State2 {
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
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	ctx context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if err := validateTestHeader(ctx); err != nil {
		return nil, err
	}

	common.LogRequest("received execute request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
		}

		if request.GetStepType() == State1 {
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					NextSteps: []*dexpb.StepMovement{
						{
							StepType:  State2,
							StepInput: request.GetStepInput(),
						},
					},
				},
			}, nil
		}
		if request.GetStepType() == State2 {
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
