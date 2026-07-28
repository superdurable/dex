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

package locking

import (
	"context"
	"fmt"
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has three steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor method does nothing
 * 		- Execute method will move to Step Waiting, and 10 instances of Step 2
 * Step2:
 * 		- WaitFor update indexed attributes
 * 		- Execute method will update data attributes and will gracefully complete flow
 * StateWaiting:
 * 		- WaitFor will proceed once the internal channel has been published to
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType                  = "locking"
	State1                        = "S1"
	State2                        = "S2"
	StateWaiting                  = "StateWaiting"
	TestDataAttributeKey1         = "test-data-attribute-1"
	TestDataAttributeKey2         = "test-data-attribute-2"
	RPCName                       = "increase-counter"
	InternalChannelName           = "test-channel"
	TestSearchAttributeKeywordKey = "CustomKeywordField"
	TestSearchAttributeIntKey     = "CustomIntField"

	ShouldUnblockStateWaiting = "shouldUnblockStateWaiting"

	InParallelS2 = 10

	NumUnusedSignals = 4

	UnusedSignalChannelName   = "test-unused-signal-channel"
	UnusedInternalChannelName = "test-unused-internal-channel"
)

var testValue = jsonObjValue("data")

var state2StepOptions = &iwfpb.StepOptions{
	WaitForLockAttributeKeys: []string{
		TestSearchAttributeIntKey,
		TestDataAttributeKey1,
	},
	ExecuteLockAttributeKeys: []string{
		TestSearchAttributeIntKey,
		TestDataAttributeKey1,
	},
}

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	rpcInvokes    int32
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
	log.Println("received worker rpc request, ", request)

	if request.GetFlowType() != WorkflowType || request.GetRpcName() != RPCName {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}

	inputObj := request.GetInput().GetObjValue()
	if inputObj == nil || inputObj.GetEncoding() != "json" {
		return nil, status.Error(codes.InvalidArgument, "input is incorrect")
	}
	inputPayload := string(inputObj.GetPayload())

	if inputPayload == ShouldUnblockStateWaiting {
		return &iwfpb.InvokeWorkerRPCResponse{
			PublishToChannel: []*iwfpb.ChannelMessage{
				{
					ChannelName: InternalChannelName,
					Value:       testValue,
				},
			},
		}, nil
	}

	signalChannelInfo := request.GetChannelInfos()[UnusedSignalChannelName]
	if signalChannelInfo.GetSize() != NumUnusedSignals {
		return nil, status.Error(codes.InvalidArgument, "incorrect signal channel size")
	}

	if h.rpcInvokes > 0 {
		internalChannelInfo := request.GetChannelInfos()[UnusedInternalChannelName]
		if h.rpcInvokes != internalChannelInfo.GetSize() {
			return nil, status.Error(codes.InvalidArgument, "incorrect internal channel size")
		}
	}
	h.rpcInvokes++

	time.Sleep(time.Millisecond)

	saInt := int64(0)
	for _, attribute := range request.GetAttributes() {
		if attribute.GetKey() == TestSearchAttributeIntKey {
			saInt = attribute.GetValue().GetIntValue()
		}
	}
	saInt++

	stepContext := request.GetContext()
	upsertAttributes := []*iwfpb.AttributeWrite{
		indexedKeywordWrite(TestSearchAttributeKeywordKey, stepContext.GetStepExecutionId()),
		indexedIntWrite(TestSearchAttributeIntKey, saInt),
	}

	daInt := 0
	for _, attribute := range request.GetAttributes() {
		if attribute.GetKey() == TestDataAttributeKey1 {
			payload, hasPayload := objPayloadFromValue(attribute.GetValue())
			if hasPayload && payload != "" {
				parsed, err := strconv.ParseInt(payload, 10, 32)
				if err != nil {
					return nil, status.Error(codes.InvalidArgument, err.Error())
				}
				daInt = int(parsed)
			}
		}
	}
	daInt++

	upsertAttributes = append(upsertAttributes,
		dataObjectWrite(TestDataAttributeKey1, fmt.Sprintf("%v", daInt)),
		dataObjectWrite(TestDataAttributeKey2, stepContext.GetStepExecutionId()),
	)

	return &iwfpb.InvokeWorkerRPCResponse{
		Output: testValue,
		StepDecision: &iwfpb.StepDecision{
			NextSteps: []*iwfpb.StepMovement{
				{
					StepType:    State2,
					StepOptions: state2StepOptions,
				},
			},
		},
		UpsertAttributes: upsertAttributes,
		RecordEvents: []*iwfpb.KV{
			{Key: "test-key", Value: testValue},
		},
		PublishToChannel: []*iwfpb.ChannelMessage{
			{
				ChannelName: UnusedInternalChannelName,
				Value:       testValue,
			},
		},
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_waitFor")

	switch request.GetStepType() {
	case State1:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
		}, nil
	case StateWaiting:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ChannelName: InternalChannelName},
				},
			},
		}, nil
	case State2:
		time.Sleep(time.Second)
		saInt := int64(0)
		for _, attribute := range request.GetAttributes() {
			if attribute.GetKey() == TestSearchAttributeIntKey {
				saInt = attribute.GetValue().GetIntValue()
			}
		}
		saInt++

		stepContext := request.GetContext()
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			},
			UpsertAttributes: []*iwfpb.AttributeWrite{
				indexedKeywordWrite(TestSearchAttributeKeywordKey, stepContext.GetStepExecutionId()),
				indexedIntWrite(TestSearchAttributeIntKey, saInt),
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *iwfpb.InvokeExecuteMethodRequest,
) (*iwfpb.InvokeExecuteMethodResponse, error) {
	log.Println("received execute request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
	}

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	h.incrementInvokeHistory(request.GetStepType() + "_execute")

	switch request.GetStepType() {
	case State1:
		nextSteps := []*iwfpb.StepMovement{
			{StepType: StateWaiting},
		}
		for i := 0; i < InParallelS2; i++ {
			nextSteps = append(nextSteps, &iwfpb.StepMovement{
				StepType:    State2,
				StepOptions: state2StepOptions,
			})
		}
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{NextSteps: nextSteps},
		}, nil
	case StateWaiting:
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
				},
			},
		}, nil
	case State2:
		time.Sleep(time.Second)
		daInt := 0
		for _, attribute := range request.GetAttributes() {
			if attribute.GetKey() == TestDataAttributeKey1 {
				payload, hasPayload := objPayloadFromValue(attribute.GetValue())
				if hasPayload && payload != "" {
					parsed, err := strconv.ParseInt(payload, 10, 32)
					if err != nil {
						return nil, status.Error(codes.InvalidArgument, err.Error())
					}
					daInt = int(parsed)
				}
			}
		}
		daInt++

		stepContext := request.GetContext()
		return &iwfpb.InvokeExecuteMethodResponse{
			UpsertAttributes: []*iwfpb.AttributeWrite{
				dataObjectWrite(TestDataAttributeKey1, fmt.Sprintf("%v", daInt)),
				dataObjectWrite(TestDataAttributeKey2, stepContext.GetStepExecutionId()),
			},
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
				},
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
	return common.TestResult{InvokeHistory: invokeHistory}
}

func (h *handler) incrementInvokeHistory(key string) {
	if value, ok := h.invokeHistory.Load(key); ok {
		h.invokeHistory.Store(key, value.(int64)+1)
		return
	}
	h.invokeHistory.Store(key, int64(1))
}

func validateStepContext(stepContext *iwfpb.Context) error {
	if stepContext.GetAttempt() <= 0 || stepContext.GetFirstAttemptTimestamp() <= 0 {
		return status.Error(
			codes.InvalidArgument,
			"attempt and firstAttemptTimestamp should be greater than zero",
		)
	}
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

func indexedIntWrite(key string, value int64) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key: key,
		Value: &iwfpb.Value{
			Kind: &iwfpb.Value_IntValue{IntValue: value},
		},
		IndexConfig: &iwfpb.IndexConfig{
			Enable: true,
			Type:   iwfpb.IndexType_INDEX_TYPE_INT,
		},
	}
}

func dataObjectWrite(key, payload string) *iwfpb.AttributeWrite {
	return &iwfpb.AttributeWrite{
		Key:   key,
		Value: jsonObjValue(payload),
	}
}
