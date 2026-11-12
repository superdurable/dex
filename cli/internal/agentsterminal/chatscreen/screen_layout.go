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
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// messageScroll is the message view position. Off the bottom, its top is
// anchored to an entry, surviving resizes.
type messageScroll struct {
	isFollowing bool
	anchor      transcriptAnchor
	// seenEntryCount is the entry count when the user scrolled away; more
	// entries show the new-messages indicator.
	seenEntryCount int
}

// messageContent is the transcript followed by the live activity. It never
// copies the transcript, whose lines are shared.
type messageContent struct {
	transcriptLines []string
	activityLines   []string
}

func (content messageContent) Len() int {
	if len(content.activityLines) == 0 {
		return len(content.transcriptLines)
	}
	return len(content.transcriptLines) + 1 + len(content.activityLines)
}

// Window returns height lines from top, padded with empty lines.
func (content messageContent) Window(top int, height int) []string {
	window := make([]string, 0, height)
	for line := top; line < top+height; line++ {
		switch {
		case line < len(content.transcriptLines):
			window = append(window, content.transcriptLines[line])
		case line > len(content.transcriptLines) && line-len(content.transcriptLines)-1 < len(content.activityLines):
			window = append(window, content.activityLines[line-len(content.transcriptLines)-1])
		default:
			window = append(window, "")
		}
	}
	return window
}

const (
	statusBarHeight = 2
	// arrowScrollStep is the rows one Up or Down moves the messages; a wheel
	// notch usually sends three.
	arrowScrollStep = 1
)

// screenLayout is one frame's split of the screen into the message view and
// the docked rows below it.
type screenLayout struct {
	width         int
	contentWidth  int
	messageHeight int
	docked        []string
	input         renderedInput
}

// View draws the full screen. It is empty while exiting, so the terminal
// leaves the alternate screen cleanly. It leaves the mouse to the terminal,
// so a plain drag selects text.
func (screen *ChatScreen) View() tea.View {
	if screen.isExiting {
		return tea.NewView("")
	}
	content, cursor := screen.renderFullScreen()
	view := tea.NewView(content)
	view.AltScreen = true
	view.Cursor = cursor
	return view
}

func (screen *ChatScreen) appendBlock(kind transcriptBlockKind, block transcriptBlock) {
	screen.history.Append(kind, block)
}

// handleScrollKey scrolls the message view and reports whether key did. Home
// and End need an empty input.
func (screen *ChatScreen) handleScrollKey(key tea.KeyPressMsg) bool {
	if screen.isPopupOpen || screen.isHelpOpen || screen.isToolPickerOpen {
		return false
	}
	page := max(screen.computeLayout().messageHeight-1, 1)
	switch key.String() {
	case "pgup":
		screen.scrollBy(-page)
	case "pgdown":
		screen.scrollBy(page)
	case "ctrl+home":
		screen.scrollToTop()
	case "ctrl+end":
		screen.scroll.isFollowing = true
	case "home":
		if !screen.input.IsEmpty() {
			return false
		}
		screen.scrollToTop()
	case "end":
		if !screen.input.IsEmpty() {
			return false
		}
		screen.scroll.isFollowing = true
	default:
		return false
	}
	return true
}

func (screen *ChatScreen) scrollToTop() {
	screen.setTop(0, screen.computeLayout())
}

// scrollBy moves the message view by delta lines. Reaching the end follows
// new lines again.
func (screen *ChatScreen) scrollBy(delta int) {
	layout := screen.computeLayout()
	screen.setTop(screen.currentTop(layout)+delta, layout)
}

// currentTop returns the first shown line of the message view.
func (screen *ChatScreen) currentTop(layout screenLayout) int {
	maximumTop := max(screen.messageContent(layout.contentWidth, screen.now(), 0).Len()-layout.messageHeight, 0)
	if screen.scroll.isFollowing {
		return maximumTop
	}
	return min(max(screen.history.LineOf(screen.scroll.anchor, layout.contentWidth), 0), maximumTop)
}

