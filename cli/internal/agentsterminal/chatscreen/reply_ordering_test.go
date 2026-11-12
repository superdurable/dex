// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

const replyAfterTool = "result: exit 0\n--- stdout ---\nhi\n\n--- stderr ---\n"

func runningShell(harness *screenHarness) sessionfeed.ToolCallChanged {
	return sessionfeed.ToolCallChanged{ToolCallID: "call-1", TurnID: "turn-1", ToolName: "run_shell_command", KeyArgument: "echo hi", Status: sessionfeed.ToolCallRunning, StartedAt: harness.now}
}

func finishedShell(harness *screenHarness) sessionfeed.ToolCallChanged {
	call := runningShell(harness)
	call.Status, call.Summary, call.Duration = sessionfeed.ToolCallSucceeded, "exit code 0", 100*time.Millisecond
	return call
}

// Text streamed before the finished tool row still prints below it,
// whole.
func TestReplyStreamedBeforeTheFinishedToolRowPrintsBelowIt(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Run it.")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "I'll run it.\n\n"},
		runningShell(harness),
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-2", Attempt: 1, Text: replyAfterTool},
	)
	require.NotContains(t, harness.scrollbackText(), "result: exit 0", "the reply waits for the row it follows")
	requireInOrder(t, harness.plainFrame(), "I'll run it.", "run_shell_command", "result: exit 0")

	harness.feed(
		finishedShell(harness),
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-2", Text: strings.TrimRight(replyAfterTool, "\n")},
		sessionfeed.TurnEnded{TurnID: "turn-1"},
	)
	scrollback := harness.scrollbackText()
	requireInOrder(t, scrollback, "I'll run it.", "run_shell_command", "result: exit 0", "hi", "--- stderr ---")
	require.Equal(t, 1, strings.Count(scrollback, "result: exit 0"))
}

// A snapshot shows the next Step's final message before the finished row.
func TestRecordedReplyBeforeTheFinishedToolRowPrintsBelowIt(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Run it.")
	harness.feed(
		runningShell(harness),
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "record-4", Text: "result: exit 0\n\n--- stderr ---", Sequence: 4},
		finishedShell(harness),
		sessionfeed.TurnEnded{TurnID: "turn-1"},
	)
	requireInOrder(t, harness.scrollbackText(), "run_shell_command", "result: exit 0", "--- stderr ---")
}

// Text the Step wrote before its tool call keeps its place above the row.
func TestReplyBeforeAToolCallIsNotHeld(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Run it.")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "I'll run "},
		runningShell(harness),
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "I'll run it now."},
	)
	requireInOrder(t, harness.scrollbackText(), "I'll run", "it now.")
	harness.feed(finishedShell(harness), sessionfeed.TurnEnded{TurnID: "turn-1"})
	requireInOrder(t, harness.scrollbackText(), "I'll run", "run_shell_command")
}

// Late text of the earlier Step prints; text after the tool still
// waits.
func TestEarlierStepTextDoesNotReleaseTheHeldReply(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Run it.")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "I'll run "},
		runningShell(harness),
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-2", Attempt: 1, Text: replyAfterTool},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "I'll run it now."},
	)
	require.NotContains(t, harness.scrollbackText(), "result: exit 0", "the step-2 reply still waits for the row")
	harness.feed(finishedShell(harness), sessionfeed.TurnEnded{TurnID: "turn-1"})
	requireInOrder(t, harness.scrollbackText(), "I'll run", "it now.", "run_shell_command", "result: exit 0")
}

// A streamed reply and its finished render show the same lines, so nothing
// jumps when the turn ends.
func TestSingleNewlinesStayLineBreaksWhenTheReplyFinishes(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Stream.")
	harness.feed(sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "Line 1: the fox.\nLine 2: the dog.\nLine 3"})
	streamed := harness.plainFrame()
	requireInOrder(t, streamed, "  Line 1: the fox.\n", "  Line 2: the dog.\n", "  Line 3")
	harness.feed(sessionfeed.TurnEnded{TurnID: "turn-1"})
	requireInOrder(t, harness.scrollbackText(), "  Line 1: the fox.\n", "  Line 2: the dog.\n", "  Line 3")
}

