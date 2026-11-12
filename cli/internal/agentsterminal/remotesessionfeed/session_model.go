// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
)

// sequenceEpochShift separates the stored sequences of switched sessions, so
// the screen prints each session's messages once.
const sequenceEpochShift = 40

// maxHeldUpdates bounds the live updates held for a turn the snapshot has
// not shown yet.
const maxHeldUpdates = 4096

// otherClientOrigin labels a user message this terminal did not send.
const otherClientOrigin = "another client"

// sessionModel turns snapshots and events into screen updates. Only the
// session loop goroutine uses it.
type sessionModel struct {
	feed      *RemoteSessionFeed
	sessionID string
	epoch     int64
	isResumed bool
	isLoaded  bool
	// isFirstApply marks the first snapshot, whose running turn started
	// before this chat, so its messages carry no origin.
	isFirstApply bool

	opened sessionfeed.SessionOpened
	// settlingTurnID is a turn the snapshot no longer runs that waits for
	// its end event; settlementInputs hold what arrived meanwhile.
	settlingTurnID   string
	settlementInputs []bufferedInput
	lastSeenSequence int64
	lastTurnNumber   int
	currentTurnID    string
	startedTurns     map[string]bool
	finishedTurns    map[string]bool
	turnEndDeadlines map[string]time.Time

	localSends []localSend
	// queuedBeforeChat are texts queued before this chat loaded; they
	// carry no origin.
	queuedBeforeChat    []string
	pendingInterrupts   map[string]dexagents.AgentInterrupt
	ownResolutions      map[string]sessionfeed.InterruptResolution
	resolvedByEvent     map[string]interruptResolvedPayload
	cancelAcceptedTurns map[string]bool
	endEvents           map[string]turnEndEvent

	liveToolCalls     map[string]sessionfeed.ToolCallChanged
	toolStartedAt     map[string]time.Time
	toolFinishedAt    map[string]time.Time
	toolEventStatus   map[string]string
	queuedMessages    []sessionfeed.QueuedMessage
	plan              sessionfeed.PlanChanged
	hasPlan           bool
	totalTokens       int64
	activity          sessionfeed.TurnActivity
	hasActivity       bool
	isFailureShown    bool
	isEventsAvailable bool

	heldUpdates map[string][]sessionfeed.SessionUpdate
	streams     map[streamKey]*streamedText
	streamOrder []streamKey
	matched     map[streamKey]bool
}

type localSend struct {
	text      string
	isPrinted bool
}

type streamKey struct {
	turnID          string
	stepExecutionID string
}

type streamedText struct {
	attempt int64
	text    string
}

type turnEndEvent struct {
	eventType string
	errorText string
}

type interruptResolvedPayload struct {
	Approved bool   `json:"approved"`
	Answered bool   `json:"answered"`
	Kind     string `json:"kind"`
}

func newSessionModel(feed *RemoteSessionFeed, sessionID string, epoch int64, isResumed bool) *sessionModel {
	return &sessionModel{
		feed:                feed,
		sessionID:           sessionID,
		epoch:               epoch,
		isResumed:           isResumed,
		startedTurns:        map[string]bool{},
		finishedTurns:       map[string]bool{},
		turnEndDeadlines:    map[string]time.Time{},
		pendingInterrupts:   map[string]dexagents.AgentInterrupt{},
		ownResolutions:      map[string]sessionfeed.InterruptResolution{},
		resolvedByEvent:     map[string]interruptResolvedPayload{},
		cancelAcceptedTurns: map[string]bool{},
		endEvents:           map[string]turnEndEvent{},
		liveToolCalls:       map[string]sessionfeed.ToolCallChanged{},
		toolStartedAt:       map[string]time.Time{},
		toolFinishedAt:      map[string]time.Time{},
		toolEventStatus:     map[string]string{},
		heldUpdates:         map[string][]sessionfeed.SessionUpdate{},
		streams:             map[streamKey]*streamedText{},
		matched:             map[streamKey]bool{},
		isEventsAvailable:   true,
	}
}

func (model *sessionModel) emit(updates ...sessionfeed.SessionUpdate) {
	model.feed.emit(updates...)
}

func (model *sessionModel) screenSequence(sequence int64) int64 {
	if sequence <= 0 {
		return 0
	}
	return model.epoch<<sequenceEpochShift + sequence
}

func (model *sessionModel) emitOpened() {
	launch := model.feed.launch
	model.opened = sessionfeed.SessionOpened{
		Command:            launch.Command.Harness,
		SessionID:          model.sessionID,
		IsNewSession:       !model.isResumed,
		AcceptsTurnControl: launch.Capabilities.HasTurnControl,
		SupportsPlanMode:   launch.Capabilities.IsPlanMode,
		Workspace:          launch.Display.Workspace,
		WebURL:             launch.WebURL(),
	}
	if launch.Display.Model != nil {
		model.opened.Model = *launch.Display.Model
	}
	model.emit(model.opened)
}