func (screen *ChatScreen) setTop(top int, layout screenLayout) {
	maximumTop := max(screen.messageContent(layout.contentWidth, screen.now(), 0).Len()-layout.messageHeight, 0)
	top = min(max(top, 0), maximumTop)
	if top >= maximumTop {
		screen.scroll.isFollowing = true
		return
	}
	if screen.scroll.isFollowing {
		screen.scroll.seenEntryCount = len(screen.history.entries)
	}
	screen.scroll.isFollowing = false
	screen.scroll.anchor = screen.history.Anchor(top, layout.contentWidth)
}

// computeLayout splits the screen: the message view gets what the docked
// rows, the input, and the status bar leave.
func (screen *ChatScreen) computeLayout() screenLayout {
	layout := screenLayout{width: screen.width, contentWidth: min(screen.width, maximumContentWidth)}
	layout.docked = screen.renderPendingBar(layout.contentWidth)
	if len(screen.plan.Items) > 0 && screen.plan.Status != sessionfeed.PlanStatusCleared {
		layout.docked = append(layout.docked, joinLeftAndRight(renderPlanLine(screen.plan.Items, layout.contentWidth, screen.theme),
			screen.theme.Secondary.Render("Ctrl-T steps"), layout.contentWidth, screen.theme.Symbols.Ellipsis))
	}
	layout.docked = append(layout.docked, screen.renderCompletionList(layout.contentWidth)...)
	layout.docked = append(layout.docked, screen.renderPathCompletions(layout.contentWidth)...)
	if len(screen.attachments) > 0 {
		layout.docked = append(layout.docked, " "+renderImageChips(screen.attachments, layout.contentWidth-1, screen.theme))
	}
	layout.input = screen.renderInput(screen.width)
	fixedHeight := 1 + len(layout.input.lines) + statusBarHeight
	for len(layout.docked) > 0 && screen.height-fixedHeight-len(layout.docked) < minimumMessageHeight {
		layout.docked = layout.docked[1:]
	}
	layout.messageHeight = max(screen.height-fixedHeight-len(layout.docked), 0)
	return layout
}

// minimumMessageHeight keeps a few message rows before docked rows give way.
const minimumMessageHeight = 3

// renderFullScreen draws every row of the screen and returns the cursor
// position, or nil when the cursor is hidden.
func (screen *ChatScreen) renderFullScreen() (string, *tea.Cursor) {
	now := screen.now()
	spinnerFrame := int(now.UnixMilli() / spinnerFrameInterval.Milliseconds())
	if screen.width < terminaltheme.MinimumUsableWidth {
		return screen.renderTooNarrowScreen(now, spinnerFrame)
	}
	if screen.pager != nil {
		lines := screen.pager.Render(screen.pagerSize(), screen.waitingInterruptsText(), screen.theme)
		return strings.Join(fitToHeight(lines, screen.height), "\n"), nil
	}
	layout := screen.computeLayout()
	messageArea := screen.renderMessageArea(layout, now, spinnerFrame)
	lines := append(messageArea, layout.docked...)
	lines = append(lines, screen.theme.Secondary.Render(strings.Repeat(screen.theme.Symbols.Rule, screen.width)))
	inputRow := len(lines)
	lines = append(lines, layout.input.lines...)
	lines = append(lines, screen.renderStatusBar(layout.contentWidth, now, spinnerFrame)...)
	var cursor *tea.Cursor
	if !screen.isPopupOpen && !screen.isHelpOpen && !screen.isInputDisabled {
		cursor = tea.NewCursor(layout.input.cursorColumn, inputRow+layout.input.cursorRow)
	}
	return strings.Join(fitToHeight(lines, screen.height), "\n"), cursor
}

// fitToHeight returns exactly height lines, keeping the last ones or padding
// at the top.
func fitToHeight(lines []string, height int) []string {
	if len(lines) > height {
		return lines[len(lines)-height:]
	}
	padding := make([]string, height-len(lines))
	return append(padding, lines...)
}

