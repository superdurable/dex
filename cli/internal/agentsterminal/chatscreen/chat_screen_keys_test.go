// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

func TestApprovalPopupKeys(t *testing.T) {
	cases := []struct {
		name     string
		keys     []tea.KeyPressMsg
		text     string
		expected sessionfeed.ResolveApproval
	}{
		{name: "y approves", keys: []tea.KeyPressMsg{textKey('y')}, expected: sessionfeed.ResolveApproval{InterruptID: "approval-1", IsApproved: true}},
		{name: "n rejects", keys: []tea.KeyPressMsg{textKey('n')}, expected: sessionfeed.ResolveApproval{InterruptID: "approval-1"}},
		{name: "Enter confirms the focused option", keys: []tea.KeyPressMsg{textKey('j'), key(tea.KeyEnter)}, expected: sessionfeed.ResolveApproval{InterruptID: "approval-1"}},
		{name: "up stops at the first option", keys: []tea.KeyPressMsg{key(tea.KeyUp), key(tea.KeyEnter)}, expected: sessionfeed.ResolveApproval{InterruptID: "approval-1", IsApproved: true}},
		{name: "r rejects with a reason", keys: []tea.KeyPressMsg{textKey('r')}, text: "too long", expected: sessionfeed.ResolveApproval{InterruptID: "approval-1", Reason: "too long"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newScreenHarness(t, harnessOptions{})
			startTurn(harness, "Post it.")
			harness.feed(connectorApproval("approval-1"))
			require.True(t, harness.screen.isPopupOpen, "an approval opens the popup")
			harness.press(testCase.keys...)
			if testCase.text != "" {
				harness.typeText(testCase.text)
				harness.press(key(tea.KeyEnter))
			}
			require.Equal(t, testCase.expected, harness.lastAction())
			require.False(t, harness.screen.isPopupOpen)
			require.Contains(t, harness.plainFrame(), "ing slack_post_channel_message…", "the popup is replaced by request feedback")
		})
	}
}

func TestApprovalPopupKeysWithTwoApprovals(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Fix and post.")
	harness.feed(connectorApproval("approval-1"), fileApproval("approval-2"))
	require.Contains(t, harness.plainFrame(), "1 of 2")

	harness.press(key(tea.KeyTab))
	require.Contains(t, harness.plainFrame(), "Approve edit_file?")
	require.Contains(t, harness.plainFrame(), "2 of 2")

	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.ResolveApproval{InterruptID: "approval-2", IsApproved: true}, harness.lastAction())
	require.True(t, harness.screen.isPopupOpen, "the next waiting approval opens")
	require.Contains(t, harness.plainFrame(), "Approve slack_post_channel_message?")

	harness.press(key(tea.KeyEscape))
	require.False(t, harness.screen.isPopupOpen)
	frame := harness.plainFrame()
	require.Contains(t, frame, "? 1 approval waiting · Tab opens")
	require.Contains(t, frame, "· approving edit_file…")

	harness.press(key(tea.KeyTab), key(tea.KeyEnter))
	require.True(t, harness.screen.isPopupOpen, "Tab then Enter reopens the waiting approval")
	require.Contains(t, harness.plainFrame(), "Approve slack_post_channel_message?")
}

func TestApprovalLettersAreTextWhileInputHasFocus(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post it.")
	harness.feed(connectorApproval("approval-1"))
	harness.press(key(tea.KeyEscape))
	harness.typeText("yes and no")
	require.Equal(t, "yes and no", harness.screen.input.Value())
	require.Len(t, harness.source.recordedActions(), 1, "only the first message was sent")
}

func TestApprovalResolvedElsewhereClosesPopup(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post it.")
	harness.feed(connectorApproval("approval-1"))
	harness.feed(sessionfeed.InterruptResolved{InterruptID: "approval-1", Resolution: sessionfeed.InterruptApproved, ResolvedBy: "Dex Web"})
	require.False(t, harness.screen.isPopupOpen)
	require.Contains(t, harness.plainFrame(), "· Approved in Dex Web.")
	require.NotContains(t, harness.plainFrame(), "waiting")
}

func TestApprovalSendFailureRestoresApproval(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post it.")
	harness.feed(connectorApproval("approval-1"))
	harness.press(textKey('y'))
	harness.screen.Update(userActionFailedMsg{
		action: sessionfeed.ResolveApproval{InterruptID: "approval-1", IsApproved: true},
		err:    errors.New("lock conflict for 30s"),
	})
	frame := harness.plainFrame()
	require.Contains(t, frame, "✗ Approval not sent: lock conflict for 30s. Press Tab to try again.")
	require.Contains(t, frame, "? 1 approval waiting · Tab opens")
}

