// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package chatscreen is the inline agents chat screen. Finished blocks go to
// the terminal scrollback.
package chatscreen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/cli/internal/agentsterminal/terminaltheme"
)

// ChatScreenConfig configures NewChatScreen.
type ChatScreenConfig struct {
	// Source feeds session updates and receives user actions. Required.
	Source sessionfeed.ChatSessionSource
	// Theme holds the styles and symbols. Required; build it with
	// terminaltheme.NewTheme.
	Theme terminaltheme.Theme
	// PrintScrollback writes finished lines above the live area. Required; see
	// NewScrollbackPrinter.
	PrintScrollback func(text string) tea.Cmd
	// Now returns the current time. Default: time.Now. Tests fix it so
	// spinner frames and elapsed times are stable.
	Now func() time.Time
	// SessionCommands are the source's slash commands. Default: none. They
	// cannot redefine /help or /exit.
	SessionCommands []sessionfeed.SlashCommand
}

// ChatScreen is the chat screen model.
type ChatScreen struct {
	ctx             context.Context
	source          sessionfeed.ChatSessionSource
	theme           terminaltheme.Theme
	printScrollback func(text string) tea.Cmd
	now             func() time.Time
	commands        slashCommandRegistry

	width  int
	height int

	session           sessionfeed.SessionOpened
	isSessionLoaded   bool
	turn              activeTurn
	isTurnRunning     bool
	lastTurnNumber    int
	totalTokens       int64
	reasoningTail     string
	planItems         []sessionfeed.PlanItem
	queuedMessages    []sessionfeed.QueuedMessage
	localQueuedTexts  []string
	isServerReachable bool
	workerState       sessionfeed.WorkerState
	exitCode          int
	isExiting         bool
	isInputDisabled   bool

	replies           *replyStream
	liveToolCalls     []sessionfeed.ToolCallChanged
	turnToolNameWidth int
	pendingLines      []string
	lastBlockKind     scrollbackBlockKind

	interrupts        []pendingInterrupt
	isPopupOpen       bool
	popupIndex        int
	popup             interruptPopup
	interruptFeedback map[string]string
	interruptOutcomes map[string]string

	input                     inputEditor
	inputHistory              []string
	historyIndex              int
	completionIndex           int
	isCompletionDismissed     bool
	isHelpOpen                bool
	pendingFocus              pendingFocusTarget
	isConfirmingQueuedDelete  bool
	cancelState               cancelState
	isExitArmed               bool
	cancelRequestedTurnID     string
	isWaitingForSessionUpdate bool
	isTicking                 bool
	isPrinting                bool
}

// activeTurn is the running turn shown in the context line.
type activeTurn struct {
	turnID     string
	number     int
	startedAt  time.Time
	activity   sessionfeed.TurnActivity
	activityAt time.Time
}

// cancelState tracks Ctrl-C while a turn runs.
type cancelState int

const (
	cancelStateNone cancelState = iota
	cancelStateRequesting
	cancelStateConfirmed
)

// pendingFocusTarget is the pending-bar row that Tab focused.
type pendingFocusTarget int

const (
	pendingFocusNone pendingFocusTarget = iota
	pendingFocusInterrupts
	pendingFocusQueuedMessage
)

// scrollbackBlockKind decides where blank lines separate printed blocks.
type scrollbackBlockKind int

const (
	scrollbackBlockNone scrollbackBlockKind = iota
	scrollbackBlockUserMessage
	scrollbackBlockReply
	scrollbackBlockToolRow
	scrollbackBlockNotice
)

type sessionUpdateMsg struct {
	update sessionfeed.SessionUpdate
}

type sessionFeedFailedMsg struct {
	err error
}

type userActionFailedMsg struct {
	action sessionfeed.UserAction
	err    error
}

type animationTickMsg struct{}

// scrollbackPrintedMsg follows each printed block, so the next block is
// printed only after it and blocks keep their order.
type scrollbackPrintedMsg struct{}

const defaultScreenWidth = 80

