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

package wf_state_options_search_attributes_loading

import (
	"context"
	"encoding/json"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has five steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 * 		- Execute method sets values for all indexed attributes used in this test
 * Step2 through Step5:
 *		- WaitFor and Execute verify all indexed attributes are loaded
 */
const (
	WorkflowType = "state_options_search_attributes_loading"
	State1       = "S1"
	State2       = "S2"
	State3       = "S3"
	State4       = "S4"
	State5       = "S5"
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

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	_ *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "unexpected worker rpc")
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	common.LogRequest("state_options_search_attributes_loading: received waitFor request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	if request.GetStepType() != State1 {
		if err := verifyAllSearchAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
	}

	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		},
	}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	common.LogRequest("state_options_search_attributes_loading: received execute request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	var response *dexpb.InvokeExecuteMethodResponse
	switch request.GetStepType() {
	case State1:
		response = getState1ExecuteResponse(request)
	case State2, State3, State4:
		if err := verifyAllSearchAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
		response = getNextStateExecuteResponse(request)
	case State5:
		if err := verifyAllSearchAttributes(request.GetAttributes()); err != nil {
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

func getState1ExecuteResponse(request *dexpb.InvokeExecuteMethodRequest) *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:  State2,
					StepInput: request.GetStepInput(),
				},
			},
		},
		UpsertAttributes: upsertSearchAttributes(),
	}
}

func getNextStateExecuteResponse(request *dexpb.InvokeExecuteMethodRequest) *dexpb.InvokeExecuteMethodResponse {
	var nextStepType string
	switch request.GetStepType() {
	case State2:
		nextStepType = State3
	case State3:
		nextStepType = State4
	case State4:
		nextStepType = State5
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{
					StepType:  nextStepType,
					StepInput: request.GetStepInput(),
				},
			},
		},
	}
}

func getState5ExecuteResponse() *dexpb.InvokeExecuteMethodResponse {
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.GracefulCompleteDecision(nil),
		},
	}
}

func verifyAllSearchAttributes(attributes []*dexpb.KV) error {
	expected := upsertSearchAttributeKVs()
	if err := matchAttributeKVsUnordered(expected, attributes); err != nil {
		return status.Error(codes.InvalidArgument, "search attributes should be the same: "+err.Error())
	}
	return nil
}

func upsertSearchAttributes() []*dexpb.AttributeWrite {
	keywordArrayPayload, _ := json.Marshal([]string{"keyword1", "keyword2"})
	return []*dexpb.AttributeWrite{
		{
			Key: "CustomKeywordField",
			Value: &dexpb.Value{
				Kind: &dexpb.Value_ObjValue{
					ObjValue: &dexpb.EncodedObject{
						Encoding: "json",
						Payload:  keywordArrayPayload,
					},
				},
			},
			IndexConfig: &dexpb.IndexConfig{
				Enable: true,
				Type:   dexpb.IndexType_INDEX_TYPE_KEYWORD_ARRAY,
			},
		},
		indexedTextWrite("CustomStringField", "I am a string"),
		indexedBoolWrite("CustomBoolField", true),
	}
}

func upsertSearchAttributeKVs() []*dexpb.KV {
	writes := upsertSearchAttributes()
	kvs := make([]*dexpb.KV, len(writes))
	for index, write := range writes {
		kvs[index] = &dexpb.KV{Key: write.GetKey(), Value: write.GetValue()}
	}
	return kvs
}

func matchAttributeKVsUnordered(expected, actual []*dexpb.KV) error {
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

func containsMatchingKV(attributes []*dexpb.KV, expected *dexpb.KV) bool {
	for _, attribute := range attributes {
		if attribute.GetKey() != expected.GetKey() {
			continue
		}
		if valuesEqual(attribute.GetValue(), expected.GetValue()) {
			return true
		}
	}
	return false
}

func valuesEqual(left, right *dexpb.Value) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftPayload, leftOk := objPayloadFromValue(left)
	rightPayload, rightOk := objPayloadFromValue(right)
	if leftOk && rightOk {
		return left.GetObjValue().GetEncoding() == right.GetObjValue().GetEncoding() &&
			string(leftPayload) == string(rightPayload)
	}
	return left.GetStringValue() == right.GetStringValue() &&
		left.GetIntValue() == right.GetIntValue() &&
		left.GetBoolValue() == right.GetBoolValue()
}

func objPayloadFromValue(value *dexpb.Value) ([]byte, bool) {
	if value == nil {
		return nil, false
	}
	objValue := value.GetObjValue()
	if objValue == nil {
		return nil, false
	}
	return objValue.GetPayload(), true
}

func indexedTextWrite(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_TEXT,
		},
	}
}

func indexedBoolWrite(key string, value bool) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_BoolValue{BoolValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_BOOL,
		},
	}
}