func TestQuestionPopupKeys(t *testing.T) {
	question := sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Which branch?", Choices: []string{"main", "release-0.6"}, AllowsFreeText: true}
	cases := []struct {
		name     string
		keys     []tea.KeyPressMsg
		text     string
		expected sessionfeed.UserAction
	}{
		{name: "Enter answers the first choice", keys: []tea.KeyPressMsg{key(tea.KeyEnter)}, expected: sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "main"}},
		{name: "Down moves to the second choice", keys: []tea.KeyPressMsg{key(tea.KeyDown), key(tea.KeyEnter)}, expected: sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "release-0.6"}},
		{name: "free text answers", keys: []tea.KeyPressMsg{key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)}, text: "feature/retry", expected: sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "feature/retry"}},
		{name: "Decline declines", keys: []tea.KeyPressMsg{key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)}, expected: sessionfeed.DeclineQuestion{InterruptID: "question-1"}},
		{name: "typing switches to a free-text answer", text: "green", expected: sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "green"}},
		{name: "typing after a move still answers in free text", keys: []tea.KeyPressMsg{key(tea.KeyDown)}, text: "just main", expected: sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "just main"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newScreenHarness(t, harnessOptions{})
			startTurn(harness, "Compare.")
			harness.feed(question)
			harness.press(testCase.keys...)
			if testCase.text != "" {
				harness.typeText(testCase.text)
				harness.press(key(tea.KeyEnter))
			}
			require.Equal(t, testCase.expected, harness.lastAction())
			harness.feed(sessionfeed.InterruptResolved{InterruptID: "question-1", Resolution: sessionfeed.InterruptAnswered})
			require.False(t, harness.screen.isPopupOpen)
		})
	}
}

func TestQuestionWithoutChoicesStartsInTextInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Name it.")
	harness.feed(sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Release name?", AllowsFreeText: true})
	harness.press(key(tea.KeyEnter))
	require.Len(t, harness.source.recordedActions(), 1, "an empty answer is not sent")
	harness.typeText("Aurora")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.AnswerQuestion{InterruptID: "question-1", Answer: "Aurora"}, harness.lastAction())
	harness.feed(sessionfeed.InterruptResolved{InterruptID: "question-1", Resolution: sessionfeed.InterruptAnswered})
	require.Contains(t, harness.plainFrame(), "· answered: Aurora")
}

func TestCtrlCWhileTurnRuns(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Run the tests.")
	harness.press(ctrlKey('c'))
	require.Equal(t, sessionfeed.CancelTurn{TurnID: "turn-1"}, harness.lastAction())
	require.Contains(t, harness.plainFrame(), "· requesting cancel…")

	harness.feed(sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeAccepted})
	require.Contains(t, harness.plainFrame(), "cancelling…")

	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
	require.Equal(t, 0, harness.screen.ExitCode())
	require.NotContains(t, harness.plainFrame(), "Exited before the cancel was confirmed")
}

func TestCtrlCTwiceBeforeCancelIsConfirmed(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 160})
	startTurn(harness, "Run the tests.")
	harness.press(ctrlKey('c'), ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
	frame := harness.scrollbackText()
	require.Contains(t, frame, "! Exited before the cancel was confirmed; the turn may still run. Resume with:")
	require.Contains(t, frame, "--resume openai-sdk-20261111-142233-a1b2c3")
}

func TestCtrlCCancelAnswers(t *testing.T) {
	cases := []struct {
		answer   sessionfeed.CancelRequestAnswered
		expected string
	}{
		{sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeTurnAlreadyEnded}, "· the turn already ended"},
		{sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeRefused}, "! Cannot cancel this turn."},
		{sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeNotConfirmed, Reason: "lock conflict after 30s"}, "✗ Cancel not confirmed: lock conflict after 30s."},
	}
	for _, testCase := range cases {
		harness := newScreenHarness(t, harnessOptions{})
		startTurn(harness, "Run the tests.")
		harness.press(ctrlKey('c'))
		harness.feed(testCase.answer)
		require.Contains(t, harness.plainFrame(), testCase.expected)
		require.NotContains(t, harness.plainFrame(), "requesting cancel")
	}
}