func (screen *ChatScreen) renderTooNarrowScreen(now time.Time, spinnerFrame int) (string, *tea.Cursor) {
	symbol, _ := screen.contextStatus(now, spinnerFrame)
	if symbol == "" {
		symbol = screen.theme.Secondary.Render(screen.theme.Symbols.Queued)
	}
	lines := []string{terminaltheme.TruncateToWidth(" "+symbol+" Resize to 40 columns for the full view.", screen.width, screen.theme.Symbols.Ellipsis)}
	input := screen.renderInput(screen.width)
	inputRow := max(screen.height-len(input.lines), 1)
	lines = append(fitToHeight(lines, inputRow), input.lines...)
	return strings.Join(fitToHeight(lines, screen.height), "\n"), tea.NewCursor(input.cursorColumn, inputRow+input.cursorRow)
}

// messageContent is the transcript followed by the live activity: the
// unfinished reply, running tools, and requests in flight.
func (screen *ChatScreen) messageContent(contentWidth int, now time.Time, spinnerFrame int) messageContent {
	return messageContent{
		transcriptLines: screen.history.Lines(contentWidth),
		activityLines:   screen.renderActivityLines(contentWidth, now, spinnerFrame),
	}
}

// renderMessageArea draws the scrolled message view with the help or a popup
// over its bottom.
func (screen *ChatScreen) renderMessageArea(layout screenLayout, now time.Time, spinnerFrame int) []string {
	height := layout.messageHeight
	if height <= 0 {
		return nil
	}
	content := screen.messageContent(layout.contentWidth, now, spinnerFrame)
	window := content.Window(screen.currentTop(layout), height)
	overlay := screen.renderOverlay(layout, height, now)
	if len(overlay) == 0 && !screen.scroll.isFollowing && len(screen.history.entries) > screen.scroll.seenEntryCount {
		overlay = []string{" " + screen.theme.Accent.Render("↓") + screen.theme.Secondary.Render(" new messages · End follows")}
	}
	copy(window[height-len(overlay):], overlay)
	return window
}

// renderOverlay returns the help or the popup, at most height lines.
func (screen *ChatScreen) renderOverlay(layout screenLayout, height int, now time.Time) []string {
	switch {
	case screen.isInputDisabled:
		return nil
	case screen.isHelpOpen:
		help := append([]string{""}, renderHelpOverlay(screen.commands, layout.contentWidth, screen.theme)...)
		if len(help) <= height {
			return help
		}
		top := min(screen.helpScrollFromTop, len(help)-height)
		return help[top : top+height]
	case screen.isToolPickerOpen:
		return screen.renderToolPicker(layout.contentWidth, height)
	case screen.isPopupOpen && screen.popupIndex < len(screen.interrupts):
		interrupt := screen.interrupts[screen.popupIndex]
		if interrupt.approval != nil {
			return screen.approvalLayout(layout).lines
		}
		position := popupPosition{index: screen.popupIndex, count: len(screen.interrupts)}
		return renderQuestionPopup(*interrupt.question, screen.popup, position, popupWidth(layout), height, screen.theme)
	default:
		return nil
	}
}

func popupWidth(layout screenLayout) int {
	return min(layout.width, maximumPopupWidth+2)
}

// approvalLayout lays out the open approval at the current size, for drawing
// and for deciding whether Approve is allowed.
func (screen *ChatScreen) approvalLayout(layout screenLayout) approvalPopupLayout {
	interrupt := screen.interrupts[screen.popupIndex]
	position := popupPosition{index: screen.popupIndex, count: len(screen.interrupts)}
	return layoutApprovalPopup(*interrupt.approval, screen.popup, position, popupWidth(layout), layout.messageHeight, screen.now(), screen.theme)
}

