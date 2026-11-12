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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// settingsLaunch is a launch document with suggested models and reasoning
// efforts.
func settingsLaunch() launchcontract.LaunchDocument {
	model := "gpt-5.2"
	return launchcontract.LaunchDocument{
		Command: launchcontract.LaunchCommand{Name: "dex-agents-test", Harness: "test"},
		Session: launchcontract.LaunchSession{FlowType: "Test", SessionID: "session-1"},
		Display: launchcontract.LaunchDisplay{Model: &model, Models: []string{"gpt-5.2", "gpt-5-mini"}},
		Capabilities: launchcontract.LaunchCapabilities{
			ReasoningEffort: &launchcontract.ReasoningEffortValues{Values: []string{"low", "medium", "high"}},
		},
	}
}

// oldLaunch is a launch document of a launcher without models or efforts.
func oldLaunch() launchcontract.LaunchDocument {
	launch := settingsLaunch()
	launch.Display.Models, launch.Capabilities.ReasoningEffort = nil, nil
	return launch
}

// newSettingsFeed returns a feed over sessions whose loop only counts
// refresh requests.
func newSettingsFeed(t *testing.T, launch launchcontract.LaunchDocument, sessions *fakeSessions) (*RemoteSessionFeed, *atomic.Int64) {
	t.Helper()
	feed := NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: sessions,
		Requests: noLocalRequests{},
		Launch:   launch,
		Timings:  FeedTimings{LockConflictBudget: 200 * time.Millisecond, SnapshotTimeout: time.Second, RPCTimeout: time.Second},
	})
	var refreshes atomic.Int64
	go func() {
		for input := range feed.inputs {
			if _, isRefresh := input.(refreshRequested); isRefresh {
				refreshes.Add(1)
			}
		}
	}()
	return feed, &refreshes
}

func sessionsWithSettings(settings map[string]any) *fakeSessions {
	snapshot := snapshotOf(idleState(0))
	snapshot.SessionSettings = settings
	return &fakeSessions{snapshot: snapshot}
}

// /model and /effort write the whole settings object, keeping other keys,
// and refresh the snapshot after each change.
func TestModelAndEffortWriteTheWholeSessionSettings(t *testing.T) {
	sessions := sessionsWithSettings(map[string]any{"mode": "fast"})
	feed, refreshes := newSettingsFeed(t, settingsLaunch(), sessions)

	require.Equal(t, []string{"The next turn uses gpt-5-mini."},
		noticeTexts(commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: modelCommandName, Argument: "gpt-5-mini"})))
	require.Equal(t, []string{"The next turn uses high reasoning effort."},
		noticeTexts(commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: effortCommandName, Argument: "high"})))
	require.Equal(t, []string{"The next turn uses the model's default reasoning effort."},
		noticeTexts(commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: effortCommandName, Argument: "default"})))

	require.Equal(t, []map[string]any{
		{"mode": "fast", "model": "gpt-5-mini"},
		{"mode": "fast", "model": "gpt-5-mini", "reasoning_effort": "high"},
		{"mode": "fast", "model": "gpt-5-mini"},
	}, sessions.recordedSettings())
	require.Eventually(t, func() bool { return refreshes.Load() == 3 }, time.Second, 5*time.Millisecond)
}

// /effort takes only a declared value or default, and says the choices.
func TestEffortRejectsAnUndeclaredValue(t *testing.T) {
	sessions := sessionsWithSettings(map[string]any{"reasoning_effort": "low"})
	feed, _ := newSettingsFeed(t, settingsLaunch(), sessions)
	notices := commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: effortCommandName, Argument: "bogus"})
	require.Equal(t, []sessionfeed.NoticeRaised{{
		Severity: sessionfeed.NoticeSeverityError,
		Text:     `Unknown reasoning effort "bogus"; choose one of low, medium, high, default.`,
	}}, notices)
	require.Equal(t, []string{"The reasoning effort is low. Choices: low, medium, high, default."},
		noticeTexts(commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: effortCommandName})))
	require.Empty(t, sessions.recordedSettings())
}