func TestCtrlCWithoutTurnControl(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 120})
	harness.feed(sessionfeed.SessionOpened{Command: "claude-sdk", Model: "sonnet", SessionID: "claude-sdk-1", AcceptsTurnControl: false})
	startTurn(harness, "Run the tests.")
	actionCount := len(harness.source.recordedActions())
	harness.press(ctrlKey('c'))
	require.Len(t, harness.source.recordedActions(), actionCount, "no cancel request is sent")
	require.Contains(t, harness.plainFrame(), "! This agent cannot cancel a running turn. Ctrl-C again exits; the turn keeps running.")
	require.False(t, harness.screen.isExiting)
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
}

func TestCtrlCWhileIdle(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.typeText("draft")
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.input.IsEmpty(), "the first Ctrl-C clears the draft")
	require.False(t, harness.screen.isExiting)
	harness.press(ctrlKey('c'))
	require.False(t, harness.screen.isExiting, "an idle Ctrl-C asks for a second press")
	require.Contains(t, harness.plainFrame(), "Press Ctrl-C again to exit")
	harness.advance(time.Second)
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting, "a second Ctrl-C within two seconds exits")
}

func TestIdleCtrlCExpiresAfterTwoSeconds(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.press(ctrlKey('c'))
	require.Len(t, harness.delayed, 1, "the hint clears itself")
	harness.advance(2 * time.Second)
	harness.deliverDelayed()
	require.NotContains(t, harness.plainFrame(), "Press Ctrl-C again")
	harness.press(ctrlKey('c'))
	require.False(t, harness.screen.isExiting, "a late second press arms again")
	harness.typeText("x")
	harness.press(key(tea.KeyBackspace), ctrlKey('c'))
	require.False(t, harness.screen.isExiting, "another key in between disarms the exit")
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
}

func TestCtrlDExitsOnlyOnEmptyInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.typeText("x")
	harness.press(ctrlKey('d'))
	require.False(t, harness.screen.isExiting)
	harness.press(key(tea.KeyBackspace), ctrlKey('d'))
	require.True(t, harness.screen.isExiting)
	require.Empty(t, harness.screen.View().Content, "the live area is cleared on exit")
}

func TestEnterQueuesWhileTurnRunsAndPendingBarActs(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Run the tests.")
	harness.typeText("Also check the nightly job.")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.SendUserMessage{Text: "Also check the nightly job."}, harness.lastAction())
	require.NotContains(t, harness.scrollbackText(), "Also check", "a queued message is not printed until its turn starts")

	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{{MessageID: "message-1", Text: "Also check the nightly job."}}})
	harness.typeText("s")
	harness.press(key(tea.KeyBackspace))
	harness.press(key(tea.KeyTab), textKey('s'))
	require.Equal(t, sessionfeed.SteerQueuedMessage{MessageID: "message-1"}, harness.lastAction())

	harness.press(key(tea.KeyTab), textKey('x'), textKey('n'))
	require.Equal(t, sessionfeed.SteerQueuedMessage{MessageID: "message-1"}, harness.lastAction(), "n keeps the message")
	harness.press(key(tea.KeyTab), textKey('x'), textKey('y'))
	require.Equal(t, sessionfeed.DeleteQueuedMessage{MessageID: "message-1"}, harness.lastAction())

	harness.feed(
		sessionfeed.QueuedMessagesChanged{},
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCompleted},
		sessionfeed.UserMessageRecorded{Text: "From the browser.", Origin: "Dex Web"},
	)
	require.Contains(t, harness.scrollbackText(), "› (Dex Web) From the browser.")

	harness.feed(sessionfeed.UserMessageRecorded{Text: "Hi from my phone.", Origin: "another client", SenderName: "Alice"})
	require.Contains(t, harness.scrollbackText(), "› Alice: Hi from my phone.")
	require.NotContains(t, harness.scrollbackText(), "(another client) Alice")
}

func TestPendingBarIgnoresSteerWithoutTurnControl(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.SessionOpened{Command: "claude-sdk", SessionID: "claude-sdk-1"})
	startTurn(harness, "Run the tests.")
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{{MessageID: "message-1", Text: "later"}}})
	harness.press(key(tea.KeyTab), textKey('s'))
	require.Equal(t, "s", harness.screen.input.Value(), "without turn control Tab focuses nothing and s is text")
	require.NotContains(t, harness.plainFrame(), "Tab acts on it")
}

