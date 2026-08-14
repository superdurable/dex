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
	"github.com/superdurable/dex/service/interpreter/interfaces"
	"github.com/superdurable/dex/service/interpreter/timers"
)

func convertToWaitingConditionState(
	ctx interfaces.UnifiedContext,
	provider interfaces.WorkflowProvider,
	returnedWaitingCondition *dexpb.WaitingCondition,
) *dexpb.WaitingConditionState {
	if returnedWaitingCondition == nil {
		return &dexpb.WaitingConditionState{}
	}
	returnedWaitingCondition = timers.FixTimerConditionFromActivityOutput(
		provider.Now(ctx), returnedWaitingCondition,
	)
	state := &dexpb.WaitingConditionState{
		WaitingConditionType:  returnedWaitingCondition.GetWaitingConditionType(),
		TimerConditions:       returnedWaitingCondition.GetTimerConditions(),
		ChannelConditions:     returnedWaitingCondition.GetChannelConditions(),
		ConditionCombinations: returnedWaitingCondition.GetConditionCombinations(),
		SubFlowConditions: make(
			[]*dexpb.SubFlowConditionState,
			0,
			len(returnedWaitingCondition.GetSubFlowConditions()),
		),
	}
	for _, subFlowCondition := range returnedWaitingCondition.GetSubFlowConditions() {
		state.SubFlowConditions = append(
			state.SubFlowConditions,
			&dexpb.SubFlowConditionState{ConditionId: subFlowCondition.GetConditionId()},
		)
	}
	return state
}
