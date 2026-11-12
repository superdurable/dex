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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
	"github.com/superdurable/dex/cli/internal/agentsterminal/typedpaths"
)

const testSessionID = "openai-sdk-20261111-142233-a1b2c3"

// shellOutput is a tool output longer than one pager page.
func shellOutput(lineCount int) string {
	lines := make([]string, 0, lineCount)
	for index := 1; index <= lineCount; index++ {
		lines = append(lines, fmt.Sprintf("=== RUN   TestCase%02d", index))
	}
	return strings.Join(lines, "\n")
}

func outputTool(callID string, turnID string, name string, output string) sessionfeed.ToolCallChanged {
	call := finishedTool(callID, name, "go test ./integ/...", sessionfeed.ToolCallSucceeded, "exit 0", 1500*time.Millisecond)
	call.TurnID = turnID
	call.Arguments = []sessionfeed.ApprovalArgument{{Name: "command", Value: "go test ./integ/..."}, {Name: "cwd", Value: "~/dex-agent-workspace"}}
	call.OutputText = output
	return call
}

func pressCtrlO(harness *screenHarness) {
	harness.press(ctrlKey('o'))
}

func TestCtrlOOpensTheLastToolOutput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 20})
	pressCtrlO(harness)
	require.Nil(t, harness.screen.pager)
	require.Contains(t, harness.scrollbackText(), "no tool output yet")
	startTurn(harness, "Run the tests.")
	harness.feed(
		outputTool("call-1", "turn-1", "read_file", "first tool"),
		outputTool("call-2", "turn-1", "run_shell_command", shellOutput(40)+"\nTOTAL \x1b[31mred\x1b[0m"),
		sessionfeed.TurnEnded{TurnID: "turn-1"},
	)
	pressCtrlO(harness)
	require.NotNil(t, harness.screen.pager)
	frame := harness.plainFrame()
	require.Contains(t, frame, "Tool output")
	require.Contains(t, frame, "✓ run_shell_command  go test ./integ/...")
	require.Contains(t, frame, "Arguments")
	require.Contains(t, frame, "cwd")
	require.Contains(t, frame, "Output")
	require.Contains(t, frame, "j/k scroll · / search · q back")
	require.Nil(t, harness.screen.View().Cursor, "the pager hides the input cursor")
	harness.press(key(tea.KeyEnd))
	frame = harness.plainFrame()
	require.Contains(t, frame, "TestCase40")
	require.Contains(t, frame, "TOTAL [31mred[0m"[:6], "the output's last line shows")
	require.NotContains(t, harness.frame(), "\x1b[31mred", "tool output cannot write escape sequences")
	require.NotContains(t, harness.scrollbackRaw(), "TestCase40", "the transcript keeps only the row")

	// Search finds a row above and n moves on.
	harness.press(key(tea.KeyHome), textKey('/'))
	harness.typeText("testcase3")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), "n next · N previous")
	firstMatch := harness.screen.pager.matchRow
	require.GreaterOrEqual(t, firstMatch, 0)
	harness.press(textKey('n'))
	require.Greater(t, harness.screen.pager.matchRow, firstMatch)
	harness.press(textKey('/'))
	harness.typeText("missing words")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.plainFrame(), `no more matches for "missing words"`)
	harness.press(textKey('q'))
	require.Nil(t, harness.screen.pager)
	require.NotNil(t, harness.screen.View().Cursor, "the input has focus again")
}

func TestToolsPickerOpensOneOutput(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	harness.typeText("/tools")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.scrollbackText(), "no tool ran yet")
	startTurn(harness, "Look around.")
	harness.feed(outputTool("call-1", "turn-1", "read_file", "old turn output"), sessionfeed.TurnEnded{TurnID: "turn-1"})
	startTurnNumbered(harness, "Look again.", "turn-2", 2)
	harness.feed(
		outputTool("call-2", "turn-2", "search_files", "first of turn 2"),
		outputTool("call-3", "turn-2", "read_file", "second of turn 2"),
	)
	harness.typeText("/tools")
	harness.press(key(tea.KeyEnter))
	frame := harness.plainFrame()
	require.Contains(t, frame, "Tools of the latest turn")
	require.Contains(t, frame, "Enter open · j/k move · Esc close")
	picker := terminaltheme.StripStyles(strings.Join(harness.screen.renderToolPicker(80, 20), "\n"))
	require.Equal(t, 1, strings.Count(picker, "read_file"), "only the latest turn's calls are listed")
	require.Contains(t, frame, "▸ ✓ read_file", "the newest call has focus")
	harness.press(textKey('k'), key(tea.KeyEnter))
	require.False(t, harness.screen.isToolPickerOpen)
	require.Contains(t, harness.plainFrame(), "first of turn 2")
	harness.press(key(tea.KeyEscape))
	require.Nil(t, harness.screen.pager)
}

