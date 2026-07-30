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

package persistence

import (
	"context"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has three steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method will update attributes and step exe locals
 * 		- Execute method will move to Step2
 * Step2:
 * 		- WaitFor method will store attribute data
 * 		- Execute method will move to Step3
 * Step3:
 * 		- WaitFor method performs some attribute checks
 * 		- Execute method performs checks on the attribute data and then gracefully completes the flow
 */
const (
	WorkflowType          = "persistence"
	State1                = "S1"
	State2                = "S2"
	State3                = "S3"
	TestDataAttributeKey  = "test-data-attribute"
	TestDataAttributeKey2 = "test-data-attribute-2"
	TestStateLocalKey     = "test-state-local"

	TestSearchAttributeKeywordKey    = "CustomKeywordField"
	TestSearchAttributeKeywordValue1 = "keyword-value1"
	TestSearchAttributeKeywordValue2 = "keyword-value2"

	TestSearchAttributeKeywordArrayKey = "CustomKeywordArrayField"
	TestSearchAttributeIntKey          = "CustomIntField"
	TestSearchAttributeBoolKey         = "CustomBoolField"
	TestSearchAttributeDoubleKey       = "CustomDoubleField"
	TestSearchAttributeDatetimeKey     = "CustomDatetimeField"
	TestSearchAttributeTextKey         = "CustomStringField"
	TestSearchAttributeIntValue1       = 1
	TestSearchAttributeIntValue2       = 2
)

var testDataAttributeVal1Payload = "test-data-attribute-value1"
var testDataAttributeVal2Payload = "test-data-attribute-value2"
var testStateLocalValPayload = "test-state-local-value"

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
		invokeData:    sync.Map{},
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	initAttributes := request.GetAttributes()
	if len(initAttributes) < 1 {
		return nil, status.Error(codes.InvalidArgument, "should have at least one init attribute")
	}
	for _, attribute := range initAttributes {
		if attribute.GetKey() == TestSearchAttributeDatetimeKey {
			if attribute.GetValue().GetStringValue() == "" {
				return nil, status.Error(codes.InvalidArgument, "key and value type not match")
			}
		}
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	switch request.GetStepType() {
	case State1:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				indexedKeywordWrite(TestSearchAttributeKeywordKey, TestSearchAttributeKeywordValue1),
				indexedIntWrite(TestSearchAttributeIntKey, TestSearchAttributeIntValue1),
				indexedBoolWrite(TestSearchAttributeBoolKey, false),
				dataObjectWrite(TestDataAttributeKey, testDataAttributeVal1Payload),
				dataObjectWrite(TestDataAttributeKey2, testDataAttributeVal1Payload),
			},
			UpsertStepExeLocals: []*dexpb.KV{
				{Key: TestStateLocalKey, Value: jsonObjValue(testStateLocalValPayload)},
			},
		}, nil
	case State2:
		h.storeKeywordIntCounts("S2_start", request.GetAttributes())
		queryAttFound := attributePayloadMatches(
			request.GetAttributes(),
			TestDataAttributeKey,
			testDataAttributeVal2Payload,
		)
		h.invokeData.Store("S2_start_queryAttFound", queryAttFound)

		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	case State3:
		foundInt := attributeIntMatches(
			request.GetAttributes(),
			TestSearchAttributeIntKey,
			TestSearchAttributeIntValue2,
		)
		if !foundInt {
			return nil, status.Error(codes.InvalidArgument, "should see the requested attribute key")
		}

		if countAttributesWithKeys(request.GetAttributes(), TestDataAttributeKey) != 1 {
			return nil, status.Error(codes.InvalidArgument, "missing query attribute key")
		}
		if countAttributesWithKeys(request.GetAttributes(), TestDataAttributeKey2) != 0 {
			return nil, status.Error(codes.InvalidArgument, "deleted query attribute key is still present")
		}

		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	switch request.GetStepType() {
	case State1:
		h.storeKeywordIntCounts("S1_decide", request.GetAttributes())

		queryAttFound := 0
		for _, attribute := range request.GetAttributes() {
			if attribute.GetKey() == TestDataAttributeKey &&
				attributeValuePayloadMatches(attribute.GetValue(), testDataAttributeVal1Payload) {
				queryAttFound++
			}
			if attribute.GetKey() == TestDataAttributeKey2 &&
				attributeValuePayloadMatches(attribute.GetValue(), testDataAttributeVal1Payload) {
				queryAttFound++
			}
		}
		h.invokeData.Store("S1_decide_queryAttFound", queryAttFound)

		localAttFound := false
		stepExeLocals := request.GetStepExeLocals()
		if len(stepExeLocals) > 0 {
			localAtt := stepExeLocals[0]
			localAttFound = localAtt.GetKey() == TestStateLocalKey &&
				attributeValuePayloadMatches(localAtt.GetValue(), testStateLocalValPayload)
		}
		h.invokeData.Store("S1_decide_localAttFound", localAttFound)

		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State2},
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				indexedKeywordWrite(TestSearchAttributeKeywordKey, TestSearchAttributeKeywordValue2),
				indexedIntWrite(TestSearchAttributeIntKey, TestSearchAttributeIntValue2),
				dataObjectWrite(TestDataAttributeKey, testDataAttributeVal2Payload),
			},
		}, nil
	case State2:
		h.storeKeywordIntCounts("S2_decide", request.GetAttributes())
		queryAttFound := attributePayloadMatches(
			request.GetAttributes(),
			TestDataAttributeKey,
			testDataAttributeVal2Payload,
		)
		h.invokeData.Store("S2_decide_queryAttFound", queryAttFound)

		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State3},
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				deleteAttribute(TestDataAttributeKey2),
			},
		}, nil
	case State3:
		foundInt := attributeIntMatches(
			request.GetAttributes(),
			TestSearchAttributeIntKey,
			TestSearchAttributeIntValue2,
		)
		if !foundInt {
			return nil, status.Error(codes.InvalidArgument, "should see the requested attribute key")
		}

		if countAttributesWithKeys(request.GetAttributes(), TestDataAttributeKey) != 1 {
			return nil, status.Error(codes.InvalidArgument, "missing query attribute key")
		}
		if countAttributesWithKeys(request.GetAttributes(), TestDataAttributeKey2) != 0 {
			return nil, status.Error(codes.InvalidArgument, "deleted query attribute key is still present")
		}

		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: common.GracefulCompleteDecision(nil),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	invokeData := make(map[string]interface{})
	h.invokeData.Range(func(key, value interface{}) bool {
		invokeData[key.(string)] = value
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory, InvokeData: invokeData}
}

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func (h *handler) storeKeywordIntCounts(prefix string, attributes []*dexpb.KV) {
	kwSaFounds := 0
	intSaFounds := 0
	for _, attribute := range attributes {
		if attribute.GetKey() == TestSearchAttributeKeywordKey {
			if attribute.GetValue().GetStringValue() == TestSearchAttributeKeywordValue1 ||
				attribute.GetValue().GetStringValue() == TestSearchAttributeKeywordValue2 {
				kwSaFounds++
			}
		}
		if attribute.GetKey() == TestSearchAttributeIntKey {
			if attribute.GetValue().GetIntValue() == TestSearchAttributeIntValue1 ||
				attribute.GetValue().GetIntValue() == TestSearchAttributeIntValue2 {
				intSaFounds++
			}
		}
	}
	h.invokeData.Store(prefix+"_kwSaFounds", kwSaFounds)
	h.invokeData.Store(prefix+"_intSaFounds", intSaFounds)
}

