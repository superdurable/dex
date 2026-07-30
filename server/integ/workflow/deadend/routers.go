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

package deadend

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow has 3 steps, using WorkerServiceServer to implement the flow directly.
 *
 * RPCWriteData:
 *		- WaitFor will upsert attributes
 * RPCTriggerState:
 *		- WaitFor will move to State1
 * State1:
 *		- WaitFor is skipped
 *      - Execute method will put the step into a dead-end.
 */
const (
	WorkflowType    = "deadend"
	RPCTriggerState = "test-RPCTriggerState"
	RPCWriteData    = "RPCWriteData"

	State1 = "S1"
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	invokeHistory sync.Map
	rpcInvokes    atomic.Int32
}

func NewHandler() *handler {
	return &handler{
		invokeHistory: sync.Map{},
	}
}

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	common.LogRequest("received worker rpc request, ", request)

	flowContext := request.GetContext()
	if flowContext.GetFlowId() == "" || flowContext.GetRunId() == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid context in the request")
	}
	if request.GetFlowType() != WorkflowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	h.rpcInvokes.Add(1)

	switch request.GetRpcName() {
	case RPCTriggerState:
		return &dexpb.InvokeWorkerRPCResponse{
			StepDecision: &dexpb.StepDecision{
				NextSteps: []*dexpb.StepMovement{
					{
						StepType:                        State1,
						FromStepExecutionIdInternalOnly: "worker-provided-source",
						StepOptions: &dexpb.StepOptions{
							SkipWaitFor: true,
						},
					},
				},
			},
		}, nil
	case RPCWriteData:
		time.Sleep(50 * time.Millisecond)
		return &dexpb.InvokeWorkerRPCResponse{
			UpsertAttributes: []*dexpb.AttributeWrite{
				{
					Key: "any key",
					Value: &dexpb.Value{
						Kind: &dexpb.Value_ObjValue{
							ObjValue: &dexpb.EncodedObject{
								Encoding: "encoding",
								Payload:  []byte("data"),
							},
						},
					},
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	_ *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	return nil, status.Error(codes.InvalidArgument, "should not be called")
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)

	if request.GetFlowType() == WorkflowType {
		if value, ok := h.invokeHistory.Load(request.GetStepType() + "_execute"); ok {
			h.invokeHistory.Store(request.GetStepType()+"_execute", value.(int64)+1)
		} else {
			h.invokeHistory.Store(request.GetStepType()+"_execute", int64(1))
		}

		if request.GetStepType() == State1 {
			return &dexpb.InvokeExecuteMethodResponse{
				StepDecision: &dexpb.StepDecision{
					CloseDecision: common.DeadEndDecision(),
				},
			}, nil
		}
	}

	return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
}

func (h *handler) GetTestResult() common.TestResult {
	invokeHistory := make(map[string]int64)
	h.invokeHistory.Range(func(key, value interface{}) bool {
		invokeHistory[key.(string)] = value.(int64)
		return true
	})
	return common.TestResult{InvokeHistory: invokeHistory}
}

func (h *handler) GetRPCInvokes() int32 {
	return h.rpcInvokes.Load()
}