// NewChatScreen returns a chat screen whose source calls use ctx. Cancel ctx
// after the program ends.
func NewChatScreen(ctx context.Context, config ChatScreenConfig) *ChatScreen {
	if ctx == nil {
		panic("chat screen context must not be nil")
	}
	if config.Source == nil {
		panic("chat screen source must not be nil")
	}
	if config.PrintScrollback == nil {
		panic("chat screen scrollback printer must not be nil")
	}
	if len(config.Theme.Symbols.SpinnerFrames) == 0 {
		panic("chat screen theme must come from terminaltheme.NewTheme")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ChatScreen{
		ctx:               ctx,
		source:            config.Source,
		theme:             config.Theme,
		printScrollback:   config.PrintScrollback,
		now:               now,
		commands:          newSlashCommandRegistry(config.SessionCommands),
		width:             defaultScreenWidth,
		isServerReachable: true,
		replies:           newReplyStream(),
		interruptFeedback: map[string]string{},
		interruptOutcomes: map[string]string{},
	}
}

// NewScrollbackPrinter downsamples each block to profile, because Bubble Tea
// prints scrollback lines unchanged.
func NewScrollbackPrinter(profile colorprofile.Profile) func(text string) tea.Cmd {
	return func(text string) tea.Cmd {
		return tea.Println(terminaltheme.DownsampleStyledText(profile, text))
	}
}

// ExitCode returns the process exit code after the program ends: 1 when the
// session's Worker or command stopped, otherwise 0.
func (screen *ChatScreen) ExitCode() int {
	return screen.exitCode
}

// Init starts reading session updates.
func (screen *ChatScreen) Init() tea.Cmd {
	return tea.Batch(screen.waitForSessionUpdate(), screen.startAnimation())
}

// Update applies one message.
func (screen *ChatScreen) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	var commands []tea.Cmd
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		screen.width, screen.height = typed.Width, typed.Height
	case tea.KeyPressMsg:
		commands = append(commands, screen.handleKey(typed))
	case tea.PasteMsg:
		screen.handlePaste(typed.Content)
	case sessionUpdateMsg:
		screen.isWaitingForSessionUpdate = false
		commands = append(commands, screen.applySessionUpdate(typed.update))
		if !screen.isExiting {
			commands = append(commands, screen.waitForSessionUpdate())
		}
	case sessionFeedFailedMsg:
		screen.isWaitingForSessionUpdate = false
		commands = append(commands, screen.handleFeedFailure(typed.err))
	case userActionFailedMsg:
		screen.handleActionFailure(typed)
	case animationTickMsg:
		screen.isTicking = false
		commands = append(commands, screen.startAnimation())
	case scrollbackPrintedMsg:
		screen.isPrinting = false
	}
	commands = append(commands, screen.flushScrollback())
	if screen.isExiting && !screen.isPrinting {
		commands = append(commands, tea.Quit)
	}
	return screen, tea.Batch(commands...)
}

// View draws the live area. It is empty while exiting, so only the
// scrollback stays on the terminal.
func (screen *ChatScreen) View() tea.View {
	if screen.isExiting {
		return tea.NewView("")
	}
	content, cursor := screen.renderLiveArea()
	view := tea.NewView(content)
	view.Cursor = cursor
	return view
}

func (screen *ChatScreen) waitForSessionUpdate() tea.Cmd {
	if screen.isWaitingForSessionUpdate {
		return nil
	}
	screen.isWaitingForSessionUpdate = true
	source, ctx := screen.source, screen.ctx
	return func() tea.Msg {
		update, err := source.NextSessionUpdate(ctx)
		if err != nil {
			return sessionFeedFailedMsg{err: err}
		}
		return sessionUpdateMsg{update: update}
	}
}

func (screen *ChatScreen) startAnimation() tea.Cmd {
	if screen.isTicking || !screen.isAnimating() {
		return nil
	}
	screen.isTicking = true
	return tea.Tick(spinnerFrameInterval, func(time.Time) tea.Msg { return animationTickMsg{} })
}

func (screen *ChatScreen) isAnimating() bool {
	return !screen.isSessionLoaded || screen.isTurnRunning || len(screen.liveToolCalls) > 0
}

func (screen *ChatScreen) submitAction(action sessionfeed.UserAction) tea.Cmd {
	source, ctx := screen.source, screen.ctx
	return func() tea.Msg {
		if err := source.SubmitUserAction(ctx, action); err != nil {
			return userActionFailedMsg{action: action, err: err}
		}
		return nil
	}
}

// handleKey routes a key to the overlay, popup, pending bar, completion
// list, or input that has focus.
func (screen *ChatScreen) handleKey(key tea.KeyPressMsg) tea.Cmd {
	if screen.isInputDisabled {
		screen.exit(screen.exitCode)
		return nil
	}
	keyName := key.String()
	if keyName == "ctrl+c" {
		return screen.handleInterruptKey()
	}
	if keyName == "f1" {
		screen.isHelpOpen = !screen.isHelpOpen
		return nil
	}
	if screen.isHelpOpen {
		if keyName == "esc" || keyName == "q" {
			screen.isHelpOpen = false
		}
		return nil
	}
	if screen.isPopupOpen {
		return screen.handlePopupKey(key)
	}
	if screen.pendingFocus != pendingFocusNone {
		return screen.handlePendingBarKey(key)
	}
	if screen.isCompletionVisible() {
		if command, handled := screen.handleCompletionKey(key); handled {
			return command
		}
	}
	return screen.handleInputKey(key)
}

func (screen *ChatScreen) handleInputKey(key tea.KeyPressMsg) tea.Cmd {
	switch key.String() {
	case "enter":
		return screen.submitInput()
	case "shift+enter", "alt+enter", "ctrl+j":
		screen.input.InsertText("\n")
		screen.isCompletionDismissed = false
	case "tab":
		if screen.input.IsEmpty() {
			screen.focusPendingBar()
		}
	case "up":
		if screen.input.IsCursorOnFirstLine() {
			screen.recallHistory(-1)
		} else {
			screen.input.MoveCursorVertically(true)
		}
	case "down":
		if screen.input.IsCursorOnLastLine() {
			screen.recallHistory(1)
		} else {
			screen.input.MoveCursorVertically(false)
		}
	case "ctrl+d":
		if screen.input.IsEmpty() {
			screen.exit(0)
		}
	case "ctrl+l":
		return tea.ClearScreen
	case "esc":
		screen.isCompletionDismissed = true
	default:
		if screen.input.HandleEditingKey(key) {
			screen.isCompletionDismissed = false
			screen.completionIndex = 0
			screen.isExitArmed = screen.isExitArmed && screen.isTurnRunning
		}
	}
	return nil
}

