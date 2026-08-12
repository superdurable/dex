// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import "github.com/superdurable/dex/gen/dexpb"

type subFlowTracker struct {
	waits map[string]*subFlowWait
}

type subFlowWait struct {
	condition *dexpb.WaitingCondition
	completed map[int32]*dexpb.FlowResult
	pending   map[int32][]*dexpb.SubFlowCompletionSignalRequest
}

func newSubFlowTracker(resumeInfos []*dexpb.StepExecutionResumeInfo) *subFlowTracker {
	tracker := &subFlowTracker{waits: map[string]*subFlowWait{}}
	for _, resumeInfo := range resumeInfos {
		if resumeInfo == nil || len(resumeInfo.GetWaitingCondition().GetSubFlowConditions()) == 0 {
			continue
		}
		completed := resumeInfo.GetCompletedConditions().GetCompletedSubFlowResults()
		if completed == nil {
			completed = map[int32]*dexpb.FlowResult{}
			if resumeInfo.CompletedConditions == nil {
				resumeInfo.CompletedConditions = &dexpb.StepExecutionCompletedConditions{}
			}
			resumeInfo.CompletedConditions.CompletedSubFlowResults = completed
		}
		tracker.register(resumeInfo.GetStepExecutionId(), resumeInfo.GetWaitingCondition(), completed)
	}
	return tracker
}

func (t *subFlowTracker) register(
	stepExecutionID string,
	condition *dexpb.WaitingCondition,
	completed map[int32]*dexpb.FlowResult,
) {
	if completed == nil {
		panic("SubFlow completed result map is nil")
	}
	t.waits[stepExecutionID] = &subFlowWait{
		condition: condition,
		completed: completed,
		pending:   map[int32][]*dexpb.SubFlowCompletionSignalRequest{},
	}
}

func (t *subFlowTracker) applyStartResult(
	stepExecutionID string, index int32, result *dexpb.StartSubFlowActivityOutput,
) {
	wait := t.waits[stepExecutionID]
	if wait == nil || result == nil {
		return
	}
	condition := subFlowConditionAt(wait.condition, index)
	if condition == nil {
		return
	}
	condition.NormalizedRequestId = result.GetNormalizedRequestId()
	condition.StartResolution = result.GetResolution()
	if result.GetTerminalResult() != nil {
		wait.completed[index] = result.GetTerminalResult()
	}
	for _, signal := range wait.pending[index] {
		t.applyCompletion(wait, signal)
	}
	delete(wait.pending, index)
}

func (t *subFlowTracker) handleCompletion(signal *dexpb.SubFlowCompletionSignalRequest) {
	if signal == nil || signal.GetResult() == nil {
		return
	}
	wait := t.waits[signal.GetStepExecutionId()]
	if wait == nil {
		return
	}
	condition := subFlowConditionAt(wait.condition, signal.GetSubFlowIndex())
	if condition == nil || condition.GetFlowId() != signal.GetResult().GetFlowId() {
		return
	}
	if condition.GetNormalizedRequestId() != signal.GetNormalizedRequestId() {
		wait.pending[signal.GetSubFlowIndex()] = append(wait.pending[signal.GetSubFlowIndex()], signal)
		return
	}
	t.applyCompletion(wait, signal)
}

func (t *subFlowTracker) applyCompletion(
	wait *subFlowWait, signal *dexpb.SubFlowCompletionSignalRequest,
) {
	condition := subFlowConditionAt(wait.condition, signal.GetSubFlowIndex())
	if condition == nil || condition.GetNormalizedRequestId() != signal.GetNormalizedRequestId() ||
		condition.GetFlowId() != signal.GetResult().GetFlowId() {
		return
	}
	if _, exists := wait.completed[signal.GetSubFlowIndex()]; !exists {
		wait.completed[signal.GetSubFlowIndex()] = signal.GetResult()
	}
}

func (t *subFlowTracker) completed(stepExecutionID string) map[int32]*dexpb.FlowResult {
	wait := t.waits[stepExecutionID]
	if wait == nil {
		return nil
	}
	return wait.completed
}

func (t *subFlowTracker) unregister(stepExecutionID string) {
	delete(t.waits, stepExecutionID)
}

func subFlowConditionAt(condition *dexpb.WaitingCondition, index int32) *dexpb.SubFlowCondition {
	if condition == nil || index < 0 || int(index) >= len(condition.GetSubFlowConditions()) {
		return nil
	}
	return condition.GetSubFlowConditions()[index]
}
