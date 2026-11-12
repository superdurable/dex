// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// heldStartRequests serves start_session only when the test releases it, and
// records every call.
type heldStartRequests struct {
	noLocalRequests
	release chan struct{}
	mutex   sync.Mutex
	methods []string
}

func (requests *heldStartRequests) HasMethod(method string) bool {
	return method == launchcontract.MethodStartSession
}

func (requests *heldStartRequests) Call(_ context.Context, method string, _ string, _ any) launcherchannel.RequestOutcome {
	requests.record(method)
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeChannelEnded}
}

func (requests *heldStartRequests) CallWithRecoveryKey(_ context.Context, method string, key string, _ any, _ func()) launcherchannel.RequestOutcome {
	requests.record(method)
	<-requests.release
	shortID, _, err := launchcontract.CandidateSessionIDs("test", key)
	if err != nil {
		panic(err)
	}
	result, err := json.Marshal(map[string]any{"session_id": shortID})
	if err != nil {
		panic(err)
	}
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeDone, Result: result}
}

func (requests *heldStartRequests) record(method string) {
	requests.mutex.Lock()
	defer requests.mutex.Unlock()
	requests.methods = append(requests.methods, method)
}

func (requests *heldStartRequests) calls() []string {
	requests.mutex.Lock()
	defer requests.mutex.Unlock()
	return append([]string(nil), requests.methods...)
}

// Esc sends no cancel, a second /new while one starts sends nothing, and the
// result comes as SessionStartFinished before the wait ends, for the screen
// to decide.
func TestSessionStartInTheBackground(t *testing.T) {
	requests := &heldStartRequests{release: make(chan struct{})}
	feed := NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: &fakeSessions{},
		Requests: requests,
		Launch:   launchcontract.LaunchDocument{Command: launchcontract.LaunchCommand{Harness: "test"}, Methods: []string{launchcontract.MethodStartSession}},
	})
	go func() {
		for range feed.inputs {
		}
	}()
	started := make(chan error, 1)
	go func() {
		started <- feed.SubmitUserAction(context.Background(), sessionfeed.RunSlashCommand{Name: newCommandName})
	}()
	require.Eventually(t, func() bool { return len(requests.calls()) == 1 }, time.Second, 5*time.Millisecond)
	require.NoError(t, feed.SubmitUserAction(context.Background(), sessionfeed.MoveSessionStartToBackground{}))
	require.NoError(t, feed.SubmitUserAction(context.Background(), sessionfeed.RunSlashCommand{Name: newCommandName}))
	require.Equal(t, []string{launchcontract.MethodStartSession}, requests.calls(), "no cancel and no second start_session")
	close(requests.release)
	require.NoError(t, <-started)
	var finishedIndex, endedIndex int
	updates := drainFeed(feed)
	for index, update := range updates {
		switch typed := update.(type) {
		case sessionfeed.SessionStartFinished:
			finishedIndex = index
			require.Regexp(t, `^test-\d{8}-\d{6}-[0-9a-f]{6}$`, typed.SessionID)
		case sessionfeed.SessionStartChanged:
			if !typed.IsWaiting {
				endedIndex = index
			}
		}
	}
	require.NotZero(t, finishedIndex)
	require.Less(t, finishedIndex, endedIndex, "the screen reads the result while it still knows how the user waits")
	notices := updatesOfType[sessionfeed.NoticeRaised](updates)
	require.Contains(t, noticeTexts(notices), "A new session is already starting.")
}

func drainFeed(feed *RemoteSessionFeed) []sessionfeed.SessionUpdate {
	var updates []sessionfeed.SessionUpdate
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		update, err := feed.NextSessionUpdate(ctx)
		cancel()
		if err != nil {
			return updates
		}
		updates = append(updates, update)
	}
}

func noticeTexts(notices []sessionfeed.NoticeRaised) []string {
	texts := make([]string, 0, len(notices))
	for _, notice := range notices {
		texts = append(texts, notice.Text)
	}
	return texts
}

// While A waits for its end event, B's question and activity wait too, and
// come after A ended and B started.
func TestNextTurnInterruptsWaitForThePreviousTurn(t *testing.T) {
	harness := newModelHarness(t)
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "a")))
	harness.drain()
	question := "Which color?"
	bAsks := snapshotOf(runningState("turn-2", 1),
		message(1, "turn-1", dexagents.MessageRoleUser, "a"),
		message(2, "turn-2", dexagents.MessageRoleUser, "b"))
	bAsks.PendingInterrupts = []dexagents.AgentInterrupt{{
		InterruptID: "question-1", Kind: dexagents.InterruptKindQuestion, ToolCallID: "call-1", ToolName: "ask_user", Question: &question,
	}}
	harness.applySnapshot(bAsks)
	held := harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.QuestionAsked](held))
	require.Empty(t, updatesOfType[sessionfeed.TurnActivityChanged](held))
	require.Empty(t, updatesOfType[sessionfeed.TurnStarted](held))
	harness.model.applyEvent(endEvent("turn-1", "turn_completed", `{}`))
	harness.applySnapshot(bAsks)
	updates := harness.drain()
	order := map[string]int{}
	for index, update := range updates {
		switch typed := update.(type) {
		case sessionfeed.TurnEnded:
			order["ended "+typed.TurnID] = index
		case sessionfeed.TurnStarted:
			order["started "+typed.TurnID] = index
		case sessionfeed.QuestionAsked:
			order["asked"] = index
		case sessionfeed.TurnActivityChanged:
			require.Equal(t, "turn-2", typed.TurnID, "the activity is B's")
			require.Equal(t, sessionfeed.TurnActivityAwaitingUser, typed.Activity)
			order["activity"] = index
		}
	}
	require.Len(t, order, 4)
	require.Less(t, order["ended turn-1"], order["started turn-2"])
	require.Less(t, order["started turn-2"], order["asked"])
	require.Less(t, order["started turn-2"], order["activity"])
}
