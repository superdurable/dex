// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
	"google.golang.org/grpc/codes"
)

// historyPageSize is how many stored messages a resumed chat shows.
const historyPageSize = 50

// loopInput is one input of the session loop, which owns the session model.
type loopInput interface {
	isLoopInput()
}

type snapshotFetched struct {
	epoch    int64
	snapshot dexagents.AgentSnapshot
	history  *dexagents.AgentMessagesPage
	// cancelledTurns are the history's turns that recent events show as
	// cancelled; the stored messages do not say.
	cancelledTurns map[string]bool
	err            error
}

type eventReceived struct {
	epoch int64
	event dexagents.AgentEvent
}

type eventsUnavailable struct {
	epoch  int64
	reason string
}

// localSendStarted asks whether the screen printed a message it sends now.
type localSendStarted struct {
	text  string
	reply chan struct{}
}

type localSendRejected struct {
	text string
}

type interruptResolving struct {
	interruptID string
	resolution  sessionfeed.InterruptResolution
	isForgotten bool
}

type cancelAccepted struct {
	turnID string
}

type refreshRequested struct{}

type sessionSwitchRequested struct {
	sessionID string
}

func (snapshotFetched) isLoopInput()        {}
func (eventReceived) isLoopInput()          {}
func (eventsUnavailable) isLoopInput()      {}
func (localSendStarted) isLoopInput()       {}
func (localSendRejected) isLoopInput()      {}
func (interruptResolving) isLoopInput()     {}
func (cancelAccepted) isLoopInput()         {}
func (refreshRequested) isLoopInput()       {}
func (sessionSwitchRequested) isLoopInput() {}

// sessionLoop owns the session model and the snapshot schedule.
type sessionLoop struct {
	feed  *RemoteSessionFeed
	model *sessionModel

	ctx               context.Context
	stopEvents        context.CancelFunc
	isFetching        bool
	isRefreshAgain    bool
	refreshTimer      *time.Timer
	loadFailures      int
	isLoadNoticeShown bool
	isUnreachable     bool
}

func newSessionLoop(feed *RemoteSessionFeed) *sessionLoop {
	return &sessionLoop{feed: feed}
}

func (loop *sessionLoop) run(ctx context.Context) error {
	loop.ctx = ctx
	loop.refreshTimer = time.NewTimer(0)
	defer loop.refreshTimer.Stop()
	loop.openSession(loop.feed.SessionID(), 0, loop.feed.launch.Session.IsResumed)
	for _, notice := range loop.feed.launch.Notices {
		loop.feed.notice(noticeSeverity(notice.Level), notice.Text)
	}
	defer func() {
		if loop.stopEvents != nil {
			loop.stopEvents()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-loop.refreshTimer.C:
			loop.startFetch()
		case input := <-loop.feed.inputs:
			loop.handleInput(input)
		}
	}
}

// openSession starts following sessionID as a new model epoch.
func (loop *sessionLoop) openSession(sessionID string, epoch int64, isResumed bool) {
	if loop.stopEvents != nil {
		loop.stopEvents()
	}
	loop.model = newSessionModel(loop.feed, sessionID, epoch, isResumed)
	loop.model.emitOpened()
	eventsContext, stopEvents := context.WithCancel(loop.ctx)
	loop.stopEvents = stopEvents
	go followAgentEvents(eventsContext, loop.feed, sessionID, epoch)
	loop.isFetching = false
	loop.loadFailures = 0
	loop.isLoadNoticeShown = false
	loop.scheduleRefresh(0)
}

func (loop *sessionLoop) handleInput(input loopInput) {
	model := loop.model
	switch typed := input.(type) {
	case snapshotFetched:
		if typed.epoch != model.epoch {
			return
		}
		loop.isFetching = false
		loop.applyFetch(typed)
	case eventReceived:
		if typed.epoch == model.epoch && model.applyEvent(typed.event) {
			loop.scheduleRefresh(0)
		}
	case eventsUnavailable:
		if typed.epoch == model.epoch {
			model.isEventsAvailable = false
			model.trySettle()
			loop.feed.notice(sessionfeed.NoticeSeverityWarning, "Live output is off: "+typed.reason+". The chat updates from the session record.")
		}
	case localSendStarted:
		model.recordLocalSend(typed.text)
		close(typed.reply)
	case localSendRejected:
		model.forgetLocalSend(typed.text)
	case interruptResolving:
		if typed.isForgotten {
			delete(model.ownResolutions, typed.interruptID)
		} else {
			model.ownResolutions[typed.interruptID] = typed.resolution
		}
		loop.scheduleRefresh(0)
	case cancelAccepted:
		model.cancelAcceptedTurns[typed.turnID] = true
		loop.scheduleRefresh(0)
	case refreshRequested:
		loop.scheduleRefresh(0)
	case sessionSwitchRequested:
		model.leave()
		loop.feed.stateMutex.Lock()
		loop.feed.sessionID = typed.sessionID
		loop.feed.stateMutex.Unlock()
		loop.feed.notice(sessionfeed.NoticeSeverityInfo, "── session "+typed.sessionID+" ──")
		loop.openSession(typed.sessionID, model.epoch+1, false)
	}
}

