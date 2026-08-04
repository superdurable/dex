// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package wf_state_options_data_attributes_loading

import (
	"context"
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

	common.LogRequest("state_options_data_attributes_loading: received waitFor request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	if request.GetStepType() != State1 {
		if err := verifyAllDataAttributes(request.GetAttributes()); err != nil {
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

	common.LogRequest("state_options_data_attributes_loading: received execute request, ", request)

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	var response *dexpb.InvokeExecuteMethodResponse
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
		UpsertAttributes: upsertDataAttributes(),
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

func verifyAllDataAttributes(attributes []*dexpb.KV) error {
	expected := upsertDataAttributeKVs()
	if err := matchAttributeKVsUnordered(expected, attributes); err != nil {
		return status.Error(codes.InvalidArgument, "data attributes should be the same: "+err.Error())
	}
	return nil
}

func upsertDataAttributes() []*dexpb.AttributeWrite {
	return []*dexpb.AttributeWrite{
		dataObjectWrite("da_wait_until1", "test-data-attribute-wait-until"),
		dataObjectWrite("da_execute1", "test-data-attribute-execute"),
		dataObjectWrite("da_other_key", "random-value"),
	}
}

func upsertDataAttributeKVs() []*dexpb.KV {
	writes := upsertDataAttributes()
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

func dataObjectWrite(key, payload string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_ObjValue{
				ObjValue: &dexpb.EncodedObject{
					Encoding: "json",
					Payload:  []byte(payload),
				},
			},
		},
	}
}

func objPayloadFromValue(value *dexpb.Value) (string, bool) {
	if value == nil {
		return "", false
	}
	objValue := value.GetObjValue()
	if objValue == nil {
		return "", false
	}
	return string(objValue.GetPayload()), true
}