func (screen *ChatScreen) handlePaste(content string) {
	switch {
	case screen.isPopupOpen && screen.popup.isEnteringText:
		screen.popup.textInput.InsertText(content)
	case !screen.isPopupOpen && !screen.isHelpOpen && screen.pendingFocus == pendingFocusNone:
		screen.input.InsertText(content)
	}
}

// submitInput sends the input as a message or runs it as a slash command.
func (screen *ChatScreen) submitInput() tea.Cmd {
	text := strings.TrimRight(screen.input.Value(), " \n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	screen.input.Clear()
	screen.isCompletionDismissed = false
	screen.completionIndex = 0
	screen.inputHistory = append(screen.inputHistory, text)
	screen.historyIndex = len(screen.inputHistory)
	if parsed, isCommand := parseSlashCommand(text); isCommand {
		return screen.runSlashCommand(parsed)
	}
	if screen.isTurnRunning {
		screen.localQueuedTexts = append(screen.localQueuedTexts, text)
	} else {
		screen.printBlock(scrollbackBlockUserMessage, renderUserMessage(text, "", screen.width, screen.theme))
	}
	return screen.submitAction(sessionfeed.SendUserMessage{Text: text})
}

func (screen *ChatScreen) runSlashCommand(parsed parsedSlashCommand) tea.Cmd {
	switch parsed.name {
	case helpCommandName:
		screen.isHelpOpen = true
		return nil
	case exitCommandName:
		screen.exit(0)
		return nil
	}
	if !screen.commands.Has(parsed.name) {
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Unknown command /%s. /help lists commands.", parsed.name))
		return nil
	}
	return screen.submitAction(sessionfeed.RunSlashCommand{Name: parsed.name, Argument: parsed.argument})
}

func (screen *ChatScreen) recallHistory(step int) {
	if len(screen.inputHistory) == 0 {
		return
	}
	screen.historyIndex = min(max(screen.historyIndex+step, 0), len(screen.inputHistory))
	if screen.historyIndex == len(screen.inputHistory) {
		screen.input.Clear()
		return
	}
	screen.input.SetValue(screen.inputHistory[screen.historyIndex])
}

func (screen *ChatScreen) isCompletionVisible() bool {
	if screen.isCompletionDismissed {
		return false
	}
	prefix, isTypingName := completionPrefix(screen.input.Value())
	return isTypingName && len(screen.commands.Matching(prefix)) > 0
}

func (screen *ChatScreen) completionMatches() []sessionfeed.SlashCommand {
	prefix, _ := completionPrefix(screen.input.Value())
	return screen.commands.Matching(prefix)
}

func (screen *ChatScreen) handleCompletionKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	matches := screen.completionMatches()
	selected := matches[min(screen.completionIndex, len(matches)-1)]
	switch key.String() {
	case "up":
		screen.completionIndex = max(screen.completionIndex-1, 0)
	case "down":
		screen.completionIndex = min(screen.completionIndex+1, len(matches)-1)
	case "tab":
		completed := "/" + selected.Name
		if selected.ArgumentHint != "" {
			completed += " "
		}
		screen.input.SetValue(completed)
		screen.completionIndex = 0
	case "enter":
		if selected.ArgumentHint != "" {
			screen.input.SetValue("/" + selected.Name + " ")
			return nil, true
		}
		screen.input.SetValue("/" + selected.Name)
		return screen.submitInput(), true
	case "esc":
		screen.isCompletionDismissed = true
	default:
		return nil, false
	}
	return nil, true
}

// handleInterruptKey applies Ctrl-C: cancel a running turn, clear a draft,
// or exit.
func (screen *ChatScreen) handleInterruptKey() tea.Cmd {
	if !screen.isTurnRunning {
		if !screen.input.IsEmpty() && !screen.isPopupOpen && !screen.isHelpOpen {
			screen.input.Clear()
			return nil
		}
		screen.exit(0)
		return nil
	}
	if screen.isExitArmed || screen.cancelState == cancelStateConfirmed {
		if screen.cancelState != cancelStateConfirmed && screen.session.AcceptsTurnControl {
			screen.printNotice(sessionfeed.NoticeSeverityWarning, "Exited before the cancel was confirmed; the turn may still run."+screen.resumeHint())
		}
		screen.exit(0)
		return nil
	}
	screen.isExitArmed = true
	if !screen.session.AcceptsTurnControl {
		screen.printNotice(sessionfeed.NoticeSeverityWarning, "This agent cannot cancel a running turn. Ctrl-C again exits; the turn keeps running.")
		return nil
	}
	screen.cancelState = cancelStateRequesting
	screen.cancelRequestedTurnID = screen.turn.turnID
	return screen.submitAction(sessionfeed.CancelTurn{TurnID: screen.turn.turnID})
}