func TestSlashCommands(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{commands: demoCommands})
	harness.typeText("/model gpt-5.3")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "model", Argument: "gpt-5.3"}, harness.lastAction())

	harness.typeText("/he")
	harness.press(key(tea.KeyEnter))
	require.True(t, harness.screen.isHelpOpen, "Enter runs the completed command")
	harness.press(key(tea.KeyF1))
	require.False(t, harness.screen.isHelpOpen, "F1 toggles help")

	harness.typeText("/unknown")
	harness.press(key(tea.KeyEscape), key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "✗ Unknown command /unknown. /help lists commands.")

	harness.typeText("/ex")
	harness.press(key(tea.KeyTab))
	require.Equal(t, "/exit", harness.screen.input.Value())
	harness.press(key(tea.KeyEnter))
	require.True(t, harness.screen.isExiting)
}

func TestQuestionMarkAndQAreTextInChat(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.typeText("?q")
	require.Equal(t, "?q", harness.screen.input.Value())
	require.False(t, harness.screen.isHelpOpen)
}

func TestInputHistoryAndNewLines(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.typeText("first")
	harness.press(key(tea.KeyEnter))
	harness.feed(sessionfeed.TurnStarted{TurnID: "turn-1", TurnNumber: 1}, sessionfeed.TurnEnded{TurnID: "turn-1"})
	harness.typeText("a")
	harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}, ctrlKey('j'))
	harness.typeText("b")
	require.Equal(t, "a\n\nb", harness.screen.input.Value())
	harness.press(key(tea.KeyUp))
	require.Equal(t, "a\n\nb", harness.screen.input.Value(), "Up in a typed draft moves the cursor")
	harness.press(key(tea.KeyUp), key(tea.KeyUp))
	require.Equal(t, "a\n\nb", harness.screen.input.Value(), "Up on a draft's first line does not replace it")
	harness.press(ctrlKey('c'), ctrlKey('p'))
	require.Equal(t, "first", harness.screen.input.Value(), "Ctrl-P recalls")
	harness.press(ctrlKey('n'))
	require.True(t, harness.screen.input.IsEmpty(), "Ctrl-N goes back to the empty input")
}

func TestUpDownScrollOnAnEmptyInputAndRecallAfterCtrlP(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 80, height: 20})
	for _, text := range []string{"first", "second", "third"} {
		harness.typeText(text)
		harness.press(key(tea.KeyEnter))
		harness.feed(sessionfeed.TurnStarted{TurnID: "turn-" + text, TurnNumber: 1}, sessionfeed.TurnEnded{TurnID: "turn-" + text})
	}
	for index := range 40 {
		harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: fmt.Sprintf("note %02d", index)})
	}
	layout := harness.screen.computeLayout()
	bottom := harness.screen.currentTop(layout)

	harness.press(key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp))
	require.True(t, harness.screen.input.IsEmpty(), "Up on an empty input does not recall")
	require.False(t, harness.screen.scroll.isFollowing)
	require.Equal(t, bottom-3*arrowScrollStep, harness.screen.currentTop(layout), "three arrows, as one wheel notch sends, scroll three rows")
	harness.press(key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown))
	require.True(t, harness.screen.scroll.isFollowing, "scrolling back to the end follows again")

	harness.press(ctrlKey('p'))
	require.Equal(t, "third", harness.screen.input.Value())
	harness.press(key(tea.KeyUp))
	require.Equal(t, "second", harness.screen.input.Value(), "Up keeps recalling a recalled message")
	harness.press(key(tea.KeyDown))
	require.Equal(t, "third", harness.screen.input.Value())
	harness.press(key(tea.KeyDown))
	require.True(t, harness.screen.input.IsEmpty(), "Down past the newest message clears the input")

	harness.press(ctrlKey('p'))
	harness.typeText("!")
	harness.press(key(tea.KeyUp))
	require.Equal(t, "third!", harness.screen.input.Value(), "an edited recall is a draft")
}

