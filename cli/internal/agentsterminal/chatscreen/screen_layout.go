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
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// messageScroll is the position of the message view. While following, the
// view shows the newest lines.
type messageScroll struct {
	isFollowing bool
	top         int
	// seenLineCount is the line count when the user scrolled away; more
	// lines show the new-messages indicator.
	seenLineCount int
}

const (
	mouseWheelStep  = 3
	statusBarHeight = 2
)

// screenLayout is one frame's split of the screen into the message view and
// the docked rows below it.
type screenLayout struct {
	width         int
	contentWidth  int
	messageHeight int
	docked        []string
	input         renderedInput
	statusBar     []string
}

// View draws the full screen. It is empty while exiting, so the terminal
// leaves the alternate screen cleanly.
func (screen *ChatScreen) View() tea.View {
	if screen.isExiting {
		return tea.NewView("")
	}
	content, cursor := screen.renderFullScreen()
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.Cursor = cursor
	return view
}

func (screen *ChatScreen) appendBlock(kind transcriptBlockKind, block transcriptBlock) {
	screen.history.Append(kind, block)
}

// handleScrollKey scrolls the message view and reports whether key did. Home
// and End need an empty input.
func (screen *ChatScreen) handleScrollKey(key tea.KeyPressMsg) bool {
	if screen.isPopupOpen || screen.isHelpOpen {
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

func (screen *ChatScreen) handleMouseWheel(wheel tea.MouseWheelMsg) {
	step := mouseWheelStep
	if wheel.Button == tea.MouseWheelUp {
		step = -mouseWheelStep
	}
	switch {
	case screen.isHelpOpen:
		screen.helpScrollFromTop = max(screen.helpScrollFromTop+step, 0)
	case screen.isPopupOpen:
		screen.popup.scrollFromBottom = max(screen.popup.scrollFromBottom-step, 0)
	default:
		screen.scrollBy(step)
	}
}

func (screen *ChatScreen) scrollToTop() {
	screen.leaveFollowing()
	screen.scroll.top = 0
}

// scrollBy moves the message view by delta lines. Reaching the end follows
// new lines again.
func (screen *ChatScreen) scrollBy(delta int) {
	layout := screen.computeLayout()
	lineCount := len(screen.messageLines(layout.contentWidth, screen.now(), 0))
	maximumTop := max(lineCount-layout.messageHeight, 0)
	top := screen.scroll.top
	if screen.scroll.isFollowing {
		top = maximumTop
	}
	top = min(max(top+delta, 0), maximumTop)
	if top >= maximumTop {
		screen.scroll.isFollowing = true
		return
	}
	screen.leaveFollowing()
	screen.scroll.top = top
}

func (screen *ChatScreen) leaveFollowing() {
	if !screen.scroll.isFollowing {
		return
	}
	layout := screen.computeLayout()
	screen.scroll.isFollowing = false
	screen.scroll.seenLineCount = len(screen.messageLines(layout.contentWidth, screen.now(), 0))
}

// computeLayout splits the screen: the message view gets what the docked
// rows, the input, and the status bar leave.
func (screen *ChatScreen) computeLayout() screenLayout {
	now := screen.now()
	spinnerFrame := int(now.UnixMilli() / spinnerFrameInterval.Milliseconds())
	layout := screenLayout{width: screen.width, contentWidth: min(screen.width, maximumContentWidth)}
	layout.docked = screen.renderPendingBar(layout.contentWidth)
	if len(screen.plan.Items) > 0 && screen.plan.Status != sessionfeed.PlanStatusCleared {
		layout.docked = append(layout.docked, renderPlanLine(screen.plan.Items, layout.contentWidth, screen.theme))
	}
	layout.docked = append(layout.docked, screen.renderCompletionList(layout.contentWidth)...)
	if len(screen.attachments) > 0 {
		layout.docked = append(layout.docked, " "+renderImageChips(screen.attachments, layout.contentWidth-1, screen.theme))
	}
	layout.input = screen.renderInput(screen.width)
	layout.statusBar = screen.renderStatusBar(layout.contentWidth, now, spinnerFrame)
	fixedHeight := 1 + len(layout.input.lines) + len(layout.statusBar)
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
	layout := screen.computeLayout()
	messageArea := screen.renderMessageArea(layout, now, spinnerFrame)
	lines := append(messageArea, layout.docked...)
	lines = append(lines, screen.theme.Secondary.Render(strings.Repeat(screen.theme.Symbols.Rule, screen.width)))
	inputRow := len(lines)
	lines = append(lines, layout.input.lines...)
	lines = append(lines, layout.statusBar...)
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

// messageLines are the transcript followed by the live activity: the
// unfinished reply, running tools, and requests in flight.
func (screen *ChatScreen) messageLines(contentWidth int, now time.Time, spinnerFrame int) []string {
	lines := screen.history.Lines(contentWidth, screen.theme)
	if activity := screen.renderActivityLines(contentWidth, now, spinnerFrame); len(activity) > 0 {
		lines = append(append(lines, ""), activity...)
	}
	return lines
}

// renderMessageArea draws the scrolled message view with the help or a popup
// over its bottom.
func (screen *ChatScreen) renderMessageArea(layout screenLayout, now time.Time, spinnerFrame int) []string {
	height := layout.messageHeight
	if height <= 0 {
		return nil
	}
	lines := screen.messageLines(layout.contentWidth, now, spinnerFrame)
	top := max(len(lines)-height, 0)
	if !screen.scroll.isFollowing {
		top = min(screen.scroll.top, top)
	}
	window := append([]string{}, lines[top:min(top+height, len(lines))]...)
	for len(window) < height {
		window = append(window, "")
	}
	overlay := screen.renderOverlay(layout, height, now)
	if len(overlay) == 0 && !screen.scroll.isFollowing && len(lines) > screen.scroll.seenLineCount {
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
	case screen.isPopupOpen && screen.popupIndex < len(screen.interrupts):
		interrupt := screen.interrupts[screen.popupIndex]
		position := popupPosition{index: screen.popupIndex, count: len(screen.interrupts)}
		popupWidth := min(layout.width, maximumPopupWidth+2)
		if interrupt.approval != nil {
			return renderApprovalPopup(*interrupt.approval, screen.popup, position, popupWidth, height, now, screen.theme)
		}
		return renderQuestionPopup(*interrupt.question, screen.popup, position, popupWidth, height, screen.theme)
	default:
		return nil
	}
}

// renderActivityLines draws the unfinished reply, running tool rows, and
// the feedback of requests in flight.
func (screen *ChatScreen) renderActivityLines(width int, now time.Time, spinnerFrame int) []string {
	var lines []string
	if pending := screen.replies.PendingText(); pending != "" {
		lines = append(lines, renderPlainReply(pending, width)...)
	} else if screen.reasoningTail != "" {
		lines = append(lines, terminaltheme.TruncateToWidth(contentIndent+screen.theme.Secondary.Render("thinking… "+screen.reasoningTail), width, screen.theme.Symbols.Ellipsis))
	}
	if len(lines) > 0 && len(screen.liveToolCalls) > 0 {
		lines = append(lines, "")
	}
	clock := toolRowClock{now: now, spinnerFrame: spinnerFrame}
	for _, call := range screen.liveToolCalls {
		lines = append(lines, renderToolRow(call, screen.turnToolNameWidth, width, clock, screen.theme)...)
	}
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
	case screen.isHelpOpen:
		return []string{"PgUp/PgDn scroll", "Esc close"}
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
		hints := []string{"y approve", "n reject", "r reason"}
		if hasNext {
			hints = append(hints, "Tab next")
		}
		return append(hints, "Esc later")
	}
	if screen.popup.isEnteringText {
		return []string{"Enter answer", "Esc back"}
	}
	hints := []string{"j/k choose", "Enter answer"}
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
	for index, message := range screen.queuedMessages {
		isLast := index == len(screen.queuedMessages)-1
		right := ""
		if isLast && screen.canActOnQueuedMessage() {
			right = screen.theme.Secondary.Render("Tab acts on it")
		}
		left := screen.theme.Secondary.Render(symbols.Queued+" queued") + "  " + message.Text
		line := left
		if right != "" {
			room := width - 2 - terminaltheme.DisplayWidth(symbols.Separator) - terminaltheme.DisplayWidth(right)
			line = terminaltheme.TruncateToWidth(left, max(room, 8), symbols.Ellipsis) + screen.theme.Secondary.Render(symbols.Separator) + right
		}
		lines = append(lines, screen.renderPendingRow(line, isLast && screen.pendingFocus == pendingFocusQueuedMessage, width))
	}
	for _, text := range screen.localQueuedTexts {
		line := screen.theme.Secondary.Render(symbols.Queued+" queueing") + "  " + text
		lines = append(lines, screen.renderPendingRow(line, false, width))
	}
	for _, message := range screen.heldMessages {
		line := screen.theme.Secondary.Render(symbols.Queued+" waiting for the session") + "  " + message.Text
		lines = append(lines, screen.renderPendingRow(line, false, width))
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
		prefix := "   "
		if index == selected {
			prefix = " " + screen.theme.Accent.Render(screen.theme.Symbols.Focus) + " "
			name = screen.theme.Focused.Render(name)
		}
		line := prefix + name + "  " + screen.theme.Secondary.Render(command.Description)
		lines = append(lines, terminaltheme.TruncateToWidth(line, width, screen.theme.Symbols.Ellipsis))
	}
	return lines
}

func completionName(command sessionfeed.SlashCommand) string {
	if command.ArgumentHint == "" {
		return "/" + command.Name
	}
	return "/" + command.Name + " " + command.ArgumentHint
}

const maximumInputRows = 8

func (screen *ChatScreen) renderInput(width int) renderedInput {
	prompt := " " + screen.theme.Accent.Render(screen.theme.Symbols.UserPrompt) + " "
	placeholder := screen.theme.Secondary.Render(screen.inputPlaceholder())
	return screen.input.Render(prompt, placeholder, width, maximumInputRows)
}

func (screen *ChatScreen) inputPlaceholder() string {
	switch {
	case !screen.isSessionLoaded:
		return "type a message; it is sent when the session loads"
	case screen.isPlanModeArmed:
		return "type what to plan"
	case screen.isTurnRunning:
		return "type to queue a message"
	case screen.session.IsNewSession:
		if screen.commands.Has("image") {
			return "Type a message. /image attaches a picture, /help lists commands."
		}
		return "Type a message. /help lists commands."
	default:
		return "type a message"
	}
}

func (screen *ChatScreen) inputHints() []string {
	switch {
	case screen.pendingDelete != nil:
		return []string{"y delete", "n keep"}
	case screen.pendingFocus == pendingFocusInterrupts:
		return []string{"Enter opens", "Esc back"}
	case screen.pendingFocus == pendingFocusQueuedMessage:
		return []string{"s steer", "x delete", "Esc back"}
	case screen.isCompletionVisible():
		return []string{"Tab complete", "Enter run", "Esc close"}
	}
	var hints []string
	if screen.input.IsEmpty() && screen.waitingInterruptCount() > 0 {
		hints = append(hints, "Tab opens approvals")
	}
	if !screen.isTurnRunning {
		return append(hints, "Enter send", "/help", "Ctrl-D exit")
	}
	hints = append(hints, "Enter queue")
	switch {
	case screen.isExitArmed || screen.cancelState == cancelStateConfirmed:
		hints = append(hints, "Ctrl-C exit")
	case screen.session.AcceptsTurnControl:
		hints = append(hints, "Ctrl-C cancel turn")
	}
	return append(hints, "/help")
}