// renderActivityLines draws the unfinished reply, running tool rows, and
// the feedback of requests in flight.
func (screen *ChatScreen) renderActivityLines(width int, now time.Time, spinnerFrame int) []string {
	var replyLines []string
	for _, held := range screen.heldReplies {
		if held.kind == replyOutputMarkdown {
			replyLines = append(replyLines, renderAssistantMarkdown(held.text, width, screen.theme)...)
		}
	}
	if pending := screen.replies.PendingText(); pending != "" {
		replyLines = append(replyLines, renderPlainReply(pending, width)...)
	} else if screen.reasoningTail != "" && len(replyLines) == 0 {
		replyLines = append(replyLines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Secondary.Render("thinking… "+screen.reasoningTail), width, screen.theme.Symbols.Ellipsis))
	}
	if len(replyLines) > maximumLiveReplyLines {
		replyLines = replyLines[len(replyLines)-maximumLiveReplyLines:]
	}
	var toolLines []string
	clock := toolRowClock{now: now, spinnerFrame: spinnerFrame}
	for _, call := range screen.liveToolCalls {
		toolLines = append(toolLines, renderToolRow(call, screen.turnToolNameWidth, width, clock, screen.theme)...)
	}
	// Text that followed a running tool row shows below it.
	isReplyAfterTools := len(screen.heldReplies) > 0 || screen.repliesAfterTools[screen.replies.PendingGroupKey()]
	first, second := replyLines, toolLines
	if isReplyAfterTools {
		first, second = toolLines, replyLines
	}
	lines := first
	if len(first) > 0 && len(second) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, second...)
	for _, interrupt := range screen.interrupts {
		if feedback, exists := screen.interruptFeedback[interrupt.InterruptID()]; exists {
			lines = append(lines, contentIndent+screen.theme.Secondary.Render(screen.theme.Symbols.Queued+" "+feedback))
		}
	}
	return lines
}

// renderStatusBar draws the session row with turn and usage, then the status
// row with key hints.
func (screen *ChatScreen) renderStatusBar(width int, now time.Time, spinnerFrame int) []string {
	symbols := screen.theme.Symbols
	separator := screen.theme.Secondary.Render(symbols.Separator)
	identity := " " + strings.Join(nonEmptyStrings(screen.session.Command, screen.session.Model, screen.theme.Secondary.Render(screen.session.Workspace)), separator)
	if screen.session.Workspace == "" {
		identity = " " + strings.Join(nonEmptyStrings(screen.session.Command, screen.session.Model), separator)
	}
	var metrics []string
	if turnNumber := screen.displayedTurnNumber(); turnNumber > 0 {
		metrics = append(metrics, fmt.Sprintf("turn %d", turnNumber))
	}
	if screen.totalTokens > 0 {
		metrics = append(metrics, formatTokenCount(screen.totalTokens))
	}
	symbol, statusText := screen.contextStatus(now, spinnerFrame)
	status := " " + strings.TrimLeft(symbol+" "+statusText, " ")
	hints := screen.currentHints()
	metricsText := ""
	if len(metrics) > 0 {
		metricsText = screen.theme.Secondary.Render(strings.Join(metrics, symbols.Separator))
	}
	return []string{
		joinLeftAndRight(identity, metricsText, width, symbols.Ellipsis),
		joinLeftAndRight(status, screen.fittingHints(hints, width-terminaltheme.DisplayWidth(status)-2), width, symbols.Ellipsis),
	}
}

// fittingHints joins as many leading hints as fit in room cells.
func (screen *ChatScreen) fittingHints(hints []string, room int) string {
	for count := len(hints); count > 0; count-- {
		text := strings.Join(hints[:count], screen.theme.Symbols.Separator)
		if terminaltheme.DisplayWidth(text) <= room {
			return screen.theme.Secondary.Render(text)
		}
	}
	return ""
}

// joinLeftAndRight right-aligns right in a width-wide row, or drops it when
// both do not fit.
func joinLeftAndRight(left string, right string, width int, ellipsis string) string {
	leftWidth, rightWidth := terminaltheme.DisplayWidth(left), terminaltheme.DisplayWidth(right)
	if right == "" || leftWidth+2+rightWidth > width {
		return terminaltheme.TruncateToWidth(left, width, ellipsis)
	}
	return left + strings.Repeat(" ", width-leftWidth-rightWidth) + right
}

