// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package result_loss

import (
	"context"
	"fmt"
	"sync"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/**
 * This test flow calls a counting fake external service from every Execute and RPC handler.
 *
 * Pipeline: S1 -> S2 -> S3. Each Execute charges the fake service once and appends its Step
 * type to the Step input; S3 completes the Flow with the joined path.
 * Hold: WaitFor waits on a Channel that nobody publishes, so the Flow stays open for RPCs.
 * RPC "charge": charges the fake service and writes the Attribute "charge-<input>".
 */
const (
	FlowType     = "result_loss"
	Step1        = "S1"
	Step2        = "S2"
	Step3        = "S3"
	StepHold     = "Hold"
	RPCCharge    = "charge"
	HoldChannel  = "never-published"
	ChargePrefix = "charge-"
)

type handler struct {
	mutex sync.Mutex
	// externalCalls counts calls to the fake external service by handler, such as "S1_execute".
	externalCalls map[string]int64
	// attemptsSeen records every Step method attempt the Worker ran, in arrival order.
	attemptsSeen []string
}

func NewHandler() *handler {
	return &handler{externalCalls: make(map[string]int64)}
}

func (h *handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != FlowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	h.recordAttempt(request.GetStepType()+"_waitFor", request.GetContext())
	if request.GetStepType() == StepHold {
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: HoldChannel}},
			},
		}, nil
	}
	return &dexpb.InvokeWaitForMethodResponse{}, nil
}

func (h *handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != FlowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	handlerName := request.GetStepType() + "_execute"
	h.recordAttempt(handlerName, request.GetContext())
	h.chargeExternalService(handlerName)
	output := &dexpb.Value{Kind: &dexpb.Value_StringValue{
		StringValue: request.GetStepInput().GetStringValue() + "/" + request.GetStepType(),
	}}
	nextStepTypes := map[string]string{Step1: Step2, Step2: Step3}
	nextStepType, hasNextStep := nextStepTypes[request.GetStepType()]
	if !hasNextStep {
		return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
			CloseDecision: &dexpb.CloseDecision{
				CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
				CloseInput:        output,
			},
		}}, nil
	}
	return &dexpb.InvokeExecuteMethodResponse{StepDecision: &dexpb.StepDecision{
		NextSteps: []*dexpb.StepMovement{{
			StepType:    nextStepType,
			StepInput:   output,
			StepOptions: FastRetryStepOptions(),
		}},
	}}, nil
}

func (h *handler) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if request.GetFlowType() != FlowType || request.GetRpcName() != RPCCharge {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}
	h.chargeExternalService(RPCCharge + "_rpc")
	return &dexpb.InvokeWorkerRPCResponse{
		Output: request.GetInput(),
		UpsertAttributes: []*dexpb.AttributeWrite{{
			Key:   ChargePrefix + request.GetInput().GetStringValue(),
			Value: request.GetInput(),
		}},
	}, nil
}

// ExternalCalls returns a copy of the fake external service's call counts.
func (h *handler) ExternalCalls() map[string]int64 {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	calls := make(map[string]int64, len(h.externalCalls))
	for name, count := range h.externalCalls {
		calls[name] = count
	}
	return calls
}

// AttemptsSeen returns the Step method attempts the Worker ran, such as "S1_execute#2".
func (h *handler) AttemptsSeen() []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	return append([]string(nil), h.attemptsSeen...)
}

func (h *handler) recordAttempt(handlerName string, stepContext *dexpb.Context) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.attemptsSeen = append(h.attemptsSeen, fmt.Sprintf("%s#%d", handlerName, stepContext.GetAttempt()))
}

// chargeExternalService stands for a non-idempotent external call, such as charging a card.
func (h *handler) chargeExternalService(handlerName string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.externalCalls[handlerName]++
}

// FastRetryStepOptions retries every method after one second, so dropped results retry quickly.
func FastRetryStepOptions() *dexpb.StepOptions {
	retryPolicy := &dexpb.RetryPolicy{
		InitialIntervalSeconds: 1,
		BackoffCoefficient:     1,
		MaximumIntervalSeconds: 1,
		MaximumAttempts:        20,
	}
	return &dexpb.StepOptions{WaitForRetryPolicy: retryPolicy, ExecuteRetryPolicy: retryPolicy}
}