func (screen *ChatScreen) resumeHint() string {
	if screen.session.SessionID == "" {
		return ""
	}
	return " Resume with: " + screen.resumeCommand()
}

func (screen *ChatScreen) resumeCommand() string {
	return "dex-agents-" + screen.session.Command + " --resume " + screen.session.SessionID
}

func (screen *ChatScreen) handlePopupKey(key tea.KeyPressMsg) tea.Cmd {
	if screen.popupIndex >= len(screen.interrupts) {
		screen.closePopup()
		return nil
	}
	interrupt := screen.interrupts[screen.popupIndex]
	result := screen.popup.HandleKey(interrupt, key)
	switch {
	case result.isClosed:
		screen.closePopup()
	case result.isNextRequested:
		screen.openPopup(screen.nextOpenableInterrupt(screen.popupIndex))
	case result.action != nil:
		screen.interrupts[screen.popupIndex].isSubmitting = true
		screen.interruptFeedback[interrupt.InterruptID()] = result.feedback
		if result.outcomeNote != "" {
			screen.interruptOutcomes[interrupt.InterruptID()] = result.outcomeNote
		}
		next := screen.nextOpenableInterrupt(screen.popupIndex)
		if next < 0 || next == screen.popupIndex {
			screen.closePopup()
		} else {
			screen.openPopup(next)
		}
		return screen.submitAction(result.action)
	}
	return nil
}

// nextOpenableInterrupt returns the index of the next interrupt after from
// that is not being submitted, wrapping around, or -1 when none is.
func (screen *ChatScreen) nextOpenableInterrupt(from int) int {
	count := len(screen.interrupts)
	for step := 1; step <= count; step++ {
		index := (from + step) % count
		if !screen.interrupts[index].isSubmitting {
			return index
		}
	}
	return -1
}

func (screen *ChatScreen) openPopup(index int) {
	if index < 0 || index >= len(screen.interrupts) {
		screen.closePopup()
		return
	}
	if screen.isPopupOpen && screen.popupIndex == index {
		return
	}
	screen.isPopupOpen = true
	screen.popupIndex = index
	screen.popup = newInterruptPopup(screen.interrupts[index])
	screen.pendingFocus = pendingFocusNone
}

func (screen *ChatScreen) closePopup() {
	screen.isPopupOpen = false
	screen.popup = interruptPopup{}
}

func (screen *ChatScreen) waitingInterruptCount() int {
	count := 0
	for _, interrupt := range screen.interrupts {
		if !interrupt.isSubmitting {
			count++
		}
	}
	return count
}

func (screen *ChatScreen) canActOnQueuedMessage() bool {
	return screen.session.AcceptsTurnControl && len(screen.queuedMessages) > 0
}

func (screen *ChatScreen) focusPendingBar() {
	switch {
	case screen.waitingInterruptCount() > 0:
		screen.pendingFocus = pendingFocusInterrupts
	case screen.canActOnQueuedMessage():
		screen.pendingFocus = pendingFocusQueuedMessage
	}
}

func (screen *ChatScreen) handlePendingBarKey(key tea.KeyPressMsg) tea.Cmd {
	keyName := key.String()
	if screen.isConfirmingQueuedDelete {
		screen.isConfirmingQueuedDelete = false
		if keyName != "y" || !screen.canActOnQueuedMessage() {
			return nil
		}
		screen.pendingFocus = pendingFocusNone
		last := screen.queuedMessages[len(screen.queuedMessages)-1]
		return screen.submitAction(sessionfeed.DeleteQueuedMessage{MessageID: last.MessageID})
	}
	switch keyName {
	case "esc":
		screen.pendingFocus = pendingFocusNone
	case "tab", "down", "up", "j", "k":
		if screen.pendingFocus == pendingFocusInterrupts && screen.canActOnQueuedMessage() {
			screen.pendingFocus = pendingFocusQueuedMessage
		} else if screen.pendingFocus == pendingFocusQueuedMessage && screen.waitingInterruptCount() > 0 {
			screen.pendingFocus = pendingFocusInterrupts
		}
	case "enter":
		if screen.pendingFocus == pendingFocusInterrupts {
			screen.openPopup(screen.nextOpenableInterrupt(-1))
		}
	case "s":
		if screen.pendingFocus == pendingFocusQueuedMessage && screen.canActOnQueuedMessage() {
			screen.pendingFocus = pendingFocusNone
			last := screen.queuedMessages[len(screen.queuedMessages)-1]
			return screen.submitAction(sessionfeed.SteerQueuedMessage{MessageID: last.MessageID})
		}
	case "x":
		if screen.pendingFocus == pendingFocusQueuedMessage && screen.canActOnQueuedMessage() {
			screen.isConfirmingQueuedDelete = true
		}
	}
	return nil
}

func (screen *ChatScreen) exit(code int) {
	screen.isExiting = true
	screen.exitCode = code
}

