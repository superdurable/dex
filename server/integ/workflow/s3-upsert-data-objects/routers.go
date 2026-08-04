// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package s3_upsert_data_objects

import (
	"context"
	"log"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 2 steps, testing S3 upsert data objects functionality.
 *
 * Step1:
 *		- WaitFor does nothing
 *      - Execute upserts large data objects that should go to S3, then transitions to Step2
 *
 * Step2:
 *		- WaitFor validates it receives the upserted data objects from S3
 *      - Execute completes flow
 */
const (
	WorkflowType      = "s3-upsert-data-objects"
	State1            = "S1"
	State2            = "S2"
	TestDataObjKey1   = "large_obj1"
	TestDataObjKey2   = "large_obj2"
	TestDataObjKey3   = "small_obj3"
	LargeDataContent1 = "this_is_a_large_data_content_that_should_be_stored_in_s3_for_upsert_testing_purposes_with_more_than_10_characters"
	LargeDataContent2 = "another_large_data_content_for_second_upserted_object_that_exceeds_the_s3_threshold_for_external_storage_testing"
	SmallDataContent3 = "small"
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
	h.incrementInvokeHistory(stepType + "_waitFor")

	if stepType == State1 {
		return &dexpb.InvokeWaitForMethodResponse{}, nil
	}

	if stepType == State2 {
		if err := h.validateUpsertedAttributes(ctx, request.GetAttributes()); err != nil {
			return nil, err
		}
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

	stepType := request.GetStepType()
	h.incrementInvokeHistory(stepType + "_execute")

	if stepType == State1 {
		log.Printf("S1 Execute: Upserting data objects - 2 large (should go to S3), 1 small (should stay in memory)")
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{
						StepType:  State2,
						StepInput: request.GetStepInput(),
					},
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				{
					Key:   TestDataObjKey1,
					Value: jsonObjValue("\"" + LargeDataContent1 + "\""),
				},
				{
					Key:   TestDataObjKey2,
					Value: jsonObjValue("\"" + LargeDataContent2 + "\""),
				},
				{
					Key:   TestDataObjKey3,
					Value: jsonObjValue("\"" + SmallDataContent3 + "\""),
				},
			},
		}, nil
	}

	if stepType == State2 {
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

func (h *handler) validateUpsertedAttributes(ctx context.Context, attributes []*dexpb.KV) error {
	log.Printf("S2 WaitUntil: Received %d data objects, validating they match upserted values", len(attributes))

	foundLargeObj1 := false
	foundLargeObj2 := false
	foundSmallObj3 := false

	for _, attribute := range attributes {
		receivedData, err := common.ObjPayloadString(ctx, h.flowClient, attribute.GetValue())
		if err != nil {
			return status.Errorf(codes.Internal, "LoadBlobs for %s: %v", attribute.GetKey(), err)
		}

		switch attribute.GetKey() {
		case TestDataObjKey1:
			expectedData := "\"" + LargeDataContent1 + "\""
			if receivedData == expectedData {
				foundLargeObj1 = true
				h.invokeData.Store("S2_large_obj1_data", LargeDataContent1)
				log.Printf("S2 WaitUntil: ✅ %s value matches upserted data (length: %d)", TestDataObjKey1, len(receivedData))
			} else {
				log.Printf(
					"S2 WaitUntil: ❌ %s value mismatch - expected: %s, received: %s",
					TestDataObjKey1,
					common.FormatForLogging(expectedData),
					common.FormatForLogging(receivedData),
				)
			}
		case TestDataObjKey2:
			expectedData := "\"" + LargeDataContent2 + "\""
			if receivedData == expectedData {
				foundLargeObj2 = true
				h.invokeData.Store("S2_large_obj2_data", LargeDataContent2)
				log.Printf("S2 WaitUntil: ✅ %s value matches upserted data (length: %d)", TestDataObjKey2, len(receivedData))
			} else {
				log.Printf(
					"S2 WaitUntil: ❌ %s value mismatch - expected: %s, received: %s",
					TestDataObjKey2,
					common.FormatForLogging(expectedData),
					common.FormatForLogging(receivedData),
				)
			}
		case TestDataObjKey3:
			expectedData := "\"" + SmallDataContent3 + "\""
			if receivedData == expectedData {
				foundSmallObj3 = true
				h.invokeData.Store("S2_small_obj3_data", SmallDataContent3)
				log.Printf("S2 WaitUntil: ✅ %s value matches upserted data (length: %d)", TestDataObjKey3, len(receivedData))
			} else {
				log.Printf(
					"S2 WaitUntil: ❌ %s value mismatch - expected: %s, received: %s",
					TestDataObjKey3,
					common.FormatForLogging(expectedData),
					common.FormatForLogging(receivedData),
				)
			}
		}
	}

	h.invokeData.Store("S2_received_large_obj1", foundLargeObj1)
	h.invokeData.Store("S2_received_large_obj2", foundLargeObj2)
	h.invokeData.Store("S2_received_small_obj3", foundSmallObj3)

	log.Printf("S2 WaitUntil: Data object validation complete - found large_obj1: %t, large_obj2: %t, small_obj3: %t",
		foundLargeObj1, foundLargeObj2, foundSmallObj3)
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
