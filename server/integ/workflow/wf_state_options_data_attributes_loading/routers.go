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

package wf_state_options_data_attributes_loading

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
 * This test flow has five steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 * 		- Execute method creates all data attribute keys used in this test
 * Step2 through Step5:
 *		- WaitFor and Execute verify all data attributes are loaded
 */
const (
	WorkflowType = "state_options_data_attributes_loading"
	State1       = "S1"
	State2       = "S2"
	State3       = "S3"
	State4       = "S4"
	State5       = "S5"
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

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	_ *iwfpb.InvokeWorkerRPCRequest,
) (*iwfpb.InvokeWorkerRPCResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "unexpected worker rpc")
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	log.Println("state_options_data_attributes_loading: received waitFor request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	if request.GetStepType() != State1 {
		if err := verifyAllDataAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
	}

	return &iwfpb.InvokeWaitForMethodResponse{
		WaitingCondition: &iwfpb.WaitingCondition{
			WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		},
	}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	log.Println("state_options_data_attributes_loading: received execute request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	var response *iwfpb.InvokeExecuteMethodResponse
	switch request.GetStepType() {
	case State1:
		response = getState1ExecuteResponse(request)
	case State2, State3, State4:
		if err := verifyAllDataAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
		response = getNextStateExecuteResponse(request)
	case State5:
		if err := verifyAllDataAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
		response = getState5ExecuteResponse()
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid step type")
	}

	return response, nil
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func getState1ExecuteResponse(request *iwfpb.InvokeExecuteMethodRequest) *iwfpb.InvokeExecuteMethodResponse {
	return &iwfpb.InvokeExecuteMethodResponse{
		StepDecision: &iwfpb.StepDecision{
			NextSteps: []*iwfpb.StepMovement{
				{
					StepType:  State2,
					StepInput: request.GetStepInput(),
				},
			},
		},
		UpsertAttributes: upsertDataAttributes(),
	}
}

func getNextStateExecuteResponse(request *iwfpb.InvokeExecuteMethodRequest) *iwfpb.InvokeExecuteMethodResponse {
	var nextStepType string
	switch request.GetStepType() {
	case State2:
		nextStepType = State3
	case State3:
		nextStepType = State4
	case State4:
		nextStepType = State5
	}
	return &iwfpb.InvokeExecuteMethodResponse{
		StepDecision: &iwfpb.StepDecision{
			NextSteps: []*iwfpb.StepMovement{
				{
					StepType:  nextStepType,
					StepInput: request.GetStepInput(),
				},
			},
		},
	}
}

func getState5ExecuteResponse() *iwfpb.InvokeExecuteMethodResponse {
	return &iwfpb.InvokeExecuteMethodResponse{
		StepDecision: &iwfpb.StepDecision{
			NextSteps: []*iwfpb.StepMovement{
				{StepType: service.GracefulCompletingFlowStepType},
			},
		},
	}
}

func verifyAllDataAttributes(attributes []*iwfpb.KV) error {
	expected := upsertDataAttributeKVs()
	if err := matchAttributeKVsUnordered(expected, attributes); err != nil {
		return status.Error(codes.InvalidArgument, "data attributes should be the same: "+err.Error())
	}
	return nil
}

func upsertDataAttributes() []*iwfpb.AttributeWrite {
	return []*iwfpb.AttributeWrite{
		dataObjectWrite("da_wait_until1", "test-data-attribute-wait-until"),
		dataObjectWrite("da_execute1", "test-data-attribute-execute"),
		dataObjectWrite("da_other_key", "random-value"),
	}
}

func upsertDataAttributeKVs() []*iwfpb.KV {
	writes := upsertDataAttributes()
	kvs := make([]*iwfpb.KV, len(writes))
	for index, write := range writes {
		kvs[index] = &iwfpb.KV{Key: write.GetKey(), Value: write.GetValue()}
	}
	return kvs
}

func matchAttributeKVsUnordered(expected, actual []*iwfpb.KV) error {
	if len(expected) != len(actual) {
		return status.Errorf(codes.InvalidArgument, "expected %d attributes, got %d", len(expected), len(actual))
	}
	for _, expectedAttribute := range expected {
		if !containsMatchingKV(actual, expectedAttribute) {
			return status.Errorf(codes.InvalidArgument, "missing attribute key %q", expectedAttribute.GetKey())
		}
	}
	return nil
}

func containsMatchingKV(attributes []*iwfpb.KV, expected *iwfpb.KV) bool {
	for _, attribute := range attributes {
		if attribute.GetKey() != expected.GetKey() {
			continue
		}
		expectedPayload, expectedOk := objPayloadFromValue(expected.GetValue())
		actualPayload, actualOk := objPayloadFromValue(attribute.GetValue())
		if expectedOk && actualOk &&
			expected.GetValue().GetObjValue().GetEncoding() == attribute.GetValue().GetObjValue().GetEncoding() &&
			expectedPayload == actualPayload {
			return true
		}
	}
	return false
}

func dataObjectWrite(key, payload string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key: key,
		Value: &iwfpb.Value{
			Kind: &iwfpb.Value_ObjValue{
				ObjValue: &iwfpb.EncodedObject{
					Encoding: "json",
					Payload:  []byte(payload),
				},
			},
		},
	}
}

func objPayloadFromValue(value *iwfpb.Value) (string, bool) {
	if value == nil {
		return "", false
	}
	objValue := value.GetObjValue()
	if objValue == nil {
		return "", false
	}
	return string(objValue.GetPayload()), true
}