// applySessionUpdate applies one session update.
func (screen *ChatScreen) applySessionUpdate(update sessionfeed.SessionUpdate) tea.Cmd {
	switch typed := update.(type) {
	case sessionfeed.SessionOpened:
		screen.session = typed
		screen.lastTurnNumber = max(screen.lastTurnNumber, typed.TurnCount)
	case sessionfeed.SessionLoaded:
		screen.isSessionLoaded = true
	case sessionfeed.UserMessageRecorded:
		screen.flushReplies()
		screen.printBlock(scrollbackBlockUserMessage, renderUserMessage(typed.Text, typed.Origin, screen.width, screen.theme))
	case sessionfeed.TurnStarted:
		screen.startTurn(typed)
	case sessionfeed.TurnActivityChanged:
		if typed.TurnID == screen.turn.turnID && typed.Activity != screen.turn.activity {
			screen.turn.activity = typed.Activity
			screen.turn.activityAt = screen.now()
		}
	case sessionfeed.TurnEnded:
		screen.endTurn(typed)
	case sessionfeed.AssistantTextDelta:
		screen.reasoningTail = ""
		screen.printReplyOutputs(screen.replies.AppendDelta(typed))
	case sessionfeed.AssistantMessageRecorded:
		screen.printReplyOutputs(screen.replies.RecordFinalMessage(typed))
	case sessionfeed.ReasoningDelta:
		screen.appendReasoning(typed.Text)
	case sessionfeed.UsageRecorded:
		screen.totalTokens = typed.TotalTokens
	case sessionfeed.NoticeRaised:
		screen.printNotice(typed.Severity, typed.Text)
	case sessionfeed.CancelRequestAnswered:
		screen.applyCancelAnswer(typed)
	case sessionfeed.WorkerStateChanged:
		screen.applyWorkerState(typed)
	case sessionfeed.ServerReachabilityChanged:
		screen.isServerReachable = typed.IsReachable
	case sessionfeed.QueuedMessagesChanged:
		screen.queuedMessages = typed.Messages
		screen.localQueuedTexts = nil
		if !screen.canActOnQueuedMessage() && screen.pendingFocus == pendingFocusQueuedMessage {
			screen.pendingFocus = pendingFocusNone
		}
	case sessionfeed.PlanChanged:
		screen.planItems = typed.Items
	case sessionfeed.ToolCallChanged:
		screen.applyToolCall(typed)
	case sessionfeed.ApprovalRequested:
		screen.addInterrupt(pendingInterrupt{approval: &typed})
	case sessionfeed.QuestionAsked:
		screen.addInterrupt(pendingInterrupt{question: &typed})
	case sessionfeed.InterruptResolved:
		screen.resolveInterrupt(typed)
	}
	return screen.startAnimation()
}

func (screen *ChatScreen) startTurn(started sessionfeed.TurnStarted) {
	now := screen.now()
	screen.isTurnRunning = true
	screen.turn = activeTurn{
		turnID:     started.TurnID,
		number:     started.TurnNumber,
		startedAt:  started.StartedAt,
		activity:   sessionfeed.TurnActivityRunningModel,
		activityAt: now,
	}
	if screen.turn.startedAt.IsZero() {
		screen.turn.startedAt = now
	}
	screen.lastTurnNumber = max(screen.lastTurnNumber, started.TurnNumber)
	screen.session.IsNewSession = false
	screen.turnToolNameWidth = 0
}

func (screen *ChatScreen) endTurn(ended sessionfeed.TurnEnded) {
	screen.printReplyOutputs(screen.replies.EndTurn(ended.TurnID))
	screen.reasoningTail = ""
	for _, call := range screen.liveToolCalls {
		screen.printToolRow(call)
	}
	screen.liveToolCalls = nil
	switch ended.Outcome {
	case sessionfeed.TurnOutcomeCancelled:
		screen.printBlock(scrollbackBlockNotice, []string{
			contentIndent + screen.theme.Secondary.Render(screen.theme.Symbols.NotRun+" turn cancelled; the reply above is incomplete"),
		})
	case sessionfeed.TurnOutcomeFailed:
		screen.printNotice(sessionfeed.NoticeSeverityError, ended.FailureText)
	}
	if ended.TurnID == screen.turn.turnID {
		screen.isTurnRunning = false
		screen.cancelState = cancelStateNone
		screen.isExitArmed = false
	}
}

func (screen *ChatScreen) appendReasoning(text string) {
	tail := []rune(strings.ReplaceAll(screen.reasoningTail+text, "\n", " "))
	if len(tail) > reasoningTailLength {
		tail = tail[len(tail)-reasoningTailLength:]
	}
	screen.reasoningTail = string(tail)
}

func (screen *ChatScreen) applyCancelAnswer(answer sessionfeed.CancelRequestAnswered) {
	if answer.TurnID != screen.cancelRequestedTurnID {
		return
	}
	switch answer.Outcome {
	case sessionfeed.CancelOutcomeAccepted:
		screen.cancelState = cancelStateConfirmed
	case sessionfeed.CancelOutcomeTurnAlreadyEnded:
		screen.cancelState = cancelStateNone
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "the turn already ended")
	case sessionfeed.CancelOutcomeRefused:
		screen.cancelState = cancelStateNone
		screen.printNotice(sessionfeed.NoticeSeverityWarning, "Cannot cancel this turn.")
	case sessionfeed.CancelOutcomeNotConfirmed:
		screen.cancelState = cancelStateNone
		screen.printNotice(sessionfeed.NoticeSeverityError, "Cancel not confirmed: "+answer.Reason+".")
	}
}

