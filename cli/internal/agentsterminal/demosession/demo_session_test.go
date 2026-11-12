// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package demosession

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// demoDriver reads the demo's updates and answers its interrupts.
type demoDriver struct {
	t       *testing.T
	ctx     context.Context
	demo    *DemoSession
	updates []sessionfeed.SessionUpdate
}

func (driver *demoDriver) readUntil(isDone func(sessionfeed.SessionUpdate) bool) sessionfeed.SessionUpdate {
	driver.t.Helper()
	for {
		update, err := driver.demo.NextSessionUpdate(driver.ctx)
		require.NoError(driver.t, err)
		driver.updates = append(driver.updates, update)
		if isDone(update) {
			return update
		}
	}
}

func (driver *demoDriver) submit(action sessionfeed.UserAction) {
	driver.t.Helper()
	require.NoError(driver.t, driver.demo.SubmitUserAction(driver.ctx, action))
}

func newDemoDriver(t *testing.T) *demoDriver {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return &demoDriver{t: t, ctx: ctx, demo: NewDemoSession(DemoSessionConfig{})}
}

func isTurnEnded(update sessionfeed.SessionUpdate) bool {
	_, isEnded := update.(sessionfeed.TurnEnded)
	return isEnded
}

func TestDemoSessionPlaysTheScriptedTurns(t *testing.T) {
	driver := newDemoDriver(t)
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isLoaded := update.(sessionfeed.SessionLoaded)
		return isLoaded
	})
	driver.submit(sessionfeed.SendUserMessage{Text: "Find flaky tests."})
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isQuestion := update.(sessionfeed.QuestionAsked)
		return isQuestion
	})
	driver.submit(sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "main"})
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		approval, isApproval := update.(sessionfeed.ApprovalRequested)
		return isApproval && approval.InterruptID == "approval-2"
	})
	driver.submit(sessionfeed.ResolveApproval{InterruptID: "approval-2", Reason: "not now"})
	driver.submit(sessionfeed.ResolveApproval{InterruptID: "approval-1", IsApproved: true})
	ended := driver.readUntil(isTurnEnded).(sessionfeed.TurnEnded)
	require.Equal(t, sessionfeed.TurnOutcomeCompleted, ended.Outcome)

	var statuses = map[string]sessionfeed.ToolCallStatus{}
	for _, update := range driver.updates {
		if call, isCall := update.(sessionfeed.ToolCallChanged); isCall {
			statuses[call.ToolName] = call.Status
		}
	}
	require.Equal(t, sessionfeed.ToolCallSucceeded, statuses["slack_post_channel_message"])
	require.Equal(t, sessionfeed.ToolCallRejected, statuses["edit_file"])

	driver.submit(sessionfeed.SendUserMessage{Text: "Again."})
	ended = driver.readUntil(isTurnEnded).(sessionfeed.TurnEnded)
	require.Equal(t, sessionfeed.TurnOutcomeFailed, ended.Outcome)
	require.Contains(t, ended.FailureText, "Set OPENAI_API_KEY")
}

func TestDemoSessionCancelWithdrawsPendingWork(t *testing.T) {
	driver := newDemoDriver(t)
	driver.submit(sessionfeed.SendUserMessage{Text: "Find flaky tests."})
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isQuestion := update.(sessionfeed.QuestionAsked)
		return isQuestion
	})
	driver.submit(sessionfeed.CancelTurn{TurnID: "turn-1"})
	ended := driver.readUntil(isTurnEnded).(sessionfeed.TurnEnded)
	require.Equal(t, sessionfeed.TurnOutcomeCancelled, ended.Outcome)
	var isAnswered, isWithdrawn bool
	for _, update := range driver.updates {
		switch typed := update.(type) {
		case sessionfeed.CancelRequestAnswered:
			isAnswered = typed.Outcome == sessionfeed.CancelOutcomeAccepted
		case sessionfeed.InterruptResolved:
			isWithdrawn = typed.InterruptID == "question-1" && typed.Resolution == sessionfeed.InterruptWithdrawn
		}
	}
	require.True(t, isAnswered, "the cancel is accepted")
	require.True(t, isWithdrawn, "the open question is withdrawn")
}

func TestDemoSessionQueuesMessagesDuringATurn(t *testing.T) {
	driver := newDemoDriver(t)
	driver.submit(sessionfeed.SendUserMessage{Text: "Find flaky tests."})
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isQuestion := update.(sessionfeed.QuestionAsked)
		return isQuestion
	})
	driver.submit(sessionfeed.SendUserMessage{Text: "Also check nightly."})
	queued := driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isQueued := update.(sessionfeed.QueuedMessagesChanged)
		return isQueued
	}).(sessionfeed.QueuedMessagesChanged)
	require.Equal(t, []sessionfeed.QueuedMessage{{MessageID: "message-1", Text: "Also check nightly."}}, queued.Messages)
	driver.submit(sessionfeed.CancelTurn{TurnID: "turn-1"})
	driver.readUntil(isTurnEnded)
	recorded := driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		_, isRecorded := update.(sessionfeed.UserMessageRecorded)
		return isRecorded
	}).(sessionfeed.UserMessageRecorded)
	require.Equal(t, "Also check nightly.", recorded.Text, "the queued message starts the next turn")
}

func isNoticeRaised(update sessionfeed.SessionUpdate) bool {
	_, isNotice := update.(sessionfeed.NoticeRaised)
	return isNotice
}

func isSessionOpened(update sessionfeed.SessionUpdate) bool {
	_, isOpened := update.(sessionfeed.SessionOpened)
	return isOpened
}

// The demo's /model and /effort change the session it describes.
func TestDemoSessionChangesTheModelAndEffort(t *testing.T) {
	driver := newDemoDriver(t)
	opened := driver.readUntil(isSessionOpened).(sessionfeed.SessionOpened)
	require.Equal(t, []string{"gpt-5.2", "gpt-5-mini", "gpt-5-nano"}, opened.Models)
	require.Equal(t, []string{"low", "medium", "high"}, opened.ReasoningEffortValues)

	driver.submit(sessionfeed.RunSlashCommand{Name: "effort", Argument: "bogus"})
	notice := driver.readUntil(isNoticeRaised).(sessionfeed.NoticeRaised)
	require.Equal(t, `Unknown reasoning effort "bogus"; choose one of low, medium, high, default.`, notice.Text)

	driver.submit(sessionfeed.RunSlashCommand{Name: "model", Argument: "gpt-5-mini"})
	driver.submit(sessionfeed.RunSlashCommand{Name: "effort", Argument: "high"})
	driver.readUntil(func(update sessionfeed.SessionUpdate) bool {
		changed, isOpened := update.(sessionfeed.SessionOpened)
		return isOpened && changed.ReasoningEffort == "high"
	})
	driver.submit(sessionfeed.RunSlashCommand{Name: "effort", Argument: "default"})
	changed := driver.readUntil(isSessionOpened).(sessionfeed.SessionOpened)
	require.Equal(t, "gpt-5-mini", changed.Model)
	require.Empty(t, changed.ReasoningEffort)
}

func TestDemoSessionStopsWithItsContext(t *testing.T) {
	demo := NewDemoSession(DemoSessionConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	_, err := demo.NextSessionUpdate(ctx)
	require.NoError(t, err)
	cancel()
	_, err = demo.NextSessionUpdate(context.Background())
	require.True(t, err == nil || errors.Is(err, sessionfeed.ErrSessionFeedClosed), "unexpected error %v", err)
	require.Eventually(t, func() bool { return errors.Is(demo.ScriptError(), context.Canceled) }, 5*time.Second, 10*time.Millisecond)
}