func (loop *sessionLoop) scheduleRefresh(delay time.Duration) {
	if loop.isFetching {
		loop.isRefreshAgain = loop.isRefreshAgain || delay == 0
		return
	}
	if !loop.refreshTimer.Stop() {
		select {
		case <-loop.refreshTimer.C:
		default:
		}
	}
	loop.refreshTimer.Reset(delay)
}

func (loop *sessionLoop) startFetch() {
	if loop.isFetching {
		return
	}
	loop.isFetching = true
	loop.isRefreshAgain = false
	model := loop.model
	isHistoryNeeded := !model.isLoaded && model.isResumed
	feed := loop.feed
	go func() {
		fetched := snapshotFetched{epoch: model.epoch}
		snapshotContext, cancel := context.WithTimeout(loop.ctx, feed.timings.SnapshotTimeout)
		fetched.snapshot, fetched.err = feed.sessions.GetAgentSnapshot(snapshotContext, model.sessionID)
		cancel()
		if fetched.err == nil && isHistoryNeeded {
			historyContext, cancelHistory := context.WithTimeout(loop.ctx, feed.timings.SnapshotTimeout)
			page, err := feed.sessions.ListAgentMessages(historyContext, model.sessionID, nil, historyPageSize)
			cancelHistory()
			if err != nil {
				feed.notice(sessionfeed.NoticeSeverityWarning, "Could not load the history: "+describeError(err)+".")
			} else {
				fetched.history = &page
				fetched.cancelledTurns = readCancelledTurns(loop.ctx, feed, model.sessionID)
			}
		}
		select {
		case feed.inputs <- fetched:
		case <-loop.ctx.Done():
		}
	}()
}

func (loop *sessionLoop) applyFetch(fetched snapshotFetched) {
	model := loop.model
	if fetched.err != nil {
		loop.feed.debugf("read the snapshot", fetched.err)
		loop.applyFetchFailure(fetched.err)
		return
	}
	if loop.isUnreachable {
		loop.isUnreachable = false
		loop.feed.emit(sessionfeed.ServerReachabilityChanged{IsReachable: true})
	}
	if fetched.snapshot.State == nil {
		// The session has not committed its start yet.
		loop.scheduleRefresh(loop.feed.timings.RunningPoll)
		return
	}
	wasLoaded := model.isLoaded
	if !wasLoaded && fetched.history != nil {
		model.emitHistory(*fetched.history, fetched.snapshot.State.ActiveTurnID, fetched.cancelledTurns)
	}
	model.applySnapshot(fetched.snapshot)
	if !wasLoaded {
		loop.loadFailures = 0
		loop.feed.emit(sessionfeed.SessionLoaded{})
	}
	delay := loop.feed.timings.IdlePoll
	if model.isBusy() {
		delay = loop.feed.timings.RunningPoll
	}
	if wait, hasWait := model.nextTurnEndDeadline(loop.feed.now()); hasWait && wait < delay {
		delay = wait
	}
	if loop.isRefreshAgain {
		delay = 0
	}
	loop.scheduleRefresh(delay)
}

// applyFetchFailure names an unreachable server once per outage.
func (loop *sessionLoop) applyFetchFailure(err error) {
	model := loop.model
	isServerDown := isUnavailable(err)
	if isServerDown && !loop.isUnreachable {
		loop.isUnreachable = true
		loop.feed.emit(sessionfeed.ServerReachabilityChanged{IsReachable: false})
		loop.feed.notice(sessionfeed.NoticeSeverityWarning, "Dex server unreachable; retrying.")
	}
	if model.isLoaded {
		loop.scheduleRefresh(loop.feed.timings.RunningPoll)
		return
	}
	if !loop.isLoadNoticeShown && !isServerDown {
		loop.isLoadNoticeShown = true
		loop.feed.notice(sessionfeed.NoticeSeverityWarning, "Session not loaded yet: the Worker is busy or unreachable. Retrying.")
	}
	loop.loadFailures++
	backoff := time.Second << min(loop.loadFailures-1, 4)
	loop.scheduleRefresh(min(backoff, loop.feed.timings.MaxLoadBackoff))
}

// Reading cancelled turns for the history is best effort: at most this many
// of the newest retained events.
const (
	historyEventPageSize  = 100
	historyEventPageCount = 3
)