// currentHints are the key hints for what has focus.
func (screen *ChatScreen) currentHints() []string {
	switch {
	case screen.isInputDisabled:
		return []string{"Press any key to exit"}
	case screen.isIdleExitArmed():
		return []string{"Press Ctrl-C again to exit"}
	case screen.isHelpOpen:
		return []string{"PgUp/PgDn scroll", "Esc close"}
	case screen.isToolPickerOpen:
		return []string{"Enter open", "j/k move", "Esc close"}
	case screen.isPopupOpen && screen.popupIndex < len(screen.interrupts):
		return screen.popupHints()
	default:
		return screen.inputHints()
	}
}

func (screen *ChatScreen) popupHints() []string {
	interrupt := screen.interrupts[screen.popupIndex]
	hasNext := len(screen.interrupts) > 1
	if interrupt.approval != nil {
		if screen.popup.isEnteringText {
			return []string{"Enter reject", "Esc back"}
		}
		if screen.popup.isReadingConsequence {
			return []string{"PgUp/PgDn read", "e back"}
		}
		hints := []string{"y approve", "n reject", "r reason"}
		if !screen.canApprove() {
			hints = []string{"e read the consequence", "n reject", "r reason"}
		}
		if hasNext {
			hints = append(hints, "Tab next")
		}
		return append(hints, "Esc later")
	}
	if screen.popup.isEnteringText {
		return []string{"Enter answer", "Esc back"}
	}
	hints := []string{"j/k choose", "Enter answer"}
	if interrupt.question.AllowsFreeText {
		// Typing starts a free-text answer, so j and k type too.
		hints = []string{"Up/Down choose", "Enter answer"}
		if !hasNext {
			hints = append(hints, "or type an answer")
		}
	}
	if hasNext {
		hints = append(hints, "Tab next")
	}
	return append(hints, "Esc later")
}

// renderPendingBar draws the waiting approvals and the queued messages.
func (screen *ChatScreen) renderPendingBar(width int) []string {
	var lines []string
	symbols := screen.theme.Symbols
	if text := screen.waitingInterruptsText(); text != "" {
		line := screen.theme.Warning.Render(symbols.AwaitingUser) + " " + text + screen.theme.Secondary.Render(symbols.Separator+"Tab opens")
		lines = append(lines, screen.renderPendingRow(line, screen.pendingFocus == pendingFocusInterrupts, width))
	}
	selectedID := screen.selectedQueuedMessage().MessageID
	isQueuedFocus := screen.pendingFocus == pendingFocusQueuedMessage
	for index, message := range screen.queuedMessages {
		isLast := index == len(screen.queuedMessages)-1
		isFocused := isQueuedFocus && message.MessageID == selectedID
		right := ""
		if screen.canActOnQueuedMessage() && ((isQueuedFocus && isFocused) || (!isQueuedFocus && isLast)) {
			right = screen.theme.Secondary.Render("Tab acts on it")
		}
		left := screen.theme.Secondary.Render(symbols.Queued+" queued") + "  " + message.Text
		line := left
		if right != "" {
			room := width - 2 - terminaltheme.DisplayWidth(symbols.Separator) - terminaltheme.DisplayWidth(right)
			line = terminaltheme.TruncateToWidth(left, max(room, 8), symbols.Ellipsis) + screen.theme.Secondary.Render(symbols.Separator) + right
		}
		lines = append(lines, screen.renderPendingRow(line, isFocused, width))
	}
	for _, text := range screen.localQueuedTexts {
		line := screen.theme.Secondary.Render(symbols.Queued+" queueing") + "  " + text
		lines = append(lines, screen.renderPendingRow(line, false, width))
	}
	for _, message := range screen.heldMessages {
		line := screen.theme.Secondary.Render(symbols.Queued+" waiting for the session") + "  " + message.Text
		lines = append(lines, screen.renderPendingRow(line, false, width))
	}
	if screen.pendingSwitchSessionID != "" {
		// The hint line shows the answers: y switch · n stay.
		question := " Started session " + screen.pendingSwitchSessionID + ". Switch to it?"
		lines = append(lines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Accent.Render(symbols.AwaitingUser)+question, width, symbols.Ellipsis))
	}
	if screen.pendingImageDrop != nil {
		lines = append(lines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Warning.Render(symbols.Warning)+screen.imageDropQuestion(), width, symbols.Ellipsis))
	}
	if screen.pendingPastedImage != nil {
		question := " Attach " + filepath.Base(screen.pendingPastedImage.path) + "?"
		lines = append(lines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Accent.Render(symbols.AwaitingUser)+question, width, symbols.Ellipsis))
	}
	if screen.pendingDelete != nil {
		question := fmt.Sprintf(" Delete the queued message %q? It will not be sent.", screen.pendingDelete.Text)
		lines = append(lines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Warning.Render(symbols.Warning)+question, width, symbols.Ellipsis))
	}
	return lines
}

