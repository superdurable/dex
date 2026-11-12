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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
)

// A message queued before a resumed chat loaded carries no origin.
func TestMessageQueuedBeforeTheChatCarriesNoOrigin(t *testing.T) {
	harness := newModelHarness(t)
	harness.model = newSessionModel(harness.feed, "session-1", 1, true)
	first := snapshotOf(idleState(2), message(1, "turn-2", dexagents.MessageRoleUser, "wait g2"))
	first.QueuedMessages = []dexagents.PendingUserMessage{{MessageID: "message-1", Message: dexagents.AgentUserMessage{Text: "long 40"}}}
	harness.applySnapshot(first)
	harness.drain()

	harness.applySnapshot(snapshotOf(runningState("turn-3", 2),
		message(1, "turn-2", dexagents.MessageRoleUser, "wait g2"),
		message(3, "turn-3", dexagents.MessageRoleUser, "long 40"),
		message(4, "turn-3", dexagents.MessageRoleUser, "from Dex Web")))
	recorded := updatesOfType[sessionfeed.UserMessageRecorded](harness.drain())
	require.Len(t, recorded, 2)
	require.Equal(t, "long 40", recorded[0].Text)
	require.Empty(t, recorded[0].Origin, "it was queued before this chat")
	require.Equal(t, otherClientOrigin, recorded[1].Origin)
}

// The history marks turns that the recent events show as cancelled.
func TestHistoryMarksCancelledTurns(t *testing.T) {
	harness := newModelHarness(t)
	// The newest page holds turn-2's cancel among many deltas; turn-1's end
	// is two pages back, so the reader goes on until it has it.
	pages := [][]dexagents.AgentEvent{
		{endEvent("turn-2", "turn_cancelled", "{}"), textEvent("turn-2", "step-2", 1, "partial")},
		{textEvent("turn-2", "step-2", 1, "more")},
		{endEvent("turn-1", "turn_completed", "{}"), textEvent("turn-1", "step-1", 1, "done")},
		{endEvent("turn-0", "turn_cancelled", "{}")},
	}
	var requestedTokens []string
	harness.sessions.listEvents = func(_ int32, beforePageToken string) (dex.StreamMessagesPage[dexagents.AgentEvent], error) {
		requestedTokens = append(requestedTokens, beforePageToken)
		index := len(requestedTokens) - 1
		page := dex.StreamMessagesPage[dexagents.AgentEvent]{NextPageToken: fmt.Sprintf("page-%d", index+1)}
		for _, event := range pages[index] {
			page.Messages = append(page.Messages, dex.ListedStreamMessage[dexagents.AgentEvent]{Value: event})
		}
		return page, nil
	}
	cancelled := readHistoryTurnEnds(context.Background(), harness.feed, "session-1", "turn-1")
	require.Equal(t, map[string]bool{"turn-2": true, "turn-1": false}, cancelled)
	require.Equal(t, []string{"", "page-1", "page-2"}, requestedTokens, "it stops at the oldest shown turn's end")

	harness.model = newSessionModel(harness.feed, "session-1", 1, true)
	harness.model.emitHistory(dexagents.AgentMessagesPage{Messages: []dexagents.AgentMessage{
		message(1, "turn-1", dexagents.MessageRoleUser, "first"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "done"),
		message(3, "turn-2", dexagents.MessageRoleUser, "wait"),
	}}, nil, cancelled)
	ended := updatesOfType[sessionfeed.TurnEnded](harness.drain())
	require.Equal(t, []sessionfeed.TurnEnded{
		{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCompleted},
		{TurnID: "turn-2", Outcome: sessionfeed.TurnOutcomeCancelled},
	}, ended)
}

// An unreachable server is named once per outage, not as a busy Worker.
func TestUnreachableServerIsSaidOnce(t *testing.T) {
	harness := newModelHarness(t)
	loop := newSessionLoop(harness.feed)
	loop.ctx = context.Background()
	loop.refreshTimer = time.NewTimer(time.Hour)
	t.Cleanup(func() { loop.refreshTimer.Stop() })
	loop.model = newSessionModel(harness.feed, "session-1", 0, false)
	unavailable := &dex.ServiceError{Code: codes.Unavailable}
	loop.applyFetchFailure(unavailable)
	loop.applyFetchFailure(unavailable)
	updates := harness.drain()
	require.Equal(t, []string{"Dex server unreachable; retrying."}, noticeTexts(updatesOfType[sessionfeed.NoticeRaised](updates)))
	require.Len(t, updatesOfType[sessionfeed.ServerReachabilityChanged](updates), 1)

	loop.isUnreachable = false
	loop.applyFetchFailure(&dex.ServiceError{Code: codes.NotFound})
	require.Equal(t, []string{"Session not loaded yet: the Worker is busy or unreachable. Retrying."}, noticeTexts(updatesOfType[sessionfeed.NoticeRaised](harness.drain())))
}