func TestApprovalWhilePagerIsOpenWaitsInThePendingBar(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	startTurn(harness, "Run the tests.")
	harness.feed(outputTool("call-1", "turn-1", "run_shell_command", "ok"))
	pressCtrlO(harness)
	harness.feed(connectorApproval("approval-1"))
	require.False(t, harness.screen.isPopupOpen, "the approval does not open under the pager")
	require.Contains(t, harness.plainFrame(), "? 1 approval waiting")
	harness.press(textKey('y'))
	require.Empty(t, approvalActions(harness), "y is not an approval in the pager")
	harness.press(textKey('q'))
	require.Contains(t, harness.plainFrame(), "1 approval waiting · Tab opens")
}

func approvalActions(harness *screenHarness) []sessionfeed.ResolveApproval {
	var approvals []sessionfeed.ResolveApproval
	for _, action := range harness.source.recordedActions() {
		if approval, isApproval := action.(sessionfeed.ResolveApproval); isApproval {
			approvals = append(approvals, approval)
		}
	}
	return approvals
}

func earlierPage(sessionID string, requestID int64, before int64, earlier int64, texts ...string) sessionfeed.EarlierMessagesRead {
	page := sessionfeed.EarlierMessagesRead{SessionID: sessionID, RequestID: requestID, BeforeSequence: before, EarlierBeforeSequence: earlier}
	for _, text := range texts {
		page.Messages = append(page.Messages,
			sessionfeed.UserMessageRecorded{Text: "question " + text},
			sessionfeed.ToolCallChanged{ToolCallID: "call-" + text, ToolName: "read_file", KeyArgument: "notes-" + text + ".md", Status: sessionfeed.ToolCallFromHistory, Summary: "12 lines"},
			sessionfeed.AssistantMessageRecorded{Text: "answer **" + text + "**"},
		)
	}
	return page
}

func TestHistoryPagerReadsEarlierPages(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 30})
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	require.Nil(t, harness.screen.pager)
	require.Contains(t, harness.scrollbackText(), "every message of this session is on screen")

	harness.feed(sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 101})
	require.Contains(t, harness.scrollbackText(), "· earlier messages: /history")
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.ReadEarlierMessages{SessionID: testSessionID, BeforeSequence: 101, RequestID: 1}, harness.lastAction())
	require.Contains(t, harness.plainFrame(), "reading earlier messages…")

	harness.feed(earlierPage("another-session", 1, 101, 51, "stale"))
	require.Contains(t, harness.plainFrame(), "reading earlier messages…", "an answer for another session is dropped")
	harness.feed(earlierPage(testSessionID, 1, 101, 51, "C", "D"))
	frame := harness.plainFrame()
	require.Contains(t, frame, "question D")
	require.Contains(t, frame, "· read_file")
	require.Contains(t, frame, "answer D")
	require.Contains(t, frame, "k at the top reads earlier")

	actionCount := len(harness.source.recordedActions())
	harness.press(key(tea.KeyHome), textKey('k'))
	require.Equal(t, sessionfeed.ReadEarlierMessages{SessionID: testSessionID, BeforeSequence: 51, RequestID: 2}, harness.lastAction())
	harness.press(textKey('k'))
	require.Len(t, harness.source.recordedActions(), actionCount+1, "one read at a time")
	firstAnswer := earlierPage(testSessionID, 2, 51, 0, "B")
	firstAnswer.DeletedBeforeSequence = 3
	harness.feed(firstAnswer, earlierPage(testSessionID, 2, 51, 0, "A"))
	text := strings.Join(harness.screen.pager.bodyRows(80, harness.screen.theme), "\n")
	plain := terminaltheme.StripStyles(text)
	requireInOrder(t, plain, "messages before #3 were removed by the retention window", "question B", "question C", "question D", "the chat continues below")
	require.NotContains(t, plain, "question A", "only the first answer to a request applies")
	harness.press(textKey('q'))
	require.Nil(t, harness.screen.pager)
}

