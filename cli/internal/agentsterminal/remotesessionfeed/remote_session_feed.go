// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package remotesessionfeed connects the agents chat screen to a real agent
// session: the snapshot is the truth, the AgentEvents Stream adds live text,
// and local requests go to the launcher.
package remotesessionfeed

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
)

// SessionRPCs are the session calls the feed makes.
// *remotesession.RemoteSessionClient implements it.
type SessionRPCs interface {
	SendUserMessage(ctx context.Context, sessionID string, message dexagents.AgentUserMessage) (bool, error)
	SteerUserMessage(ctx context.Context, sessionID string, request dexagents.SteerUserMessageRequest) (bool, error)
	DeleteQueuedMessage(ctx context.Context, sessionID string, messageID string) (bool, error)
	CancelTurn(ctx context.Context, sessionID string, turnID *string) (bool, error)
	ResolveInterrupt(ctx context.Context, sessionID string, interruptID string, isApproved bool, rejectionMessage *string) (bool, error)
	AnswerQuestion(ctx context.Context, sessionID string, interruptID string, answer string) (bool, error)
	ExecutePlan(ctx context.Context, sessionID string, revision int64) (bool, error)
	UpdateSessionSettings(ctx context.Context, sessionID string, sessionSettings map[string]any) (bool, error)
	GetAgentSnapshot(ctx context.Context, sessionID string) (dexagents.AgentSnapshot, error)
	ListAgentMessages(ctx context.Context, sessionID string, beforeSequence *int64, limit int64) (dexagents.AgentMessagesPage, error)
	ListAgentEvents(ctx context.Context, sessionID string, pageSize int32, beforePageToken string) (dex.StreamMessagesPage[dexagents.AgentEvent], error)
	ReadAgentEvent(ctx context.Context, sessionID string, resumeToken string) (dexagents.AgentEvent, dex.StreamMessage, error)
}

// LocalRequests are the launcher's local requests and notifications.
// *launcherchannel.LauncherChannel implements it.
type LocalRequests interface {
	HasMethod(method string) bool
	DisableMethod(method string)
	Call(ctx context.Context, method string, key string, params any) launcherchannel.RequestOutcome
	CallWithRecoveryKey(ctx context.Context, method string, key string, params any, onStillRunning func()) launcherchannel.RequestOutcome
	Notifications() <-chan launcherchannel.Notification
}

// FeedTimings are the feed's waits and poll intervals.
type FeedTimings struct {
	// SnapshotTimeout bounds one get_agent_snapshot call.
	SnapshotTimeout time.Duration
	// RunningPoll is the snapshot interval while a turn runs.
	RunningPoll time.Duration
	// IdlePoll is the snapshot interval while the session waits.
	IdlePoll time.Duration
	// LockConflictBudget bounds retries of a call that hits an RPC lock.
	LockConflictBudget time.Duration
	// RPCTimeout bounds one session RPC attempt.
	RPCTimeout time.Duration
	// TurnEndEventWait is how long a finished turn waits for its end event.
	TurnEndEventWait time.Duration
	// MaxLoadBackoff caps the wait between first snapshot attempts.
	MaxLoadBackoff time.Duration
}

// DefaultFeedTimings returns the timings of the terminal UI design.
func DefaultFeedTimings() FeedTimings {
	return FeedTimings{
		SnapshotTimeout:    15 * time.Second,
		RunningPoll:        time.Second,
		IdlePoll:           5 * time.Second,
		LockConflictBudget: 30 * time.Second,
		RPCTimeout:         30 * time.Second,
		TurnEndEventWait:   2 * time.Second,
		MaxLoadBackoff:     10 * time.Second,
	}
}

// RemoteSessionFeedConfig configures NewRemoteSessionFeed.
type RemoteSessionFeedConfig struct {
	// Sessions makes the session calls. Required.
	Sessions SessionRPCs
	// Requests serves the local requests. Required.
	Requests LocalRequests
	// Launch is the checked launch document of mode chat.
	Launch launchcontract.LaunchDocument
	// Timings are the waits; zero fields take DefaultFeedTimings.
	Timings FeedTimings
	// HomeDirectory expands a leading ~ in /image paths; empty for none.
	HomeDirectory string
	// WorkingDirectory resolves relative /image paths.
	WorkingDirectory string
	// Now returns the current time. Default: time.Now.
	Now func() time.Time
	// Debugf writes one debug log line: a constant event name and the kind of
	// err, from ErrorKindForLog, never its text. nil for none.
	Debugf func(event string, err error)
	// OpenSessionPicker opens the session picker for /sessions. nil hides
	// /sessions.
	OpenSessionPicker func()
}