// isBusy reports a session that runs or will soon run a turn.
func (model *sessionModel) isBusy() bool {
	return model.currentTurnID != "" || len(model.pendingInterrupts) > 0 || len(model.queuedMessages) > 0 ||
		len(model.turnEndDeadlines) > 0 || len(model.localSends) > 0
}

// recordLocalSend notes a message this terminal sends. The screen printed it
// when no turn runs or starts, as the screen decides.
func (model *sessionModel) recordLocalSend(text string) {
	isPrinted := model.currentTurnID == ""
	for _, send := range model.localSends {
		if send.isPrinted {
			isPrinted = false
		}
	}
	model.localSends = append(model.localSends, localSend{text: text, isPrinted: isPrinted})
}

func (model *sessionModel) forgetLocalSend(text string) {
	for index := len(model.localSends) - 1; index >= 0; index-- {
		if model.localSends[index].text == text {
			model.localSends = append(model.localSends[:index], model.localSends[index+1:]...)
			return
		}
	}
}

// takeLocalSend removes the local send of text and reports whether one
// existed and whether the screen printed it.
func (model *sessionModel) takeLocalSend(text string) (bool, bool) {
	for index, send := range model.localSends {
		if strings.TrimSpace(send.text) == strings.TrimSpace(text) {
			model.localSends = append(model.localSends[:index], model.localSends[index+1:]...)
			return true, send.isPrinted
		}
	}
	return false, false
}

// emitHistory shows the newest stored page of a resumed session, without the
// running turn, which the live path shows.
func (model *sessionModel) emitHistory(page dexagents.AgentMessagesPage, activeTurnID *string, cancelledTurns map[string]bool) {
	window := sessionfeed.HistoryWindowChanged{}
	if page.NextBeforeSequence != nil {
		window.EarlierBeforeSequence = *page.NextBeforeSequence
	}
	if page.DeletedBeforeSequence != nil {
		window.DeletedBeforeSequence = *page.DeletedBeforeSequence
	}
	if window.EarlierBeforeSequence > 0 || window.DeletedBeforeSequence > 0 {
		model.emit(window)
	}
	turnInProgress := ""
	for _, message := range page.Messages {
		if activeTurnID != nil && message.TurnID == *activeTurnID {
			continue
		}
		if message.TurnID != turnInProgress {
			model.endHistoryTurn(turnInProgress, cancelledTurns[turnInProgress])
			turnInProgress = message.TurnID
		}
		model.emitStoredMessage(message, true)
		model.lastSeenSequence = max(model.lastSeenSequence, message.Sequence)
	}
	model.endHistoryTurn(turnInProgress, cancelledTurns[turnInProgress])
}

func (model *sessionModel) endHistoryTurn(turnID string, isCancelled bool) {
	if turnID == "" {
		return
	}
	model.finishedTurns[turnID] = true
	model.lastTurnNumber = max(model.lastTurnNumber, turnNumberOf(turnID, model.lastTurnNumber))
	// Without an end event the turn shows no marker, which is all Completed shows.
	ended := sessionfeed.TurnEnded{TurnID: turnID, Outcome: sessionfeed.TurnOutcomeCompleted}
	if isCancelled {
		ended.Outcome = sessionfeed.TurnOutcomeCancelled
	}
	model.emit(ended)
}

// takeQueuedBeforeChat removes text from the messages queued before this
// chat and reports whether it was one.
func (model *sessionModel) takeQueuedBeforeChat(text string) bool {
	for index, queued := range model.queuedBeforeChat {
		if strings.TrimSpace(queued) == strings.TrimSpace(text) {
			model.queuedBeforeChat = append(model.queuedBeforeChat[:index], model.queuedBeforeChat[index+1:]...)
			return true
		}
	}
	return false
}

