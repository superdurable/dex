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

type SubFlowStarter struct {
	provider         interfaces.WorkflowProvider
	activities       *Activities
	tracker          *SubFlowTracker
	stepExecutionID  string
	parentFlowConfig *dexpb.FlowConfig
	condition        *dexpb.WaitingCondition
	doneByIdx        []bool
	startErr         error
}

func NewSubFlowStarter(
	provider interfaces.WorkflowProvider,
	activities *Activities,
	tracker *SubFlowTracker,
	stepExecutionID string,
	parentFlowConfig *dexpb.FlowConfig,
	condition *dexpb.WaitingCondition,
) *SubFlowStarter {
	return &SubFlowStarter{
		provider:         provider,
		activities:       activities,
		tracker:          tracker,
		stepExecutionID:  stepExecutionID,
		parentFlowConfig: parentFlowConfig,
		condition:        condition,
		doneByIdx:        make([]bool, len(condition.GetSubFlowConditions())),
	}
}

func (s *SubFlowStarter) StartAll(ctx interfaces.UnifiedContext) error {
	ctx = s.provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
		StartToCloseTimeout:                 30 * time.Second,
		LocalActivityScheduleToCloseTimeout: 2 * time.Minute,
	})
	for index := range s.condition.GetSubFlowConditions() {
		startCtx := s.provider.ExtendContextWithValue(ctx, "subFlowIndex", index)
		s.provider.GoNamed(startCtx, fmt.Sprintf("start-sub-flow-%d", index), s.startOne)
	}
	if err := s.provider.Await(ctx, s.isAllDone); err != nil {
		return err
	}
	return s.startErr
}

func (s *SubFlowStarter) startOne(ctx interfaces.UnifiedContext) {
	index, ok := s.provider.GetContextValue(ctx, "subFlowIndex").(int)
	if !ok {
		panic("cannot read SubFlow index from workflow context")
	}
	condition := s.condition.GetSubFlowConditions()[index]
	var output dexpb.StartSubFlowActivityOutput
	err := s.provider.ExecuteLocalActivity(
		&output,
		ctx,
		s.activities.StartSubFlow,
		&dexpb.StartSubFlowActivityInput{
			Condition:             condition,
			ParentFlowConfig:      s.parentFlowConfig,
			ParentStepExecutionId: s.stepExecutionID,
		},
	)
	if err != nil && s.startErr == nil {
		s.startErr = err
	}
	if err == nil {
		s.tracker.ApplyImmediateFlowResultFromStart(s.stepExecutionID, int32(index), &output)
	}
	s.doneByIdx[index] = true
}

func (s *SubFlowStarter) isAllDone() bool {
	for _, done := range s.doneByIdx {
		if !done {
			return false
		}
	}
	return true
}
