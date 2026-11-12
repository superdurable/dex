// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// Golden frames cover the key screens' rendering; other tests assert
// semantics.

// goldenHeight keeps golden screens short.
const goldenHeight = 24

var demoCommands = []sessionfeed.SlashCommand{
	{Name: "model", ArgumentHint: "ID", Description: "change the model"},
	{Name: "image", ArgumentHint: "PATH", Description: "attach an image to the next message"},
}

func TestEmptySessionFrames(t *testing.T) {
	widthMatrix(t, "empty_session", []int{40, 80}, func(harness *screenHarness) {})
}

func TestToolTurnFrames(t *testing.T) {
	widthMatrix(t, "tool_turn", []int{40, 60, 80, 140}, playToolTurn)
}

func TestApprovalFrames(t *testing.T) {
	widthMatrix(t, "approval_connector", []int{40, 80, 140}, func(harness *screenHarness) {
		startTurn(harness, "Post the summary to #eng.")
		harness.feed(connectorApproval("approval-1"))
	})
}

func TestApprovalWithPreviewFrame(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{height: goldenHeight})
	startTurn(harness, "Fix the backoff and tell #eng.")
	approval := connectorApproval("approval-1")
	approval.Deadline = harness.now.Add(9*time.Minute + 20*time.Second)
	harness.feed(approval, fileApproval("approval-2"))
	harness.press(key(tea.KeyTab))
	set.add("second of two, with preview", harness.frame())
	set.compare("approval_two_with_preview")
}

func TestQuestionFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	question := sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Which branch should I compare against?", Choices: []string{"main", "release-0.6"}, AllowsFreeText: true}
	harness := newScreenHarness(t, harnessOptions{height: goldenHeight})
	startTurn(harness, "Compare the branches.")
	harness.feed(question)
	set.add("choices and free text", harness.frame())
	noColor := newScreenHarness(t, harnessOptions{height: goldenHeight, profile: colorprofile.ASCII})
	startTurn(noColor, "Compare the branches.")
	noColor.feed(question)
	noColor.press(key(tea.KeyDown))
	set.add("no_color, second choice focused", noColor.frame())
	set.compare("question")
}

func TestPendingBarFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{height: goldenHeight})
	startTurn(harness, "Post the summary.")
	harness.feed(connectorApproval("approval-1"), fileApproval("approval-2"))
	harness.press(key(tea.KeyEscape))
	harness.feed(sessionfeed.QueuedMessagesChanged{Messages: []sessionfeed.QueuedMessage{
		{MessageID: "message-1", Text: "Also check the nightly job."},
	}})
	harness.press(key(tea.KeyTab), key(tea.KeyTab))
	set.add("queued message focused", harness.frame())
	harness.press(textKey('x'))
	set.add("delete confirmation", harness.frame())
	set.compare("pending_bar")
}

func TestErrorFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{height: goldenHeight})
	startTurn(harness, "Summarize.")
	harness.feed(
		sessionfeed.TurnEnded{TurnID: "turn-1", Outcome: sessionfeed.TurnOutcomeFailed, FailureText: "The turn failed: model call returned 401 (invalid API key). Set OPENAI_API_KEY and send again."},
		sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityWarning, Text: "Live output is off: the Dex server keeps no event stream. Replies appear when each turn ends."},
		sessionfeed.ServerReachabilityChanged{IsReachable: false},
		sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopped, ExitCode: 137},
	)
	set.add("errors, then the Worker stopped", harness.frame())
	set.compare("errors")
}

func TestHelpAndCompletionFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	harness := newScreenHarness(t, harnessOptions{height: goldenHeight, commands: demoCommands})
	harness.press(key(tea.KeyF1))
	set.add("help", harness.frame())
	harness.press(key(tea.KeyEscape))
	harness.typeText("/m")
	set.add("completion /m", harness.frame())
	set.compare("help_and_completion")
}

func TestNarrowAndASCIIFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	narrow := newScreenHarness(t, harnessOptions{height: goldenHeight, width: 30})
	playToolTurn(narrow)
	set.add("width 30", narrow.frame())
	ascii := newScreenHarness(t, harnessOptions{height: goldenHeight, isASCII: true})
	playToolTurn(ascii)
	ascii.feed(connectorApproval("approval-1"))
	set.add("ASCII symbols", ascii.frame())
	set.compare("narrow_and_ascii")
}

func TestStartupLogoFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	cases := []struct {
		label   string
		options harnessOptions
	}{
		{"UTF-8", harnessOptions{height: goldenHeight, showsLogo: true}},
		{"ASCII", harnessOptions{height: goldenHeight, showsLogo: true, isASCII: true}},
		{"no_color", harnessOptions{height: goldenHeight, showsLogo: true, profile: colorprofile.ASCII}},
	}
	for _, testCase := range cases {
		harness := newScreenHarness(t, testCase.options)
		set.add(testCase.label, harness.frame())
	}
	set.compare("startup_logo")
}

func TestStartupLogoSkipsSmallTerminals(t *testing.T) {
	for _, options := range []harnessOptions{{showsLogo: true, width: 39}, {showsLogo: true, height: 14}} {
		harness := newScreenHarness(t, options)
		if strings.Contains(harness.scrollbackText(), "Super Durable") {
			t.Fatalf("logo shown at %dx%d", options.width, options.height)
		}
	}
	harness := newScreenHarness(t, harnessOptions{showsLogo: true})
	harness.screen.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if count := strings.Count(harness.scrollbackText(), "Super Durable"); count != 1 {
		t.Fatalf("the logo shows once, got %d", count)
	}
}

func TestStartupLogoUsesTheBrandGreenOfTheBackground(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{showsLogo: true, profile: colorprofile.TrueColor})
	dark := harness.screen.View().Content
	require.Contains(t, dark, "  ▐█▌\x1b[38;2;112;238;169m", "the bar takes the default foreground; a dark background gets #70eea9")

	harness.screen.Update(tea.BackgroundColorMsg{Color: color.White})
	light := harness.screen.View().Content
	require.Contains(t, light, "  ▐█▌\x1b[38;2;0;134;80m", "a light background gets #008650")
	require.NotContains(t, light, "38;2;112;238;169m")
}

func TestDexWebURLsAreLinks(t *testing.T) {
	const webURL = "http://127.0.0.1:8802"
	harness := newScreenHarness(t, harnessOptions{})
	welcome := harness.screen.View().Content
	require.Contains(t, welcome, "\x1b]8;;"+webURL+"\x1b\\", "the welcome Dex Web URL is a link")
	require.Contains(t, welcome, "4;34", "the link is underlined in the accent color")
	isWebRowShown := false
	for _, line := range harness.screen.welcome().Render(25, harness.screen.theme) {
		if strings.Contains(line, "Dex Web:") {
			isWebRowShown = true
			require.LessOrEqual(t, displayWidthForTest(line), 25)
			require.True(t, strings.HasSuffix(line, "\x1b]8;;\x1b\\"), "a truncated link still closes: %q", line)
		}
	}
	require.True(t, isWebRowShown)

	sessionURL := webURL + "/agents/sessions/s-a1b2c3"
	harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "Dex Web: " + sessionURL})
	notice := harness.screen.View().Content
	require.Contains(t, notice, "\x1b]8;;"+sessionURL+"\x1b\\", "the notice links the whole URL")
	require.Contains(t, stripForTest(notice), "Dex Web: "+sessionURL)

	narrow := renderNoticeLine(sessionfeed.NoticeSeverityError, "Dex Web did not open. Open "+sessionURL+" yourself.", 30, harness.screen.theme)
	for _, line := range narrow {
		require.LessOrEqual(t, displayWidthForTest(line), 30, "escape bytes take no cells")
		if strings.Contains(line, "8802") || strings.Contains(line, "sessions") {
			require.Contains(t, line, "\x1b]8;;"+sessionURL+"\x1b\\", "each wrapped piece links the whole URL")
			require.Contains(t, line, "\x1b]8;;\x1b\\", "each line closes its link")
		}
	}
	require.Equal(t, "✗DexWebdidnotopen.Open"+sessionURL+"yourself.", strings.Join(strings.Fields(stripForTest(strings.Join(narrow, ""))), ""), "wrapping keeps every character")
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