func (screen *ChatScreen) applyWorkerState(changed sessionfeed.WorkerStateChanged) {
	screen.workerState = changed.State
	if changed.State != sessionfeed.WorkerStateStopped {
		return
	}
	text := fmt.Sprintf("The Worker stopped (exit %d), so this chat cannot continue.", changed.ExitCode)
	resume := changed.ResumeCommand
	if resume == "" && screen.session.SessionID != "" {
		resume = screen.resumeCommand()
	}
	if resume != "" {
		text += " Exit and resume with: " + resume
	}
	screen.flushReplies()
	screen.printNotice(sessionfeed.NoticeSeverityError, text)
	screen.disableInput(1)
}

func (screen *ChatScreen) handleFeedFailure(err error) tea.Cmd {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	screen.flushReplies()
	text := "The command stopped, so this chat cannot continue."
	if !errors.Is(err, sessionfeed.ErrSessionFeedClosed) {
		text = fmt.Sprintf("The session feed failed: %v. This chat cannot continue.", err)
	}
	if hint := screen.resumeHint(); hint != "" {
		text += hint
	}
	screen.printNotice(sessionfeed.NoticeSeverityError, text)
	screen.exit(1)
	return nil
}

func (screen *ChatScreen) disableInput(exitCode int) {
	screen.isInputDisabled = true
	screen.exitCode = exitCode
	screen.isTurnRunning = false
	screen.closePopup()
	screen.isHelpOpen = false
	screen.pendingFocus = pendingFocusNone
}

func (screen *ChatScreen) handleActionFailure(failure userActionFailedMsg) {
	switch action := failure.action.(type) {
	case sessionfeed.ResolveApproval:
		screen.restoreInterrupt(action.InterruptID)
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Approval not sent: %v. Press Tab to try again.", failure.err))
	case sessionfeed.AnswerQuestion:
		screen.restoreInterrupt(action.InterruptID)
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Answer not sent: %v. Press Tab to try again.", failure.err))
	case sessionfeed.DeclineQuestion:
		screen.restoreInterrupt(action.InterruptID)
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Decline not sent: %v. Press Tab to try again.", failure.err))
	case sessionfeed.CancelTurn:
		screen.cancelState = cancelStateNone
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Cancel not confirmed: %v.", failure.err))
	case sessionfeed.SendUserMessage:
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Not sent: %v. Press Up to edit and send again.", failure.err))
	default:
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Request failed: %v.", failure.err))
	}
}

func (screen *ChatScreen) restoreInterrupt(interruptID string) {
	delete(screen.interruptFeedback, interruptID)
	delete(screen.interruptOutcomes, interruptID)
	for index := range screen.interrupts {
		if screen.interrupts[index].InterruptID() == interruptID {
			screen.interrupts[index].isSubmitting = false
		}
	}
}

func (screen *ChatScreen) applyToolCall(call sessionfeed.ToolCallChanged) {
	screen.turnToolNameWidth = min(max(screen.turnToolNameWidth, terminaltheme.DisplayWidth(call.ToolName)), maximumToolNameWidth)
	index := -1
	for existingIndex, existing := range screen.liveToolCalls {
		if existing.ToolCallID == call.ToolCallID {
			index = existingIndex
			break
		}
	}
	if index < 0 {
		screen.flushReplies()
	}
	if call.Status.IsFinished() {
		if index >= 0 {
			screen.liveToolCalls = append(screen.liveToolCalls[:index], screen.liveToolCalls[index+1:]...)
		}
		screen.printToolRow(call)
		return
	}
	if index >= 0 {
		screen.liveToolCalls[index] = call
		return
	}
	screen.liveToolCalls = append(screen.liveToolCalls, call)
}

func (screen *ChatScreen) addInterrupt(interrupt pendingInterrupt) {
	for _, existing := range screen.interrupts {
		if existing.InterruptID() == interrupt.InterruptID() {
			return
		}
	}
	screen.interrupts = append(screen.interrupts, interrupt)
	if !screen.isPopupOpen && screen.pendingFocus == pendingFocusNone && !screen.isHelpOpen && screen.input.IsEmpty() {
		screen.openPopup(len(screen.interrupts) - 1)
	}
}

func (screen *ChatScreen) resolveInterrupt(resolved sessionfeed.InterruptResolved) {
	index := -1
	for existingIndex, existing := range screen.interrupts {
		if existing.InterruptID() == resolved.InterruptID {
			index = existingIndex
			break
		}
	}
	delete(screen.interruptFeedback, resolved.InterruptID)
	outcome, hasOutcome := screen.interruptOutcomes[resolved.InterruptID]
	delete(screen.interruptOutcomes, resolved.InterruptID)
	if index < 0 {
		return
	}
	switch {
	case resolved.ResolvedBy != "":
		screen.printNotice(sessionfeed.NoticeSeverityInfo, resolutionText(resolved.Resolution)+" in "+resolved.ResolvedBy+".")
	case hasOutcome && resolved.Resolution != sessionfeed.InterruptWithdrawn:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, outcome)
	}
	wasOpen := screen.isPopupOpen && screen.popupIndex == index
	screen.interrupts = append(screen.interrupts[:index], screen.interrupts[index+1:]...)
	switch {
	case wasOpen:
		screen.isPopupOpen = false
		next := screen.nextOpenableInterrupt(index - 1)
		if next >= 0 {
			screen.openPopup(next)
		} else {
			screen.closePopup()
		}
	case screen.isPopupOpen && screen.popupIndex > index:
		screen.popupIndex--
	}
	if screen.waitingInterruptCount() == 0 && screen.pendingFocus == pendingFocusInterrupts {
		screen.pendingFocus = pendingFocusNone
	}
}