// emitStoredMessage shows one stored message. User messages of history carry
// no origin; tool messages become neutral rows.
func (model *sessionModel) emitStoredMessage(message dexagents.AgentMessage, isHistory bool) {
	text := ""
	if message.Text != nil {
		text = *message.Text
	}
	switch message.Role {
	case dexagents.MessageRoleUser:
		if message.Origin != nil {
			model.emitSessionWrittenMessage(*message.Origin, text, message.Sequence)
			return
		}
		recorded := sessionfeed.UserMessageRecorded{Text: text, TurnID: message.TurnID, Sequence: model.screenSequence(message.Sequence), Images: storedImages(message.Images)}
		if !isHistory && !model.isFirstApply {
			if isLocal, isPrinted := model.takeLocalSend(text); isPrinted {
				return
			} else if !isLocal && !model.takeQueuedBeforeChat(text) {
				recorded.Origin = otherClientOrigin
			}
		}
		model.emit(recorded)
	case dexagents.MessageRoleAssistant:
		if strings.TrimSpace(text) == "" {
			return
		}
		stepExecutionID := "record-" + strconv.FormatInt(message.Sequence, 10)
		if !isHistory {
			stepExecutionID = model.matchStream(message.TurnID, text, stepExecutionID)
		}
		model.emit(sessionfeed.AssistantMessageRecorded{TurnID: message.TurnID, StepExecutionID: stepExecutionID, Text: text, Sequence: model.screenSequence(message.Sequence)})
	case dexagents.MessageRoleTool:
		if message.ToolCallID == nil {
			return
		}
		if previous, isLive := model.liveToolCalls[*message.ToolCallID]; isLive && previous.Status.IsFinished() {
			return
		}
		model.emit(historyToolRow(message))
	}
}

// planExecutionRevisionPattern reads the revision from the text of
// buildPlanExecutionMessage, which the agents conformance texts fix.
var planExecutionRevisionPattern = regexp.MustCompile(`^\[Dex\] The user approved plan revision (\d+)\.`)

// emitSessionWrittenMessage shows a user message the session wrote itself.
// A plan execution becomes one notice line. Notes for the model and origins
// this release does not know show nothing: the turn's outcome already shows.
func (model *sessionModel) emitSessionWrittenMessage(origin dexagents.AgentMessageOrigin, text string, sequence int64) {
	if origin != dexagents.AgentMessageOriginPlanExecution {
		return
	}
	notice := "Running the approved plan"
	if match := planExecutionRevisionPattern.FindStringSubmatch(text); match != nil {
		notice = "Running plan revision " + match[1]
	}
	// The sequence keeps a repeated execution of one revision from being hidden as a repeat.
	model.emit(sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: notice, Sequence: model.screenSequence(sequence)})
}

// matchStream returns the streamed Step whose text the final message
// continues, else the first unmatched Step of the turn, else fallback.
func (model *sessionModel) matchStream(turnID string, text string, fallback string) string {
	var firstUnmatched *streamKey
	for index := range model.streamOrder {
		key := model.streamOrder[index]
		if key.turnID != turnID || model.matched[key] {
			continue
		}
		streamed := strings.TrimSpace(model.streams[key].text)
		if strings.HasPrefix(strings.TrimSpace(text), streamed) {
			model.matched[key] = true
			return key.stepExecutionID
		}
		if firstUnmatched == nil {
			firstUnmatched = &key
		}
	}
	if firstUnmatched != nil {
		model.matched[*firstUnmatched] = true
		return firstUnmatched.stepExecutionID
	}
	return fallback
}

// applySnapshot reconciles the screen with one snapshot. state is not nil.
func (model *sessionModel) applySnapshot(snapshot dexagents.AgentSnapshot) {
	if model.settlingTurnID != "" {
		model.bufferWhileSettling(bufferedInput{snapshot: &snapshot})
		model.replayWhenSettled()
		return
	}
	model.applySnapshotNow(snapshot)
}

func (model *sessionModel) applySnapshotNow(snapshot dexagents.AgentSnapshot) {
	state := snapshot.State
	activeTurnID := ""
	if state.ActiveTurnID != nil {
		activeTurnID = *state.ActiveTurnID
	}
	if !model.isLoaded {
		model.isLoaded = true
		model.isFirstApply = true
		for _, queued := range snapshot.QueuedMessages {
			model.queuedBeforeChat = append(model.queuedBeforeChat, queued.Message.Text)
		}
		defer func() { model.isFirstApply = false }()
		if model.isResumed {
			// The history page showed the finished turns.
			model.skipFinishedTurns(snapshot, activeTurnID)
		}
	}
	isSettled := model.applyNewMessages(snapshot, activeTurnID)
	if model.currentTurnID != "" && model.currentTurnID != activeTurnID {
		model.settleTurn(model.currentTurnID, snapshot)
	}
	if model.settlingTurnID != "" {
		// The previous turn waits for its end event: nothing else of this
		// snapshot shows yet. It replays whole once the turn settled.
		model.bufferWhileSettling(bufferedInput{snapshot: &snapshot})
		return
	}
	if isSettled && activeTurnID != "" && !model.finishedTurns[activeTurnID] && !model.startedTurns[activeTurnID] {
		model.startTurn(activeTurnID, nil)
	}
	if model.currentTurnID != "" && model.currentTurnID == activeTurnID {
		model.applyToolCalls(snapshot)
	}
	model.applyInterrupts(snapshot)
	model.applyActivity(snapshot)
	model.applyQueue(snapshot)
	model.applyPlan(snapshot)
	model.applyUsage(snapshot.Usage)
	model.applyOpened(snapshot)
	if state.Status == dexagents.AgentSessionStatusFailed && !model.isFailureShown {
		model.isFailureShown = true
		model.feed.markFailure()
		reason := "unknown reason"
		if state.LastTurnError != nil {
			reason = *state.LastTurnError
		}
		model.feed.notice(sessionfeed.NoticeSeverityError, "The session failed: "+reason+". Start a new session with /new or exit.")
	}
}