func TestHistoryPagerShowsAFailedRead(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	harness.feed(sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 9})
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	harness.feed(sessionfeed.EarlierMessagesRead{SessionID: testSessionID, RequestID: 1, BeforeSequence: 9, FailureText: "Could not read earlier messages: unavailable. Try /history again."})
	require.Contains(t, harness.plainFrame(), "✗ Could not read earlier messages: unavailable.")
	require.True(t, harness.screen.pager.canReadEarlier(), "k at the top tries again")
}

func TestSessionSwitchClosesTheHistoryPager(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	harness.feed(sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 9})
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-150000-ffffff", IsNewSession: true})
	require.Nil(t, harness.screen.pager)
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	require.Contains(t, harness.scrollbackText(), "every message of this session is on screen")
}

func newImageHarness(t *testing.T) (*screenHarness, string) {
	t.Helper()
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "diagram.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "diagram-old.png"), []byte("png"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("text"), 0o600))
	harness := newScreenHarness(t, harnessOptions{commands: demoCommands, skipIntro: true})
	resolver := typedpaths.NewPathResolver(directory, directory)
	harness.screen.pathResolver = &resolver
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: testSessionID, IsNewSession: true}, sessionfeed.SessionLoaded{})
	return harness, imagePath
}

func TestPastedImagePathAsksToAttach(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.screen.Update(tea.PasteMsg{Content: "'" + imagePath + "'"})
	frame := harness.plainFrame()
	require.Contains(t, frame, "? Attach diagram.png?")
	require.Contains(t, frame, "Enter attach · Esc keep text")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.RunSlashCommand{Name: "image", Argument: imagePath}, harness.lastAction())
	require.True(t, harness.screen.input.IsEmpty())

	harness.screen.Update(tea.PasteMsg{Content: imagePath})
	harness.press(key(tea.KeyEscape))
	require.Equal(t, imagePath, harness.screen.input.Value(), "Esc keeps the pasted text")
	harness.screen.input.Clear()

	harness.screen.Update(tea.PasteMsg{Content: imagePath})
	harness.press(textKey('!'))
	require.Equal(t, imagePath+"!", harness.screen.input.Value(), "typing keeps the text and types on")
	harness.screen.input.Clear()

	harness.screen.Update(tea.PasteMsg{Content: filepath.Join(filepath.Dir(imagePath), "notes.txt")})
	require.Nil(t, harness.screen.pendingPastedImage, "only image files ask")
}

func TestPastedImagePathNeedsTheImageCommand(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{})
	directory := t.TempDir()
	imagePath := filepath.Join(directory, "diagram.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("png"), 0o600))
	resolver := typedpaths.NewPathResolver(directory, directory)
	harness.screen.pathResolver = &resolver
	harness.screen.Update(tea.PasteMsg{Content: imagePath})
	require.Nil(t, harness.screen.pendingPastedImage)
	require.Equal(t, imagePath, harness.screen.input.Value())
}

func TestTabCompletesImagePaths(t *testing.T) {
	harness, _ := newImageHarness(t)
	harness.typeText("/image dia")
	harness.press(key(tea.KeyTab))
	require.Equal(t, "/image diagram", harness.screen.input.Value())
	frame := harness.plainFrame()
	require.Contains(t, frame, "diagram-old.png")
	require.Contains(t, frame, "diagram.png")
	harness.typeText(".")
	require.Empty(t, harness.screen.pathCompletions, "typing clears the candidates")
	harness.press(key(tea.KeyTab))
	require.Equal(t, "/image diagram.png", harness.screen.input.Value())
	harness.screen.input.SetValue("/image zzz")
	harness.press(key(tea.KeyTab))
	require.Contains(t, harness.scrollbackText(), "no file matches zzz")
}

func TestRemovingAnImageChipDetachesIt(t *testing.T) {
	harness, _ := newImageHarness(t)
	harness.feed(sessionfeed.ImageAttached{Attachment: sessionfeed.ImageAttachment{Name: "diagram.png", MimeType: "image/png", SizeBytes: 2048}})
	harness.press(key(tea.KeyBackspace))
	require.Empty(t, harness.screen.attachments)
	require.Equal(t, sessionfeed.DetachImage{}, harness.lastAction())
}