func resolutionText(resolution sessionfeed.InterruptResolution) string {
	switch resolution {
	case sessionfeed.InterruptApproved:
		return "Approved"
	case sessionfeed.InterruptRejected:
		return "Rejected"
	case sessionfeed.InterruptAnswered:
		return "Answered"
	case sessionfeed.InterruptDeclined:
		return "Declined"
	default:
		return "Withdrawn"
	}
}

func (screen *ChatScreen) flushReplies() {
	screen.printReplyOutputs(screen.replies.FlushPending())
}

func (screen *ChatScreen) printReplyOutputs(outputs []replyOutput) {
	for _, output := range outputs {
		switch output.kind {
		case replyOutputCorrection:
			screen.printNotice(sessionfeed.NoticeSeverityInfo, output.text)
		default:
			screen.printBlock(scrollbackBlockReply, renderAssistantMarkdown(output.text, screen.width, screen.theme))
		}
	}
}

func (screen *ChatScreen) printToolRow(call sessionfeed.ToolCallChanged) {
	clock := toolRowClock{now: screen.now()}
	screen.printBlock(scrollbackBlockToolRow, renderToolRow(call, screen.turnToolNameWidth, screen.width, clock, screen.theme))
}

func (screen *ChatScreen) printNotice(severity sessionfeed.NoticeSeverity, text string) {
	screen.printBlock(scrollbackBlockNotice, renderNoticeLine(severity, text, screen.width, screen.theme))
}

// printBlock queues lines for the scrollback. A blank line separates blocks,
// except between consecutive tool rows or notices.
func (screen *ChatScreen) printBlock(kind scrollbackBlockKind, lines []string) {
	if len(lines) == 0 {
		return
	}
	isSameGroup := kind == screen.lastBlockKind && (kind == scrollbackBlockToolRow || kind == scrollbackBlockNotice)
	if screen.lastBlockKind != scrollbackBlockNone && !isSameGroup {
		screen.pendingLines = append(screen.pendingLines, "")
	}
	screen.pendingLines = append(screen.pendingLines, lines...)
	screen.lastBlockKind = kind
}

// flushScrollback keeps one print in flight, because Bubble Tea runs commands
// concurrently. A nil printer command printed synchronously.
func (screen *ChatScreen) flushScrollback() tea.Cmd {
	if screen.isPrinting || len(screen.pendingLines) == 0 {
		return nil
	}
	text := strings.Join(screen.pendingLines, "\n")
	screen.pendingLines = nil
	printCommand := screen.printScrollback(text)
	if printCommand == nil {
		return nil
	}
	screen.isPrinting = true
	return tea.Sequence(printCommand, func() tea.Msg { return scrollbackPrintedMsg{} })
}

// renderLiveArea draws the redrawn bottom area and returns the terminal
// cursor position, or nil when the cursor is hidden.
func (screen *ChatScreen) renderLiveArea() (string, *tea.Cursor) {
	width := max(screen.width, 1)
	now := screen.now()
	spinnerFrame := int(now.UnixMilli() / spinnerFrameInterval.Milliseconds())
	if terminaltheme.ClassifyWidth(width) == terminaltheme.WidthTooNarrow {
		return screen.renderTooNarrowArea(width, now, spinnerFrame)
	}
	lines := screen.renderActivityLines(width, now, spinnerFrame)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	var cursor *tea.Cursor
	switch {
	case screen.isInputDisabled:
		lines = append(lines, renderHintLine([]string{"Press any key to exit"}, width, screen.theme))
	case screen.isHelpOpen:
		lines = append(lines, screen.capToScreenShare(renderHelpOverlay(screen.commands, width, screen.theme))...)
		lines = append(lines, "", renderHintLine([]string{"Esc close"}, width, screen.theme))
	case screen.isPopupOpen && screen.popupIndex < len(screen.interrupts):
		lines = append(lines, screen.renderPopup(width, now)...)
		lines = append(lines, renderHintLine(screen.popupHints(), width, screen.theme))
	default:
		lines = append(lines, screen.renderPendingBar(width)...)
		if len(screen.planItems) > 0 {
			lines = append(lines, renderPlanLine(screen.planItems, width, screen.theme))
		}
		lines = append(lines, screen.renderContextLine(width, now, spinnerFrame))
		lines = append(lines, screen.renderCompletionList(width)...)
		input := screen.renderInput(width)
		cursorRow := len(lines) + input.cursorRow
		lines = append(lines, input.lines...)
		lines = append(lines, renderHintLine(screen.inputHints(), width, screen.theme))
		cursor = tea.NewCursor(input.cursorColumn, cursorRow)
	}
	if screen.height > 0 && len(lines) > screen.height-1 {
		dropped := len(lines) - (screen.height - 1)
		lines = lines[dropped:]
		if cursor != nil {
			cursor.Y -= dropped
			if cursor.Y < 0 {
				cursor = nil
			}
		}
	}
	return strings.Join(lines, "\n"), cursor
}

