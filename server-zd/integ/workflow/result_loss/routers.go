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
	"time"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Execute and RPC handlers call a counting fake service; S1 -> S2 -> S3 completes, Hold stays open.
const (
	FlowType     = "result_loss"
	Step1        = "S1"
	Step2        = "S2"
	Step3        = "S3"
	StepHold     = "Hold"
	RPCCharge    = "charge"
	HoldChannel  = "never-published"
	LogChannel   = "result-log"
	ChargePrefix = "charge-"
)

type resultLossWorker struct {
	// onRetry runs before a retried Step method answers; nil skips it.
	onRetry func(handlerName string, attempt int32)
	mutex   sync.Mutex
	// externalCalls counts calls to the fake external service by handler, such as "S1_execute".
	externalCalls map[string]int64
	// attemptsSeen records every Step method attempt the Worker ran, in arrival order.
	attemptsSeen []string
	// attemptStarts records when each attempt in attemptsSeen arrived.
	attemptStarts map[string]time.Time
}

func NewResultLossWorker(onRetry func(handlerName string, attempt int32)) *resultLossWorker {
	return &resultLossWorker{
		onRetry:       onRetry,
		externalCalls: make(map[string]int64),
		attemptStarts: make(map[string]time.Time),
	}
}

func (worker *resultLossWorker) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	if request.GetFlowType() != FlowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	if request.GetStepType() == StepHold {
		return &dexpb.InvokeWaitForMethodResponse{
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				ChannelConditions:    []*dexpb.ChannelCondition{{ChannelName: HoldChannel}},
			},
		}, nil
	}
	handlerName := request.GetStepType() + "_waitFor"
	attempt := request.GetContext().GetAttempt()
	worker.startAttempt(handlerName, attempt)
	return &dexpb.InvokeWaitForMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{attemptAttribute(handlerName, attempt)},
		PublishToChannel: []*dexpb.ChannelMessage{attemptMessage(handlerName, attempt)},
	}, nil
}

func (worker *resultLossWorker) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	if request.GetFlowType() != FlowType {
		return nil, status.Error(codes.InvalidArgument, "invalid flow type")
	}
	handlerName := request.GetStepType() + "_execute"
	attempt := request.GetContext().GetAttempt()
	worker.startAttempt(handlerName, attempt)
	worker.chargeExternalService(handlerName)
	output := &dexpb.Value{Kind: &dexpb.Value_StringValue{
		StringValue: request.GetStepInput().GetStringValue() + "/" + request.GetStepType(),
	}}
	response := &dexpb.InvokeExecuteMethodResponse{
		UpsertAttributes: []*dexpb.AttributeWrite{attemptAttribute(handlerName, attempt)},
		PublishToChannel: []*dexpb.ChannelMessage{attemptMessage(handlerName, attempt)},
	}
	nextStepTypes := map[string]string{Step1: Step2, Step2: Step3}
	nextStepType, hasNextStep := nextStepTypes[request.GetStepType()]
	if !hasNextStep {
		response.StepDecision = &dexpb.StepDecision{CloseDecision: &dexpb.CloseDecision{
			CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
			CloseInput:        output,
		}}
		return response, nil
	}
	response.StepDecision = &dexpb.StepDecision{NextSteps: []*dexpb.StepMovement{{
		StepType:    nextStepType,
		StepInput:   output,
		StepOptions: FastRetryStepOptions(),
	}}}
	return response, nil
}

func (worker *resultLossWorker) InvokeWorkerRPC(
	_ context.Context,
	request *dexpb.InvokeWorkerRPCRequest,
) (*dexpb.InvokeWorkerRPCResponse, error) {
	if request.GetFlowType() != FlowType || request.GetRpcName() != RPCCharge {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("invalid rpc name: %s", request.GetRpcName()))
	}
	worker.chargeExternalService(RPCCharge + "_rpc")
	return &dexpb.InvokeWorkerRPCResponse{
		Output: request.GetInput(),
		UpsertAttributes: []*dexpb.AttributeWrite{{
			Key:   ChargePrefix + request.GetInput().GetStringValue(),
			Value: request.GetInput(),
		}},
	}, nil
}

// ExternalCalls returns a copy of the fake external service's call counts.
func (worker *resultLossWorker) ExternalCalls() map[string]int64 {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	calls := make(map[string]int64, len(worker.externalCalls))
	for name, count := range worker.externalCalls {
		calls[name] = count
	}
	return calls
}

// AttemptsSeen returns the Step method attempts the Worker ran, such as "S1_execute#2".
func (worker *resultLossWorker) AttemptsSeen() []string {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	return append([]string(nil), worker.attemptsSeen...)
}

// AttemptStart returns when the attempt named like "S1_execute#2" arrived, and whether it arrived.
func (worker *resultLossWorker) AttemptStart(attemptName string) (time.Time, bool) {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	start, hasStarted := worker.attemptStarts[attemptName]
	return start, hasStarted
}

func (worker *resultLossWorker) startAttempt(handlerName string, attempt int32) {
	worker.mutex.Lock()
	attemptName := AttemptName(handlerName, attempt)
	worker.attemptsSeen = append(worker.attemptsSeen, attemptName)
	worker.attemptStarts[attemptName] = time.Now()
	worker.mutex.Unlock()
	if attempt > 1 && worker.onRetry != nil {
		worker.onRetry(handlerName, attempt)
	}
}

// chargeExternalService stands for a non-idempotent external call, such as charging a card.
func (worker *resultLossWorker) chargeExternalService(handlerName string) {
	worker.mutex.Lock()
	defer worker.mutex.Unlock()
	worker.externalCalls[handlerName]++
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

// AttemptName names one method attempt, such as "S1_execute#2".
func AttemptName(handlerName string, attempt int32) string {
	return fmt.Sprintf("%s#%d", handlerName, attempt)
}

// AttemptAttributeKey is the Attribute each method attempt writes, such as "S1_execute-a2".
func AttemptAttributeKey(handlerName string, attempt int32) string {
	return fmt.Sprintf("%s-a%d", handlerName, attempt)
}

func attemptAttribute(handlerName string, attempt int32) *dexpb.AttributeWrite {
	return &dexpb.AttributeWrite{
		Key:   AttemptAttributeKey(handlerName, attempt),
		Value: &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: AttemptName(handlerName, attempt)}},
	}
}

func attemptMessage(handlerName string, attempt int32) *dexpb.ChannelMessage {
	return &dexpb.ChannelMessage{
		ChannelName: LogChannel,
		Value:       &dexpb.Value{Kind: &dexpb.Value_StringValue{StringValue: AttemptName(handlerName, attempt)}},
	}
}