// skipFinishedTurns marks the finished turns of a resumed session's first
// snapshot as shown, because the history page showed them.
func (model *sessionModel) skipFinishedTurns(snapshot dexagents.AgentSnapshot, activeTurnID string) {
	for _, message := range snapshot.Messages {
		if message.TurnID != activeTurnID {
			model.lastSeenSequence = max(model.lastSeenSequence, message.Sequence)
			model.finishedTurns[message.TurnID] = true
			model.lastTurnNumber = max(model.lastTurnNumber, turnNumberOf(message.TurnID, model.lastTurnNumber))
		}
	}
}

// applyNewMessages shows stored messages after the last one shown, turn by
// turn. A turn that started and ended between two snapshots shows here. It
// stops before the first message of a later turn while the current turn waits
// for its end event, and reports whether it got through.
func (model *sessionModel) applyNewMessages(snapshot dexagents.AgentSnapshot, activeTurnID string) bool {
	messages := append([]dexagents.AgentMessage(nil), snapshot.Messages...)
	sort.Slice(messages, func(left, right int) bool { return messages[left].Sequence < messages[right].Sequence })
	for index, message := range messages {
		if message.Sequence <= model.lastSeenSequence {
			continue
		}
		turnID := message.TurnID
		if model.finishedTurns[turnID] {
			model.lastSeenSequence = message.Sequence
			continue
		}
		if model.currentTurnID != "" && model.currentTurnID != turnID && !model.settleTurn(model.currentTurnID, snapshot) {
			return false
		}
		model.lastSeenSequence = message.Sequence
		if !model.startedTurns[turnID] {
			model.startTurn(turnID, userMessagesOfTurn(messages[index:], turnID, message.Sequence))
			if message.Role == dexagents.MessageRoleUser {
				continue
			}
		}
		if message.Role == dexagents.MessageRoleTool && turnID == activeTurnID {
			continue
		}
		model.emitStoredMessage(message, false)
	}
	return true
}

// userMessagesOfTurn returns the user messages that start a turn: the ones
// at its beginning.
func userMessagesOfTurn(messages []dexagents.AgentMessage, turnID string, firstSequence int64) []dexagents.AgentMessage {
	var starters []dexagents.AgentMessage
	for _, message := range messages {
		if message.Sequence < firstSequence || message.TurnID != turnID {
			continue
		}
		if message.Role != dexagents.MessageRoleUser {
			break
		}
		starters = append(starters, message)
	}
	return starters
}

func (model *sessionModel) startTurn(turnID string, userMessages []dexagents.AgentMessage) {
	if model.startedTurns[turnID] {
		return
	}
	model.startedTurns[turnID] = true
	for _, message := range userMessages {
		model.lastSeenSequence = max(model.lastSeenSequence, message.Sequence)
		model.emitStoredMessage(message, false)
	}
	number := turnNumberOf(turnID, model.lastTurnNumber)
	model.lastTurnNumber = max(model.lastTurnNumber, number)
	started := sessionfeed.TurnStarted{TurnID: turnID, TurnNumber: number, StartedAt: model.feed.now()}
	if len(userMessages) > 0 {
		if createdAt, err := time.Parse(time.RFC3339Nano, userMessages[0].CreatedAt); err == nil {
			started.StartedAt = createdAt
		}
	}
	model.currentTurnID = turnID
	model.hasActivity = false
	model.opened.IsNewSession = false
	model.emit(started)
	held := model.heldUpdates[turnID]
	delete(model.heldUpdates, turnID)
	model.emit(held...)
}

