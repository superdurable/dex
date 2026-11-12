// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package chatscreen is the full-screen agents chat: a scrollable message
// view, the input, and a two-line status bar.
package chatscreen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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
	// Now returns the current time. Default: time.Now. Tests fix it so
	// spinner frames and elapsed times are stable.
	Now func() time.Time
	// SessionCommands are the source's slash commands. Default: none. They
	// cannot redefine the screen's own commands.
	SessionCommands []sessionfeed.SlashCommand
	// ShowsStartupLogo shows the Dex logo in the welcome area on a 40x15
	// terminal or larger. Default false.
	ShowsStartupLogo bool
}

// ChatScreen is the chat screen model.
type ChatScreen struct {
	ctx      context.Context
	source   sessionfeed.ChatSessionSource
	theme    terminaltheme.Theme
	now      func() time.Time
	commands slashCommandRegistry

	width  int
	height int

	session           sessionfeed.SessionOpened
	isSessionLoaded   bool
	turn              activeTurn
	isTurnRunning     bool
	lastTurnNumber    int
	totalTokens       int64
	reasoningTail     string
	plan              sessionfeed.PlanChanged
	isPlanModeArmed   bool
	attachments       []sessionfeed.ImageAttachment
	heldMessages      []sessionfeed.SendUserMessage
	isTurnStarting    bool
	queuedMessages    []sessionfeed.QueuedMessage
	localQueuedTexts  []string
	printedSequences  map[int64]bool
	notices           *noticeFilter
	isServerReachable bool
	workerState       sessionfeed.WorkerState
	exitCode          int
	isExiting         bool
	isInputDisabled   bool

	replies           *replyStream
	liveToolCalls     []sessionfeed.ToolCallChanged
	turnToolNameWidth int
	history           transcript
	scroll            messageScroll
	lastReplyText     string
	isLogoAllowed     bool

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
	pendingDelete             *sessionfeed.QueuedMessage
	isSessionStartWaiting     bool
	pendingSwitchSessionID    string
	cancelState               cancelState
	isExitArmed               bool
	cancelRequestedTurnID     string
	isWaitingForSessionUpdate bool
	isTicking                 bool
	helpScrollFromTop         int
	exitReason                string
	// openedSession is the first SessionOpened, which the welcome area shows
	// with the newest model.
	openedSession    sessionfeed.SessionOpened
	isSessionKnown   bool
	sessionCommands  []sessionfeed.SlashCommand
	messageQueue     []sessionfeed.SendUserMessage
	isSendingMessage bool
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

// messageSubmittedMsg ends one SendUserMessage; messages go one at a time
// so they keep their order.
type messageSubmittedMsg struct {
	message sessionfeed.SendUserMessage
	err     error
}

type animationTickMsg struct{}

const (
	defaultScreenWidth  = 80
	defaultScreenHeight = 24
)

// NewChatScreen returns a chat screen whose source calls use ctx. Cancel ctx
// after the program ends.
func NewChatScreen(ctx context.Context, config ChatScreenConfig) *ChatScreen {
	if ctx == nil {
		panic("chat screen context must not be nil")
	}
	if config.Source == nil {
		panic("chat screen source must not be nil")
	}
	if len(config.Theme.Symbols.SpinnerFrames) == 0 {
		panic("chat screen theme must come from terminaltheme.NewTheme")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	screen := &ChatScreen{
		ctx:               ctx,
		source:            config.Source,
		theme:             config.Theme,
		now:               now,
		commands:          newSlashCommandRegistry(config.SessionCommands, false),
		sessionCommands:   config.SessionCommands,
		isLogoAllowed:     config.ShowsStartupLogo,
		width:             defaultScreenWidth,
		height:            defaultScreenHeight,
		scroll:            messageScroll{isFollowing: true},
		isServerReachable: true,
		replies:           newReplyStream(),
		interruptFeedback: map[string]string{},
		interruptOutcomes: map[string]string{},
		printedSequences:  map[int64]bool{},
		notices:           newNoticeFilter(),
	}
	screen.history = newTranscript(config.Theme)
	screen.history.Append(transcriptBlockWelcome, screen.welcome())
	return screen
}

func (screen *ChatScreen) welcome() welcomeBlock {
	isLogoShown := screen.isLogoAllowed && screen.width >= minimumLogoWidth && screen.height >= minimumLogoHeight
	session := screen.session
	session.IsNewSession = screen.openedSession.IsNewSession
	return welcomeBlock{session: session, isLogoShown: isLogoShown}
}

// ExitSummary is the line printed after the full screen closes: where the
// session stays.
func (screen *ChatScreen) ExitSummary() string {
	if screen.session.SessionID == "" {
		return screen.exitReason
	}
	summary := "Session " + screen.session.SessionID + " stays in Dex. Resume with: " + screen.resumeCommand()
	return strings.TrimSpace(screen.exitReason + " " + summary)
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
		screen.width, screen.height = max(typed.Width, 1), max(typed.Height, 1)
		screen.history.Replace(0, screen.welcome())
	case tea.MouseWheelMsg:
		screen.handleMouseWheel(typed)
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
	case messageSubmittedMsg:
		screen.isSendingMessage = false
		if typed.err != nil {
			screen.handleActionFailure(userActionFailedMsg{action: typed.message, err: typed.err})
		}
		commands = append(commands, screen.submitNextMessage())
	case animationTickMsg:
		screen.isTicking = false
		commands = append(commands, screen.startAnimation())
	}
	if screen.isExiting {
		commands = append(commands, tea.Quit)
	}
	return screen, tea.Batch(commands...)
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

// queueMessage submits message after the ones before it.
func (screen *ChatScreen) queueMessage(message sessionfeed.SendUserMessage) tea.Cmd {
	screen.messageQueue = append(screen.messageQueue, message)
	return screen.submitNextMessage()
}

func (screen *ChatScreen) submitNextMessage() tea.Cmd {
	if screen.isSendingMessage || len(screen.messageQueue) == 0 {
		return nil
	}
	message := screen.messageQueue[0]
	screen.messageQueue = screen.messageQueue[1:]
	screen.isSendingMessage = true
	source, ctx := screen.source, screen.ctx
	return func() tea.Msg {
		return messageSubmittedMsg{message: message, err: source.SubmitUserAction(ctx, message)}
	}
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
	if screen.handleScrollKey(key) {
		return nil
	}
	if keyName == "ctrl+z" {
		screen.printNotice(sessionfeed.NoticeSeverityWarning, "Ctrl-Z does not suspend this chat. Exit with Ctrl-D; the session keeps running.")
		return nil
	}
	if keyName == "f1" {
		screen.isHelpOpen, screen.helpScrollFromTop = !screen.isHelpOpen, 0
		return nil
	}
	if screen.isHelpOpen {
		switch keyName {
		case "esc", "q":
			screen.isHelpOpen = false
		case "pgup":
			screen.helpScrollFromTop = max(screen.helpScrollFromTop-popupScrollStep, 0)
		case "pgdown":
			screen.helpScrollFromTop += popupScrollStep
		}
		return nil
	}
	if screen.pendingSwitchSessionID != "" && screen.input.IsEmpty() && !screen.isPopupOpen {
		if command, isHandled := screen.handleSwitchKey(keyName); isHandled {
			return command
		}
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
		if screen.isSessionStartWaiting {
			screen.isSessionStartWaiting = false
			return screen.submitAction(sessionfeed.MoveSessionStartToBackground{})
		}
	default:
		if key.String() == "backspace" && screen.input.IsEmpty() && len(screen.attachments) > 0 {
			screen.attachments = screen.attachments[:len(screen.attachments)-1]
			return nil
		}
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
	message := sessionfeed.SendUserMessage{Text: text, Images: screen.attachments, IsPlanMode: screen.isPlanModeArmed}
	screen.attachments, screen.isPlanModeArmed = nil, false
	if !screen.isSessionLoaded {
		screen.heldMessages = append(screen.heldMessages, message)
		return nil
	}
	return screen.sendMessage(message)
}

// sendMessage prints message unless a turn runs or starts, then submits it.
func (screen *ChatScreen) sendMessage(message sessionfeed.SendUserMessage) tea.Cmd {
	if screen.isTurnRunning || screen.isTurnStarting {
		screen.localQueuedTexts = append(screen.localQueuedTexts, message.Text)
	} else {
		screen.printUserMessage(userMessageBlock{text: message.Text, images: message.Images})
		screen.isTurnStarting = true
	}
	return screen.queueMessage(message)
}

// releaseHeldMessages sends the messages typed before the first snapshot.
func (screen *ChatScreen) releaseHeldMessages() tea.Cmd {
	held := screen.heldMessages
	screen.heldMessages = nil
	commands := make([]tea.Cmd, 0, len(held))
	for _, message := range held {
		commands = append(commands, screen.sendMessage(message))
	}
	return tea.Batch(commands...)
}

func (screen *ChatScreen) runSlashCommand(parsed parsedSlashCommand) tea.Cmd {
	if !screen.commands.Has(parsed.name) {
		screen.printNotice(sessionfeed.NoticeSeverityError, fmt.Sprintf("Unknown command /%s. /help lists commands.", parsed.name))
		return nil
	}
	switch parsed.name {
	case helpCommandName:
		screen.isHelpOpen, screen.helpScrollFromTop = true, 0
		return nil
	case copyCommandName:
		return screen.copyLastReply()
	case exitCommandName:
		screen.exit(0)
		return nil
	case approveCommandName:
		screen.openWaitingInterrupt()
		return nil
	case steerCommandName:
		return screen.steerLastQueuedMessage()
	case dropCommandName:
		screen.confirmQueuedMessageDelete()
		return nil
	case planCommandName:
		screen.isPlanModeArmed = true
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "the next message asks for a plan")
		return nil
	case runCommandName:
		return screen.runPlan()
	}
	return screen.submitAction(sessionfeed.RunSlashCommand{Name: parsed.name, Argument: parsed.argument})
}

// copyLastReply copies the last reply's markdown with OSC 52, which most
// terminals pass to the system clipboard.
func (screen *ChatScreen) copyLastReply() tea.Cmd {
	if screen.lastReplyText == "" {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "no reply to copy yet")
		return nil
	}
	screen.printNotice(sessionfeed.NoticeSeverityInfo, "copied the last reply to the clipboard")
	return tea.SetClipboard(screen.lastReplyText)
}

// openWaitingInterrupt opens the first waiting approval or question. It
// never approves anything: the popup shows the consequences first.
func (screen *ChatScreen) openWaitingInterrupt() {
	next := screen.nextOpenableInterrupt(-1)
	if next < 0 {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "nothing is waiting for you")
		return
	}
	screen.openPopup(next)
}

