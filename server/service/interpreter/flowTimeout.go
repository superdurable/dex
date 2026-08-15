// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import (
	"fmt"
	"time"

	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

// FlowTimeout enforces a durable soft timeout without closing the backend workflow early.
type FlowTimeout struct {
	provider            interfaces.WorkflowProvider
	policy              dexpb.FlowTimeoutPolicy
	timeoutSeconds      int32
	deadlineUnixSeconds int64
	timer               interfaces.Future
}

// NewFlowTimeout restores or initializes one execution's timeout deadline.
func NewFlowTimeout(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	input *dexpb.InterpreterWorkflowInput,
) *FlowTimeout {
	if provider == nil || input == nil {
		panic("flow timeout requires non-nil dependencies")
	}
	deadlineUnixSeconds := int64(0)
	if input.GetIsResumeFromContinueAsNew() {
		deadlineUnixSeconds = input.GetContinueAsNewInput().GetFlowTimeoutDeadlineUnixTimestampSeconds()
	}
	timeout := &FlowTimeout{
		provider:            provider,
		policy:              input.GetFlowTimeoutPolicy(),
		timeoutSeconds:      input.GetConfiguredFlowTimeoutSeconds(),
		deadlineUnixSeconds: deadlineUnixSeconds,
	}
	if timeout.timeoutSeconds == 0 {
		if timeout.policy != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED {
			panic("disabled Flow timeout has a policy")
		}
		return timeout
	}
	if timeout.deadlineUnixSeconds == 0 {
		if input.GetIsResumeFromContinueAsNew() {
			panic("continued Flow timeout has no deadline")
		}
		deadline := provider.Now(ctx).Add(time.Duration(timeout.timeoutSeconds) * time.Second)
		timeout.deadlineUnixSeconds = deadline.Unix()
		if deadline.Nanosecond() != 0 {
			timeout.deadlineUnixSeconds++
		}
	}
	switch timeout.policy {
	case dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_FAIL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL,
		dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER:
		return timeout
	default:
		panic("enabled Flow timeout has an invalid policy")
	}
}

// Start begins the fail or cancel policy timer.
func (t *FlowTimeout) Start(ctx interfaces.UnifiedContext) {
	if t.timeoutSeconds == 0 || t.IsHandler() {
		return
	}
	remaining := time.Unix(t.deadlineUnixSeconds, 0).Sub(t.provider.Now(ctx))
	if remaining < 0 {
		remaining = 0
	}
	t.timer = t.provider.NewTimer(ctx, remaining)
}

// IsHandler reports whether the timeout uses the system Step execution.
func (t *FlowTimeout) IsHandler() bool {
	return t.policy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER
}

// DeadlineUnixSeconds returns the deadline carried across continue-as-new.
func (t *FlowTimeout) DeadlineUnixSeconds() int64 {
	return t.deadlineUnixSeconds
}

// IsTriggered reports whether the fail or cancel timer fired.
func (t *FlowTimeout) IsTriggered() bool {
	return t.timer != nil && t.timer.IsReady()
}

// Error returns the timeout's terminal error after it fires.
func (t *FlowTimeout) Error() error {
	if !t.IsTriggered() {
		panic("Flow timeout error requested before timeout")
	}
	reason := fmt.Sprintf("Flow timed out after %d seconds", t.timeoutSeconds)
	if t.policy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL {
		return t.provider.NewCanceledError(reason)
	}
	return t.provider.NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT,
		reason,
	)
}
