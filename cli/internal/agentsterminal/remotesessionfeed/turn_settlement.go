// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	dexagents "github.com/superdurable/dex/agents/go/core"
)

// maxSettlementInputs bounds the snapshots and events buffered while a turn
// waits for its end event. The wait lasts at most TurnEndEventWait, so the
// bound is rarely met; past it, a new snapshot replaces the last buffered
// input and a new event is dropped, as live events are best effort.
const maxSettlementInputs = 1024

// bufferedInput is one snapshot or one event that arrived while a turn
// settles. Exactly one field is set.
type bufferedInput struct {
	snapshot *dexagents.AgentSnapshot
	event    *dexagents.AgentEvent
}

func (model *sessionModel) bufferWhileSettling(input bufferedInput) {
	if len(model.settlementInputs) < maxSettlementInputs {
		model.settlementInputs = append(model.settlementInputs, input)
		return
	}
	if input.snapshot != nil {
		model.settlementInputs[len(model.settlementInputs)-1] = input
	}
}

// canSettle reports whether the settling turn may end now: its end event
// came, live events are off, or its wait ran out.
func (model *sessionModel) canSettle() bool {
	turnID := model.settlingTurnID
	if turnID == "" || model.finishedTurns[turnID] {
		return true
	}
	if _, hasEndEvent := model.endEvents[turnID]; hasEndEvent || !model.isEventsAvailable {
		return true
	}
	deadline, isWaiting := model.turnEndDeadlines[turnID]
	return isWaiting && !model.feed.now().Before(deadline)
}

// replayWhenSettled replays the buffered inputs in arrival order through the
// normal path once the settling turn may end. The first snapshot ends it; a
// later turn that must wait in turn buffers the rest again.
func (model *sessionModel) replayWhenSettled() {
	if model.settlingTurnID == "" || !model.canSettle() {
		return
	}
	inputs := model.settlementInputs
	model.settlementInputs = nil
	settledTurnID := model.settlingTurnID
	var lastSnapshot dexagents.AgentSnapshot
	for index, input := range inputs {
		if model.settlingTurnID != "" && model.settlingTurnID != settledTurnID && !model.canSettle() {
			model.settlementInputs = append(model.settlementInputs, inputs[index:]...)
			return
		}
		if input.snapshot != nil {
			lastSnapshot = *input.snapshot
			model.applySnapshotNow(lastSnapshot)
		} else {
			model.applyEventNow(*input.event)
		}
	}
	if model.settlingTurnID == settledTurnID && len(model.settlementInputs) == 0 {
		// No buffered snapshot named a later turn: end this one now.
		model.finishTurn(settledTurnID, lastSnapshot)
	}
}

// trySettle replays once live events turned off or a wait ran out without a
// new snapshot.
func (model *sessionModel) trySettle() {
	model.replayWhenSettled()
}