// RemoteSessionFeed is a sessionfeed.ChatSessionSource over one agent session.
type RemoteSessionFeed struct {
	sessions SessionRPCs
	requests LocalRequests
	launch   launchcontract.LaunchDocument
	timings  FeedTimings
	home     string
	workDir  string
	now      func() time.Time
	debugf   func(event string, err error)

	openSessionPicker func()

	updates *updateQueue
	inputs  chan loopInput

	// actionMutex keeps one local request action at a time, in order.
	actionMutex sync.Mutex

	isStartingSession atomic.Bool

	// switchGate lets session actions run together and a switch alone.
	switchGate         sync.RWMutex
	sessionActionCount atomic.Int64

	stateMutex sync.Mutex
	// imageRequests ends when the chat leaves the session.
	imageRequests       context.Context
	cancelImageRequests context.CancelFunc
	sessionID           string
	isChannelEnded      bool
	hasFailure          bool
	attachedImages      int
	isRunning           bool
}

// NewRemoteSessionFeed creates the feed. Call Run to start it.
func NewRemoteSessionFeed(config RemoteSessionFeedConfig) *RemoteSessionFeed {
	if config.Sessions == nil || config.Requests == nil {
		panic("remotesessionfeed: NewRemoteSessionFeed needs Sessions and Requests")
	}
	timings := withDefaultTimings(config.Timings)
	now := config.Now
	if now == nil {
		now = time.Now
	}
	debugf := config.Debugf
	if debugf == nil {
		debugf = func(string, error) {}
	}
	imageRequests, cancelImageRequests := context.WithCancel(context.Background())
	return &RemoteSessionFeed{
		sessions: config.Sessions,
		requests: config.Requests,
		launch:   config.Launch,
		timings:  timings,
		home:     config.HomeDirectory,
		workDir:  config.WorkingDirectory,
		now:      now,
		debugf:   debugf,

		openSessionPicker: config.OpenSessionPicker,

		updates: newUpdateQueue(),
		inputs:  make(chan loopInput, 256),

		sessionID:           config.Launch.Session.SessionID,
		imageRequests:       imageRequests,
		cancelImageRequests: cancelImageRequests,
	}
}

func withDefaultTimings(timings FeedTimings) FeedTimings {
	defaults := DefaultFeedTimings()
	pick := func(value time.Duration, fallback time.Duration) time.Duration {
		if value <= 0 {
			return fallback
		}
		return value
	}
	return FeedTimings{
		SnapshotTimeout:    pick(timings.SnapshotTimeout, defaults.SnapshotTimeout),
		RunningPoll:        pick(timings.RunningPoll, defaults.RunningPoll),
		IdlePoll:           pick(timings.IdlePoll, defaults.IdlePoll),
		LockConflictBudget: pick(timings.LockConflictBudget, defaults.LockConflictBudget),
		RPCTimeout:         pick(timings.RPCTimeout, defaults.RPCTimeout),
		TurnEndEventWait:   pick(timings.TurnEndEventWait, defaults.TurnEndEventWait),
		MaxLoadBackoff:     pick(timings.MaxLoadBackoff, defaults.MaxLoadBackoff),
	}
}

// Run follows the session until ctx ends. It returns ctx's error.
func (feed *RemoteSessionFeed) Run(ctx context.Context) error {
	feed.stateMutex.Lock()
	if feed.isRunning {
		feed.stateMutex.Unlock()
		return errors.New("remotesessionfeed: Run called twice")
	}
	feed.isRunning = true
	feed.stateMutex.Unlock()
	go feed.relayNotifications(ctx)
	loop := newSessionLoop(feed)
	return loop.run(ctx)
}

// NextSessionUpdate returns the next update.
func (feed *RemoteSessionFeed) NextSessionUpdate(ctx context.Context) (sessionfeed.SessionUpdate, error) {
	return feed.updates.Next(ctx)
}

// SubmitUserAction runs one action. Session outcomes arrive as updates; an
// error means the action did not reach the session.
func (feed *RemoteSessionFeed) SubmitUserAction(ctx context.Context, action sessionfeed.UserAction) error {
	return feed.runAction(ctx, action)
}

// SlashCommands returns the commands the launch document's methods allow.
func (feed *RemoteSessionFeed) SlashCommands() []sessionfeed.SlashCommand {
	commands := []sessionfeed.SlashCommand{
		{Name: modelCommandName, ArgumentHint: "ID", Description: "change the model from the next turn"},
	}
	if feed.launch.HasMethod(launchcontract.MethodPutImage) && feed.launch.Capabilities.Images != nil {
		commands = append(commands, sessionfeed.SlashCommand{Name: imageCommandName, ArgumentHint: "PATH", Description: "attach an image to the next message"})
	}
	if feed.launch.HasMethod(launchcontract.MethodStartSession) {
		commands = append(commands, sessionfeed.SlashCommand{Name: newCommandName, Description: "start a new session and switch to it"})
	}
	if feed.openSessionPicker != nil {
		commands = append(commands, sessionfeed.SlashCommand{Name: sessionsCommandName, Description: "switch to another session of this command"})
	}
	if feed.launch.HasMethod(launchcontract.MethodOpenURL) && feed.launch.WebURL() != "" {
		commands = append(commands, sessionfeed.SlashCommand{Name: webCommandName, Description: "open this session in Dex Web"})
	}
	if feed.launch.HasMethod(launchcontract.MethodListConnectors) {
		commands = append(commands, sessionfeed.SlashCommand{Name: connectorsCommandName, Description: "list the connector tools"})
	}
	return commands
}

