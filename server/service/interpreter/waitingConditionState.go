// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package interpreter

import "github.com/superdurable/dex/gen/dexpb"

func newWaitingConditionState(condition *dexpb.WaitingCondition) *dexpb.WaitingConditionState {
	state := &dexpb.WaitingConditionState{
		WaitingConditionType:  condition.GetWaitingConditionType(),
		TimerConditions:       condition.GetTimerConditions(),
		ChannelConditions:     condition.GetChannelConditions(),
		ConditionCombinations: condition.GetConditionCombinations(),
		SubFlowConditions: make(
			[]*dexpb.SubFlowConditionState, 0, len(condition.GetSubFlowConditions()),
		),
	}
	for _, subFlowCondition := range condition.GetSubFlowConditions() {
		state.SubFlowConditions = append(
			state.SubFlowConditions,
			&dexpb.SubFlowConditionState{ConditionId: subFlowCondition.GetConditionId()},
		)
	}
	return state
}
