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
	"github.com/superdurable/dex/service"
	"github.com/superdurable/dex/service/interpreter/interfaces"
)

// FlowTimeout defines one execution's soft timeout behavior.
type FlowTimeout struct {
	policy         dexpb.FlowTimeoutPolicy
	timeoutSeconds int32
}

// NewFlowTimeout validates one execution's configured timeout.
func NewFlowTimeout(input *dexpb.InterpreterWorkflowInput) *FlowTimeout {
	if input == nil {
		panic("flow timeout requires workflow input")
	}
	timeout := &FlowTimeout{
		policy:         input.GetFlowTimeoutPolicy(),
		timeoutSeconds: input.GetConfiguredFlowTimeoutSeconds(),
	}
	if timeout.timeoutSeconds == 0 {
		if timeout.policy != dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_UNSPECIFIED {
			panic("disabled Flow timeout has a policy")
		}
		return timeout
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

// IsEnabled reports whether this execution has a soft timeout.
func (t *FlowTimeout) IsEnabled() bool {
	return t.timeoutSeconds > 0
}

// UsesHandler reports whether timeout expiry invokes the Worker hook.
func (t *FlowTimeout) UsesHandler() bool {
	return t.policy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_HANDLER
}

// ResetSnapshotForRetry gives a retried continued run a fresh timeout budget.
func (t *FlowTimeout) ResetSnapshotForRetry(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	snapshot *dexpb.ContinueAsNewDump,
) {
	if !t.IsEnabled() || provider == nil || snapshot == nil {
		panic("enabled Flow timeout, provider, and snapshot are required")
	}
	timeoutResumeInfo := t.NewStepExecutionResumeInfo(ctx, provider)
	timeoutResumeIndex := -1
	resumeInfos := snapshot.GetStepExecutionsToResume()
	for index, resumeInfo := range resumeInfos {
		if resumeInfo.GetStepExecutionId() != service.FlowTimeoutStepExecutionID {
			continue
		}
		if timeoutResumeIndex >= 0 {
			panic("duplicate Flow timeout Step execution")
		}
		timeoutResumeIndex = index
	}
	if timeoutResumeIndex >= 0 {
		resumeInfos[timeoutResumeIndex] = timeoutResumeInfo
	} else {
		resumeInfos = append(resumeInfos, timeoutResumeInfo)
	}
	snapshot.StepExecutionsToResume = resumeInfos

	staleSkipTimers := snapshot.GetStaleSkipTimers()[:0]
	for _, staleSkipTimer := range snapshot.GetStaleSkipTimers() {
		if staleSkipTimer.GetStepExecutionId() != service.FlowTimeoutStepExecutionID {
			staleSkipTimers = append(staleSkipTimers, staleSkipTimer)
		}
	}
	snapshot.StaleSkipTimers = staleSkipTimers
}

// NewStepExecutionResumeInfo creates the system Step in its waiting phase.
func (t *FlowTimeout) NewStepExecutionResumeInfo(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
) *dexpb.StepExecutionResumeInfo {
	if !t.IsEnabled() || provider == nil {
		panic("enabled Flow timeout and provider are required")
	}
	return &dexpb.StepExecutionResumeInfo{
		StepExecutionId: service.FlowTimeoutStepExecutionID,
		Step: &dexpb.StepMovement{
			StepType:                        service.FlowTimeoutStepType,
			StepOptions:                     &dexpb.StepOptions{},
			FromStepExecutionIdInternalOnly: service.StartingStepFromStepExecutionId,
		},
		WaitingCondition: t.newWaitingCondition(ctx, provider),
	}
}

func (t *FlowTimeout) newWaitingCondition(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
) *dexpb.WaitingConditionState {
	deadline := provider.Now(ctx).Add(
		time.Duration(t.timeoutSeconds) * time.Second,
	)
	deadlineUnixSeconds := deadline.Unix()
	if deadline.Nanosecond() != 0 {
		deadlineUnixSeconds++
	}
	return &dexpb.WaitingConditionState{
		WaitingConditionType: dexpb.WaitingConditionType_WAITING_CONDITION_TYPE_ANY_COMPLETED,
		TimerConditions: []*dexpb.TimerCondition{
			{FiringUnixTimestampSeconds: deadlineUnixSeconds},
		},
	}
}

// NewTerminalError returns the FAIL or CANCEL policy result.
func (t *FlowTimeout) NewTerminalError(provider interfaces.WorkflowProvider) error {
	if provider == nil || !t.IsEnabled() || t.UsesHandler() {
		panic("terminal Flow timeout requires FAIL or CANCEL policy")
	}
	reason := fmt.Sprintf("Flow timed out after %d seconds", t.timeoutSeconds)
	if t.policy == dexpb.FlowTimeoutPolicy_FLOW_TIMEOUT_POLICY_CANCEL {
		return provider.NewCanceledError(reason)
	}
	return provider.NewFlowError(
		dexpb.FlowErrorType_FLOW_ERROR_TYPE_FLOW_TIMEOUT,
		reason,
	)
}
