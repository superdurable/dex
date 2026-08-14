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

type SubFlowTracker struct {
	parentFlowID string
	waits        map[string]*subFlowWait
	byFlowID     map[string]*trackedSubFlow
}

type subFlowWait struct {
	condition *dexpb.WaitingConditionState
	completed map[int32]*dexpb.FlowResult
}

type trackedSubFlow struct {
	wait  *subFlowWait
	index int32
}

func NewSubFlowTracker(
	parentFlowID string,
	resumeInfos []*dexpb.StepExecutionResumeInfo,
) *SubFlowTracker {
	if parentFlowID == "" {
		panic("SubFlow tracker requires a parent Flow ID")
	}
	tracker := &SubFlowTracker{
		parentFlowID: parentFlowID,
		waits:        map[string]*subFlowWait{},
		byFlowID:     map[string]*trackedSubFlow{},
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
		tracker.Register(resumeInfo.GetStepExecutionId(), resumeInfo.GetWaitingCondition(), completed)
	}
	return tracker
}

func (t *SubFlowTracker) Register(
	stepExecutionID string,
	condition *dexpb.WaitingConditionState,
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
	for index := range condition.GetSubFlowConditions() {
		flowID := service.SubFlowID(
			t.parentFlowID, stepExecutionID, int32(index),
		)
		t.byFlowID[flowID] = &trackedSubFlow{wait: wait, index: int32(index)}
	}
}

func (t *SubFlowTracker) ApplyStartResult(
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

func (t *SubFlowTracker) HandleCompletion(signal *dexpb.SubFlowCompletionSignalRequest) {
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

func (t *SubFlowTracker) Completed(stepExecutionID string) map[int32]*dexpb.FlowResult {
	wait := t.waits[stepExecutionID]
	if wait == nil {
		return nil
	}
	return wait.completed
}

func (t *SubFlowTracker) Unregister(stepExecutionID string) {
	wait := t.waits[stepExecutionID]
	if wait == nil {
		return
	}
	for index := range wait.condition.GetSubFlowConditions() {
		delete(t.byFlowID, service.SubFlowID(
			t.parentFlowID, stepExecutionID, int32(index),
		))
	}
	delete(t.waits, stepExecutionID)
}
