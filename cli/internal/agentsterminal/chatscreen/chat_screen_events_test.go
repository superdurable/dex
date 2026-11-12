// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// requireInOrder checks that each part appears in text after the previous.
func requireInOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	offset := 0
	for _, part := range parts {
		index := strings.Index(text[offset:], part)
		require.GreaterOrEqual(t, index, 0, "%q is missing after offset %d in:\n%s", part, offset, text)
		offset += index + len(part)
	}
}

func TestScrollbackOrderOfATurn(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	playToolTurn(harness)
	harness.feed(
		finishedTool("call-3", "run_shell_command", "go test ./integ/... -run Retry", sessionfeed.ToolCallSucceeded, "exit 0", 14800*time.Millisecond),
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-2", Attempt: 1, Text: "Found 3 open issues.\n\n- #812 times out\n"},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-2", Text: "Found 3 open issues.\n\n- #812 times out\n"},
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCompleted},
	)
	scrollback := harness.scrollbackText()
	requireInOrder(t, scrollback,
		"› Find open issues", "I'll search the issue tracker first.",
		"✓ search_files", "✓ read_file", "✓ run_shell_command", "exit 0",
		"Found 3 open issues.", "- #812 times out")
	require.NotContains(t, scrollback, "reply from the session record", "matching records add no correction")
	require.Equal(t, 1, strings.Count(scrollback, "I'll search the issue tracker first."))
}

func TestReplyReconciliation(t *testing.T) {
	cases := []struct {
		name             string
		deltas           []string
		record           string
		isCorrected      bool
		expectedInOrder  []string
		expectedOnceText string
	}{
		{name: "record continues a shorter stream", deltas: []string{"hello\n"}, record: "hello world", isCorrected: true, expectedInOrder: []string{"hello", "· reply from the session record:", "hello world"}},
		{name: "record adds the end of a paragraph", deltas: []string{"A first.\n\nB"}, record: "A first.\n\nB and C.", expectedInOrder: []string{"A first.", "B and C."}, expectedOnceText: "A first."},
		{name: "record lacks only trailing whitespace", deltas: []string{"Done.\n\n"}, record: "Done.", expectedOnceText: "Done."},
		{name: "record differs inside the text", deltas: []string{"The job runs at 01:00."}, record: "The job runs at 02:00.", isCorrected: true, expectedInOrder: []string{"01:00", "· reply from the session record:", "02:00"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newScreenHarness(t, harnessOptions{width: 100})
			startTurn(harness, "Go.")
			for _, delta := range testCase.deltas {
				harness.feed(sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: delta})
			}
			harness.feed(sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: testCase.record})
			scrollback := harness.scrollbackText()
			require.Equal(t, testCase.isCorrected, strings.Contains(scrollback, "reply from the session record"), scrollback)
			requireInOrder(t, scrollback, testCase.expectedInOrder...)
			if testCase.expectedOnceText != "" {
				require.Equal(t, 1, strings.Count(scrollback, testCase.expectedOnceText), scrollback)
			}
		})
	}
}

func TestReplyRetryAndLateDeltas(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Go.")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "First try"},
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 2, Text: "Second try.\n\n"},
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: " late from attempt 1"},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "Second try."},
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCancelled},
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 2, Text: "after the turn ended"},
	)
	scrollback := harness.scrollbackText()
	requireInOrder(t, scrollback, "First try", "· retrying model call (attempt 2); text above is from attempt 1", "Second try.", "○ turn cancelled; the reply above is incomplete")
	require.NotContains(t, scrollback, "late from attempt 1")
	require.NotContains(t, scrollback, "after the turn ended")
	require.NotContains(t, scrollback, "reply from the session record")
	require.Equal(t, 1, strings.Count(scrollback, "Second try."))
}

func TestDeleteConfirmationPinsTheMessage(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Go.")
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{{MessageID: "message-1", Text: "first"}}})
	harness.press(key(tea.KeyTab), textKey('x'))
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{
		{MessageID: "message-1", Text: "first"}, {MessageID: "message-2", Text: "from Dex Web"},
	}})
	harness.press(textKey('y'))
	require.Equal(t, sessionfeed.DeleteQueuedMessage{MessageID: "message-1"}, harness.lastAction(), "y deletes the message shown when x was pressed")

	harness.press(key(tea.KeyTab), textKey('x'))
	actionCount := len(harness.source.recordedActions())
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{{MessageID: "message-3", Text: "newer"}}})
	require.Contains(t, harness.plainFrame(), "· the queued message already left the queue; nothing was deleted")
	require.NotContains(t, harness.plainFrame(), "Delete the queued message")
	harness.press(textKey('y'))
	require.Len(t, harness.source.recordedActions(), actionCount, "the cancelled confirmation deletes nothing")
}