func TestRunIsGreyInCompletionWhenItCannotRun(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{skipIntro: true})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: testSessionID, SupportsPlanMode: true}, sessionfeed.SessionLoaded{})
	harness.typeText("/r")
	require.Contains(t, harness.plainFrame(), "/run  no draft plan to run")
	harness.feed(sessionfeed.PlanChanged{Revision: 2, Status: sessionfeed.PlanStatusDraft, IsInPlanInteraction: true, Items: []sessionfeed.PlanItem{{Title: "Write tests"}}})
	require.Contains(t, harness.plainFrame(), "/run  approve and run the draft plan")
}

func TestToolRowsCountReturnedImages(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{width: 100})
	startTurn(harness, "Take a screenshot.")
	call := finishedTool("call-1", "browser_screenshot", "https://example.com", sessionfeed.ToolCallSucceeded, "saved", 800*time.Millisecond)
	call.ImageCount = 2
	harness.feed(call)
	require.Contains(t, harness.scrollbackText(), "saved ▣ 2")
}

// Turn IDs repeat across sessions, so a switch forgets the old calls.
func TestSessionSwitchForgetsTheToolOutputs(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	startTurn(harness, "Run the tests.")
	harness.feed(outputTool("call-1", "turn-1", "run_shell_command", "old session output"), sessionfeed.TurnEnded{TurnID: "turn-1"})
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-150000-ffffff", IsNewSession: true}, sessionfeed.SessionLoaded{})
	pressCtrlO(harness)
	require.Nil(t, harness.screen.pager)
	require.Contains(t, harness.scrollbackText(), "no tool output yet")
	startTurn(harness, "Run them again.")
	harness.feed(outputTool("call-9", "turn-1", "run_shell_command", "new session output"))
	require.Len(t, harness.screen.turnTools, 1)
	pressCtrlO(harness)
	require.Contains(t, harness.plainFrame(), "new session output")
}

// Lower-casing changes the byte length of some letters; the match must not.
func TestPagerSearchHighlightsAfterLettersThatChangeLength(t *testing.T) {
	row := "İİİ Ⱥ the needle here"
	highlighted := highlightMatch(row, "NEEDLE")
	require.Equal(t, row, terminaltheme.StripStyles(highlighted))
	require.Contains(t, highlighted, "\x1b[7mneedle")
	harness := newScreenHarness(t, harnessOptions{height: 20})
	startTurn(harness, "Read it.")
	harness.feed(outputTool("call-1", "turn-1", "read_file", row), sessionfeed.TurnEnded{TurnID: "turn-1"})
	pressCtrlO(harness)
	harness.press(textKey('/'))
	harness.typeText("needle")
	harness.press(key(tea.KeyEnter))
	require.GreaterOrEqual(t, harness.screen.pager.matchRow, 0)
	require.Contains(t, harness.plainFrame(), row)
}

// A path pasted after "/image " is the command's argument, not a new
// attachment.
func TestPastedImagePathAfterImageCommandIsText(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	harness.typeText("/image ")
	harness.screen.Update(tea.PasteMsg{Content: imagePath})
	require.Nil(t, harness.screen.pendingPastedImage)
	require.Equal(t, "/image "+imagePath, harness.screen.input.Value())
}

// An answer for a pager closed and opened again never fills the new one.
func TestHistoryAnswerForAClosedPagerIsDropped(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 30})
	harness.feed(sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 101})
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	harness.press(textKey('q'))
	harness.typeText("/history")
	harness.press(key(tea.KeyEnter))
	require.Equal(t, sessionfeed.ReadEarlierMessages{SessionID: testSessionID, BeforeSequence: 101, RequestID: 2}, harness.lastAction())
	harness.feed(earlierPage(testSessionID, 1, 101, 51, "from the closed pager"))
	require.Contains(t, harness.plainFrame(), "reading earlier messages…", "the first pager's answer is dropped")
	harness.feed(earlierPage(testSessionID, 2, 101, 51, "for this pager"))
	frame := harness.plainFrame()
	require.Contains(t, frame, "question for this pager")
	require.NotContains(t, frame, "from the closed pager")
}

