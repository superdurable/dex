// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package request_id_dedup

import (
	"context"
	"strings"
	"sync"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow keeps one Step waiting on a Channel that no test publishes to, so the run stays
 * open while tests repeat request IDs.
 *
 * HoldStep:
 *		- WaitFor waits for one message on HoldChannelName
 *		- Execute gracefully completes the flow
 * RPCName:
 *		- writes its input to RPCInputAttributeKey and returns the input as its output
 *		- an input starting with GatedInputPrefix blocks until ReleaseGatedRPCs
 */
const (
	WorkflowType         = "request-id-dedup"
	HoldStep             = "HoldStep"
	HoldChannelName      = "hold"
	PendingChannelName   = "pending"
	RPCName              = "record-input"
	RPCInputAttributeKey = "rpc-input"
	GatedInputPrefix     = "gated-"
)

// Handler counts RPC handler runs per Flow ID and can hold gated RPCs.
type Handler struct {
	rpcRunsMutex sync.Mutex
	rpcRuns      map[string]int32
	rpcEntered   chan string
	rpcRelease   chan struct{}
	releaseOnce  sync.Once
}

func NewHandler() *Handler {
	return &Handler{
		rpcRuns:    make(map[string]int32),
		rpcEntered: make(chan string, 16),
		rpcRelease: make(chan struct{}),
	}
}

func (h *Handler) InvokeWorkerRPC(
	ctx context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	common.LogRequest("received worker rpc request, ", request)
	if request.GetFlowType() != WorkflowType || request.GetRpcName() != RPCName {
		return nil, status.Errorf(codes.InvalidArgument, "invalid rpc name: %s", request.GetRpcName())
	}
	flowID := request.GetContext().GetFlowId()
	h.recordRPCRun(flowID)
	input := request.GetInput()
	if strings.HasPrefix(input.GetStringValue(), GatedInputPrefix) {
		h.rpcEntered <- flowID
		select {
		case <-h.rpcRelease:
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	return &dexpb.InvokeWorkerRPCResponse{
		Output: input,
		UpsertAttributes: []*dexpb.AttributeWrite{
			{Key: RPCInputAttributeKey, Value: input},
		},
	}, nil
}

func (h *Handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)
	if request.GetFlowType() != WorkflowType || request.GetStepType() != HoldStep {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
			ChannelConditions: []*dexpb.ChannelCondition{
				{ChannelName: HoldChannelName},
			},
		},
	}, nil
}

func (h *Handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)
	if request.GetFlowType() != WorkflowType || request.GetStepType() != HoldStep {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.GracefulCompleteDecision(nil),
		},
	}, nil
}

// RPCEntered receives the Flow ID of each gated RPC once its handler starts waiting.
func (h *Handler) RPCEntered() <-chan string {
	return h.rpcEntered
}

// ReleaseGatedRPCs lets every waiting and later gated RPC return.
func (h *Handler) ReleaseGatedRPCs() {
	h.releaseOnce.Do(func() { close(h.rpcRelease) })
}

func (h *Handler) recordRPCRun(flowID string) {
	h.rpcRunsMutex.Lock()
	defer h.rpcRunsMutex.Unlock()
	h.rpcRuns[flowID]++
}

// RPCRunCount returns how many times the RPC handler ran for flowID.
func (h *Handler) RPCRunCount(flowID string) int32 {
	h.rpcRunsMutex.Lock()
	defer h.rpcRunsMutex.Unlock()
	return h.rpcRuns[flowID]
}