func (screen *ChatScreen) renderTooNarrowArea(width int, now time.Time, spinnerFrame int) (string, *tea.Cursor) {
	symbol, _ := screen.contextStatus(now, spinnerFrame)
	if symbol == "" {
		symbol = screen.theme.Secondary.Render(screen.theme.Symbols.Queued)
	}
	lines := []string{terminaltheme.TruncateToWidth(" "+symbol+" Resize to 40 columns for the full view.", width, screen.theme.Symbols.Ellipsis)}
	input := screen.renderInput(width)
	cursorRow := len(lines) + input.cursorRow
	lines = append(lines, input.lines...)
	return strings.Join(lines, "\n"), tea.NewCursor(input.cursorColumn, cursorRow)
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

func (screen *ChatScreen) renderPopup(width int, now time.Time) []string {
	interrupt := screen.interrupts[screen.popupIndex]
	position := popupPosition{index: screen.popupIndex, count: len(screen.interrupts)}
	if interrupt.approval != nil {
		maximumHeight := 0
		if screen.height > 0 {
			maximumHeight = max(screen.height-4, 8)
		}
		return renderApprovalPopup(*interrupt.approval, screen.popup, position, width, maximumHeight, now, screen.theme)
	}
	return renderQuestionPopup(*interrupt.question, screen.popup, position, width, screen.theme)
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
		line := renderLeftRightLine(left, right, width-2, symbols.Ellipsis)
		lines = append(lines, screen.renderPendingRow(line, isLast && screen.pendingFocus == pendingFocusQueuedMessage, width))
	}
	for _, text := range screen.localQueuedTexts {
		line := screen.theme.Secondary.Render(symbols.Queued+" queueing") + "  " + text
		lines = append(lines, screen.renderPendingRow(line, false, width))
	}
	if screen.isConfirmingQueuedDelete {
		lines = append(lines, contentIndent+screen.theme.Warning.Render(symbols.Warning)+" Delete the queued message? It will not be sent.")
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

// renderContextLine draws the context line. When it does not fit, it drops
// the command, then the turn.
func (screen *ChatScreen) renderContextLine(width int, now time.Time, spinnerFrame int) string {
	widthClass := terminaltheme.ClassifyWidth(width)
	symbols := screen.theme.Symbols
	symbol, statusText := screen.contextStatus(now, spinnerFrame)
	status := statusText
	if symbol != "" {
		status = symbol + " " + statusText
	}
	isNewSession := screen.session.IsNewSession && !screen.isTurnRunning && screen.isSessionLoaded
	newSessionLabel := screen.theme.Strong.Render("New session")
	if widthClass == terminaltheme.WidthCompact {
		if isNewSession {
			status = newSessionLabel
		}
		return " " + terminaltheme.TruncateToWidth(status, width-1, symbols.Ellipsis)
	}
	model := screen.session.Model
	if widthClass == terminaltheme.WidthNarrow {
		model = terminaltheme.TruncateKeepingTail(model, 16, symbols.Ellipsis)
	}
	right := ""
	if screen.totalTokens > 0 {
		right = screen.theme.Secondary.Render(formatTokenCount(screen.totalTokens))
	}
	command := screen.session.Command
	turn := ""
	if turnNumber := screen.displayedTurnNumber(); turnNumber > 0 && !isNewSession {
		turn = fmt.Sprintf("turn %d", turnNumber)
	}
	separator := screen.theme.Secondary.Render(symbols.Separator)
	joinParts := func() string {
		if isNewSession {
			return " " + strings.Join(nonEmptyStrings(newSessionLabel, command, model), separator)
		}
		return " " + strings.Join(nonEmptyStrings(command, model, turn, status), separator)
	}
	left := joinParts()
	room := width
	if right != "" {
		room -= terminaltheme.DisplayWidth(right) + 2
	}
	if terminaltheme.DisplayWidth(left) > room && !isNewSession {
		command = ""
		left = joinParts()
	}
	if terminaltheme.DisplayWidth(left) > room && !isNewSession {
		turn = ""
		left = joinParts()
	}
	return renderLeftRightLine(left, right, width, symbols.Ellipsis)
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
	case screen.isConfirmingQueuedDelete:
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

// capToScreenShare keeps at most 60 percent of the screen height of lines.
func (screen *ChatScreen) capToScreenShare(lines []string) []string {
	if screen.height <= 0 {
		return lines
	}
	limit := max(screen.height*6/10, 3)
	if len(lines) <= limit {
		return lines
	}
	hidden := len(lines) - limit + 1
	return append(lines[:limit-1], contentIndent+screen.theme.Secondary.Render(moreLinesText(hidden, screen.theme.Symbols.Ellipsis)))
}