// runPlan approves the draft plan when the session can run it now.
func (screen *ChatScreen) runPlan() tea.Cmd {
	switch {
	case len(screen.plan.Items) == 0 || screen.plan.Status != sessionfeed.PlanStatusDraft:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "no draft plan to run")
		return nil
	case screen.isTurnRunning || screen.isTurnStarting:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "wait for the turn to end, then /run")
		return nil
	case !screen.plan.IsInPlanInteraction:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "the session is not waiting for plan approval")
		return nil
	}
	return screen.submitAction(sessionfeed.ExecutePlan{Revision: screen.plan.Revision})
}

func (screen *ChatScreen) steerLastQueuedMessage() tea.Cmd {
	if !screen.explainQueuedMessageUnavailable("steer") {
		return nil
	}
	screen.pendingFocus = pendingFocusNone
	last := screen.queuedMessages[len(screen.queuedMessages)-1]
	return screen.submitAction(sessionfeed.SteerQueuedMessage{MessageID: last.MessageID})
}

func (screen *ChatScreen) confirmQueuedMessageDelete() {
	if !screen.explainQueuedMessageUnavailable("delete") {
		return
	}
	screen.askToDeleteLastQueuedMessage()
}

// askToDeleteLastQueuedMessage pins the last queued message, so the
// confirmation deletes the message the user saw.
func (screen *ChatScreen) askToDeleteLastQueuedMessage() {
	last := screen.queuedMessages[len(screen.queuedMessages)-1]
	screen.pendingDelete = &last
	screen.pendingFocus = pendingFocusQueuedMessage
}