// ExitCode returns dexcli's exit code for the chat: 1 when the channel
// ended or a turn or the session failed, otherwise 0. A stopped Worker alone
// is 0, because the user exited; the launcher reports the Worker.
func (feed *RemoteSessionFeed) ExitCode() int {
	feed.stateMutex.Lock()
	defer feed.stateMutex.Unlock()
	if feed.isChannelEnded || feed.hasFailure {
		return 1
	}
	return 0
}

// SessionID returns the session the chat shows now.
func (feed *RemoteSessionFeed) SessionID() string {
	feed.stateMutex.Lock()
	defer feed.stateMutex.Unlock()
	return feed.sessionID
}

// RequestRefresh reads the snapshot soon, for example after the chat
// returns to the foreground.
func (feed *RemoteSessionFeed) RequestRefresh() {
	select {
	case feed.inputs <- refreshRequested{}:
	default:
	}
}

func (feed *RemoteSessionFeed) markFailure() {
	feed.stateMutex.Lock()
	defer feed.stateMutex.Unlock()
	feed.hasFailure = true
}

func (feed *RemoteSessionFeed) emit(updates ...sessionfeed.SessionUpdate) {
	for _, update := range updates {
		feed.updates.Push(update)
	}
}

func (feed *RemoteSessionFeed) notice(severity sessionfeed.NoticeSeverity, text string) {
	feed.emit(sessionfeed.NoticeRaised{Severity: severity, Text: text})
}

// relayNotifications turns the launcher's notices and Worker states into
// updates, and closes the feed when the channel ends.
func (feed *RemoteSessionFeed) relayNotifications(ctx context.Context) {
	notifications := feed.requests.Notifications()
	for {
		select {
		case <-ctx.Done():
			return
		case notification, isOpen := <-notifications:
			if !isOpen {
				feed.stateMutex.Lock()
				feed.isChannelEnded = true
				feed.stateMutex.Unlock()
				feed.updates.Close()
				return
			}
			feed.relayNotification(notification)
		}
	}
}

func (feed *RemoteSessionFeed) relayNotification(notification launcherchannel.Notification) {
	if notification.Notice != nil {
		feed.notice(noticeSeverity(notification.Notice.Level), notification.Notice.Text)
		return
	}
	if notification.Worker == nil {
		return
	}
	changed := sessionfeed.WorkerStateChanged{State: workerState(notification.Worker.State), ExitCode: notification.Worker.ExitCode}
	if changed.State == sessionfeed.WorkerStateStopped {
		changed.ResumeCommand = feed.launch.Command.Name + " --resume " + feed.SessionID()
	}
	feed.emit(changed)
}

func noticeSeverity(level string) sessionfeed.NoticeSeverity {
	switch level {
	case "warning":
		return sessionfeed.NoticeSeverityWarning
	case "error":
		return sessionfeed.NoticeSeverityError
	default:
		return sessionfeed.NoticeSeverityInfo
	}
}

func workerState(state string) sessionfeed.WorkerState {
	switch state {
	case launchcontract.WorkerStateReconnecting:
		return sessionfeed.WorkerStateReconnecting
	case launchcontract.WorkerStateStopping:
		return sessionfeed.WorkerStateStopping
	case launchcontract.WorkerStateStopped:
		return sessionfeed.WorkerStateStopped
	default:
		return sessionfeed.WorkerStateReady
	}
}

// updateQueue is an unbounded FIFO of updates, so producers never block on
// the screen.
type updateQueue struct {
	mutex    sync.Mutex
	items    []sessionfeed.SessionUpdate
	signal   chan struct{}
	isClosed bool
}

func newUpdateQueue() *updateQueue {
	return &updateQueue{signal: make(chan struct{}, 1)}
}

func (queue *updateQueue) Push(update sessionfeed.SessionUpdate) {
	queue.mutex.Lock()
	if !queue.isClosed {
		queue.items = append(queue.items, update)
	}
	queue.mutex.Unlock()
	queue.wake()
}

func (queue *updateQueue) Close() {
	queue.mutex.Lock()
	queue.isClosed = true
	queue.mutex.Unlock()
	queue.wake()
}

func (queue *updateQueue) wake() {
	select {
	case queue.signal <- struct{}{}:
	default:
	}
}

// Next returns the oldest update; after Close, the remaining ones, then
// sessionfeed.ErrSessionFeedClosed.
func (queue *updateQueue) Next(ctx context.Context) (sessionfeed.SessionUpdate, error) {
	for {
		queue.mutex.Lock()
		if len(queue.items) > 0 {
			update := queue.items[0]
			queue.items[0] = nil
			queue.items = queue.items[1:]
			queue.mutex.Unlock()
			return update, nil
		}
		isClosed := queue.isClosed
		queue.mutex.Unlock()
		if isClosed {
			return nil, sessionfeed.ErrSessionFeedClosed
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-queue.signal:
		}
	}
}