func validateStepContext(stepContext *dexpb.Context) error {
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}
	return nil
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

func attributeValuePayloadMatches(value *dexpb.Value, expectedPayload string) bool {
	payload, ok := objPayloadFromValue(value)
	return ok && payload == expectedPayload
}

func attributePayloadMatches(attributes []*dexpb.KV, key, expectedPayload string) bool {
	for _, attribute := range attributes {
		if attribute.GetKey() == key {
			return attributeValuePayloadMatches(attribute.GetValue(), expectedPayload)
		}
	}
	return false
}

func attributeIntMatches(attributes []*dexpb.KV, key string, expected int64) bool {
	for _, attribute := range attributes {
		if attribute.GetKey() == key && attribute.GetValue().GetIntValue() == expected {
			return true
		}
	}
	return false
}

func countAttributesWithKeys(attributes []*dexpb.KV, keys ...string) int {
	found := 0
	for _, attribute := range attributes {
		for _, key := range keys {
			if attribute.GetKey() == key {
				found++
			}
		}
	}
	return found
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

func indexedKeywordWrite(key, value string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_StringValue{StringValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_KEYWORD,
		},
	}
}

func indexedIntWrite(key string, value int64) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_IntValue{IntValue: value},
		},
		IndexConfig: &dexpb.IndexConfig{
			Enable: true,
			Type:   dexpb.IndexType_INDEX_TYPE_INT,
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

func dataObjectWrite(key, payload string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(payload),
	}
}

func deleteAttribute(key string) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key: key,
		Value: &dexpb.Value{
			Kind: &dexpb.Value_NullValue{},
		},
	}
}
