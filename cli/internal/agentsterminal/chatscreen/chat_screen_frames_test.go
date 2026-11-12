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
	"github.com/charmbracelet/colorprofile"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// Golden frames cover only rendering, because a width and color matrix is too
// costly in PTY tests.

var demoCommands = []sessionfeed.SlashCommand{
	{Name: "model", ArgumentHint: "ID", Description: "change the model"},
	{Name: "image", ArgumentHint: "PATH", Description: "attach an image to the next message"},
}

func TestEmptySessionFrames(t *testing.T) {
	widthMatrix(t, "empty_session", allWidths, func(harness *screenHarness) {})
}

func TestLoadingSessionFrame(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{skipIntro: true})
	harness.typeText("hello")
	set.add("loading, draft typed", harness.frame())
	set.compare("loading_session")
}

func TestToolTurnFrames(t *testing.T) {
	widthMatrix(t, "tool_turn", allWidths, playToolTurn)
}

func TestToolStatusFrames(t *testing.T) {
	widthMatrix(t, "tool_statuses", []int{80, 120}, func(harness *screenHarness) {
		startTurn(harness, "Clean up the build.")
		now := harness.now
		harness.feed(
			finishedTool("call-1", "read_file", "Makefile", sessionfeed.ToolCallSucceeded, "88 lines", 120*time.Millisecond),
			sessionfeed.ToolCallChanged{ToolCallID: "call-2", ToolName: "grep_files", KeyArgument: "TODO in src/", Status: sessionfeed.ToolCallSucceeded, Summary: "40 matches", Duration: 300 * time.Millisecond, IsOutputTruncated: true},
			sessionfeed.ToolCallChanged{ToolCallID: "call-3", ToolName: "run_shell_command", KeyArgument: "make lint", Status: sessionfeed.ToolCallFailed, Summary: "exit 2", Duration: 4200 * time.Millisecond, OutputTail: []string{"lint: unused variable x", "lint: missing doc comment", "lint: line too long", "lint: shadowed err"}},
			sessionfeed.ToolCallChanged{ToolCallID: "call-4", ToolName: "http_request", KeyArgument: "POST https://ci.example.com/retry", Status: sessionfeed.ToolCallOutcomeUnknown, Duration: 30 * time.Second},
			sessionfeed.ToolCallChanged{ToolCallID: "call-5", ToolName: "delete_file", KeyArgument: "build/", Status: sessionfeed.ToolCallRejected, Reason: "keep the cache"},
			sessionfeed.ToolCallChanged{ToolCallID: "call-6", ToolName: "write_file", KeyArgument: "build/out.txt", Status: sessionfeed.ToolCallCancelled},
			sessionfeed.ToolCallChanged{ToolCallID: "call-7", ToolName: "write_file", KeyArgument: "build/log.txt", Status: sessionfeed.ToolCallSkipped, Reason: "an earlier call failed"},
			sessionfeed.ToolCallChanged{ToolCallID: "call-8", ToolName: "run_shell_command", KeyArgument: "make test", Status: sessionfeed.ToolCallRunning, StartedAt: now.Add(-100 * time.Millisecond)},
			sessionfeed.ToolCallChanged{ToolCallID: "call-9", ToolName: "edit_file", KeyArgument: "src/main.go", Status: sessionfeed.ToolCallAwaitingApproval, StartedAt: now},
			sessionfeed.ToolCallChanged{ToolCallID: "call-10", ToolName: "write_file", KeyArgument: "notes.md", Status: sessionfeed.ToolCallQueued, StartedAt: now},
		)
	})
}

