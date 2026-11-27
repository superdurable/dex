// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package expiredtimer is a one-Step Flow that waits on one short timer, then completes.
package expiredtimer

import (
	"context"
	"sync/atomic"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"github.com/superdurable/dex/server-zd/integ/workflow/common"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	WorkflowType = "expired_timer"
	WaitStep     = "WaitStep"
	// WaitStepExecutionID is the Step execution ID of the Flow's only Step.
	WaitStepExecutionID = WaitStep + "-1"
	// TimerDurationSeconds is the duration of the Step's only timer.
	TimerDurationSeconds = 2
)

type handler struct {
	dexpb.UnimplementedWorkerServiceServer
	executeCount atomic.Int64
}

// NewHandler returns a Worker handler that counts the Step's Execute calls.
func NewHandler() *handler {
	return &handler{}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	common.LogRequest("received waitFor request, ", request)
	if request.GetFlowType() != WorkflowType || request.GetStepType() != WaitStep {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
	return &dexpb.InvokeWaitForMethodResponse{
		WaitingCondition: &dexpb.WaitingCondition{
			TimerConditions: []*dexpb.TimerCondition{
				{
					ConditionId:     "expiring-timer",
					DurationSeconds: TimerDurationSeconds,
				},
			},
			WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
		},
	}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	common.LogRequest("received execute request, ", request)
	if request.GetFlowType() != WorkflowType || request.GetStepType() != WaitStep {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type or step type")
	}
	h.executeCount.Add(1)
	return &dexpb.InvokeExecuteMethodResponse{
		StepDecision: &dexpb.StepDecision{
			CloseDecision: common.GracefulCompleteDecision(nil),
		},
	}, nil
}

// ExecuteCount returns how many times the Step's Execute method ran, across all Flows.
func (h *handler) ExecuteCount() int64 {
	return h.executeCount.Load()
}
