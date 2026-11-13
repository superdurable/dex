// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package chatscreen

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// requireWellFormedScreen checks a frame has height rows, each within width
// and without a trailing space.
func requireWellFormedScreen(t *testing.T, content string, width int, height int) {
	t.Helper()
	lines := splitLines(content)
	require.Len(t, lines, height, "the frame fills the screen")
	for _, line := range lines {
		require.LessOrEqual(t, displayWidthForTest(line), width, "line %q is wider than %d", line, width)
		require.False(t, strings.HasSuffix(stripForTest(line), " "), "line %q ends with a space", line)
	}
}

func TestResizeLaysOutEveryFrameAgain(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 140, height: 30, showsLogo: true})
	playToolTurn(harness)
	first := harness.screen.View().Content
	for _, size := range [][2]int{{140, 30}, {80, 30}, {40, 30}, {80, 20}, {140, 30}} {
		harness.screen.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		content := harness.screen.View().Content
		requireWellFormedScreen(t, content, size[0], size[1])
		plain := stripForTest(content)
		require.Equal(t, 1, strings.Count(plain, "type to queue a message"), "one input at %v:\n%s", size, plain)
		require.Equal(t, 1, strings.Count(plain, "running tools"), "one status at %v:\n%s", size, plain)
	}
	require.Equal(t, first, harness.screen.View().Content, "returning to 140x30 gives the first frame back")
}

func TestPopupFitsShortScreensWithLongContent(t *testing.T) {
	longApproval := connectorApproval("approval-1")
	var consequence []string
	for step := 1; step <= 12; step++ {
		consequence = append(consequence, fmt.Sprintf("Step %d posts the summary to #eng with connection support-slack.", step))
	}
	longApproval.EffectSummary = strings.Join(consequence, " ")
	longApproval.Arguments = append(longApproval.Arguments, sessionfeed.ApprovalArgument{Name: "blocks", Value: strings.Repeat("line\n", 30)})
	manyChoices := sessionfeed.QuestionAsked{InterruptID: "question-1", Question: strings.Repeat("Which branch should I compare against? ", 12)}
	for index := range 12 {
		manyChoices.Choices = append(manyChoices.Choices, fmt.Sprintf("release-%d", index))
	}
	for _, height := range []int{9, 12, 14, 24} {
		approval := newScreenHarness(t, harnessOptions{width: 80, height: height})
		startTurn(approval, "Post it.")
		approval.feed(longApproval)
		content := approval.screen.View().Content
		requireWellFormedScreen(t, content, 80, height)
		plain := stripForTest(content)
		require.Contains(t, plain, "Approve slack_post_channel_message?", "height %d", height)
		actionCount := len(approval.source.recordedActions())
		if strings.Contains(plain, "y approve") {
			require.Contains(t, withoutSpaces(popupText(plain)), withoutSpaces(longApproval.EffectSummary), "approve shows only with the whole consequence at height %d:\n%s", height, plain)
		} else {
			require.Contains(t, plain, "Enlarge the window or press e to read the consequence.", "height %d:\n%s", height, plain)
			approval.press(textKey('y'), key(tea.KeyEnter))
			require.Len(t, approval.source.recordedActions(), actionCount, "approve is refused before the consequence was read at height %d", height)
			approval.press(textKey('e'))
			var seenLines []string
			for range 20 {
				for _, line := range splitLines(stripForTest(approval.screen.View().Content)) {
					inner := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│"))
					if strings.HasPrefix(strings.TrimSpace(line), "│") && !strings.HasPrefix(inner, "lines ") && !slices.Contains(seenLines, inner) {
						seenLines = append(seenLines, inner)
					}
				}
				approval.press(key(tea.KeyPgDown))
			}
			require.Equal(t, withoutSpaces(longApproval.EffectSummary), withoutSpaces(strings.Join(seenLines, " ")), "the reading view shows every consequence line at height %d", height)
			require.True(t, approval.screen.popup.hasReadConsequence)
			approval.press(textKey('e'))
		}
		approval.press(textKey('y'))
		require.Equal(t, sessionfeed.ResolveApproval{InterruptID: "approval-1", IsApproved: true}, approval.lastAction(), "height %d", height)

		question := newScreenHarness(t, harnessOptions{width: 80, height: height})
		startTurn(question, "Ask.")
		question.feed(manyChoices)
		question.press(key(tea.KeyUp))
		for range 11 {
			question.press(key(tea.KeyDown))
		}
		content = question.screen.View().Content
		requireWellFormedScreen(t, content, 80, height)
		plain = stripForTest(content)
		require.Contains(t, plain, "The agent asks", "height %d", height)
		require.Contains(t, plain, "▸ release-11", "the focused choice stays visible at height %d:\n%s", height, plain)
	}

	harness := newScreenHarness(t, harnessOptions{width: 80, height: 30})
	startTurn(harness, "Post it.")
	harness.feed(longApproval)
	plain := stripForTest(harness.screen.View().Content)
	require.Contains(t, plain, "↑", "hidden argument rows are marked")
	require.Contains(t, withoutSpaces(popupText(plain)), withoutSpaces(longApproval.EffectSummary), "the consequence has reserved rows")
	harness.press(key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp), key(tea.KeyPgUp))
	scrolled := stripForTest(harness.screen.View().Content)
	require.Contains(t, scrolled, "channel  #eng", "PgUp scrolls the arguments:\n%s", scrolled)
	require.Contains(t, withoutSpaces(popupText(scrolled)), withoutSpaces(longApproval.EffectSummary), "the consequence stays while the arguments scroll")
}

