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

type subFlowStarter struct {
	provider         interfaces.WorkflowProvider
	activities       *Activities
	tracker          *subFlowTracker
	stepExecutionID  string
	parentFlowConfig *dexpb.FlowConfig
	condition        *dexpb.WaitingCondition
	done             []bool
	err              error
}

func newSubFlowStarter(
	provider interfaces.WorkflowProvider,
	activities *Activities,
	tracker *subFlowTracker,
	stepExecutionID string,
	parentFlowConfig *dexpb.FlowConfig,
	condition *dexpb.WaitingCondition,
) *subFlowStarter {
	return &subFlowStarter{
		provider:         provider,
		activities:       activities,
		tracker:          tracker,
		stepExecutionID:  stepExecutionID,
		parentFlowConfig: parentFlowConfig,
		condition:        condition,
		done:             make([]bool, len(condition.GetSubFlowConditions())),
	}
}

func (s *subFlowStarter) startAll(ctx interfaces.UnifiedContext) error {
	ctx = s.provider.WithActivityOptions(ctx, interfaces.ActivityOptions{
		StartToCloseTimeout:                 30 * time.Second,
		LocalActivityScheduleToCloseTimeout: 2 * time.Minute,
	})
	for index := range s.condition.GetSubFlowConditions() {
		startCtx := s.provider.ExtendContextWithValue(ctx, "subFlowIndex", index)
		s.provider.GoNamed(startCtx, fmt.Sprintf("start-sub-flow-%d", index), s.startOne)
	}
	if err := s.provider.Await(ctx, s.allDone); err != nil {
		return err
	}
	return s.err
}

func (s *subFlowStarter) startOne(ctx interfaces.UnifiedContext) {
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
			Condition:        condition,
			ParentFlowConfig: s.parentFlowConfig,
		},
	)
	if err != nil && s.err == nil {
		s.err = err
	}
	if err == nil {
		s.tracker.applyStartResult(s.stepExecutionID, int32(index), &output)
	}
	s.done[index] = true
}

func (s *subFlowStarter) allDone() bool {
	for _, done := range s.done {
		if !done {
			return false
		}
	}
	return true
}
