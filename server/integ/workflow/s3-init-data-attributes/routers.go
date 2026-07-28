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

package s3_init_data_attributes

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/integ/workflow/common"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, testing S3 data attribute loading functionality.
 *
 * Step1:
 *		- WaitFor loads and validates data attributes from S3
 *      - Execute transitions to Step2
 *
 * Step2:
 *		- WaitFor does nothing
 *      - Execute loads and validates data attributes from S3, then completes flow
 */
const (
	WorkflowType      = "s3-init-data-attributes"
	State1            = "S1"
	State2            = "S2"
	TestDataAttrKey1  = "test-da-key1"
	TestDataAttrKey2  = "test-da-key2"
	TestDataAttrKey3  = "test-da-key3"
	LargeDataContent1 = "this_is_a_large_data_content_that_should_be_stored_in_s3_for_testing_purposes_with_more_than_10_characters"
	LargeDataContent2 = "another_large_data_content_for_second_attribute_that_exceeds_the_s3_threshold_for_external_storage_testing"
	SmallDataContent3 = "small"
)

var TestDataAttributeVal1 = jsonObjValue("\"" + LargeDataContent1 + "\"")

var TestDataAttributeVal2 = jsonObjValue("\"" + LargeDataContent2 + "\"")

var TestDataAttributeVal3 = jsonObjValue("\"" + SmallDataContent3 + "\"")

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
	flowClient    iwfpb.FlowServiceClient
	invokeHistory sync.Map
	invokeData    sync.Map
}

func NewHandler(flowClient iwfpb.FlowServiceClient) *handler {
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
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

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
	h.incrementInvokeHistory(stepType + "_waitFor")

	if stepType == State1 {
		h.invokeHistory.Store(stepType+"_waitFor_input", request.GetStepInput())
		if err := h.validateInitialAttributes(ctx, request.GetAttributes(), "S1 WaitUntil", "S1_waitFor"); err != nil {
			return nil, err
		}
		return &iwfpb.InvokeWaitForMethodResponse{}, nil
	}

	if stepType == State2 {
		return &iwfpb.InvokeWaitForMethodResponse{}, nil
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) InvokeExecuteMethod(
	ctx context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

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

	if stepType == State1 {
		h.invokeHistory.Store(stepType+"_execute_input", request.GetStepInput())
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{
						StepType:  State2,
						StepInput: request.GetStepInput(),
					},
				},
			},
		}, nil
	}

	if stepType == State2 {
		h.invokeHistory.Store(stepType+"_execute_input", request.GetStepInput())
		if err := h.validateInitialAttributes(ctx, request.GetAttributes(), "S2 Execute", "S2_execute"); err != nil {
			return nil, err
		}
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{
						StepType:  service.GracefulCompletingFlowStepType,
						StepInput: request.GetStepInput(),
					},
				},
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

func (h *handler) validateInitialAttributes(
	ctx context.Context, attributes []*iwfpb.KV, logPrefix, storePrefix string,
) error {
	log.Printf("%s: Received %d data attributes, validating they match initial values", logPrefix, len(attributes))

	foundAttr1 := false
	foundAttr2 := false
	foundAttr3 := false
	validationErrors := []string{}

	for _, attribute := range attributes {
		receivedData, err := common.ObjPayloadString(ctx, h.flowClient, attribute.GetValue())
		if err != nil {
			return status.Errorf(codes.Internal, "LoadBlobs for %s: %v", attribute.GetKey(), err)
		}

		switch attribute.GetKey() {
		case TestDataAttrKey1:
			expectedData := string(TestDataAttributeVal1.GetObjValue().GetPayload())
			if receivedData == expectedData {
				foundAttr1 = true
				h.invokeData.Store(storePrefix+"_attr1_data", LargeDataContent1)
				log.Printf("%s: ✅ %s value matches initial data (length: %d)", logPrefix, TestDataAttrKey1, len(receivedData))
			} else {
				validationErrors = append(validationErrors, "attr1 mismatch")
				log.Printf("%s: ❌ %s value mismatch - expected: %s, received: %s", logPrefix, TestDataAttrKey1, expectedData, receivedData)
			}
		case TestDataAttrKey2:
			expectedData := string(TestDataAttributeVal2.GetObjValue().GetPayload())
			if receivedData == expectedData {
				foundAttr2 = true
				h.invokeData.Store(storePrefix+"_attr2_data", LargeDataContent2)
				log.Printf("%s: ✅ %s value matches initial data (length: %d)", logPrefix, TestDataAttrKey2, len(receivedData))
			} else {
				validationErrors = append(validationErrors, "attr2 mismatch")
				log.Printf("%s: ❌ %s value mismatch - expected: %s, received: %s", logPrefix, TestDataAttrKey2, expectedData, receivedData)
			}
		case TestDataAttrKey3:
			expectedData := string(TestDataAttributeVal3.GetObjValue().GetPayload())
			if receivedData == expectedData {
				foundAttr3 = true
				h.invokeData.Store(storePrefix+"_attr3_data", SmallDataContent3)
				log.Printf("%s: ✅ %s value matches initial data (length: %d)", logPrefix, TestDataAttrKey3, len(receivedData))
			} else {
				validationErrors = append(validationErrors, "attr3 mismatch")
				log.Printf("%s: ❌ %s value mismatch - expected: %s, received: %s", logPrefix, TestDataAttrKey3, expectedData, receivedData)
			}
		}
	}

	allValidationsPass := foundAttr1 && foundAttr2 && foundAttr3 && len(validationErrors) == 0
	log.Printf("%s: Data attribute validation complete - all match initial values: %t", logPrefix, allValidationsPass)

	h.invokeData.Store(storePrefix+"_attr1_found", foundAttr1)
	h.invokeData.Store(storePrefix+"_attr2_found", foundAttr2)
	h.invokeData.Store(storePrefix+"_attr3_found", foundAttr3)
	h.invokeData.Store(storePrefix+"_total_attrs", len(attributes))
	h.invokeData.Store(storePrefix+"_validation_pass", allValidationsPass)
	return nil
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
