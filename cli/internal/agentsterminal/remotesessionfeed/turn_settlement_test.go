// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// updateIndex returns the index of the first update that isWanted accepts.
func updateIndex(t *testing.T, updates []sessionfeed.SessionUpdate, description string, isWanted func(sessionfeed.SessionUpdate) bool) int {
	t.Helper()
	for index, update := range updates {
		if isWanted(update) {
			return index
		}
	}
	t.Fatalf("no %s among %d updates", description, len(updates))
	return -1
}

func turnEndedOf(turnID string) func(sessionfeed.SessionUpdate) bool {
	return func(update sessionfeed.SessionUpdate) bool {
		ended, isEnded := update.(sessionfeed.TurnEnded)
		return isEnded && ended.TurnID == turnID
	}
}

func turnStartedOf(turnID string) func(sessionfeed.SessionUpdate) bool {
	return func(update sessionfeed.SessionUpdate) bool {
		started, isStarted := update.(sessionfeed.TurnStarted)
		return isStarted && started.TurnID == turnID
	}
}

func startTurnOne(harness *modelHarness) {
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "a")))
	harness.drain()
}

// B's plan, drafted while A waits for its end event, shows after A ended and
// B started.
func TestNextTurnPlanWaitsForThePreviousTurn(t *testing.T) {
	harness := newModelHarness(t)
	startTurnOne(harness)
	bPlans := snapshotOf(runningState("turn-2", 1),
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-2", dexagents.MessageRoleUser, "plan it"))
	bPlans.Plan = &dexagents.AgentPlan{Revision: 1, Status: dexagents.PlanStatusDraft, Tasks: []dexagents.AgentPlanTask{{Content: "write tests"}}}
	harness.applySnapshot(bPlans)
	require.Empty(t, harness.drain(), "nothing of B, nor A's end, while A waits")
	harness.model.applyEvent(endEvent("turn-1", "turn_completed", `{}`))
	updates := harness.drain()
	ended := updateIndex(t, updates, "A's end", turnEndedOf("turn-1"))
	started := updateIndex(t, updates, "B's start", turnStartedOf("turn-2"))
	planned := updateIndex(t, updates, "B's plan", func(update sessionfeed.SessionUpdate) bool {
		_, isPlan := update.(sessionfeed.PlanChanged)
		return isPlan
	})
	require.Less(t, ended, started)
	require.Less(t, started, planned)
}

// A question of B that appears and is answered by another client while A
// waits still shows, then resolves, after the wait runs out.
func TestShortLivedQuestionDuringTheWaitIsReplayed(t *testing.T) {
	harness := newModelHarness(t)
	startTurnOne(harness)
	question := "Which color?"
	messages := []dexagents.AgentMessage{
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-2", dexagents.MessageRoleUser, "b"),
	}
	asking := snapshotOf(runningState("turn-2", 1), messages...)
	asking.PendingInterrupts = []dexagents.AgentInterrupt{{
		InterruptID: "question-1", Kind: dexagents.InterruptKindQuestion, ToolCallID: "call-1", ToolName: "ask_user", Question: &question,
	}}
	answered := snapshotOf(runningState("turn-2", 1), messages...)
	answered.ToolCalls = []dexagents.AgentToolCall{{ToolCallID: "call-1", TurnID: "turn-2", ToolName: "ask_user", Status: dexagents.ToolCallStatusSucceeded}}
	harness.applySnapshot(asking)
	harness.applySnapshot(answered)
	require.Empty(t, harness.drain(), "B waits for A")
	harness.now = harness.now.Add(3 * time.Second)
	harness.applySnapshot(answered)
	updates := harness.drain()
	ended := updateIndex(t, updates, "A's end", turnEndedOf("turn-1"))
	require.Equal(t, sessionfeed.TurnOutcomeCompleted, updates[ended].(sessionfeed.TurnEnded).Outcome, "the wait ran out without an end event")
	started := updateIndex(t, updates, "B's start", turnStartedOf("turn-2"))
	asked := updateIndex(t, updates, "the question", func(update sessionfeed.SessionUpdate) bool {
		_, isQuestion := update.(sessionfeed.QuestionAsked)
		return isQuestion
	})
	resolved := updateIndex(t, updates, "the answer", func(update sessionfeed.SessionUpdate) bool {
		_, isResolved := update.(sessionfeed.InterruptResolved)
		return isResolved
	})
	require.Less(t, ended, started)
	require.Less(t, started, asked)
	require.Less(t, asked, resolved)
	answer := updates[resolved].(sessionfeed.InterruptResolved)
	require.Equal(t, sessionfeed.InterruptAnswered, answer.Resolution)
	require.Equal(t, otherClientOrigin, answer.ResolvedBy)
}

// B's live text during the wait shows after B started, in order.
func TestNextTurnEventsDuringTheWaitAreReplayedInOrder(t *testing.T) {
	harness := newModelHarness(t)
	startTurnOne(harness)
	bRunning := snapshotOf(runningState("turn-2", 1),
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-2", dexagents.MessageRoleUser, "b"))
	harness.applySnapshot(bRunning)
	harness.model.applyEvent(textEvent("turn-2", "step-b", 1, "Hel"))
	harness.model.applyEvent(textEvent("turn-2", "step-b", 1, "lo"))
	require.Empty(t, harness.drain())
	harness.model.applyEvent(endEvent("turn-1", "turn_failed", `{"error":"model down"}`))
	updates := harness.drain()
	ended := updateIndex(t, updates, "A's end", turnEndedOf("turn-1"))
	require.Equal(t, sessionfeed.TurnOutcomeFailed, updates[ended].(sessionfeed.TurnEnded).Outcome)
	started := updateIndex(t, updates, "B's start", turnStartedOf("turn-2"))
	deltas := updatesOfType[sessionfeed.AssistantTextDelta](updates[started:])
	require.Len(t, deltas, 2)
	require.Equal(t, "Hel", deltas[0].Text)
	require.Equal(t, "lo", deltas[1].Text)
}