func (screen *ChatScreen) renderPendingRow(line string, isFocused bool, width int) string {
	prefix := contentIndent
	if isFocused {
		prefix = screen.theme.Accent.Render(screen.theme.Symbols.Focus) + " "
	}
	return terminaltheme.TruncateToWidth(prefix+line, width, screen.theme.Symbols.Ellipsis)
}

func (screen *ChatScreen) waitingInterruptsText() string {
	if screen.isPopupOpen {
		return ""
	}
	approvalCount, questionCount := 0, 0
	for _, interrupt := range screen.interrupts {
		switch {
		case interrupt.isSubmitting:
		case interrupt.approval != nil:
			approvalCount++
		default:
			questionCount++
		}
	}
	var parts []string
	if approvalCount > 0 {
		parts = append(parts, countedNoun(approvalCount, "approval"))
	}
	if questionCount > 0 {
		parts = append(parts, countedNoun(questionCount, "question"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ") + " waiting"
}

func countedNoun(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func (screen *ChatScreen) displayedTurnNumber() int {
	if screen.isTurnRunning {
		return screen.turn.number
	}
	return screen.lastTurnNumber
}

// contextStatus returns the styled status symbol, empty for none, and the
// status text.
func (screen *ChatScreen) contextStatus(now time.Time, spinnerFrame int) (string, string) {
	theme := screen.theme
	symbols := theme.Symbols
	spinner := theme.Info.Render(symbols.SpinnerFrame(spinnerFrame))
	switch {
	case screen.workerState == sessionfeed.WorkerStateStopped:
		return theme.Error.Render(symbols.Failed), "Worker stopped"
	case !screen.isServerReachable:
		return theme.Error.Render(symbols.Failed), "server unreachable" + symbols.Separator + "retrying"
	case !screen.isSessionLoaded:
		return spinner, "loading session"
	case screen.workerState == sessionfeed.WorkerStateReconnecting:
		return theme.Warning.Render(symbols.Warning), "Worker reconnecting"
	case screen.workerState == sessionfeed.WorkerStateStopping:
		return theme.Warning.Render(symbols.Warning), "Worker stopping"
	case !screen.isTurnRunning:
		return "", theme.Secondary.Render("ready")
	case screen.cancelState == cancelStateRequesting:
		return theme.Secondary.Render(symbols.Queued), "requesting cancel…"
	case screen.cancelState == cancelStateConfirmed:
		return spinner, "cancelling…"
	case screen.turn.activity == sessionfeed.TurnActivityAwaitingUser:
		return theme.Warning.Render(symbols.AwaitingUser), "waiting for you"
	}
	symbol := theme.Info.Render(symbols.Running)
	if now.Sub(screen.turn.activityAt) > spinnerDelay {
		symbol = spinner
	}
	text := activityText(screen.turn.activity)
	if screen.turn.activity == sessionfeed.TurnActivityRunningModel && screen.isWritingReply {
		text = "writing reply…"
	}
	if elapsed := now.Sub(screen.turn.startedAt); elapsed >= elapsedTimeDelay {
		text += " " + formatElapsedSeconds(elapsed)
	}
	return symbol, text
}

func activityText(activity sessionfeed.TurnActivity) string {
	switch activity {
	case sessionfeed.TurnActivityRunningTools:
		return "running tools"
	case sessionfeed.TurnActivityCompacting:
		return "compacting"
	default:
		return "running model"
	}
}

const maximumCompletionRows = 6

func (screen *ChatScreen) renderCompletionList(width int) []string {
	if !screen.isCompletionVisible() {
		return nil
	}
	matches := screen.completionMatches()
	selected := min(screen.completionIndex, len(matches)-1)
	first := max(selected-maximumCompletionRows+1, 0)
	last := min(first+maximumCompletionRows, len(matches))
	nameWidth := 0
	for _, command := range matches[first:last] {
		nameWidth = max(nameWidth, terminaltheme.DisplayWidth(completionName(command)))
	}
	lines := make([]string, 0, last-first)
	for index := first; index < last; index++ {
		command := matches[index]
		name := terminaltheme.PadRight(completionName(command), nameWidth)
		description := command.Description
		unavailableReason := ""
		if command.Name == runCommandName {
			unavailableReason = screen.runUnavailableReason()
		}
		prefix := "   "
		switch {
		case unavailableReason != "":
			name, description = screen.theme.Secondary.Render(name), unavailableReason
			if index == selected {
				prefix = " " + screen.theme.Secondary.Render(screen.theme.Symbols.Focus) + " "
			}
		case index == selected:
			prefix = " " + screen.theme.Accent.Render(screen.theme.Symbols.Focus) + " "
			name = screen.theme.Focused.Render(name)
		}
		line := prefix + name + "  " + screen.theme.Secondary.Render(description)
		lines = append(lines, terminaltheme.TruncateToWidth(line, width, screen.theme.Symbols.Ellipsis))
	}
	return lines
}

// maximumPathCompletionRows bounds the docked /image path candidates.
const maximumPathCompletionRows = 6

// renderPathCompletions lists the /image candidates of the last Tab.
func (screen *ChatScreen) renderPathCompletions(width int) []string {
	if len(screen.pathCompletions) == 0 {
		return nil
	}
	shown := screen.pathCompletions[:min(len(screen.pathCompletions), maximumPathCompletionRows)]
	lines := make([]string, 0, len(shown)+1)
	for _, completion := range shown {
		lines = append(lines, terminaltheme.TruncateKeepingTail("   "+screen.theme.Secondary.Render(completion), width, screen.theme.Symbols.Ellipsis))
	}
	if hidden := len(screen.pathCompletions) - len(shown); hidden > 0 || screen.isPathCompletionPartial {
		text := moreLinesText(hidden, screen.theme.Symbols.Ellipsis)
		if screen.isPathCompletionPartial {
			text = screen.theme.Symbols.Ellipsis + " more may match; type more of the name"
		}
		lines = append(lines, "   "+screen.theme.Secondary.Render(text))
	}
	return lines
}

// renderToolPicker lists the latest turn's tools for /tools.
func (screen *ChatScreen) renderToolPicker(width int, height int) []string {
	lines := []string{"", contentIndent + screen.theme.Title.Render("Tools of the latest turn")}
	room := max(height-len(lines), 1)
	first := max(min(screen.toolPickerIndex-room/2, len(screen.turnTools)-room), 0)
	for index := first; index < min(first+room, len(screen.turnTools)); index++ {
		call := screen.turnTools[index]
		call.OutputTail, call.HiddenOutputLineCount = nil, 0
		row := renderToolRow(call, screen.turnToolNameWidth, width, toolRowClock{now: screen.now()}, screen.theme)[0]
		if index == screen.toolPickerIndex {
			row = screen.theme.Accent.Render(screen.theme.Symbols.Focus) + " " + strings.TrimPrefix(row, contentIndent)
		}
		lines = append(lines, row)
	}
	return lines
}

// completionName is "/name", "/name ARG", or "/name [ARG]" for an optional
// argument.
func completionName(command sessionfeed.SlashCommand) string {
	switch {
	case command.ArgumentHint == "":
		return "/" + command.Name
	case command.IsArgumentOptional:
		return "/" + command.Name + " [" + command.ArgumentHint + "]"
	}
	return "/" + command.Name + " " + command.ArgumentHint
}

const maximumInputRows = 8

func (screen *ChatScreen) renderInput(width int) renderedInput {
	prompt := " " + screen.theme.Accent.Render(screen.theme.Symbols.UserPrompt) + " "
	if screen.isInputDisabled {
		prompt = " " + screen.theme.Secondary.Render(screen.theme.Symbols.UserPrompt) + " "
	}
	placeholder := screen.theme.Secondary.Render(screen.inputPlaceholder())
	return screen.input.Render(prompt, placeholder, width, maximumInputRows)
}

func (screen *ChatScreen) inputPlaceholder() string {
	switch {
	case screen.isInputDisabled:
		return "this chat cannot continue"
	case !screen.isSessionLoaded:
		return "type a message; it is sent when the session loads"
	case screen.isPlanModeArmed:
		return "type what to plan"
	case screen.isTurnRunning:
		return "type to queue a message"
	case screen.session.IsNewSession:
		if screen.commands.Has(imageCommandName) {
			return "Type a message. /image attaches a picture, /help lists commands."
		}
		return "Type a message. /help lists commands."
	default:
		return "type a message"
	}
}

func (screen *ChatScreen) inputHints() []string {
	switch {
	case screen.pendingPastedImage != nil:
		return []string{"Enter attach", "Esc keep text"}
	case screen.pendingDelete != nil:
		return []string{"y delete", "n keep"}
	case screen.pendingImageDrop != nil:
		return []string{"y drop", "n stay"}
	case screen.pendingSwitchSessionID != "" && screen.input.IsEmpty():
		return []string{"y switch", "n stay"}
	case screen.isSessionStartWaiting && !screen.isCompletionVisible():
		return []string{"Esc keep starting in the background", "/help"}
	case screen.pendingFocus == pendingFocusInterrupts:
		return []string{"Enter opens", "Esc back"}
	case screen.pendingFocus == pendingFocusQueuedMessage:
		return []string{"s steer", "x delete", "Esc back"}
	case screen.isCompletionVisible():
		return []string{"Tab complete", "Enter run", "Esc close"}
	}
	var hints []string
	if screen.input.IsEmpty() && screen.waitingInterruptCount() > 0 {
		hints = append(hints, screen.waitingInterruptsHint())
	}
	if !screen.isTurnRunning {
		return append(hints, "Enter send", "/help", "Ctrl-D exit")
	}
	hints = append(hints, "Enter queue")
	switch {
	case screen.isExitArmed || screen.cancelState == cancelStateConfirmed:
		hints = append(hints, "Ctrl-C exit")
	case screen.canRequestCancel():
		hints = append(hints, "Esc interrupt")
	}
	return append(hints, "/help")
}

// waitingInterruptsHint names what Tab opens: approvals, questions, or both.
func (screen *ChatScreen) waitingInterruptsHint() string {
	hasApproval, hasQuestion := false, false
	for _, interrupt := range screen.interrupts {
		switch {
		case interrupt.isSubmitting:
		case interrupt.approval != nil:
			hasApproval = true
		default:
			hasQuestion = true
		}
	}
	switch {
	case hasApproval && hasQuestion:
		return "Tab opens them"
	case hasQuestion:
		return "Tab opens questions"
	}
	return "Tab opens approvals"
}
