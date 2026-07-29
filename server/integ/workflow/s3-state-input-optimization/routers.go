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

package s3_state_input_optimization

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 3 steps, testing S3 step input optimization functionality.
 * All steps use the same large input data to test deduplication.
 *
 * Step1:
 *		- WaitFor stores input data for verification
 *      - Execute transitions to Step2 with same input
 *
 * Step2:
 *		- WaitFor stores input data for verification
 *      - Execute transitions to Step3 with same input
 *
 * Step3:
 *		- WaitFor stores input data for verification
 *      - Execute completes flow
 */
const (
	WorkflowType = "s3-state-input-optimization"
	State1       = "S1"
	State2       = "S2"
	State3       = "S3"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	flowClient    dexpb.FlowServiceClient
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler(flowClient dexpb.FlowServiceClient) *handler {
	if flowClient == nil {
		panic("flowClient is required")
	}
	return &handler{
		flowClient:    flowClient,
		invokeHistory: sync.Map{},
		invokeData:    sync.Map{},
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	stepType := request.GetStepType()
	if stepType != State1 && stepType != State2 && stepType != State3 {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(stepType + "_waitFor")
	if err := h.storeStepInputData(ctx, stepType, request.GetStepInput()); err != nil {
		return nil, err
	}

	return &dexpb.InvokeWaitForMethodResponse{}, nil
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	stepType := request.GetStepType()
	h.incrementInvokeHistory(stepType + "_execute")

	switch stepType {
	case State1:
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
	case State2:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{
						StepType:  State3,
						StepInput: request.GetStepInput(),
					},
				},
			},
		}, nil
	case State3:
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

func (h *handler) GetTestResult() common.TestResult {
	outInvokehistory := make(map[string]interface{})
	h.invokeHistory.Range(func(key, value interface{}) bool {
		outInvokehistory[key.(string)] = value
		return true
	})

	outInvokeData := make(map[string]interface{})
	h.invokeData.Range(func(key, value interface{}) bool {
		outInvokeData[key.(string)] = value
		return true
	})

	for key, value := range outInvokeData {
		outInvokehistory[key] = value
	}

	return common.TestResult{InvokeData: outInvokehistory}
}

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func (h *handler) storeStepInputData(ctx context.Context, stepType string, stepInput *dexpb.Value) error {
	inputData, err := common.ObjPayloadString(ctx, h.flowClient, stepInput)
	if err != nil {
		return status.Errorf(codes.Internal, "LoadBlobs step input: %v", err)
	}
	if inputData == "" {
		return nil
	}
	h.invokeData.Store(stepType+"_input_data", inputData)
	log.Printf(
		"%s WaitUntil: Received input data (length: %d): %s",
		stepType,
		len(inputData),
		common.FormatForLogging(inputData),
	)
	return nil
}