func TestLateCancelAnswerDoesNotAffectTheNextTurn(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Go.")
	harness.press(ctrlKey('c'))
	require.Equal(t, sessionfeed.CancelTurn{TurnID: "turn-1"}, harness.lastAction())
	harness.feed(
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCompleted},
		sessionfeed.UserMessageRecorded{Text: "queued earlier"},
		sessionfeed.TurnStarted{TurnID: "turn-2", TurnNumber: 2, StartedAt: harness.now},
		sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeAccepted},
	)
	require.NotContains(t, harness.plainFrame(), "cancelling…")
	harness.press(ctrlKey('c'))
	require.False(t, harness.screen.isExiting, "the first Ctrl-C of turn 2 cancels instead of exiting")
	require.Equal(t, sessionfeed.CancelTurn{TurnID: "turn-2"}, harness.lastAction())
	harness.feed(sessionfeed.CancelRequestAnswered{TurnID: "turn-1", Outcome: sessionfeed.CancelOutcomeRefused})
	require.NotContains(t, harness.scrollbackText(), "Cannot cancel this turn.", "an answer for another turn is ignored")
}

func TestMessagesBeforeTheFirstSnapshotWait(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100, skipIntro: true})
	harness.typeText("hello")
	harness.press(key(tea.KeyEnter))
	harness.typeText("second")
	harness.press(key(tea.KeyEnter))
	require.Empty(t, harness.source.recordedActions(), "nothing is sent before the snapshot")
	frame := harness.plainFrame()
	require.Contains(t, frame, "· waiting for the session  hello")
	require.Contains(t, frame, "· waiting for the session  second")
	require.NotContains(t, harness.scrollbackText(), "hello")

	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-1", AcceptsTurnControl: true}, sessionfeed.SessionLoaded{})
	require.Equal(t, []sessionfeed.UserAction{
		sessionfeed.SendUserMessage{Text: "hello"},
		sessionfeed.SendUserMessage{Text: "second"},
	}, harness.source.recordedActions(), "held messages go in order")
	require.Equal(t, 1, strings.Count(harness.scrollbackText(), "› hello"))
	require.NotContains(t, harness.scrollbackText(), "second", "the second message prints when its queued turn starts")
	require.Contains(t, harness.plainFrame(), "· queueing  second")
}

func TestMessageSentWhileTheTurnStartsIsNotPrintedTwice(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.typeText("one")
	harness.press(key(tea.KeyEnter))
	harness.typeText("two")
	harness.press(key(tea.KeyEnter))
	harness.feed(
		sessionfeed.TurnStarted{TurnID: "turn-1", TurnNumber: 1},
		sessionfeed.TurnEnded{TurnID: "turn-1"},
		sessionfeed.UserMessageRecorded{Text: "two", TurnID: "turn-2", Sequence: 4},
		sessionfeed.UserMessageRecorded{Text: "two", TurnID: "turn-2", Sequence: 4},
	)
	scrollback := harness.scrollbackText()
	require.Equal(t, 1, strings.Count(scrollback, "› one"))
	require.Equal(t, 1, strings.Count(scrollback, "› two"), "a sequence prints once")
}

func TestRejectedMessageReleasesTheStartingTurn(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.typeText("one")
	harness.press(key(tea.KeyEnter))
	harness.feed(sessionfeed.UserMessageRejected{Text: "one", Reason: "this agent takes no images. Removed 1 image; send again."})
	require.Contains(t, harness.scrollbackText(), "✗ Not sent: this agent takes no images. Removed 1 image; send again.")
	harness.typeText("two")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.scrollbackText(), "› two", "the next message prints because no turn is starting")
}

func TestHistoryRecordsAndRetentionWindow(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(
		sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 40, DeletedBeforeSequence: 12},
		sessionfeed.UserMessageRecorded{Text: "old question", Sequence: 40},
		sessionfeed.ToolCallChanged{ToolCallID: "call-1", ToolName: "read_file", KeyArgument: "go.mod", Status: sessionfeed.ToolCallFromHistory, Summary: "module example.com/app"},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-9", StepExecutionID: "step-1", Text: "old answer", Sequence: 42},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-9", StepExecutionID: "step-1", Text: "old answer", Sequence: 42},
	)
	scrollback := harness.scrollbackText()
	requireInOrder(t, scrollback, "· messages before #12 were removed by the retention window", "· earlier messages: /history", "› old question", "· read_file", "module example.com/app", "old answer")
	require.Equal(t, 1, strings.Count(scrollback, "old answer"))
	require.NotContains(t, scrollback, "✓ read_file", "history tool rows show no status")
}