// Any switch closes the readers and prompts of the session it leaves.
func TestSessionSwitchClosesEveryPagerAndPrompt(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	startTurn(harness, "Run the tests.")
	harness.feed(outputTool("call-1", "turn-1", "run_shell_command", "old session secret output"))
	pressCtrlO(harness)
	require.NotNil(t, harness.screen.pager)
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-150000-ffffff", IsNewSession: true})
	require.Nil(t, harness.screen.pager, "the tool pager closes")
	require.NotContains(t, harness.plainFrame(), "old session secret output")

	harness.feed(sessionfeed.SessionLoaded{})
	harness.screen.Update(tea.PasteMsg{Content: imagePath})
	require.NotNil(t, harness.screen.pendingPastedImage)
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: "openai-sdk-20261111-160000-eeeeee", IsNewSession: true})
	require.Nil(t, harness.screen.pendingPastedImage, "the attach prompt closes")
	require.Equal(t, imagePath, harness.screen.input.Value(), "the pasted text stays")
}

func switchSession(harness *screenHarness, sessionID string) {
	harness.feed(sessionfeed.SessionOpened{Command: "openai-sdk", SessionID: sessionID, IsNewSession: true}, sessionfeed.SessionLoaded{})
}

// The old session's approvals and questions never reach the new one: not
// open, not waiting, not behind Tab.
func TestSessionSwitchDropsTheOldInterrupts(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 30})
	startTurn(harness, "Post the summary.")
	harness.feed(connectorApproval("approval-1"))
	require.True(t, harness.screen.isPopupOpen)
	switchSession(harness, "openai-sdk-20261111-150000-ffffff")
	require.False(t, harness.screen.isPopupOpen, "the approval popup closes")
	require.NotContains(t, harness.plainFrame(), "Approve slack_post_channel_message?")
	harness.press(key(tea.KeyTab))
	require.False(t, harness.screen.isPopupOpen, "Tab finds nothing waiting")
	require.Equal(t, pendingFocusNone, harness.screen.pendingFocus)
	harness.press(textKey('y'))
	require.Empty(t, approvalActions(harness), "y is text, not an approval")

	question := sessionfeed.QuestionAsked{InterruptID: "question-1", Question: "Which branch?", Choices: []string{"main"}}
	harness.screen.input.Clear()
	harness.feed(question)
	harness.press(key(tea.KeyEscape))
	require.False(t, harness.screen.isPopupOpen)
	require.Contains(t, harness.plainFrame(), "1 question waiting")
	switchSession(harness, "openai-sdk-20261111-160000-eeeeee")
	require.NotContains(t, harness.plainFrame(), "question waiting")
	harness.press(key(tea.KeyTab))
	require.False(t, harness.screen.isPopupOpen, "Tab after the switch opens no old question")
	require.Empty(t, harness.screen.interrupts)
}

// A completion read for the old session never fills the new one's input.
func TestImagePathCompletionForTheOldSessionIsDropped(t *testing.T) {
	harness, _ := newImageHarness(t)
	harness.typeText("/image dia")
	_, command := harness.screen.Update(key(tea.KeyTab))
	switchSession(harness, "openai-sdk-20261111-150000-ffffff")
	harness.runCommand(command)
	require.Equal(t, "/image dia", harness.screen.input.Value())
}

// An empty result of a cut-short scan says so instead of "no file matches".
func TestPartialScanWithoutAMatchSaysSo(t *testing.T) {
	harness, _ := newImageHarness(t)
	harness.typeText("/image zzz")
	harness.screen.applyPathCompletions(pathCompletionsFoundMsg{
		requestID: harness.screen.pathCompletionRequestID, sessionID: testSessionID, inputText: "/image zzz", partial: "zzz",
		completions: typedpaths.PathCompletions{IsPartial: true},
	})
	text := harness.scrollbackText()
	require.Contains(t, text, "no match in the first 2000 entries; type more of the name")
	require.NotContains(t, text, "no file matches")
}