// settleTurn finishes a turn the snapshot no longer runs once its end event
// arrived, after a short wait for it, at once without live events, or in the
// first snapshot. It reports whether the turn is finished; until then the
// next turn waits, so a late end event still decides this turn's outcome.
func (model *sessionModel) settleTurn(turnID string, snapshot dexagents.AgentSnapshot) bool {
	if model.finishedTurns[turnID] {
		return true
	}
	_, hasEndEvent := model.endEvents[turnID]
	if hasEndEvent || !model.isEventsAvailable || model.isFirstApply {
		model.finishTurn(turnID, snapshot)
		return true
	}
	deadline, isWaiting := model.turnEndDeadlines[turnID]
	if !isWaiting {
		model.turnEndDeadlines[turnID] = model.feed.now().Add(model.feed.timings.TurnEndEventWait)
		model.settlingTurnID = turnID
		return false
	}
	if model.feed.now().Before(deadline) {
		model.settlingTurnID = turnID
		return false
	}
	model.finishTurn(turnID, snapshot)
	return true
}

func (model *sessionModel) nextTurnEndDeadline(now time.Time) (time.Duration, bool) {
	var earliest time.Duration
	hasDeadline := false
	for _, deadline := range model.turnEndDeadlines {
		wait := max(deadline.Sub(now), 0)
		if !hasDeadline || wait < earliest {
			earliest, hasDeadline = wait, true
		}
	}
	return earliest, hasDeadline
}

// finishTurn ends a turn: its unfinished tool rows, then TurnEnded.
func (model *sessionModel) finishTurn(turnID string, snapshot dexagents.AgentSnapshot) {
	if model.finishedTurns[turnID] {
		return
	}
	model.finishedTurns[turnID] = true
	delete(model.turnEndDeadlines, turnID)
	if model.settlingTurnID == turnID {
		model.settlingTurnID = ""
	}
	delete(model.heldUpdates, turnID)
	outcome, failureText := model.turnOutcome(turnID, snapshot)
	model.finishToolRows(turnID, snapshot, outcome)
	ended := sessionfeed.TurnEnded{TurnID: turnID, Outcome: outcome}
	if outcome == sessionfeed.TurnOutcomeFailed {
		model.feed.markFailure()
		ended.FailureText = "The turn failed: " + failureText + ". Send a message to try again."
	}
	if model.currentTurnID == turnID {
		model.currentTurnID = ""
		model.hasActivity = false
	}
	model.emit(ended)
}

func (model *sessionModel) turnOutcome(turnID string, snapshot dexagents.AgentSnapshot) (sessionfeed.TurnOutcome, string) {
	// last_turn_error belongs to the last finished turn, so it speaks for
	// this turn only when this turn is that one.
	var lastTurnError *string
	if snapshot.State != nil && snapshot.State.TurnCount == int64(turnNumberOf(turnID, 0)) {
		lastTurnError = snapshot.State.LastTurnError
	}
	if event, hasEvent := model.endEvents[turnID]; hasEvent {
		switch event.eventType {
		case "turn_cancelled":
			return sessionfeed.TurnOutcomeCancelled, ""
		case "turn_failed":
			reason := event.errorText
			if reason == "" && lastTurnError != nil {
				reason = *lastTurnError
			}
			if reason == "" {
				reason = "unknown reason"
			}
			return sessionfeed.TurnOutcomeFailed, reason
		}
		return sessionfeed.TurnOutcomeCompleted, ""
	}
	if model.cancelAcceptedTurns[turnID] {
		return sessionfeed.TurnOutcomeCancelled, ""
	}
	if lastTurnError != nil {
		return sessionfeed.TurnOutcomeFailed, *lastTurnError
	}
	return sessionfeed.TurnOutcomeCompleted, ""
}

// finishToolRows gives every live row of turnID a final state, from the
// tool's events, its stored message, or the turn outcome.
func (model *sessionModel) finishToolRows(turnID string, snapshot dexagents.AgentSnapshot, outcome sessionfeed.TurnOutcome) {
	storedTools := map[string]dexagents.AgentMessage{}
	for _, message := range snapshot.Messages {
		if message.Role == dexagents.MessageRoleTool && message.ToolCallID != nil && message.TurnID == turnID {
			storedTools[*message.ToolCallID] = message
		}
	}
	for _, toolCallID := range sortedToolCallIDs(model.liveToolCalls) {
		row := model.liveToolCalls[toolCallID]
		if row.Status.IsFinished() {
			continue
		}
		if status, hasStatus := model.toolEventStatus[toolCallID]; hasStatus {
			row.Status = toolCallStatus(dexagents.AgentToolCall{Status: dexagents.ToolCallStatus(status)})
		}
		if !row.Status.IsFinished() {
			if stored, isStored := storedTools[toolCallID]; isStored {
				row = historyToolRow(stored)
			} else if outcome == sessionfeed.TurnOutcomeCancelled {
				row.Status = sessionfeed.ToolCallCancelled
			} else {
				row.Status = sessionfeed.ToolCallOutcomeUnknown
			}
		}
		model.liveToolCalls[toolCallID] = row
		model.emit(row)
	}
	for toolCallID, row := range model.liveToolCalls {
		if row.Status.IsFinished() {
			delete(model.liveToolCalls, toolCallID)
		}
	}
}