func TestStreamingReplyFrames(t *testing.T) {
	widthMatrix(t, "streaming_reply", []int{60, 80}, func(harness *screenHarness) {
		startTurn(harness, "Summarize the flaky tests.")
		harness.feed(
			sessionfeed.ReasoningDelta{TurnID: "turn-1", Text: "The user wants a summary of flaky tests from the tracker."},
		)
		harness.feed(
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "Found **3** open issues. Draft summary:\n\n"},
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "- #812 TestRetryBackoff times out on arm64\n- #844 TestRetryJitter fails when `GOMAXPROCS=1`\n- #851 the nightly"},
			sessionfeed.UsageRecorded{TotalTokens: 18_400},
			sessionfeed.PlanChanged{Items: []sessionfeed.PlanItem{
				{Title: "Search the tracker", Status: sessionfeed.PlanItemDone},
				{Title: "Read the retry test", Status: sessionfeed.PlanItemDone},
				{Title: "Draft the summary", Status: sessionfeed.PlanItemInProgress},
				{Title: "Post it to #eng", Status: sessionfeed.PlanItemPending},
			}},
		)
		harness.advance(12 * time.Second)
	})
}

func TestCorrectionFrames(t *testing.T) {
	widthMatrix(t, "corrections", []int{80}, func(harness *screenHarness) {
		startTurn(harness, "Check the nightly job.")
		harness.feed(
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "The nightly job runs"},
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 2, Text: "The nightly job runs at 02:00 UTC.\n\n"},
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: " late delta from attempt 1"},
			sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "The nightly job runs at 02:00 UTC and retries twice."},
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-2", Attempt: 1, Text: "I will now"},
			sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-2", Text: "Retries come from the CI config."},
			sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeCompleted},
			sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-2", Attempt: 1, Text: "duplicate after the turn ended"},
		)
		startTurnNumbered(harness, "Now fix it.", "turn-2", 2)
		harness.feed(
			sessionfeed.AssistantTextDelta{TurnID: "turn-2", StepExecutionID: "step-1", Attempt: 1, Text: "Editing the CI config so that"},
			sessionfeed.ToolCallChanged{ToolCallID: "call-1", ToolName: "edit_file", KeyArgument: ".ci/nightly.yml", Status: sessionfeed.ToolCallCancelled},
			sessionfeed.TurnEnded{TurnID: "turn-2", Outcome: sessionfeed.TurnOutcomeCancelled},
		)
	})
}

func TestApprovalFrames(t *testing.T) {
	widthMatrix(t, "approval_connector", allWidths, func(harness *screenHarness) {
		startTurn(harness, "Post the summary to #eng.")
		harness.feed(connectorApproval("approval-1"))
	})
}

func TestApprovalWithPreviewFrames(t *testing.T) {
	widthMatrix(t, "approval_two_with_preview", []int{80}, func(harness *screenHarness) {
		startTurn(harness, "Fix the backoff and tell #eng.")
		approval := connectorApproval("approval-1")
		approval.Deadline = harness.now.Add(9*time.Minute + 20*time.Second)
		harness.feed(approval, fileApproval("approval-2"))
		harness.press(key(tea.KeyTab))
	})
}

func TestApprovalReasonAndExpandedFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post the summary.")
	approval := connectorApproval("approval-1")
	approval.EffectSummary = ""
	harness.feed(approval)
	harness.press(textKey('e'))
	set.add("arguments expanded, effect not described", harness.frame())
	harness.press(textKey('e'), textKey('r'))
	harness.typeText("too long for the channel")
	set.add("reason input", harness.frame())
	set.compare("approval_reason_and_expanded")
}

func TestCredentialPreviewFrame(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Rotate the key.")
	harness.feed(sessionfeed.ApprovalRequested{
		InterruptID: "approval-1", ToolName: "write_file", SourceLabel: "local file", ScopeLabel: "workspace", EffectLabel: "write",
		Arguments:     []sessionfeed.ApprovalArgument{{Name: "path", Value: ".env"}},
		Preview:       &sessionfeed.FileChangePreview{Path: ".env", AddedLineCount: 1, RemovedLineCount: 1, IsCredentialFile: true},
		EffectSummary: "Changes .env in the workspace.",
	})
	set.add("credential file", harness.frame())
	set.compare("approval_credential_file")
}

func TestQuestionFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	cases := []struct {
		label    string
		question sessionfeed.QuestionAsked
	}{
		{"choices and free text", sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Which branch should I compare against?", Choices: []string{"main", "release-0.6"}, AllowsFreeText: true}},
		{"no choices", sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "What should the release be called?", AllowsFreeText: true}},
		{"choices only", sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Deploy now?", Choices: []string{"yes", "no"}}},
	}
	for _, testCase := range cases {
		harness := newScreenHarness(t, harnessOptions{})
		startTurn(harness, "Compare the branches.")
		harness.feed(testCase.question)
		set.add(testCase.label, harness.frame())
	}
	noColor := newScreenHarness(t, harnessOptions{profile: colorprofile.ASCII})
	startTurn(noColor, "Compare the branches.")
	noColor.feed(cases[0].question)
	noColor.press(key(tea.KeyDown))
	set.add("no_color, second choice focused", noColor.frame())
	set.compare("question")
}

func TestPendingBarFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Post the summary.")
	harness.feed(connectorApproval("approval-1"), fileApproval("approval-2"))
	harness.press(key(tea.KeyEscape))
	harness.typeText("Also check the nightly job.")
	harness.press(key(tea.KeyEnter))
	set.add("approvals waiting, message queueing", harness.frame())
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{
		{MessageID: "message-1", Text: "Also check the nightly job."},
	}})
	harness.press(key(tea.KeyTab))
	set.add("approvals focused", harness.frame())
	harness.press(key(tea.KeyTab))
	set.add("queued message focused", harness.frame())
	harness.press(textKey('x'))
	set.add("delete confirmation", harness.frame())
	set.compare("pending_bar")
}

func TestErrorFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{})
	startTurn(harness, "Summarize.")
	harness.feed(
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeFailed, FailureText: "The turn failed: model call returned 401 (invalid API key). Set OPENAI_API_KEY and send again."},
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityError, Text: "The session failed: tool step exceeded its attempts. Start a new session with /new; this one stays in Dex Web."},
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityError, Text: "Not sent: this agent takes no images. Removed 1 image; send again."},
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityWarning, Text: "Live output is off: the Dex server keeps no event stream. Replies appear when each turn ends."},
		sessionfeed.ServerReachabilityChanged{IsReachable: false},
	)
	harness.typeText("/nope")
	harness.press(key(tea.KeyEscape), key(tea.KeyEnter))
	set.add("errors, server unreachable", harness.frame())
	harness.feed(
		sessionfeed.ServerReachabilityChanged{IsReachable: true},
		sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopped, ExitCode: 137},
	)
	set.add("worker stopped", harness.frame())
	set.compare("errors")
}

func TestHelpAndCompletionFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{commands: demoCommands})
	harness.press(key(tea.KeyF1))
	set.add("help", harness.frame())
	harness.press(key(tea.KeyEscape))
	harness.typeText("/m")
	set.add("completion /m", harness.frame())
	harness.press(key(tea.KeyTab))
	set.add("completed /model", harness.frame())
	short := newScreenHarness(t, harnessOptions{commands: demoCommands, height: 12})
	short.typeText("/help")
	short.press(key(tea.KeyEnter))
	set.add("help capped at 60 percent of 12 rows", short.frame())
	set.compare("help_and_completion")
}

func TestNarrowAndASCIIFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	narrow := newScreenHarness(t, harnessOptions{width: 30})
	playToolTurn(narrow)
	set.add("width 30", narrow.frame())
	ascii := newScreenHarness(t, harnessOptions{isASCII: true})
	playToolTurn(ascii)
	ascii.feed(connectorApproval("approval-1"))
	set.add("ASCII symbols", ascii.frame())
	set.compare("narrow_and_ascii")
}

func TestMultiLineInputFrame(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{width: 40})
	harness.typeText("First line")
	harness.press(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	harness.typeText("second line that is long enough to wrap")
	set.add("two lines, wrapped", harness.frame())
	set.compare("multi_line_input")
	if !strings.Contains(harness.screen.input.Value(), "\n") {
		t.Fatalf("Shift-Enter did not insert a newline: %q", harness.screen.input.Value())
	}
}

