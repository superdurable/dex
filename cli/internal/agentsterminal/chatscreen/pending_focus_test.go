// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

func queueTwoMessages(harness *screenHarness) {
	startTurn(harness, "Run the tests.")
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{
		{MessageID: "message-1", Text: "first queued"},
		{MessageID: "message-2", Text: "second queued"},
	}})
}

// Up and Down pick which queued message s and x act on.
func TestPendingBarSelectsTheQueuedMessage(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	queueTwoMessages(harness)
	harness.press(key(tea.KeyTab))
	require.Equal(t, "message-2", harness.screen.selectedQueuedMessage().MessageID, "Tab focuses the last message")
	harness.press(key(tea.KeyUp))
	require.Contains(t, harness.plainFrame(), "▸ · queued  first queued")
	harness.press(textKey('s'))
	require.Equal(t, sessionfeed.SteerQueuedMessage{MessageID: "message-1"}, harness.lastAction())

	harness.press(key(tea.KeyTab), key(tea.KeyUp), key(tea.KeyDown), textKey('x'))
	require.Contains(t, harness.plainFrame(), `Delete the queued message "second queued"?`)
	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.DeleteQueuedMessage{MessageID: "message-2"}, harness.lastAction())
}

// Esc from the delete question returns focus to the input, so letters
// type text.
func TestEscFromTheDeleteQuestionReturnsToTheInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	queueTwoMessages(harness)
	harness.press(key(tea.KeyTab), textKey('x'), key(tea.KeyEscape))
	require.Equal(t, pendingFocusNone, harness.screen.pendingFocus)
	actionCount := len(harness.source.recordedActions())
	harness.typeText("/steer")
	require.Equal(t, "/steer", harness.screen.input.Value())
	require.Len(t, harness.source.recordedActions(), actionCount, "no letter acted on the queue")
}

// A letter that is not a pending-bar key leaves the bar and is typed.
func TestPrintableKeyInThePendingBarTypesIntoTheInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	queueTwoMessages(harness)
	harness.feed(connectorApproval("approval-1"))
	harness.press(key(tea.KeyEscape), key(tea.KeyTab))
	require.Equal(t, pendingFocusInterrupts, harness.screen.pendingFocus)
	harness.typeText("so")
	require.Equal(t, pendingFocusNone, harness.screen.pendingFocus)
	require.Equal(t, "so", harness.screen.input.Value(), "s on the approvals row is text")
	harness.press(key(tea.KeyBackspace), key(tea.KeyBackspace), key(tea.KeyTab), textKey('/'))
	require.Equal(t, "/", harness.screen.input.Value())
}

// An approval that arrives while the user types waits; once the input stays
// empty for a second, it opens.
func TestDeferredApprovalOpensWhenTheInputStaysEmpty(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post it.")
	harness.typeText("draft")
	harness.feed(connectorApproval("approval-1"))
	require.False(t, harness.screen.isPopupOpen, "typing defers the popup")
	for range len("draft") {
		harness.press(key(tea.KeyBackspace))
	}
	require.Len(t, harness.delayed, 1)
	harness.typeText("x")
	harness.deliverDelayed()
	require.False(t, harness.screen.isPopupOpen, "typing again cancels the wait")
	harness.press(key(tea.KeyBackspace))
	harness.deliverDelayed()
	require.True(t, harness.screen.isPopupOpen, "the input stayed empty")

	harness.press(key(tea.KeyEscape))
	harness.typeText("y")
	harness.press(key(tea.KeyBackspace))
	require.Empty(t, harness.delayed, "after Esc later the approval stays in the pending bar")
}

// Interrupts pending when the session loads stay in the pending bar.
func TestInterruptsPendingAtLoadDoNotOpen(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{skipIntro: true})
	harness.feed(
		sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-142233-a1b2c3", AcceptsTurnControl: true},
		sessionfeed.TurnStarted{TurnID: "turn-2", TurnNumber: 2, StartedAt: harness.now},
		connectorApproval("approval-1"),
		sessionfeed.SessionLoaded{},
	)
	require.False(t, harness.screen.isPopupOpen)
	require.Contains(t, harness.plainFrame(), "? 1 approval waiting · Tab opens")
	require.Empty(t, harness.delayed, "it never opens by itself")
	harness.press(key(tea.KeyTab), key(tea.KeyEnter))
	require.True(t, harness.screen.isPopupOpen)
}

