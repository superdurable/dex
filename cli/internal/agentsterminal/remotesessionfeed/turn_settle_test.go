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

// Turn B's message and active turn appear before A's end event: A waits for
// it, B starts only after A ended, and the late event decides A's outcome.
func TestPreviousTurnWaitsForItsLateEndEvent(t *testing.T) {
	harness := newModelHarness(t)
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "a")))
	harness.drain()
	bRunning := snapshotOf(runningState("turn-2", 1),
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-2", dexagents.MessageRoleUser, "b"))
	harness.applySnapshot(bRunning)
	updates := harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.TurnEnded](updates), "A waits for its end event")
	require.Empty(t, updatesOfType[sessionfeed.TurnStarted](updates), "B waits for A")
	harness.model.applyEvent(endEvent("turn-1", "turn_cancelled", `{}`))
	harness.applySnapshot(bRunning)
	updates = harness.drain()
	ended := updatesOfType[sessionfeed.TurnEnded](updates)
	require.Len(t, ended, 1)
	require.Equal(t, "turn-1", ended[0].TurnID)
	require.Equal(t, sessionfeed.TurnOutcomeCancelled, ended[0].Outcome)
	started := updatesOfType[sessionfeed.TurnStarted](updates)
	require.Len(t, started, 1)
	require.Equal(t, "turn-2", started[0].TurnID)
	endedIndex, startedIndex := -1, -1
	for index, update := range updates {
		switch typed := update.(type) {
		case sessionfeed.TurnEnded:
			endedIndex = index
		case sessionfeed.TurnStarted:
			if typed.TurnID == "turn-2" {
				startedIndex = index
			}
		}
	}
	require.Less(t, endedIndex, startedIndex, "A ends before B starts")
}

// last_turn_error belongs to the last finished turn: when B failed, A
// without an end event still completed.
func TestLastTurnErrorOfALaterTurnIsNotTheEarlierTurns(t *testing.T) {
	harness := newModelHarness(t)
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "a")))
	harness.drain()
	bFailed := snapshotOf(idleState(2),
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "done a"),
		message(3, "turn-2", dexagents.MessageRoleUser, "b"))
	reason := "b broke"
	bFailed.State.LastTurnError = &reason
	for range 3 {
		harness.applySnapshot(bFailed)
		harness.now = harness.now.Add(3 * time.Second)
	}
	harness.applySnapshot(bFailed)
	ended := updatesOfType[sessionfeed.TurnEnded](harness.drain())
	require.Len(t, ended, 2)
	require.Equal(t, "turn-1", ended[0].TurnID)
	require.Equal(t, sessionfeed.TurnOutcomeCompleted, ended[0].Outcome)
	require.Equal(t, "turn-2", ended[1].TurnID)
	require.Equal(t, sessionfeed.TurnOutcomeFailed, ended[1].Outcome)
	require.Contains(t, ended[1].FailureText, reason)
}

// The first snapshot of a new session shows a turn that finished before it,
// for example one another client ran, without waiting for events.
func TestFirstSnapshotOfANewSessionShowsFinishedTurns(t *testing.T) {
	harness := newModelHarness(t)
	harness.model = newSessionModel(harness.feed, "session-2", 1, false)
	harness.applySnapshot(snapshotOf(idleState(1),
		message(1, "turn-1", dexagents.MessageRoleUser, "hello"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "hi")))
	updates := harness.drain()
	require.Len(t, updatesOfType[sessionfeed.UserMessageRecorded](updates), 1)
	require.Len(t, updatesOfType[sessionfeed.TurnStarted](updates), 1)
	recorded := updatesOfType[sessionfeed.AssistantMessageRecorded](updates)
	require.Len(t, recorded, 1)
	require.Equal(t, int64(1)<<sequenceEpochShift+2, recorded[0].Sequence, "a switched session's sequences never collide with the first's")
	ended := updatesOfType[sessionfeed.TurnEnded](updates)
	require.Len(t, ended, 1)
	require.Equal(t, sessionfeed.TurnOutcomeCompleted, ended[0].Outcome)
}

// A resumed session's first snapshot leaves finished turns to the history.
func TestFirstSnapshotOfAResumedSessionSkipsFinishedTurns(t *testing.T) {
	harness := newModelHarness(t)
	harness.model = newSessionModel(harness.feed, "session-1", 0, true)
	harness.applySnapshot(snapshotOf(idleState(1),
		message(1, "turn-1", dexagents.MessageRoleUser, "hello"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "hi")))
	updates := harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.UserMessageRecorded](updates))
	require.Empty(t, updatesOfType[sessionfeed.TurnEnded](updates))
}
