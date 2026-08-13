// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import (
	"github.com/superdurable/dex/gen/dexpb"
	"github.com/superdurable/dex/service"
)

type subFlowTracker struct {
	waits    map[string]*subFlowWait
	byFlowID map[string]*trackedSubFlow
}

type subFlowWait struct {
	condition *dexpb.WaitingCondition
	completed map[int32]*dexpb.FlowResult
}

type trackedSubFlow struct {
	wait  *subFlowWait
	index int32
}

func newSubFlowTracker(resumeInfos []*dexpb.StepExecutionResumeInfo) *subFlowTracker {
	tracker := &subFlowTracker{
		waits:    map[string]*subFlowWait{},
		byFlowID: map[string]*trackedSubFlow{},
	}
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
	wait := &subFlowWait{
		condition: condition,
		completed: completed,
	}
	t.waits[stepExecutionID] = wait
	for index, subFlowCondition := range condition.GetSubFlowConditions() {
		flowID := service.SubFlowID(
			subFlowCondition.GetParentFlowId(), stepExecutionID, int32(index),
		)
		t.byFlowID[flowID] = &trackedSubFlow{wait: wait, index: int32(index)}
	}
}

func (t *subFlowTracker) applyStartResult(
	stepExecutionID string, index int32, result *dexpb.StartSubFlowActivityOutput,
) {
	wait := t.waits[stepExecutionID]
	if wait == nil || result == nil {
		return
	}
	if index < 0 || int(index) >= len(wait.condition.GetSubFlowConditions()) {
		return
	}
	if result.GetImmediateFlowResult() != nil {
		if _, exists := wait.completed[index]; !exists {
			wait.completed[index] = result.GetImmediateFlowResult()
		}
	}
}

func (t *subFlowTracker) handleCompletion(signal *dexpb.SubFlowCompletionSignalRequest) {
	result := signal.GetFlowResult()
	if result == nil {
		return
	}
	tracked := t.byFlowID[signal.GetSubFlowId()]
	if tracked == nil {
		return
	}
	if _, exists := tracked.wait.completed[tracked.index]; !exists {
		tracked.wait.completed[tracked.index] = result
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
	wait := t.waits[stepExecutionID]
	if wait == nil {
		return
	}
	for index, condition := range wait.condition.GetSubFlowConditions() {
		delete(t.byFlowID, service.SubFlowID(
			condition.GetParentFlowId(), stepExecutionID, int32(index),
		))
	}
	delete(t.waits, stepExecutionID)
}