// explainQueuedMessageUnavailable reports whether a queued message can be
// acted on, printing why not when it cannot.
func (screen *ChatScreen) explainQueuedMessageUnavailable(verb string) bool {
	switch {
	case len(screen.queuedMessages) == 0:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "no queued message to "+verb)
		return false
	case !screen.session.AcceptsTurnControl:
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "this agent cannot "+verb+" queued messages")
		return false
	}
	return true
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
	if interrupt.approval != nil && screen.handleConsequenceKey(key) {
		return nil
	}
	result := screen.popup.HandleKey(interrupt, key)
	if resolve, isResolve := result.action.(sessionfeed.ResolveApproval); isResolve && resolve.IsApproved && !screen.canApprove() {
		return nil
	}
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

// canApprove reports whether the consequence is on screen now or was read in
// full.
func (screen *ChatScreen) canApprove() bool {
	return screen.popup.hasReadConsequence || screen.approvalLayout(screen.computeLayout()).isConsequenceVisible
}

// handleConsequenceKey runs the reading view's keys (e, PgUp/PgDn, Esc) and
// reports whether key was one.
func (screen *ChatScreen) handleConsequenceKey(key tea.KeyPressMsg) bool {
	layout := screen.approvalLayout(screen.computeLayout())
	popup := &screen.popup
	if !popup.isReadingConsequence {
		if key.String() != "e" || layout.isConsequenceVisible {
			return false
		}
		popup.isReadingConsequence, popup.consequenceScroll = true, 0
	} else {
		maximumScroll := max(layout.consequenceLineCount-layout.readingRoom, 0)
		// A page is the reading room, so paging never skips a line.
		page := max(layout.readingRoom, 1)
		switch key.String() {
		case "pgdown", "down", "j":
			popup.consequenceScroll = min(popup.consequenceScroll+page, maximumScroll)
		case "pgup", "up", "k":
			popup.consequenceScroll = max(popup.consequenceScroll-page, 0)
		case "e", "esc":
			popup.isReadingConsequence = false
			return true
		default:
			return true
		}
	}
	if popup.consequenceScroll+layout.readingRoom >= layout.consequenceLineCount {
		popup.hasReadConsequence = true
	}
	return true
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

// handleSwitchKey answers "Switch to it?" for a session /new started in the
// background. Only y, n, and Esc answer; other keys type as usual.
func (screen *ChatScreen) handleSwitchKey(keyName string) (tea.Cmd, bool) {
	sessionID := screen.pendingSwitchSessionID
	switch keyName {
	case "y":
		screen.pendingSwitchSessionID = ""
		return screen.submitAction(sessionfeed.SwitchSession{SessionID: sessionID}), true
	case "n", "esc":
		screen.pendingSwitchSessionID = ""
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "staying in this session; resume "+sessionID+" later with /sessions")
		return nil, true
	}
	return nil, false
}

