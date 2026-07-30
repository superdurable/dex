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

package s3GetSetDataAttributes

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * Test flow for S3 external storage with get/set data attributes APIs.
 * Tests both small data (stays in Temporal) and large data (goes to S3).
 *
 * Step1:
 *   - Simple flow that waits and completes
 *
 * The main testing is done via direct API calls to get/set data attributes,
 * not through flow step transitions.
 */

const (
	WorkflowType = "s3-get-set-data-attributes"
	State1       = "S1"

	SmallDataKey        = "small-data"
	LargeDataKey        = "large-data"
	AnotherLargeDataKey = "another-large-data"

	SmallDataContent = "small"

	LargeDataContent        = "large-data-content-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	AnotherLargeDataContent = "another-large-data-content-yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"

	UpdatedSmallDataContent = "updated-small"
	UpdatedLargeDataContent = "updated-large-data-content-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
)

var (
	SmallDataValue = jsonObjValue("\"" + SmallDataContent + "\"")

	LargeDataValue = jsonObjValue("\"" + LargeDataContent + "\"")

	AnotherLargeDataValue = jsonObjValue("\"" + AnotherLargeDataContent + "\"")

	UpdatedSmallDataValue = jsonObjValue("\"" + UpdatedSmallDataContent + "\"")

	UpdatedLargeDataValue = jsonObjValue("\"" + UpdatedLargeDataContent + "\"")
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
}

func NewHandler() *handler {
	return &handler{}
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	if request.GetStepType() == State1 {
		h.invokeHistory.Store("S1_waitFor", int64(1))
		return &dexpb.InvokeWaitForMethodResponse{}, nil
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	if request.GetStepType() == State1 {
		h.invokeHistory.Store("S1_execute", int64(1))
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
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

func jsonObjValue(payload string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte(payload),
			},
		},
	}
}