func (model *sessionModel) applyToolCalls(snapshot dexagents.AgentSnapshot) {
	for _, call := range snapshot.ToolCalls {
		if call.TurnID != model.currentTurnID || model.currentTurnID == "" {
			continue
		}
		row := liveToolRow(call)
		if startedAt, isKnown := model.toolStartedAt[call.ToolCallID]; isKnown {
			row.StartedAt = startedAt
			if finishedAt, isFinished := model.toolFinishedAt[call.ToolCallID]; isFinished && row.Status.IsFinished() {
				row.Duration = finishedAt.Sub(startedAt)
			}
		}
		if previous, exists := model.liveToolCalls[call.ToolCallID]; exists {
			if row.StartedAt.IsZero() {
				row.StartedAt = previous.StartedAt
			}
			if reflect.DeepEqual(previous, row) {
				continue
			}
		}
		model.liveToolCalls[call.ToolCallID] = row
		model.emit(row)
	}
}

func (model *sessionModel) applyInterrupts(snapshot dexagents.AgentSnapshot) {
	current := map[string]bool{}
	for _, interrupt := range snapshot.PendingInterrupts {
		current[interrupt.InterruptID] = true
		if _, isKnown := model.pendingInterrupts[interrupt.InterruptID]; isKnown {
			continue
		}
		model.pendingInterrupts[interrupt.InterruptID] = interrupt
		model.emit(interruptUpdate(interrupt, snapshot.ToolCalls))
	}
	for _, interruptID := range sortedInterruptIDs(model.pendingInterrupts) {
		if current[interruptID] {
			continue
		}
		interrupt := model.pendingInterrupts[interruptID]
		delete(model.pendingInterrupts, interruptID)
		resolved := sessionfeed.InterruptResolved{InterruptID: interruptID}
		if own, isOwn := model.ownResolutions[interruptID]; isOwn {
			resolved.Resolution = own
			delete(model.ownResolutions, interruptID)
		} else {
			resolved.Resolution = model.foreignResolution(interrupt, snapshot.ToolCalls)
			if resolved.Resolution != sessionfeed.InterruptWithdrawn {
				resolved.ResolvedBy = otherClientOrigin
			}
		}
		model.emit(resolved)
	}
}

func (model *sessionModel) foreignResolution(interrupt dexagents.AgentInterrupt, calls []dexagents.AgentToolCall) sessionfeed.InterruptResolution {
	isQuestion := interrupt.Kind == dexagents.InterruptKindQuestion
	if payload, hasEvent := model.resolvedByEvent[interrupt.InterruptID]; hasEvent {
		switch {
		case isQuestion && payload.Answered:
			return sessionfeed.InterruptAnswered
		case isQuestion:
			return sessionfeed.InterruptDeclined
		case payload.Approved:
			return sessionfeed.InterruptApproved
		default:
			return sessionfeed.InterruptRejected
		}
	}
	for _, call := range calls {
		if call.ToolCallID != interrupt.ToolCallID {
			continue
		}
		if call.CancellationReason != nil {
			return sessionfeed.InterruptWithdrawn
		}
		switch call.Status {
		case dexagents.ToolCallStatusRejected:
			if isQuestion {
				return sessionfeed.InterruptDeclined
			}
			return sessionfeed.InterruptRejected
		case dexagents.ToolCallStatusAwaitingApproval, dexagents.ToolCallStatusAwaitingAnswer:
			return sessionfeed.InterruptWithdrawn
		default:
			if isQuestion {
				return sessionfeed.InterruptAnswered
			}
			return sessionfeed.InterruptApproved
		}
	}
	return sessionfeed.InterruptWithdrawn
}

func (model *sessionModel) applyActivity(snapshot dexagents.AgentSnapshot) {
	if model.currentTurnID == "" {
		return
	}
	activity := sessionfeed.TurnActivityRunningModel
	switch {
	case len(snapshot.PendingInterrupts) > 0:
		activity = sessionfeed.TurnActivityAwaitingUser
	case snapshot.State.Status == dexagents.AgentSessionStatusRunningTools:
		activity = sessionfeed.TurnActivityRunningTools
	case snapshot.State.Status == dexagents.AgentSessionStatusCompactingContext:
		activity = sessionfeed.TurnActivityCompacting
	}
	if model.hasActivity && activity == model.activity {
		return
	}
	model.activity, model.hasActivity = activity, true
	model.emit(sessionfeed.TurnActivityChanged{TurnID: model.currentTurnID, Activity: activity})
}

