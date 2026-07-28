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

package rpcStorage

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/superdurable/iwf/integ/workflow/common"
	"log"
	"strings"
	"sync"

	"github.com/superdurable/iwf/gen/iwfpb"
	"github.com/superdurable/iwf/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * Test workflow for RPC external storage functionality.
 * Tests updating data attributes with both small and large data via RPC methods.
 *
 * Step1:
 *   - Sets up initial data attributes (small and large)
 *   - Waits for RPC to update the data attributes
 * Step2:
 *   - Completes the workflow
 */

const (
	WorkflowType            = "rpc-external-storage"
	State1                  = "S1"
	State2                  = "S2"
	UpdateDataAttributesRPC = "update-data-attributes"
	closeWorkflowChannel    = "close-workflow"

	SmallDataKey = "small-data"
	LargeDataKey = "large-data"

	SmallDataContent = "small-data-content"

	InitialSmallDataContent = "initial-small-data"
)

var (
	LargeDataContent = "large-data-content-" + strings.Repeat("x", 1000)

	InitialLargeDataContent = "initial-large-data-" + strings.Repeat("y", 1000)
)

var (
	SmallDataValue   = jsonStringValue(SmallDataContent)
	LargeDataValue   = jsonStringValue(LargeDataContent)
	InitialSmallData = jsonStringValue(InitialSmallDataContent)
	InitialLargeData = jsonStringValue(InitialLargeDataContent)
	TestInput        = jsonStringValue("test-input-value")
	TestOutput       = jsonStringValue("test-output-value")
)

type handler struct {
	iwfpb.UnimplementedWorkerServiceServer
	testData sync.Map
}

func NewHandler() *handler {
	return &handler{
		testData: sync.Map{},
	}
}

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *iwfpb.InvokeWorkerRPCRequest,
) (*iwfpb.InvokeWorkerRPCResponse, error) {
	log.Println("received worker rpc request, ", request)

	flowContext := request.GetContext()
	if flowContext.GetFlowId() == "" || flowContext.GetRunId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid context in the request")
	}
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid flow type: %s", request.GetFlowType()))
	}

	h.testData.Store(request.GetRpcName()+"-input", request.GetInput())
	h.testData.Store(request.GetRpcName()+"-received-data", request.GetAttributes())

	if request.GetRpcName() != UpdateDataAttributesRPC {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unknown RPC name: %s", request.GetRpcName()))
	}

	for _, attribute := range request.GetAttributes() {
		if attribute.GetValue().GetKind() == nil {
			return nil, status.Error(codes.InvalidArgument, "RPC should receive hydrated attribute values")
		}
	}

	return &iwfpb.InvokeWorkerRPCResponse{
		Output: TestOutput,
		UpsertAttributes: []*iwfpb.AttributeWrite{
			{Key: SmallDataKey, Value: SmallDataValue},
			{Key: LargeDataKey, Value: LargeDataValue},
		},
		PublishToChannel: []*iwfpb.ChannelMessage{
			{
				ChannelName: closeWorkflowChannel,
				Value:       jsonStringValue("close"),
			},
		},
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *iwfpb.InvokeWaitForMethodRequest,
) (*iwfpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	switch request.GetStepType() {
	case State1:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*iwfpb.ChannelCondition{
					{ChannelName: closeWorkflowChannel},
				},
			},
			UpsertAttributes: []*iwfpb.AttributeWrite{
				{Key: SmallDataKey, Value: InitialSmallData},
				{Key: LargeDataKey, Value: InitialLargeData},
			},
		}, nil
	case State2:
		return &iwfpb.InvokeWaitForMethodResponse{
			WaitingCondition: &iwfpb.WaitingCondition{
				WaitingConditionType: iwfpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
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

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	switch request.GetStepType() {
	case State1:
		channelResults := request.GetConditionResults().GetChannelResults()
		if len(channelResults) == 0 {
			return nil, status.Error(codes.InvalidArgument, "expected close-workflow channel message")
		}
		return &iwfpb.InvokeExecuteMethodResponse{
			StepDecision: &iwfpb.StepDecision{
				NextSteps: []*iwfpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
				},
			},
		}, nil
	case State2:
		return &iwfpb.InvokeExecuteMethodResponse{
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
	history := make(map[string]int64)
	testData := make(map[string]interface{})
	h.testData.Range(func(key, value interface{}) bool {
		testData[key.(string)] = value
		return true
	})
	return common.TestResult{InvokeHistory: history, InvokeData: testData}
}

func jsonStringValue(value string) *iwfpb.Value {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &iwfpb.Value{
		Kind: &iwfpb.Value_ObjValue{
			ObjValue: &iwfpb.EncodedObject{
				Encoding: "json",
				Payload:  payload,
			},
		},
	}
}
