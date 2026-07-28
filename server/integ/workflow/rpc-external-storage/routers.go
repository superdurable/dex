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
	"log"
	"strings"
	"sync"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"github.com/superdurable/dex/service"
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
	dexpb.UnimplementedWorkerServiceServer
	flowClient dexpb.FlowServiceClient
	testData   sync.Map
}

func NewHandler(flowClient dexpb.FlowServiceClient) *handler {
	if flowClient == nil {
		panic("flowClient is required")
	}
	return &handler{
		flowClient: flowClient,
		testData:   sync.Map{},
	}
}

func (h *handler) InvokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	log.Println("received worker rpc request, ", request)

	flowContext := request.GetContext()
	if flowContext.GetFlowId() == "" || flowContext.GetRunId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid context in the request")
	}
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid flow type: %s", request.GetFlowType()))
	}

	h.testData.Store(request.GetRpcName()+"-input", request.GetInput())

	resolvedAttributes := make([]*dexpb.KV, 0, len(request.GetAttributes()))
	for _, attribute := range request.GetAttributes() {
		if attribute.GetValue().GetKind() == nil {
			return nil, status.Error(codes.InvalidArgument, "RPC attribute value kind is required")
		}
		resolved, err := common.LoadBlobsValue(ctx, h.flowClient, attribute.GetValue())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "LoadBlobs for %s: %v", attribute.GetKey(), err)
		}
		resolvedAttributes = append(resolvedAttributes, &dexpb.KV{
			Key:   attribute.GetKey(),
			Value: resolved,
		})
	}
	h.testData.Store(request.GetRpcName()+"-received-data", resolvedAttributes)

	if request.GetRpcName() != UpdateDataAttributesRPC {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unknown RPC name: %s", request.GetRpcName()))
	}

	return &dexpb.InvokeWorkerRPCResponse{
		Output: TestOutput,
		UpsertAttributes: []*dexpb.AttributeWrite{
			{Key: SmallDataKey, Value: SmallDataValue},
			{Key: LargeDataKey, Value: LargeDataValue},
		},
		PublishToChannel: []*dexpb.ChannelMessage{
			{
				ChannelName: closeWorkflowChannel,
				Value:       jsonStringValue("close"),
			},
		},
	}, nil
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	log.Println("received waitFor request, ", request)

	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}

	switch request.GetStepType() {
	case State1:
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions: []*dexpb.ChannelCondition{
					{ChannelName: closeWorkflowChannel},
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{
				{Key: SmallDataKey, Value: InitialSmallData},
				{Key: LargeDataKey, Value: InitialLargeData},
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
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{StepType: service.GracefulCompletingFlowStepType},
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
	history := make(map[string]int64)
	testData := make(map[string]interface{})
	h.testData.Range(func(key, value interface{}) bool {
		testData[key.(string)] = value
		return true
	})
	return common.TestResult{InvokeHistory: history, InvokeData: testData}
}

func jsonStringValue(value string) *dexpb.Value {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &dexpb.Value{
		Kind: &dexpb.Value_ObjValue{
			ObjValue: &dexpb.EncodedObject{
				Encoding: "json",
				Payload:  payload,
			},
		},
	}
}