func TestNoticesAreCleanedCutAndDeduplicated(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityWarning, Text: "bad\x1b]0;title\x07\ttext\u0085end"})
	require.NotContains(t, harness.scrollbackRaw(), "\x1b]0;")
	require.Contains(t, harness.scrollbackText(), "! bad ]0;title  text end")

	long := strings.Repeat("é", 600)
	harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: long})
	shown := strings.ReplaceAll(strings.ReplaceAll(harness.scrollbackText(), "\n", ""), " ", "")
	require.Contains(t, shown, strings.Repeat("é", 499)+"…")
	require.NotContains(t, shown, strings.Repeat("é", 500))

	countRepeats := func() int { return strings.Count(harness.scrollbackText(), "Worker reconnecting.") }
	repeat := sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityWarning, Text: "Worker reconnecting."}
	harness.feed(repeat)
	harness.advance(5 * time.Second)
	harness.feed(repeat, sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityError, Text: "Worker reconnecting."})
	require.Equal(t, 2, countRepeats(), "the repeat is hidden; another level is shown")
	harness.advance(5100 * time.Millisecond)
	harness.feed(repeat)
	require.Equal(t, 3, countRepeats(), "a hidden repeat does not restart the 10 s window")
}

// A notice for a stored message prints once per sequence, so a repeated plan
// execution of one revision shows again within the repeat window.
func TestNoticesWithASequencePrintOncePerSequence(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	countRuns := func() int { return strings.Count(harness.scrollbackText(), "Running plan revision 2") }
	first := sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 2", Sequence: 5}
	harness.feed(first, first)
	require.Equal(t, 1, countRuns(), "the same stored message prints once")
	harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 2", Sequence: 9})
	require.Equal(t, 2, countRuns(), "another execution of the revision is not a repeat")
}

func TestSessionTextCannotWriteEscapeSequences(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Go.")
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "a\x1b]8;;http://x\x07link\n\n"},
		finishedTool("call-1", "run\x1b[2J", "arg\x1b[H", sessionfeed.ToolCallSucceeded, "ok\x9b", time.Second),
	)
	raw := harness.scrollbackRaw() + harness.screen.View().Content
	require.NotContains(t, raw, "\x1b]8;")
	require.NotContains(t, raw, "\x1b[2J")
	require.NotContains(t, raw, "\x1b[H")
	require.NotContains(t, raw, "\u009b")
}

func TestImageAttachments(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100, commands: demoCommands})
	reference := json.RawMessage(`{"type":"image","uri":"file:///media/ab12","mime_type":"image/png","sha256":"ab12","size_bytes":245760}`)
	attachment := sessionfeed.ImageAttachment{Name: "diagram.png", MimeType: "image/png", SizeBytes: 245760, Reference: reference}
	harness.feed(sessionfeed.ImageAttached{Attachment: attachment}, sessionfeed.ImageAttached{Attachment: attachment})
	require.Contains(t, harness.plainFrame(), "▣ diagram.png · image/png · 240 KB  ▣ diagram.png")
	harness.press(key(tea.KeyBackspace))
	require.Len(t, harness.screen.attachments, 1, "Backspace on an empty input removes the last chip")
	harness.typeText("Explain this.")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.SendUserMessage{Text: "Explain this.", Images: []sessionfeed.ImageAttachment{attachment}}, harness.lastAction())
	requireInOrder(t, harness.scrollbackText(), "› Explain this.", "▣ diagram.png · image/png · 240 KB")
	require.Empty(t, harness.screen.attachments)
}

func TestPlanCommands(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.typeText("/plan")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "Unknown command /plan", "without plan mode there is no /plan")

	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-142233-a1b2c3", AcceptsTurnControl: true, SupportsPlanMode: true})
	harness.typeText("/plan")
	harness.press(key(tea.KeyEnter))
	harness.typeText("Refactor the retry loop.")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.SendUserMessage{Text: "Refactor the retry loop.", IsPlanMode: true}, harness.lastAction())
	harness.feed(sessionfeed.TurnStarted{TurnID: "turn-1", TurnNumber: 1})
	harness.typeText("/run")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "· no draft plan to run")

	harness.feed(
		sessionfeed.PlanChanged{Revision: 3, Status: sessionfeed.PlanStatusDraft, IsInPlanInteraction: true, Items: []sessionfeed.PlanItem{{Title: "Read the loop"}}},
	)
	harness.typeText("/run")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "· wait for the turn to end, then /run")
	harness.feed(sessionfeed.TurnEnded{TurnID: "turn-1"})
	harness.typeText("/run")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.ExecutePlan{Revision: 3}, harness.lastAction())
}

func TestWorkerStoppingShowsInTheContextLine(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.feed(sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopping})
	require.Contains(t, harness.plainFrame(), "! Worker stopping")
}

func TestWideTerminalsKeepColumnsNearTheText(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 200})
	playToolTurn(harness)
	for _, line := range splitLines(stripForTest(harness.screen.View().Content)) {
		if strings.Trim(line, "─") == "" {
			continue
		}
		require.LessOrEqual(t, displayWidthForTest(line), maximumContentWidth, "line %q reaches too far right", line)
	}
	require.Contains(t, harness.plainFrame(), "turn 1 · 1.2k tok")
}