func (model *sessionModel) applyQueue(snapshot dexagents.AgentSnapshot) {
	messages := make([]sessionfeed.QueuedMessage, 0, len(snapshot.QueuedMessages))
	for _, queued := range snapshot.QueuedMessages {
		messages = append(messages, sessionfeed.QueuedMessage{MessageID: queued.MessageID, Text: queued.Message.Text})
	}
	if reflect.DeepEqual(messages, model.queuedMessages) || (len(messages) == 0 && len(model.queuedMessages) == 0) {
		return
	}
	model.queuedMessages = messages
	model.emit(sessionfeed.QueuedMessagesChanged{Messages: messages})
}

func (model *sessionModel) applyPlan(snapshot dexagents.AgentSnapshot) {
	if snapshot.Plan == nil {
		if model.hasPlan {
			model.hasPlan = false
			model.plan = sessionfeed.PlanChanged{Status: sessionfeed.PlanStatusCleared}
			model.emit(model.plan)
		}
		return
	}
	plan := sessionfeed.PlanChanged{
		Revision:            snapshot.Plan.Revision,
		Status:              planStatus(snapshot.Plan.Status),
		IsInPlanInteraction: snapshot.State.InteractionMode == dexagents.InteractionModePlanning,
	}
	for _, task := range snapshot.Plan.Tasks {
		plan.Items = append(plan.Items, sessionfeed.PlanItem{Title: task.Content, Status: planItemStatus(task.Status)})
	}
	if model.hasPlan && reflect.DeepEqual(plan, model.plan) {
		return
	}
	model.plan, model.hasPlan = plan, true
	model.emit(plan)
}

func (model *sessionModel) applyUsage(usage dexagents.AgentUsage) {
	total := usage.InputTokens + usage.OutputTokens
	if total == model.totalTokens {
		return
	}
	model.totalTokens = total
	model.emit(sessionfeed.UsageRecorded{TotalTokens: total})
}

func (model *sessionModel) applyOpened(snapshot dexagents.AgentSnapshot) {
	opened := model.opened
	opened.TurnCount = int(snapshot.State.TurnCount)
	opened.AcceptsTurnControl = model.feed.launch.Capabilities.HasTurnControl && snapshot.State.AcceptsTurnControl
	opened.IsNewSession = snapshot.State.TurnCount == 0 && snapshot.State.ActiveTurnID == nil && len(snapshot.Messages) == 0
	if configured, isString := snapshot.SessionSettings["model"].(string); isString && configured != "" {
		opened.Model = configured
	}
	if reflect.DeepEqual(opened, model.opened) {
		return
	}
	model.opened = opened
	model.emit(opened)
}

// applyEvent applies one live event and reports whether the snapshot should
// be read now.
// applyEvent applies a live event and reports whether the snapshot should be
// read now. While a finished turn waits for its end event, events of other
// turns are buffered with the snapshots, in arrival order.
func (model *sessionModel) applyEvent(event dexagents.AgentEvent) bool {
	if model.settlingTurnID != "" && (event.TurnID == nil || *event.TurnID != model.settlingTurnID) {
		model.bufferWhileSettling(bufferedInput{event: &event})
		return false
	}
	isRefreshNeeded := model.applyEventNow(event)
	model.replayWhenSettled()
	return isRefreshNeeded
}

