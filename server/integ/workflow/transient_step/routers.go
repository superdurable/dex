// Legacy Materials in this file remain under their original licenses.
// See LEGACY_NOTICES.md.

// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications after the Legacy Cutoff are licensed under the
// Super Durable Source License 1.0.
// Legacy Materials remain under their original licenses.
// See LICENSE and LEGACY_NOTICES.md.

package transient_step

import (
	"context"
	"sync"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	FlowType                = "transient_step"
	SourceStep              = "source"
	TransientStep           = "transient"
	TimerConditionID        = "source-timer"
	TimerDurationSeconds    = int64(600)
	WaitAttributeKey        = "wait-attribute"
	TransientAttributeKey   = "transient-attribute"
	WaitAttributeValue      = "wait-complete"
	TransientAttributeValue = "transient-complete"
	SourceWaitCall          = "source-wait"
	TransientWaitCall       = "transient-wait"
	TransientExecuteCall    = "transient-execute"
	SourceExecuteCall       = "source-execute"
)

type Result struct {
	Calls                  []string
	TransientCompletedUnix int64
}

type Handler struct {
	dexpb.UnimplementedWorkerServiceServer

	mu                     sync.Mutex
	calls                  []string
	transientStarted       chan struct{}
	releaseTransient       chan struct{}
	transientStartedOnce   sync.Once
	releaseTransientOnce   sync.Once
	transientCompletedUnix int64
}

func NewHandler() *Handler {
	return &Handler{
		transientStarted: make(chan struct{}),
		releaseTransient: make(chan struct{}),
	}
}

func (h *Handler) InvokeWaitForMethod(
	_ context.Context,
	request *dexpb.InvokeWaitForMethodRequest,
) (*dexpb.InvokeWaitForMethodResponse, error) {
	switch request.GetStepType() {
	case SourceStep:
		h.recordCall(SourceWaitCall)
		return &dexpb.InvokeWaitForMethodResponse{
			UpsertAttributes: []*dexpb.AttributeWrite{{
				Key:   WaitAttributeKey,
				Value: stringValue(WaitAttributeValue),
			}},
			WaitingCondition: &dexpb.WaitingCondition{
				WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ALL_COMPLETED,
				TimerConditions: []*dexpb.TimerCondition{{
					ConditionId:     TimerConditionID,
					DurationSeconds: TimerDurationSeconds,
				}},
			},
			TransientStepMovement: &dexpb.StepMovement{
				StepType: TransientStep,
				StepOptions: &dexpb.StepOptions{
					SkipWaitFor: true,
				},
			},
		}, nil
	case TransientStep:
		h.recordCall(TransientWaitCall)
		return nil, status.Error(codes.FailedPrecondition, "transient WaitFor must not run")
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown step")
	}
}

func (h *Handler) InvokeExecuteMethod(
	_ context.Context,
	request *dexpb.InvokeExecuteMethodRequest,
) (*dexpb.InvokeExecuteMethodResponse, error) {
	switch request.GetStepType() {
	case TransientStep:
		h.recordCall(TransientExecuteCall)
		if request.GetContext().GetStepExecutionId() != TransientStep+"-1" {
			return nil, status.Error(codes.InvalidArgument, "invalid transient execution ID")
		}
		if request.GetContext().GetFromStepExecutionId() != SourceStep+"-1" {
			return nil, status.Error(codes.InvalidArgument, "invalid transient lineage")
		}
		if request.GetConditionResults() != nil {
			return nil, status.Error(codes.InvalidArgument, "transient condition results must be nil")
		}
		if attributeValue(request.GetAttributes(), WaitAttributeKey) != WaitAttributeValue {
			return nil, status.Error(codes.InvalidArgument, "WaitFor attribute is unavailable")
		}
		h.transientStartedOnce.Do(func() {
			close(h.transientStarted)
		})
		<-h.releaseTransient
		h.mu.Lock()
		h.transientCompletedUnix = time.Now().Unix()
		h.mu.Unlock()
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: &dexpb.CloseDecision{
					CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_DEAD_END,
				},
			},
			UpsertAttributes: []*dexpb.AttributeWrite{{
				Key:   TransientAttributeKey,
				Value: stringValue(TransientAttributeValue),
			}},
		}, nil
	case SourceStep:
		h.recordCall(SourceExecuteCall)
		if attributeValue(request.GetAttributes(), TransientAttributeKey) !=
			TransientAttributeValue {
			return nil, status.Error(codes.InvalidArgument, "transient attribute is unavailable")
		}
		if !timerCompleted(request.GetConditionResults()) {
			return nil, status.Error(codes.InvalidArgument, "source timer is incomplete")
		}
		return &dexpb.InvokeExecuteMethodResponse{
			StepDecision: &dexpb.StepDecision{
				CloseDecision: &dexpb.CloseDecision{
					CloseDecisionType: dexpb.CloseDecisionType_CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
				},
			},
		}, nil
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown step")
	}
}

func (h *Handler) TransientStarted() <-chan struct{} {
	return h.transientStarted
}

func (h *Handler) ReleaseTransient() {
	h.releaseTransientOnce.Do(func() {
		close(h.releaseTransient)
	})
}

func (h *Handler) GetResult() Result {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Result{
		Calls:                  append([]string(nil), h.calls...),
		TransientCompletedUnix: h.transientCompletedUnix,
	}
}

func (h *Handler) recordCall(call string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, call)
}

func attributeValue(attributes []*dexpb.KV, key string) string {
	for _, attribute := range attributes {
		if attribute.GetKey() == key {
			return attribute.GetValue().GetStringValue()
		}
	}
	return ""
}

func timerCompleted(results *dexpb.ConditionResults) bool {
	for _, result := range results.GetTimerResults() {
		if result.GetConditionId() == TimerConditionID &&
			result.GetConditionStatus() == dexpb.ConditionStatus_CONDITION_STATUS_COMPLETED {
			return true
		}
	}
	return false
}

func stringValue(value string) *dexpb.Value {
	return &dexpb.Value{
		Kind: &dexpb.Value_StringValue{StringValue: value},
	}
}