// failingLocalRequests fails every local request with one error code.
type failingLocalRequests struct {
	noLocalRequests
	code    string
	message string
}

func (failingLocalRequests) HasMethod(string) bool { return true }
func (requests failingLocalRequests) Call(context.Context, string, string, any) launcherchannel.RequestOutcome {
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeFailed, Error: &launchcontract.ChannelError{Code: requests.code, Message: requests.message}}
}
func (requests failingLocalRequests) CallWithRecoveryKey(ctx context.Context, method string, key string, params any, _ func()) launcherchannel.RequestOutcome {
	return requests.Call(ctx, method, key, params)
}

func newCommandFeed(t *testing.T, requests LocalRequests, home string) *RemoteSessionFeed {
	t.Helper()
	webURL, model := "http://127.0.0.1:8802", "gpt-5.2"
	feed := NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: &fakeSessions{},
		Requests: requests,
		Launch: launchcontract.LaunchDocument{
			Command:      launchcontract.LaunchCommand{Harness: "test"},
			Session:      launchcontract.LaunchSession{FlowType: "Test", SessionID: "session-1"},
			Dex:          launchcontract.DexConnection{WebURL: &webURL},
			Display:      launchcontract.LaunchDisplay{Model: &model},
			Methods:      []string{launchcontract.MethodOpenURL, launchcontract.MethodPutImage},
			Capabilities: launchcontract.LaunchCapabilities{Images: &launchcontract.ImageLimits{MaxPerMessage: 4, MaxBytes: 1 << 20}},
		},
		Timings:          FeedTimings{LockConflictBudget: 200 * time.Millisecond, SnapshotTimeout: time.Second, RPCTimeout: time.Second},
		HomeDirectory:    home,
		WorkingDirectory: home,
	})
	go func() {
		for range feed.inputs {
		}
	}()
	return feed
}

func commandNotices(t *testing.T, feed *RemoteSessionFeed, command sessionfeed.RunSlashCommand) []sessionfeed.NoticeRaised {
	t.Helper()
	require.NoError(t, feed.SubmitUserAction(context.Background(), command))
	return updatesOfType[sessionfeed.NoticeRaised](drainFeed(feed))
}

// With the browser switched off, /web gives the link as information.
func TestWebWithTheBrowserOffShowsTheLink(t *testing.T) {
	feed := newCommandFeed(t, failingLocalRequests{code: launchcontract.ErrorCodeBrowserOff, message: "Opening a browser is turned off."}, t.TempDir())
	notices := commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: webCommandName})
	require.Equal(t, []sessionfeed.NoticeRaised{{Severity: sessionfeed.NoticeSeverityInfo, Text: "Dex Web: http://127.0.0.1:8802/v1/flows/session-1"}}, notices)
}

func TestImageNoticesWriteTheHomeDirectoryAsTilde(t *testing.T) {
	home := t.TempDir()
	feed := newCommandFeed(t, failingLocalRequests{}, home)
	notices := commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: imageCommandName, Argument: "~/missing.png"})
	require.Equal(t, []string{"Image not attached: ~/missing.png does not exist."}, noticeTexts(notices))
}

// /model alone shows the model; an ID with spaces changes nothing.
func TestModelCommandShowsTheModelAndRejectsSpaces(t *testing.T) {
	feed := newCommandFeed(t, failingLocalRequests{}, t.TempDir())
	feed.sessions.(*fakeSessions).snapshot = snapshotOf(idleState(0))
	require.Equal(t, []string{"The model is gpt-5.2 (the command's default). Type a model ID after /model to change it."},
		noticeTexts(commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: modelCommandName})))
	notices := commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: modelCommandName, Argument: "gpt 5"})
	require.Equal(t, []sessionfeed.NoticeRaised{{Severity: sessionfeed.NoticeSeverityError, Text: "A model ID has no spaces; the model did not change."}}, notices)
	commands, err := json.Marshal(feed.SlashCommands())
	require.NoError(t, err)
	require.Contains(t, string(commands), `"Name":"model","ArgumentHint":"ID","IsArgumentOptional":true`)
}