func TestCancelledTurnSaysTheReplyIsIncompleteOnlyAfterAReply(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Wait.")
	harness.feed(sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCancelled})
	require.Contains(t, harness.scrollbackText(), "○ turn cancelled")
	require.NotContains(t, harness.scrollbackText(), "incomplete", "nothing was printed, so nothing is incomplete")

	startTurnNumbered(harness, "Write a lot.", "turn-2", 2)
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-2", StepExecutionID: "step-1", Attempt: 1, Text: "Line 1"},
		sessionfeed.TurnEnded{TurnID: "turn-2", Outcome: sessionfeed.TurnOutcomeCancelled},
	)
	require.Contains(t, harness.scrollbackText(), "turn cancelled; the reply above is incomplete")
}

func TestStatusSaysWritingReplyWhileTextStreams(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Stream.")
	require.Contains(t, harness.plainFrame(), "running model")
	require.Contains(t, harness.plainFrame(), "Esc interrupt")
	harness.feed(sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "Hello"})
	require.Contains(t, harness.plainFrame(), "writing reply")
	harness.feed(runningShell(harness))
	require.NotContains(t, harness.plainFrame(), "writing reply", "a tool call ends the writing")
}

// A switched session shows its first reply and counts turns again.
func TestSwitchedSessionShowsItsFirstReplyAndCountsAgain(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurnNumbered(harness, "Third.", "turn-3", 3)
	harness.feed(sessionfeed.UsageRecorded{TotalTokens: 1500}, sessionfeed.AssistantTextDelta{TurnID: "turn-3", StepExecutionID: "step-1", Attempt: 1, Text: "old"}, sessionfeed.TurnEnded{TurnID: "turn-3"})
	startTurnNumbered(harness, "First.", "turn-1", 1)
	harness.feed(sessionfeed.TurnEnded{TurnID: "turn-1"})
	require.Contains(t, harness.plainFrame(), "turn 3")

	switchSession(harness, "openai-sdk-20261111-150000-ffffff")
	frame := harness.plainFrame()
	require.NotContains(t, frame, "turn 3", "the new session counts its own turns")
	require.NotContains(t, frame, "1.5k tok", "the new session counts its own usage")
	startTurn(harness, "after new one")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "echo: after new one"},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "echo: after new one", Sequence: 2},
		sessionfeed.TurnEnded{TurnID: "turn-1"},
	)
	requireInOrder(t, harness.scrollbackText(), "› after new one", "echo: after new one")
	require.Contains(t, harness.plainFrame(), "turn 1")
}

// The welcome area keeps the session the chat started with; the exit line
// names both sessions.
func TestSwitchedSessionKeepsTheOriginalWelcome(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	switchSession(harness, "openai-sdk-20261111-150000-ffffff")
	scrollback := harness.scrollbackText()
	require.Contains(t, scrollback, "session:   openai-sdk-20261111-142233-a1b2c3 (new)")
	require.NotContains(t, strings.SplitN(scrollback, "Type a message", 2)[0], "ffffff")
	harness.press(ctrlKey('d'))
	require.Contains(t, harness.screen.ExitSummary(), "Session openai-sdk-20261111-150000-ffffff keeps running")
	require.Contains(t, harness.screen.ExitSummary(), "This chat started in session openai-sdk-20261111-142233-a1b2c3.")
}

// The input and the status rows agree that the chat cannot continue.
func TestStoppedWorkerDisablesTheInput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopped, ExitCode: 137})
	frame := harness.plainFrame()
	require.Contains(t, frame, "Worker stopped")
	require.Contains(t, frame, "Press any key to exit")
	require.Contains(t, frame, "› this chat cannot continue", "the input says it is off")
	dimPrompt := harness.screen.theme.Secondary.Render(harness.screen.theme.Symbols.UserPrompt)
	require.Contains(t, harness.screen.View().Content, " "+dimPrompt+" ", "the prompt is dim, not the accent color")
	require.Nil(t, harness.screen.View().Cursor, "the disabled input shows no cursor")
	harness.press(key(tea.KeyEnter))
	require.True(t, harness.screen.isExiting)
}
