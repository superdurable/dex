// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package s3_start_input

import (
	"context"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 1 step, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor does nothing
 *      - Execute will gracefully complete flow
 */
const (
	WorkflowType = "s3-start-input"
	State1       = "S1"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowClient    dexpb.FlowServiceClient
	invokeHistory sync.Map
}

func NewHandler(flowClient dexpb.FlowServiceClient) *handler {
	if flowClient == nil {
		panic("flowClient is required")
	}
	return &handler{
		flowClient:    flowClient,
		invokeHistory: sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	ctx context.Context,
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

	if request.GetFlowType() == WorkflowType && request.GetStepType() == State1 {
		h.incrementInvokeHistory(State1 + "_waitFor")
		resolved, err := common.LoadBlobsValue(ctx, h.flowClient, request.GetStepInput())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "LoadBlobs step input: %v", err)
		}
		h.invokeHistory.Store(State1+"_waitFor_input", resolved)
		return &dexpb.InvokeWaitForMethodResponse{}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	ctx context.Context,
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_execute")
	resolved, err := common.LoadBlobsValue(ctx, h.flowClient, request.GetStepInput())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "LoadBlobs step input: %v", err)
	}
	h.invokeHistory.Store(request.GetStepType()+"_execute_input", resolved)

	if request.GetStepType() == State1 {
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(request.GetStepInput()),
			},
		}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) GetTestResult() common.TestResult {
	outInvokehistory := make(map[string]interface{})
	h.invokeHistory.Range(func(key, value interface{}) bool {
		outInvokehistory[key.(string)] = value
		return true
	})
	return common.TestResult{InvokeData: outInvokehistory}
}

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}