// readCancelledTurns returns the turns the newest events show as
// cancelled, up to the first read error.
func readCancelledTurns(ctx context.Context, feed *RemoteSessionFeed, sessionID string) map[string]bool {
	cancelled := map[string]bool{}
	pageToken := ""
	for range historyEventPageCount {
		listContext, cancel := context.WithTimeout(ctx, feed.timings.SnapshotTimeout)
		page, err := feed.sessions.ListAgentEvents(listContext, sessionID, historyEventPageSize, pageToken)
		cancel()
		if err != nil {
			feed.debugf("read the history events", err)
			return cancelled
		}
		for _, message := range page.Messages {
			if event := message.Value; event.Type == "turn_cancelled" && event.TurnID != nil {
				cancelled[*event.TurnID] = true
			}
		}
		if page.NextPageToken == "" {
			return cancelled
		}
		pageToken = page.NextPageToken
	}
	return cancelled
}

// followAgentEvents reads the session's AgentEvents from now on and hands
// them to the loop until ctx ends or the Stream store is off.
func followAgentEvents(ctx context.Context, feed *RemoteSessionFeed, sessionID string, epoch int64) {
	send := func(input loopInput) bool {
		select {
		case feed.inputs <- input:
			return true
		case <-ctx.Done():
			return false
		}
	}
	resumeToken, isStarted := "", false
	for ctx.Err() == nil {
		if !isStarted {
			listContext, cancel := context.WithTimeout(ctx, feed.timings.SnapshotTimeout)
			page, err := feed.sessions.ListAgentEvents(listContext, sessionID, 1, "")
			cancel()
			if err != nil {
				if isStreamStoreOff(err) {
					send(eventsUnavailable{epoch: epoch, reason: describeError(err)})
					return
				}
				if !sleepFor(ctx, time.Second) {
					return
				}
				continue
			}
			if len(page.Messages) > 0 {
				resumeToken = page.Messages[0].ResumeToken
			}
			isStarted = true
		}
		readContext, cancel := context.WithTimeout(ctx, time.Minute)
		event, message, err := feed.sessions.ReadAgentEvent(readContext, sessionID, resumeToken)
		cancel()
		var longPollTimeout *dex.LongPollTimeoutError
		switch {
		case err == nil:
			feed.debugf("event "+knownEventTypeForLog(event.Type), nil)
			resumeToken = message.ResumeToken
			if !send(eventReceived{epoch: epoch, event: event}) {
				return
			}
		case errors.As(err, &longPollTimeout), errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		case isStreamStoreOff(err):
			send(eventsUnavailable{epoch: epoch, reason: describeError(err)})
			return
		default:
			feed.debugf("read events", err)
			if !sleepFor(ctx, time.Second) {
				return
			}
		}
	}
}

func sleepFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func serviceErrorCode(err error) (codes.Code, bool) {
	var serviceError *dex.ServiceError
	if errors.As(err, &serviceError) {
		return serviceError.Code, true
	}
	return codes.OK, false
}

func isUnavailable(err error) bool {
	code, isService := serviceErrorCode(err)
	return isService && code == codes.Unavailable
}

// isStreamStoreOff reports the FAILED_PRECONDITION a server without a
// Stream store returns.
func isStreamStoreOff(err error) bool {
	code, isService := serviceErrorCode(err)
	return isService && code == codes.FailedPrecondition
}

// knownEventTypes are the AgentEvents types the debug log names; others,
// such as adapter-prefixed types, show as "other".
var knownEventTypes = map[string]bool{
	"turn_started": true, "turn_completed": true, "turn_failed": true, "turn_steered": true, "turn_cancelled": true,
	"text_delta": true, "reasoning_delta": true, "tool_call_started": true, "tool_call_finished": true,
	"tool_call_deferred": true, "interrupt_raised": true, "interrupt_resolved": true, "usage_recorded": true,
	"timer_started": true, "plan_updated": true, "plan_execution_started": true, "tool_task_started": true,
	"compaction_started": true, "compaction_finished": true,
}

func knownEventTypeForLog(eventType string) string {
	if knownEventTypes[eventType] {
		return eventType
	}
	return "other"
}

// ErrorKindForLog names an error for the debug log without its text: the
// gRPC code and Dex sub-status, or a fixed word.
func ErrorKindForLog(err error) string {
	var serviceError *dex.ServiceError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &serviceError):
		return fmt.Sprintf("grpc %s, sub-status %d", serviceError.Code, uint8(serviceError.SubStatus))
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline exceeded"
	case errors.Is(err, net.ErrClosed), errors.Is(err, syscall.EPIPE):
		return "closed"
	}
	return "error"
}

// describeError shows a gRPC error as its status, Dex sub-status, and
// message, never its metadata.
func describeError(err error) string {
	var serviceError *dex.ServiceError
	if errors.As(err, &serviceError) {
		text := serviceError.Code.String()
		if serviceError.SubStatus != 0 {
			text += fmt.Sprintf(" (%v)", serviceError.SubStatus)
		}
		if serviceError.Detail != "" {
			text += ": " + serviceError.Detail
		}
		return text
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer in time"
	}
	return fmt.Sprint(err)
}