func playToolTurn(harness *screenHarness) {
	startTurn(harness, "Find open issues about flaky tests and post a summary to #eng.")
	now := harness.now
	harness.feed(
		sessionfeed.AssistantTextDelta{TurnID: "turn-1", StepExecutionID: "step-1", Attempt: 1, Text: "I'll search the issue tracker first.\n\n"},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "I'll search the issue tracker first."},
		sessionfeed.TurnActivityChanged{TurnID: "turn-1", Activity: sessionfeed.TurnActivityRunningTools},
		finishedTool("call-1", "search_files", `"flaky" in tests/`, sessionfeed.ToolCallSucceeded, "12 matches", 400*time.Millisecond),
		finishedTool("call-2", "read_file", "tests/integ/retry_test.go", sessionfeed.ToolCallSucceeded, "210 lines", 100*time.Millisecond),
		sessionfeed.ToolCallChanged{
			ToolCallID: "call-3", ToolName: "run_shell_command", KeyArgument: "go test ./integ/... -run Retry",
			Status: sessionfeed.ToolCallRunning, StartedAt: now.Add(-6200 * time.Millisecond),
			OutputTail:            []string{"=== RUN   TestRetryBackoff", "--- PASS: TestRetryBackoff (0.31s)"},
			HiddenOutputLineCount: 18,
		},
		sessionfeed.UsageRecorded{TotalTokens: 1240},
	)
	harness.advance(9 * time.Second)
}

func startTurn(harness *screenHarness, text string) {
	startTurnNumbered(harness, text, "turn-1", 1)
}

func startTurnNumbered(harness *screenHarness, text string, turnID string, number int) {
	harness.typeText(text)
	harness.press(key(tea.KeyEnter))
	harness.feed(sessionfeed.TurnStarted{TurnID: turnID, TurnNumber: number, StartedAt: harness.now})
	harness.advance(300 * time.Millisecond)
}

func finishedTool(callID string, name string, argument string, status sessionfeed.ToolCallStatus, summary string, duration time.Duration) sessionfeed.ToolCallChanged {
	return sessionfeed.ToolCallChanged{ToolCallID: callID, ToolName: name, KeyArgument: argument, Status: status, Summary: summary, Duration: duration}
}

func connectorApproval(interruptID string) sessionfeed.ApprovalRequested {
	return sessionfeed.ApprovalRequested{
		InterruptID: interruptID, ToolCallID: "call-4", ToolName: "slack_post_channel_message",
		SourceLabel: "connector", ScopeLabel: "slack/support-slack", EffectLabel: "write",
		Arguments: []sessionfeed.ApprovalArgument{
			{Name: "channel", Value: "#eng"},
			{Name: "text", Value: "Found 3 open issues about flaky tests: #812 TestRetryBackoff times out on arm64; #844 TestRetryJitter fails when GOMAXPROCS=1; #851 the nightly job retries the whole suite instead of the failed package."},
		},
		EffectSummary: "Posts to #eng with connection support-slack.",
	}
}

func fileApproval(interruptID string) sessionfeed.ApprovalRequested {
	return sessionfeed.ApprovalRequested{
		InterruptID: interruptID, ToolCallID: "call-5", ToolName: "edit_file",
		SourceLabel: "local file", ScopeLabel: "workspace", EffectLabel: "write",
		Arguments: []sessionfeed.ApprovalArgument{{Name: "path", Value: "src/retry.go"}},
		Preview: &sessionfeed.FileChangePreview{
			Path: "src/retry.go", AddedLineCount: 1, RemovedLineCount: 1,
			Lines: []sessionfeed.DiffLine{
				{Kind: sessionfeed.DiffLineContext, LineNumber: 11, Text: "func nextBackoff(attempt int) time.Duration {"},
				{Kind: sessionfeed.DiffLineRemoved, LineNumber: 12, Text: "\tbackoff := time.Second"},
				{Kind: sessionfeed.DiffLineAdded, LineNumber: 12, Text: "\tbackoff := 2 * time.Second"},
			},
		},
		EffectSummary: "Changes src/retry.go in the workspace.",
	}
}