func (model *sessionModel) applyEventNow(event dexagents.AgentEvent) bool {
	turnID := ""
	if event.TurnID != nil {
		turnID = *event.TurnID
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		payload = map[string]any{}
	}
	switch event.Type {
	case "text_delta":
		text, _ := payload["text"].(string)
		if text == "" || turnID == "" || model.finishedTurns[turnID] {
			return false
		}
		if !model.recordStreamedText(turnID, event.StepExecutionID, event.Attempt, text) {
			return false
		}
		return model.emitLive(turnID, sessionfeed.AssistantTextDelta{TurnID: turnID, StepExecutionID: event.StepExecutionID, Attempt: int(event.Attempt), Text: text})
	case "reasoning_delta":
		text, _ := payload["text"].(string)
		if text == "" || turnID == "" || model.finishedTurns[turnID] {
			return false
		}
		return model.emitLive(turnID, sessionfeed.ReasoningDelta{TurnID: turnID, Text: text})
	case "usage_recorded":
		if session, isObject := payload["session"].(map[string]any); isObject {
			model.applyUsage(dexagents.AgentUsage{InputTokens: jsonInt64(session["input_tokens"]), OutputTokens: jsonInt64(session["output_tokens"])})
		}
		return false
	case "tool_call_started":
		if toolCallID, isString := payload["tool_call_id"].(string); isString {
			model.toolStartedAt[toolCallID] = model.feed.now()
		}
		return true
	case "tool_call_finished":
		if toolCallID, isString := payload["tool_call_id"].(string); isString {
			model.toolFinishedAt[toolCallID] = model.feed.now()
			if status, isStatus := payload["status"].(string); isStatus {
				model.toolEventStatus[toolCallID] = status
			}
		}
		return true
	case "interrupt_resolved":
		if interruptID, isString := payload["interrupt_id"].(string); isString {
			var resolved interruptResolvedPayload
			if err := json.Unmarshal([]byte(event.Payload), &resolved); err == nil {
				model.resolvedByEvent[interruptID] = resolved
			}
		}
		return true
	case "compaction_started":
		if turnID != "" && turnID == model.currentTurnID {
			model.activity, model.hasActivity = sessionfeed.TurnActivityCompacting, true
			model.emit(sessionfeed.TurnActivityChanged{TurnID: turnID, Activity: sessionfeed.TurnActivityCompacting})
		}
		return true
	case "turn_completed", "turn_failed", "turn_cancelled", "turn_steered":
		if turnID != "" {
			errorText, _ := payload["error"].(string)
			model.endEvents[turnID] = turnEndEvent{eventType: event.Type, errorText: errorText}
		}
		return true
	}
	return true
}

// recordStreamedText keeps the text of the highest attempt of each Step and
// reports whether the delta belongs to it.
func (model *sessionModel) recordStreamedText(turnID string, stepExecutionID string, attempt int64, text string) bool {
	key := streamKey{turnID: turnID, stepExecutionID: stepExecutionID}
	streamed, exists := model.streams[key]
	switch {
	case !exists:
		model.streams[key] = &streamedText{attempt: attempt, text: text}
		model.streamOrder = append(model.streamOrder, key)
	case attempt < streamed.attempt:
		return false
	case attempt > streamed.attempt:
		streamed.attempt, streamed.text = attempt, text
	default:
		streamed.text += text
	}
	return true
}

// emitLive shows a live update, or holds it until the snapshot starts its
// turn. It reports whether the snapshot should be read now.
func (model *sessionModel) emitLive(turnID string, update sessionfeed.SessionUpdate) bool {
	if model.startedTurns[turnID] {
		model.emit(update)
		return false
	}
	held := model.heldUpdates[turnID]
	if len(held) < maxHeldUpdates {
		model.heldUpdates[turnID] = append(held, update)
	}
	return len(held) == 0
}

// leave ends what the screen shows of this session before a switch.
func (model *sessionModel) leave() {
	if model.currentTurnID != "" {
		model.emit(sessionfeed.TurnEnded{TurnID: model.currentTurnID, Outcome: sessionfeed.TurnOutcomeCompleted})
		model.currentTurnID = ""
	}
	for _, interruptID := range sortedInterruptIDs(model.pendingInterrupts) {
		model.emit(sessionfeed.InterruptResolved{InterruptID: interruptID, Resolution: sessionfeed.InterruptWithdrawn})
	}
	if len(model.queuedMessages) > 0 {
		model.emit(sessionfeed.QueuedMessagesChanged{})
	}
	if model.hasPlan {
		model.emit(sessionfeed.PlanChanged{Status: sessionfeed.PlanStatusCleared})
	}
}

// turnNumberOf reads n from "turn-<n>", else returns one more than previous.
func turnNumberOf(turnID string, previous int) int {
	if number, err := strconv.Atoi(strings.TrimPrefix(turnID, "turn-")); err == nil && strings.HasPrefix(turnID, "turn-") {
		return number
	}
	return previous + 1
}

func jsonInt64(value any) int64 {
	if number, isNumber := value.(float64); isNumber {
		return int64(number)
	}
	return 0
}

func sortedToolCallIDs(rows map[string]sessionfeed.ToolCallChanged) []string {
	ids := make([]string, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedInterruptIDs(interrupts map[string]dexagents.AgentInterrupt) []string {
	ids := make([]string, 0, len(interrupts))
	for id := range interrupts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func storedImages(images []dexagents.AgentImageReference) []sessionfeed.ImageAttachment {
	var attachments []sessionfeed.ImageAttachment
	for _, image := range images {
		reference, err := json.Marshal(image)
		if err != nil {
			// A decoded reference always encodes; skip one that does not.
			continue
		}
		attachments = append(attachments, sessionfeed.ImageAttachment{MimeType: image.MimeType, SizeBytes: image.SizeBytes, Reference: reference})
	}
	return attachments
}