func (screen *ChatScreen) handlePendingBarKey(key tea.KeyPressMsg) tea.Cmd {
	keyName := key.String()
	if screen.pendingDelete != nil {
		target := *screen.pendingDelete
		screen.pendingDelete = nil
		if keyName != "y" {
			return nil
		}
		screen.pendingFocus = pendingFocusNone
		return screen.submitAction(sessionfeed.DeleteQueuedMessage{MessageID: target.MessageID})
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
			screen.askToDeleteLastQueuedMessage()
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
	var command tea.Cmd
	switch typed := replaceUpdateControlCharacters(update).(type) {
	case sessionfeed.SessionOpened:
		if screen.isSessionKnown && typed.SessionID != screen.session.SessionID {
			// Another session: hold typed messages until its first snapshot.
			screen.isSessionLoaded = false
			screen.pendingSwitchSessionID = ""
			// Message sequences and turn IDs repeat across sessions.
			screen.flushReplies()
			screen.printedSequences = map[int64]bool{}
			screen.replies = newReplyStream()
		}
		screen.session = typed
		if !screen.isSessionKnown {
			screen.openedSession, screen.isSessionKnown = typed, true
		}
		screen.lastTurnNumber = max(screen.lastTurnNumber, typed.TurnCount)
		screen.history.Replace(0, screen.welcome())
		screen.commands = newSlashCommandRegistry(screen.sessionCommands, typed.SupportsPlanMode)
	case sessionfeed.SessionLoaded:
		if !screen.isSessionLoaded {
			screen.isSessionLoaded = true
			command = screen.releaseHeldMessages()
		}
	case sessionfeed.UserMessageRecorded:
		screen.printRecordedUserMessage(typed)
	case sessionfeed.UserMessageRejected:
		screen.isTurnStarting = false
		screen.printNotice(sessionfeed.NoticeSeverityError, "Not sent: "+typed.Reason)
	case sessionfeed.HistoryWindowChanged:
		screen.printHistoryWindow(typed)
	case sessionfeed.ImageAttached:
		screen.attachments = append(screen.attachments, typed.Attachment)
	case sessionfeed.SessionStartChanged:
		screen.isSessionStartWaiting = typed.IsWaiting
	case sessionfeed.SessionStartFinished:
		if screen.isSessionStartWaiting {
			// The user still waits for /new: switch now.
			screen.isSessionStartWaiting = false
			screen.printNotice(sessionfeed.NoticeSeverityInfo, "Started session "+typed.SessionID+"; switching to it.")
			command = screen.submitAction(sessionfeed.SwitchSession{SessionID: typed.SessionID})
		} else {
			screen.pendingSwitchSessionID = typed.SessionID
		}
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
		if typed.Sequence <= 0 || !screen.printedSequences[typed.Sequence] {
			screen.printedSequences[typed.Sequence] = typed.Sequence > 0
			screen.printReplyOutputs(screen.replies.RecordFinalMessage(typed))
		}
	case sessionfeed.ReasoningDelta:
		screen.appendReasoning(typed.Text)
	case sessionfeed.UsageRecorded:
		screen.totalTokens = typed.TotalTokens
	case sessionfeed.NoticeRaised:
		if shown, isShown := screen.notices.Admit(typed.Severity, typed.Text, screen.now()); isShown {
			screen.printNotice(typed.Severity, shown)
		}
	case sessionfeed.CancelRequestAnswered:
		screen.applyCancelAnswer(typed)
	case sessionfeed.WorkerStateChanged:
		screen.applyWorkerState(typed)
	case sessionfeed.ServerReachabilityChanged:
		screen.isServerReachable = typed.IsReachable
	case sessionfeed.QueuedMessagesChanged:
		screen.applyQueuedMessages(typed.Messages)
	case sessionfeed.PlanChanged:
		screen.plan = typed
	case sessionfeed.ToolCallChanged:
		screen.applyToolCall(typed)
	case sessionfeed.ApprovalRequested:
		screen.addInterrupt(pendingInterrupt{approval: &typed})
	case sessionfeed.QuestionAsked:
		screen.addInterrupt(pendingInterrupt{question: &typed})
	case sessionfeed.InterruptResolved:
		screen.resolveInterrupt(typed)
	}
	return tea.Batch(command, screen.startAnimation())
}

// printRecordedUserMessage prints a message once per stored sequence.
func (screen *ChatScreen) printRecordedUserMessage(recorded sessionfeed.UserMessageRecorded) {
	if recorded.Sequence > 0 {
		if screen.printedSequences[recorded.Sequence] {
			return
		}
		screen.printedSequences[recorded.Sequence] = true
	}
	screen.flushReplies()
	screen.printUserMessage(userMessageBlock{text: recorded.Text, origin: recorded.Origin, images: recorded.Images})
}

func (screen *ChatScreen) printUserMessage(block userMessageBlock) {
	screen.lastReplyText = ""
	screen.appendBlock(transcriptBlockUserMessage, block)
}

func (screen *ChatScreen) printHistoryWindow(window sessionfeed.HistoryWindowChanged) {
	if window.DeletedBeforeSequence > 0 {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, fmt.Sprintf("messages before #%d were removed by the retention window", window.DeletedBeforeSequence))
	}
	if window.EarlierBeforeSequence > 0 && window.EarlierBeforeSequence != window.DeletedBeforeSequence {
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "earlier messages are not shown")
	}
}