func TestRecalledSlashCommandsKeepRecallingWithUpDown(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	for _, text := range []string{"hello", "/copy", "/help"} {
		harness.typeText(text)
		harness.press(key(tea.KeyEnter))
		if harness.screen.isHelpOpen {
			harness.press(key(tea.KeyEscape))
		}
	}
	harness.press(ctrlKey('p'))
	require.Equal(t, "/help", harness.screen.input.Value())
	require.False(t, harness.screen.isCompletionVisible(), "a recalled command does not open the completion list")
	harness.press(key(tea.KeyUp))
	require.Equal(t, "/copy", harness.screen.input.Value(), "Up keeps recalling past a recalled command")
	harness.press(key(tea.KeyUp))
	require.Equal(t, "hello", harness.screen.input.Value())
	harness.press(key(tea.KeyDown), key(tea.KeyDown))
	require.Equal(t, "/help", harness.screen.input.Value(), "Down keeps recalling too")

	harness.press(key(tea.KeyUp))
	require.Equal(t, "/copy", harness.screen.input.Value())
	harness.press(key(tea.KeyBackspace))
	require.True(t, harness.screen.isCompletionVisible(), "editing the recalled command opens the completion list again")
}

func TestPasteGoesToTheFocusedInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.screen.Update(tea.PasteMsg{Content: "line one\r\nline two"})
	require.Equal(t, "line one\nline two", harness.screen.input.Value())
}

func TestWorkerStoppedDisablesInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopped, ExitCode: 1})
	harness.typeText("x")
	require.True(t, harness.screen.isExiting)
	require.Equal(t, 1, harness.screen.ExitCode())
}

func TestFeedClosedExits(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.screen.Update(sessionFeedFailedMsg{err: sessionfeed.ErrSessionFeedClosed})
	require.True(t, harness.screen.isExiting)
	require.Equal(t, 1, harness.screen.ExitCode())
	require.Contains(t, harness.scrollbackText(), "✗ The command stopped, so this chat cannot continue.")
}

func TestResizeReflowsLiveArea(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 120})
	playToolTurn(harness)
	require.Contains(t, harness.plainFrame(), "openai-sdk · gpt-5.2")
	require.Contains(t, harness.plainFrame(), "turn 1")
	harness.screen.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	frame := harness.screen.View().Content
	for _, line := range splitLines(frame) {
		require.LessOrEqual(t, displayWidthForTest(line), 50, "line %q is wider than the terminal", line)
	}
	harness.screen.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	require.Contains(t, harness.plainFrame(), "Resize to 40 columns")
}

func TestLiveAreaFitsEveryWidth(t *testing.T) {
	for width := 20; width <= 160; width++ {
		harness := newScreenHarness(t, harnessOptions{width: width})
		playToolTurn(harness)
		harness.feed(connectorApproval("approval-1"), fileApproval("approval-2"))
		limit := min(width, maximumContentWidth)
		for _, line := range splitLines(harness.screen.View().Content) {
			require.LessOrEqual(t, displayWidthForTest(line), width, "width %d: line %q is too wide", width, line)
			require.False(t, strings.HasSuffix(stripForTest(line), " "), "width %d: line %q ends with a space", width, line)
		}
		for _, block := range []string{harness.scrollbackRaw()} {
			for _, line := range splitLines(block) {
				require.LessOrEqual(t, displayWidthForTest(line), limit, "width %d: scrollback line %q is too wide", width, line)
				require.False(t, strings.HasSuffix(stripForTest(line), " "), "width %d: scrollback line %q ends with a space", width, line)
			}
		}
	}
}

func TestPendingSlashCommands(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 120})
	harness.typeText("/approve")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "· nothing is waiting for you")
	harness.typeText("/steer")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "· no queued message to steer")

	startTurn(harness, "Post it.")
	harness.feed(connectorApproval("approval-1"))
	harness.press(key(tea.KeyEscape))
	actionCount := len(harness.source.recordedActions())
	harness.typeText("/approve")
	harness.press(key(tea.KeyEnter))
	require.True(t, harness.screen.isPopupOpen, "/approve opens the popup")
	require.Len(t, harness.source.recordedActions(), actionCount, "/approve never approves by itself")
	harness.press(key(tea.KeyEscape))

	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{{MessageID: "message-1", Text: "later"}}})
	harness.typeText("/steer")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.SteerQueuedMessage{MessageID: "message-1"}, harness.lastAction())
	harness.typeText("/drop")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), `Delete the queued message "later"? It will not be sent.`)
	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.DeleteQueuedMessage{MessageID: "message-1"}, harness.lastAction())
}

func TestCtrlZShowsAHintInsteadOfSuspending(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 120})
	harness.press(ctrlKey('z'))
	require.False(t, harness.screen.isExiting)
	require.Contains(t, harness.plainFrame(), "! Ctrl-Z does not suspend this chat. Exit with Ctrl-D; the session keeps running.")
}
