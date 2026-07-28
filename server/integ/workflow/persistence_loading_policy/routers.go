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

package persistence_loading_policy

import (
	"context"
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/persistence"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has two steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor skipped
 * 		- Execute method verifies the loaded attributes then moves to a dead-end.
 * Step2:
 * 		- WaitFor method verifies the loaded attributes
 * 		- Execute method verifies the loaded attributes then gracefully completes the flow
 */
const (
	WorkflowType = "persistence_loading_policy"
	State1       = "S1"
	State2       = "S2"
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
	request *iwfpb.InvokeWorkerRPCRequest,
) (*iwfpb.InvokeWorkerRPCResponse, error) {
	log.Println("persistence_loading_policy: received rpc request, ", request)

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	h.incrementInvokeHistory("rpc")

	loadingTypeInput := request.GetInput()
	if err := verifyLoadedAttributes(request.GetAttributes()); err != nil {
		return nil, err
	}

	return &iwfpb.InvokeWorkerRPCResponse{
		StepDecision: getStepDecision(State2, loadingTypeInput),
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("persistence_loading_policy: received waitFor request, ", request)

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	if request.GetStepType() == State2 {
		if err := verifyLoadedAttributes(request.GetAttributes()); err != nil {
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
	log.Println("persistence_loading_policy: received execute request, ", request)

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	loadingTypeInput := request.GetStepInput()

	if request.GetStepType() == State2 {
		if err := verifyLoadedAttributes(request.GetAttributes()); err != nil {
			return nil, err
		}
	}

	var upsertAttributes []*iwfpb.AttributeWrite
	if request.GetStepType() == State1 {
		upsertAttributes = expectedAllAttributes()
	}

	var nextStepType string
	switch request.GetStepType() {
	case State1:
		nextStepType = service.DeadEndFlowStepType
	case State2:
		nextStepType = service.GracefulCompletingFlowStepType
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid step type")
	}

	return &iwfpb.InvokeExecuteMethodResponse{
		StepDecision:     getStepDecision(nextStepType, loadingTypeInput),
		UpsertAttributes: upsertAttributes,
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

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func verifyLoadedAttributes(attributes []*iwfpb.KV) error {
	expected := expectedAllAttributeKVs()
	if err := matchAttributeKVsUnordered(expected, attributes); err != nil {
		return status.Error(codes.InvalidArgument, "attributes should be the same: "+err.Error())
	}
	return nil
}

func expectedAllAttributes() []*iwfpb.AttributeWrite {
	return []*iwfpb.AttributeWrite{
		indexedKeywordWrite(persistence.TestSearchAttributeKeywordKey, "test-search-attribute-1"),
		indexedTextWrite(persistence.TestSearchAttributeTextKey, "test-search-attribute-2"),
		dataObjectWrite("da_1", "test-data-attribute-value1"),
		dataObjectWrite("da_2", "test-data-attribute-value2"),
	}
}

func expectedAllAttributeKVs() []*iwfpb.KV {
	writes := expectedAllAttributes()
	kvs := make([]*iwfpb.KV, len(writes))
	for index, write := range writes {
		kvs[index] = &iwfpb.KV{Key: write.GetKey(), Value: write.GetValue()}
	}
	return kvs
}

func getStepDecision(nextStepType string, loadingTypeInput *iwfpb.Value) *iwfpb.StepDecision {
	return &iwfpb.StepDecision{
		NextSteps: []*iwfpb.StepMovement{
			{
				StepType: nextStepType,
				StepOptions: &iwfpb.StepOptions{
					WaitForLockAttributeKeys: []string{
						persistence.TestSearchAttributeTextKey,
						"da_2",
					},
					ExecuteLockAttributeKeys: []string{
						persistence.TestSearchAttributeTextKey,
						"da_2",
					},
				},
				StepInput: loadingTypeInput,
			},
		},
	}
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
		if valuesEqual(attribute.GetValue(), expected.GetValue()) {
			return true
		}
	}
	return false
}

func valuesEqual(left, right *iwfpb.Value) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftPayload, leftOk := objPayloadFromValue(left)
	rightPayload, rightOk := objPayloadFromValue(right)
	if leftOk && rightOk {
		return left.GetObjValue().GetEncoding() == right.GetObjValue().GetEncoding() &&
			leftPayload == rightPayload
	}
	return left.GetStringValue() == right.GetStringValue() &&
		left.GetIntValue() == right.GetIntValue() &&
		left.GetBoolValue() == right.GetBoolValue()
}

func jsonObjValue(payload string) *iwfpb.Value {
	return &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: "json",
				Payload:  []byte(payload),
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

func indexedKeywordWrite(key, value string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key: key,
		Value: &iwfpb.Value{
			Kind: &iwfpb.Value_StringValue{StringValue: value},
		},
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	}
}

func indexedTextWrite(key, value string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key: key,
		Value: &iwfpb.Value{
			Kind: &iwfpb.Value_StringValue{StringValue: value},
		},
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_TEXT,
		},
	}
}

func dataObjectWrite(key, payload string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(payload),
	}
}