// Esc on an empty input cancels the running turn like Ctrl-C, but never
// exits.
func TestEscCancelsTheRunningTurn(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Run the tests.")
	harness.typeText("x")
	harness.press(key(tea.KeyEscape))
	require.Empty(t, cancelActions(harness), "Esc with a draft does not cancel")
	harness.press(key(tea.KeyBackspace), key(tea.KeyEscape))
	require.Equal(t, []sessionfeed.CancelTurn{{TurnID: "turn-1"}}, cancelActions(harness))
	harness.press(key(tea.KeyEscape))
	require.Len(t, cancelActions(harness), 1, "a second Esc sends nothing")
	harness.press(ctrlKey('c'))
	require.False(t, harness.screen.isExiting, "Ctrl-C after Esc only arms the exit")
	require.Len(t, cancelActions(harness), 1)
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
}

func TestEscClosesThePopupWithoutCancelling(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post it.")
	harness.feed(connectorApproval("approval-1"))
	harness.press(key(tea.KeyEscape))
	require.False(t, harness.screen.isPopupOpen)
	require.Empty(t, cancelActions(harness))
}

func cancelActions(harness *screenHarness) []sessionfeed.CancelTurn {
	var cancels []sessionfeed.CancelTurn
	for _, action := range harness.source.recordedActions() {
		if cancel, isCancel := action.(sessionfeed.CancelTurn); isCancel {
			cancels = append(cancels, cancel)
		}
	}
	return cancels
}

// A question is worded as a question, not an approval.
func TestWaitingQuestionIsCalledAQuestion(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Ask me.")
	harness.feed(
		sessionfeed.ToolCallChanged{ToolCallID: "call-1", TurnID: "turn-1", ToolName: "ask_user", KeyArgument: "Which color?", Status: sessionfeed.ToolCallAwaitingAnswer},
		sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Which color?", Choices: []string{"red", "green"}},
	)
	harness.press(key(tea.KeyEscape))
	frame := harness.plainFrame()
	require.Contains(t, frame, "awaiting your answer")
	require.NotContains(t, frame, "awaiting approval")
	require.Contains(t, frame, "? 1 question waiting · Tab opens")
	require.Contains(t, frame, "Tab opens questions")
}

// A finished write_todos row lists the plan steps; Ctrl-P opens them all.
func TestPlanStepsShowAsAChecklist(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.press(ctrlKey('p'))
	require.Contains(t, harness.scrollbackText(), "no plan yet")
	startTurn(harness, "Plan it.")
	items := []sessionfeed.PlanItem{
		{Title: "Read the file", Status: sessionfeed.PlanItemDone},
		{Title: "Fix the test", Status: sessionfeed.PlanItemInProgress},
		{Title: "Ship it", Status: sessionfeed.PlanItemPending},
	}
	call := finishedTool("call-1", "write_todos", "3 steps", sessionfeed.ToolCallSucceeded, "plan updated", 0)
	call.TurnID, call.PlanItems = "turn-1", items
	harness.feed(call, sessionfeed.PlanChanged{Revision: 1, Status: sessionfeed.PlanStatusDraft, Items: items}, sessionfeed.TurnEnded{TurnID: "turn-1"})
	requireInOrder(t, harness.scrollbackText(), "write_todos", "3 steps", "plan updated", "☑ Read the file", "☐ Fix the test", "☐ Ship it")
	require.Contains(t, harness.plainFrame(), "plan · 1 of 3 done · ▸ Fix the test")
	require.Contains(t, harness.plainFrame(), "Ctrl-P steps")

	harness.press(ctrlKey('p'))
	require.NotNil(t, harness.screen.pager)
	pager := harness.plainFrame()
	requireInOrder(t, pager, "Plan", "revision 1 · draft · 1 of 3 done", "☑ Read the file", "☐ Fix the test", "☐ Ship it")
	items[2].Status = sessionfeed.PlanItemDone
	harness.feed(sessionfeed.PlanChanged{Revision: 2, Status: sessionfeed.PlanStatusActive, Items: items})
	require.Contains(t, harness.plainFrame(), "revision 2 · running · 2 of 3 done", "the open pager follows the plan")
	harness.press(key(tea.KeyEscape))
	require.Nil(t, harness.screen.pager)
}
