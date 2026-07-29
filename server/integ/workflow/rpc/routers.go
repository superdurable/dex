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

package rpc

import (
	"context"
	"fmt"
	"github.com/superdurable/dex/integ/workflow/common"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has two steps, using WorkerServiceServer to implement the flow directly.
 *
 * Step1:
 *		- WaitFor updates attribute data and then waits until the channel has been published to
 * 		- Execute method moves to Step2
 * Step2:
 *		- WaitFor method does nothing
 *      - Execute method will gracefully complete flow
 */
const (
	WorkflowType              = "rpc"
	State1                    = "S1"
	State2                    = "S2"
	TestInterStateChannelName = "test-TestInterStateChannelName"
	RPCName                   = "test-RPCName"
	RPCNameReadOnly           = "test-RPC-readonly"
	RPCNameError              = "test-RPC-error"

	TestDataAttributeKey = "test-data-attribute"

	TestSearchAttributeKeywordKey    = "CustomKeywordField"
	TestSearchAttributeKeywordValue1 = "keyword-value1"
	TestSearchAttributeKeywordValue2 = "keyword-value2"

	TestSearchAttributeIntKey    = "CustomIntField"
	TestSearchAttributeBoolKey   = "CustomBoolField"
	TestSearchAttributeIntValue1 = 1
	TestSearchAttributeIntValue2 = 2

	WorkerApiErrorDetails = "test-details"
	WorkerApiErrorType    = "test-type"
)

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

var TestDataAttributeVal1 = jsonObjValue("test-data-attribute-value1")

var TestDataAttributeVal2 = jsonObjValue("test-data-attribute-value2")

var TestInput = jsonObjValue("test-input-value")

var TestOutput = jsonObjValue("test-output-value")

var TestRecordEvent = jsonObjValue("test-record-event-value")

var TestInterstateChannelValue = jsonObjValue("test-interstatechannel-value")

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	common.LogRequest("received worker rpc request, ", request)

	flowContext := request.GetContext()
	if flowContext.GetFlowId() == "" || flowContext.GetRunId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid context in the request")
	}
	if request.GetFlowType() != WorkflowType ||
		(request.GetRpcName() != RPCName &&
			request.GetRpcName() != RPCNameReadOnly &&
			request.GetRpcName() != RPCNameError) {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}

	h.invokeData.Store(request.GetRpcName()+"-input", request.GetInput())
	h.invokeData.Store(request.GetRpcName()+"-attributes", request.GetAttributes())

	if request.GetRpcName() == RPCNameReadOnly {
		return &dexpb.InvokeWorkerRPCResponse{
			Output: TestOutput,
		}, nil
	}
	if request.GetRpcName() == RPCNameError {
		workerErr := &dexpb.WorkerErrorResponse{
			Detail:    WorkerApiErrorDetails,
			ErrorType: WorkerApiErrorType,
		}
		st := status.New(codes.Unavailable, "worker RPC failed")
		st, err := st.WithDetails(workerErr)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		return nil, st.Err()
	}

	return &dexpb.InvokeWorkerRPCResponse{
		Output: TestOutput,
		StepDecision: &dexpb.StepDecision{
			NextSteps: []*dexpb.StepMovement{
				{StepType: State2},
			},
		},
		UpsertAttributes: []*dexpb.AttributeWrite{
			indexedKeywordWrite(TestSearchAttributeKeywordKey, TestSearchAttributeKeywordValue2),
			indexedIntWrite(TestSearchAttributeIntKey, TestSearchAttributeIntValue2),
			dataObjectWrite(TestDataAttributeKey, "test-data-attribute-value2"),
		},
		RecordEvents: []*dexpb.KV{
			{Key: "test-key", Value: TestRecordEvent},
		},
		PublishToChannel: []*dexpb.ChannelMessage{
			{
				ChannelName: TestInterStateChannelName,
				Value:       TestInterstateChannelValue,
			},
		},
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)

	if err := validateStepContext(request.GetContext()); err != nil {
		return nil, err
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
				ChannelConditions: []*dexpb.ChannelCondition{
					{ChannelName: TestInterStateChannelName},
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				indexedKeywordWrite(TestSearchAttributeKeywordKey, TestSearchAttributeKeywordValue1),
				indexedIntWrite(TestSearchAttributeIntKey, TestSearchAttributeIntValue1),
				indexedBoolWrite(TestSearchAttributeBoolKey, false),
				dataObjectWrite(TestDataAttributeKey, "test-data-attribute-value1"),
			},
		}, nil
	case State2:
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
		channelResults := request.GetConditionResults().GetChannelResults()
		if len(channelResults) == 0 ||
			channelResults[0].GetConditionStatus() != dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED ||
			channelResults[0].GetChannelName() != TestInterStateChannelName {
			return nil, status.Error(codes.InvalidArgument, "the channel should be received")
		}
		values := channelResults[0].GetValues()
		if len(values) > 0 {
			h.invokeData.Store(TestInterStateChannelName, values[0])
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: State2},
				},
			},
		}, nil
	case State2:
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
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