// applyQueuedMessages replaces the queue and cancels a pinned delete whose
// message left it.
func (screen *ChatScreen) applyQueuedMessages(messages []sessionfeed.QueuedMessage) {
	screen.queuedMessages = messages
	screen.localQueuedTexts = nil
	if screen.pendingDelete != nil && !containsQueuedMessage(messages, screen.pendingDelete.MessageID) {
		screen.pendingDelete = nil
		screen.printNotice(sessionfeed.NoticeSeverityInfo, "the queued message already left the queue; nothing was deleted")
	}
	if !screen.canActOnQueuedMessage() && screen.pendingFocus == pendingFocusQueuedMessage {
		screen.pendingFocus = pendingFocusNone
	}
}

func containsQueuedMessage(messages []sessionfeed.QueuedMessage, messageID string) bool {
	for _, message := range messages {
		if message.MessageID == messageID {
			return true
		}
	}
	return false
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
	screen.isTurnStarting = false
	screen.cancelState = cancelStateNone
	screen.cancelRequestedTurnID = ""
	screen.isExitArmed = false
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
		screen.appendBlock(transcriptBlockNotice, turnCancelledBlock{})
	case sessionfeed.TurnOutcomeFailed:
		screen.printNotice(sessionfeed.NoticeSeverityError, ended.FailureText)
	}
	if ended.TurnID == screen.turn.turnID {
		screen.isTurnRunning = false
		screen.cancelState = cancelStateNone
		screen.cancelRequestedTurnID = ""
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

// applyCancelAnswer applies the answer only to the request still waiting
// for it, in the turn that still runs.
func (screen *ChatScreen) applyCancelAnswer(answer sessionfeed.CancelRequestAnswered) {
	isCurrentRequest := screen.cancelState == cancelStateRequesting && screen.isTurnRunning &&
		answer.TurnID == screen.cancelRequestedTurnID && answer.TurnID == screen.turn.turnID
	if !isCurrentRequest {
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
	screen.exitReason = fmt.Sprintf("The Worker stopped (exit %d).", changed.ExitCode)
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
	screen.exitReason = "The command stopped."
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
		screen.isTurnStarting = false
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
			screen.lastReplyText = strings.TrimLeft(screen.lastReplyText+"\n\n"+output.text, "\n")
			screen.appendBlock(transcriptBlockReply, replyBlock{markdown: output.text})
		}
	}
}

func (screen *ChatScreen) printToolRow(call sessionfeed.ToolCallChanged) {
	screen.appendBlock(transcriptBlockToolRow, toolRowBlock{call: call, nameWidth: screen.turnToolNameWidth, printedAt: screen.now()})
}

func (screen *ChatScreen) printNotice(severity sessionfeed.NoticeSeverity, text string) {
	screen.appendBlock(transcriptBlockNotice, noticeBlock{severity: severity, text: text})
}