// The diff preview says which side ends without a newline.
func TestFileChangePreviewShowsTheMissingNewline(t *testing.T) {
	theme := terminaltheme.NewTheme(colorprofile.ANSI, true)
	preview := sessionfeed.FileChangePreview{Path: "notes/a.txt", AddedLineCount: 1, RemovedLineCount: 1, Lines: []sessionfeed.DiffLine{
		{Kind: sessionfeed.DiffLineRemoved, LineNumber: 1, Text: "last", IsMissingFinalNewline: true},
		{Kind: sessionfeed.DiffLineAdded, LineNumber: 1, Text: "last"},
	}}
	plain := terminaltheme.StripStyles(strings.Join(renderFileChangePreview(preview, 70, theme), "\n"))
	requireInOrder(t, plain, "- last", `\ the file now has no newline at its end`, "+ last")
	preview.Lines[0].IsMissingFinalNewline, preview.Lines[1].IsMissingFinalNewline = false, true
	plain = terminaltheme.StripStyles(strings.Join(renderFileChangePreview(preview, 70, theme), "\n"))
	requireInOrder(t, plain, "- last", "+ last", `\ no newline at the end after this write`)
}

// The first Ctrl-C closes a reader; the draft behind it follows the usual
// idle rules.
func TestCtrlCClosesThePagerBeforeTheDraft(t *testing.T) {
	harness := newScreenHarness(t, harnessOptions{height: 24})
	startTurn(harness, "Run the tests.")
	harness.feed(outputTool("call-1", "turn-1", "run_shell_command", "ok"), sessionfeed.TurnEnded{TurnID: "turn-1"})
	harness.typeText("unsent draft")
	pressCtrlO(harness)
	harness.press(ctrlKey('c'))
	require.Nil(t, harness.screen.pager)
	require.False(t, harness.screen.isExiting)
	require.Equal(t, "unsent draft", harness.screen.input.Value())
	harness.press(ctrlKey('c'))
	require.False(t, harness.screen.isExiting)
	require.True(t, harness.screen.input.IsEmpty())
	harness.press(ctrlKey('c'))
	require.True(t, harness.screen.isExiting)
}

// Tab reads the disk off the event loop, and a result for an input that
// changed meanwhile is dropped.
func TestImagePathCompletionRunsOffTheEventLoop(t *testing.T) {
	harness, _ := newImageHarness(t)
	harness.typeText("/image dia")
	_, command := harness.screen.Update(key(tea.KeyTab))
	require.NotNil(t, command)
	require.Equal(t, "/image dia", harness.screen.input.Value(), "the key returns before the directory is read")
	harness.typeText("x")
	harness.runCommand(command)
	require.Equal(t, "/image diax", harness.screen.input.Value(), "a stale result is dropped")
	require.Empty(t, harness.screen.pathCompletions)
}

// The completed argument keeps the user's quotes or escapes.
func TestImagePathCompletionKeepsQuoting(t *testing.T) {
	harness, imagePath := newImageHarness(t)
	spacedPath := filepath.Join(filepath.Dir(imagePath), "my diagram.png")
	require.NoError(t, os.WriteFile(spacedPath, []byte("png"), 0o600))
	harness.typeText(`/image "my`)
	harness.press(key(tea.KeyTab))
	require.Equal(t, `/image "my diagram.png"`, harness.screen.input.Value())
	harness.screen.input.SetValue(`/image my\ d`)
	harness.press(key(tea.KeyTab))
	require.Equal(t, `/image my\ diagram.png`, harness.screen.input.Value())
}

func TestPagerFrames(t *testing.T) {
	set := newGoldenFrameSet(t)
	for _, width := range []int{40, 80} {
		harness := newScreenHarness(t, harnessOptions{width: width, height: goldenHeight})
		startTurn(harness, "Run the tests.")
		harness.feed(outputTool("call-1", "turn-1", "run_shell_command", shellOutput(30)), sessionfeed.TurnEnded{TurnID: "turn-1"})
		pressCtrlO(harness)
		set.add(fmt.Sprintf("tool output, ansi width %d", width), harness.frame())
	}
	noColor := newScreenHarness(t, harnessOptions{width: 80, height: goldenHeight, profile: colorprofile.ASCII})
	noColor.feed(sessionfeed.HistoryWindowChanged{EarlierBeforeSequence: 101})
	noColor.typeText("/history")
	noColor.press(key(tea.KeyEnter))
	noColor.feed(earlierPage(testSessionID, 1, 101, 51, "C"))
	noColor.press(textKey('/'))
	noColor.typeText("answer")
	noColor.press(key(tea.KeyEnter))
	set.add("history with a match, no_color width 80", noColor.frame())
	set.compare("pager")
}