func TestMessageViewScrollsAndFollows(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 80, height: 20})
	for index := range 40 {
		harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: fmt.Sprintf("note %02d", index)})
	}
	require.Contains(t, stripForTest(harness.screen.View().Content), "note 39", "the view follows the newest lines")

	harness.press(key(tea.KeyPgUp))
	scrolled := stripForTest(harness.screen.View().Content)
	require.NotContains(t, scrolled, "note 39")
	require.False(t, harness.screen.scroll.isFollowing)

	harness.feed(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "note 40"})
	scrolled = stripForTest(harness.screen.View().Content)
	require.Contains(t, scrolled, "↓ new messages · End follows")
	require.NotContains(t, scrolled, "note 40", "a scrolled view stays where the user left it")

	harness.press(key(tea.KeyEnd))
	following := stripForTest(harness.screen.View().Content)
	require.Contains(t, following, "note 40")
	require.NotContains(t, following, "new messages")

	harness.press(key(tea.KeyPgUp))
	require.False(t, harness.screen.scroll.isFollowing, "PgUp scrolls up")
	for range 20 {
		harness.press(key(tea.KeyPgDown))
	}
	require.True(t, harness.screen.scroll.isFollowing, "scrolling to the end follows again")

	harness.press(key(tea.KeyHome))
	require.Contains(t, stripForTest(harness.screen.View().Content), "command:", "Home on an empty input shows the welcome area")
	harness.typeText("draft")
	harness.press(key(tea.KeyEnd))
	require.False(t, harness.screen.scroll.isFollowing, "with a draft, End moves the input cursor instead")
}

func TestViewIsFullScreenAndLeavesTheMouseToTheTerminal(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	view := harness.screen.View()
	requireWellFormedScreen(t, view.Content, 80, 40)
	require.True(t, view.AltScreen)
	require.Equal(t, tea.MouseModeNone, view.MouseMode, "a plain drag selects text")
	require.Contains(t, stripForTest(view.Content), "Drag selects text")
}

func TestExitSummary(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	require.Equal(t, "Session openai-sdk-20261111-142233-a1b2c3 keeps running. Resume it with: dex-agents-openai-sdk --resume openai-sdk-20261111-142233-a1b2c3", harness.screen.ExitSummary())
	harness.feed(sessionfeed.WorkerStateChanged{State: sessionfeed.WorkerStateStopped, ExitCode: 137})
	require.True(t, strings.HasPrefix(harness.screen.ExitSummary(), "The Worker stopped (exit 137). Session openai-sdk-"))
}

func TestSessionSwitchHandedOffExitsWithoutSummary(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	harness.feed(sessionfeed.SessionSwitchHandedOff{SessionID: "openai-sdk-20261111-150000-ffffff"})
	require.True(t, harness.screen.isExiting)
	require.Equal(t, 0, harness.screen.ExitCode())
	require.Empty(t, harness.screen.ExitSummary(), "the launcher says how to resume")
}

func TestCopyLastReply(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	harness.typeText("/copy")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.scrollbackText(), "· no reply to copy yet")
	startTurn(harness, "Go.")
	harness.feed(
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-1", Text: "First part."},
		sessionfeed.AssistantMessageRecorded{TurnID: "turn-1", StepExecutionID: "step-2", Text: "Second part."},
	)
	require.Equal(t, "First part.\n\nSecond part.", harness.screen.lastReplyText)
	_, command := harness.screen.Update(key(tea.KeyEnter))
	require.Nil(t, command, "Enter on an empty input does nothing")
	harness.typeText("/copy")
	_, command = harness.screen.Update(key(tea.KeyEnter))
	require.NotNil(t, command, "/copy returns the clipboard command")
	require.Contains(t, harness.scrollbackText(), "· copied the last reply to the clipboard")
}

// popupText returns the text inside popup borders as one space-joined line.
func popupText(screenText string) string {
	var words []string
	for _, line := range splitLines(screenText) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "│") {
			continue
		}
		words = append(words, strings.Fields(strings.Trim(trimmed, "│"))...)
	}
	return strings.Join(words, " ")
}

// withoutSpaces drops all whitespace, so wrapping at hyphens does not matter.
func withoutSpaces(text string) string {
	return strings.Join(strings.Fields(text), "")
}