// A launcher without reasoning efforts gets a notice and no RPC.
func TestEffortWithoutDeclaredValuesSaysUnsupported(t *testing.T) {
	sessions := sessionsWithSettings(nil)
	feed, _ := newSettingsFeed(t, oldLaunch(), sessions)
	for _, argument := range []string{"", "high"} {
		notices := commandNotices(t, feed, sessionfeed.RunSlashCommand{Name: effortCommandName, Argument: argument})
		require.Equal(t, []sessionfeed.NoticeRaised{{Severity: sessionfeed.NoticeSeverityInfo, Text: "This agent has no reasoning effort setting."}}, notices)
	}
	require.Empty(t, sessions.recordedSettings())
	var names []string
	for _, command := range feed.SlashCommands() {
		names = append(names, command.Name)
	}
	require.Contains(t, names, effortCommandName, "an old launcher still gets /effort, which explains itself")
}

// An /effort accepted for the previous session never writes the new one.
func TestEffortOfASwitchedSessionDoesNotRun(t *testing.T) {
	sessions := sessionsWithSettings(nil)
	feed, _ := newSettingsFeed(t, settingsLaunch(), sessions)
	ctx := sessionfeed.TargetSessionContext(context.Background(), "session-0")
	require.NoError(t, feed.SubmitUserAction(ctx, sessionfeed.RunSlashCommand{Name: effortCommandName, Argument: "high"}))
	require.Equal(t, []string{"Not sent: the chat switched sessions."}, noticeTexts(updatesOfType[sessionfeed.NoticeRaised](drainFeed(feed))))
	require.Empty(t, sessions.recordedSettings())
}

// SessionOpened carries the suggested models, the declared efforts, and the
// session's effort, which a removed key clears.
func TestSessionOpenedCarriesModelsAndReasoningEffort(t *testing.T) {
	feed, _ := newSettingsFeed(t, settingsLaunch(), &fakeSessions{})
	model := newSessionModel(feed, "session-1", 0, false)
	model.emitOpened()
	snapshot := snapshotOf(idleState(0))
	snapshot.SessionSettings = map[string]any{"model": "gpt-5-mini", "reasoning_effort": "high"}
	model.applySnapshot(snapshot)
	snapshot.SessionSettings = map[string]any{"model": "gpt-5-mini"}
	model.applySnapshot(snapshot)

	opened := updatesOfType[sessionfeed.SessionOpened](drainFeed(feed))
	require.Len(t, opened, 3)
	require.Equal(t, []string{"gpt-5.2", "gpt-5-mini"}, opened[0].Models)
	require.Equal(t, []string{"low", "medium", "high"}, opened[0].ReasoningEffortValues)
	require.Equal(t, "gpt-5.2", opened[0].Model)
	require.Equal(t, "", opened[0].ReasoningEffort)
	require.Equal(t, "gpt-5-mini", opened[1].Model)
	require.Equal(t, "high", opened[1].ReasoningEffort)
	require.Equal(t, "", opened[2].ReasoningEffort, "default removed the key")
}

// An old launch document offers display.model only and no efforts.
func TestSessionOpenedOfAnOldLaunchDocument(t *testing.T) {
	feed, _ := newSettingsFeed(t, oldLaunch(), &fakeSessions{})
	model := newSessionModel(feed, "session-1", 0, false)
	model.emitOpened()
	opened := updatesOfType[sessionfeed.SessionOpened](drainFeed(feed))
	require.Len(t, opened, 1)
	require.Equal(t, []string{"gpt-5.2"}, opened[0].Models)
	require.Nil(t, opened[0].ReasoningEffortValues)
}

// Without display.model and display.models, /model offers nothing.
func TestSessionOpenedWithoutAnyModel(t *testing.T) {
	launch := oldLaunch()
	launch.Display.Model = nil
	feed, _ := newSettingsFeed(t, launch, &fakeSessions{snapshot: dexagents.AgentSnapshot{}})
	model := newSessionModel(feed, "session-1", 0, false)
	model.emitOpened()
	opened := updatesOfType[sessionfeed.SessionOpened](drainFeed(feed))
	require.Len(t, opened, 1)
	require.Nil(t, opened[0].Models)
}
