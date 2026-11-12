// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

import "time"

// SessionUpdate is one change the chat screen draws. The concrete types
// below are the only implementations.
type SessionUpdate interface {
	isSessionUpdate()
}

// SessionOpened describes the session. A source sends it first and after each
// change.
type SessionOpened struct {
	// Command is the agents command name, such as "openai-sdk".
	Command string
	// Model is the session's model ID; empty when not known yet.
	Model string
	// SessionID is the session Flow ID.
	SessionID string
	// IsNewSession reports a session with no turns yet.
	IsNewSession bool
	// TurnCount is the number of finished turns.
	TurnCount int
	// AcceptsTurnControl reports whether the session can cancel a turn and
	// steer or delete queued messages.
	AcceptsTurnControl bool
	// SupportsPlanMode reports a Flow built with plan mode, which enables
	// /plan and /run.
	SupportsPlanMode bool
}

// SessionLoaded reports that the first snapshot arrived. The screen holds
// typed messages until then and submits them in order.
type SessionLoaded struct{}

// UserMessageRecorded reports a user message the screen did not print: from
// history, another client, or a queue.
type UserMessageRecorded struct {
	// Text is the message text.
	Text string
	// Origin names the client that sent it, such as "Dex Web"; empty for
	// this terminal or unknown.
	Origin string
	// TurnID identifies the turn the message started; empty when unknown.
	TurnID string
	// Sequence is the stored sequence, or 0 for none. Each positive sequence
	// prints once.
	Sequence int64
	// Images are the attached images.
	Images []ImageAttachment
}

// UserMessageRejected reports that the session refused a SendUserMessage,
// for example blank text or images the runner cannot take.
type UserMessageRejected struct {
	// Text is the refused message text.
	Text string
	// Reason says why and what to do next.
	Reason string
}

// HistoryWindowChanged describes the history above the oldest message shown.
type HistoryWindowChanged struct {
	// EarlierBeforeSequence is the sequence before which more messages can
	// be read, or 0 when none can.
	EarlierBeforeSequence int64
	// DeletedBeforeSequence is the sequence before which retention deleted
	// messages, or 0 when none were deleted.
	DeletedBeforeSequence int64
}

// ImageAttached reports an image stored for the next message, such as after
// /image.
type ImageAttached struct {
	// Attachment is the stored image.
	Attachment ImageAttachment
}

// TurnStarted reports that a turn began.
type TurnStarted struct {
	// TurnID identifies the turn.
	TurnID string
	// TurnNumber is the 1-based number shown in the context line.
	TurnNumber int
	// StartedAt is when the turn started; the context line shows the
	// elapsed time from it.
	StartedAt time.Time
}

// TurnActivity is what a running turn is doing.
type TurnActivity int

const (
	// TurnActivityRunningModel means the model is producing a reply.
	TurnActivityRunningModel TurnActivity = iota
	// TurnActivityRunningTools means tools are running.
	TurnActivityRunningTools
	// TurnActivityCompacting means the session is compacting its context.
	TurnActivityCompacting
	// TurnActivityAwaitingUser means the turn waits for an approval or an
	// answer.
	TurnActivityAwaitingUser
)

// TurnActivityChanged reports what the running turn is doing now.
type TurnActivityChanged struct {
	// TurnID identifies the turn.
	TurnID string
	// Activity is the new activity.
	Activity TurnActivity
}

// TurnOutcome is how a turn ended.
type TurnOutcome int

const (
	// TurnOutcomeCompleted means the turn finished normally.
	TurnOutcomeCompleted TurnOutcome = iota
	// TurnOutcomeCancelled means the turn was cancelled; its reply may be
	// incomplete.
	TurnOutcomeCancelled
	// TurnOutcomeFailed means the turn failed.
	TurnOutcomeFailed
)

// TurnEnded reports that a turn ended. The source sends the turn's final
// assistant messages before it.
type TurnEnded struct {
	// TurnID identifies the turn.
	TurnID string
	// Outcome is how the turn ended.
	Outcome TurnOutcome
	// FailureText says what happened and what to do next, when the turn failed.
	FailureText string
}

// AssistantTextDelta carries streamed reply text. Only the highest Attempt of
// a Step is drawn.
type AssistantTextDelta struct {
	// TurnID identifies the turn.
	TurnID string
	// StepExecutionID identifies the model Step that produced the text.
	StepExecutionID string
	// Attempt is the Step attempt number, starting at 1.
	Attempt int
	// Text is the new text.
	Text string
}

// AssistantMessageRecorded carries a Step's final message. The screen
// reconciles it with the streamed text.
type AssistantMessageRecorded struct {
	// TurnID identifies the turn.
	TurnID string
	// StepExecutionID identifies the model Step; it matches the deltas'.
	StepExecutionID string
	// Text is the complete message text in markdown.
	Text string
	// Sequence is the stored message sequence, or 0 when unknown. The
	// screen prints each positive sequence once.
	Sequence int64
}

// ReasoningDelta carries model reasoning. Only its tail shows, never in the
// scrollback.
type ReasoningDelta struct {
	// TurnID identifies the turn.
	TurnID string
	// Text is the new reasoning text.
	Text string
}

// UsageRecorded reports the session's token usage so far.
type UsageRecorded struct {
	// TotalTokens is the total token count of the session.
	TotalTokens int64
}

// NoticeSeverity is how a notice is marked.
type NoticeSeverity int

const (
	// NoticeSeverityInfo is a neutral note, marked with a dim dot.
	NoticeSeverityInfo NoticeSeverity = iota
	// NoticeSeverityWarning is marked with "!".
	NoticeSeverityWarning
	// NoticeSeverityError is marked with a red cross.
	NoticeSeverityError
)

// NoticeRaised asks for one notice line. The screen cleans, cuts, and
// deduplicates it.
type NoticeRaised struct {
	// Severity selects the symbol.
	Severity NoticeSeverity
	// Text says what happened and what to do next, in one or two short
	// sentences.
	Text string
}

// CancelOutcome is the session's answer to a CancelTurn action.
type CancelOutcome int

const (
	// CancelOutcomeAccepted means the session accepted the cancel; the turn
	// ends soon with TurnOutcomeCancelled.
	CancelOutcomeAccepted CancelOutcome = iota
	// CancelOutcomeTurnAlreadyEnded means no turn was running any more.
	CancelOutcomeTurnAlreadyEnded
	// CancelOutcomeRefused means the session refused to cancel the turn.
	CancelOutcomeRefused
	// CancelOutcomeNotConfirmed means the request failed or timed out; the
	// turn may still run.
	CancelOutcomeNotConfirmed
)

// CancelRequestAnswered reports the outcome of a CancelTurn action.
type CancelRequestAnswered struct {
	// TurnID identifies the turn the cancel targeted.
	TurnID string
	// Outcome is the session's answer.
	Outcome CancelOutcome
	// Reason explains CancelOutcomeNotConfirmed, such as "lock conflict
	// after 30s".
	Reason string
}

// WorkerState is the state of the Worker that runs the session.
type WorkerState int

const (
	// WorkerStateReady means the Worker is registered and polling.
	WorkerStateReady WorkerState = iota
	// WorkerStateReconnecting means the Worker lost its poll stream and is
	// reconnecting.
	WorkerStateReconnecting
	// WorkerStateStopping means the Worker is draining before it exits.
	WorkerStateStopping
	// WorkerStateStopped means the Worker exited; the chat cannot continue.
	WorkerStateStopped
)

// WorkerStateChanged reports a change of the Worker state.
type WorkerStateChanged struct {
	// State is the new state.
	State WorkerState
	// ExitCode is the Worker's exit code when State is WorkerStateStopped.
	ExitCode int
	// ResumeCommand is the shell command that resumes this session, shown
	// when the Worker stopped.
	ResumeCommand string
}

// ServerReachabilityChanged reports whether the Dex server answers.
type ServerReachabilityChanged struct {
	// IsReachable is false while requests to the server fail and retry.
	IsReachable bool
}

// QueuedMessage is a user message that waits for the running turn to end.
type QueuedMessage struct {
	// MessageID identifies the message for steer and delete.
	MessageID string
	// Text is the message text.
	Text string
}

// QueuedMessagesChanged replaces the list of queued messages.
type QueuedMessagesChanged struct {
	// Messages are the queued messages, oldest first.
	Messages []QueuedMessage
}

// PlanItemStatus is the state of one plan item.
type PlanItemStatus int

const (
	// PlanItemPending has not started.
	PlanItemPending PlanItemStatus = iota
	// PlanItemInProgress is the current item.
	PlanItemInProgress
	// PlanItemDone is finished.
	PlanItemDone
)

// PlanItem is one item of the session plan.
type PlanItem struct {
	// Title is the item text.
	Title string
	// Status is the item state.
	Status PlanItemStatus
}

// PlanStatus is the state of the session plan.
type PlanStatus int

const (
	// PlanStatusDraft waits for approval with /run.
	PlanStatusDraft PlanStatus = iota
	// PlanStatusActive is running.
	PlanStatusActive
	// PlanStatusCompleted is done.
	PlanStatusCompleted
	// PlanStatusCleared was cleared; the screen hides the plan line.
	PlanStatusCleared
)

// PlanChanged replaces the session plan. An empty Items list hides the
// plan line.
type PlanChanged struct {
	// Revision is the plan revision that ExecutePlan approves.
	Revision int64
	// Status is the plan state.
	Status PlanStatus
	// Items are the plan items in order.
	Items []PlanItem
	// IsInPlanInteraction reports the session's plan interaction mode,
	// which /run requires.
	IsInPlanInteraction bool
}

func (SessionOpened) isSessionUpdate()             {}
func (SessionLoaded) isSessionUpdate()             {}
func (UserMessageRecorded) isSessionUpdate()       {}
func (UserMessageRejected) isSessionUpdate()       {}
func (HistoryWindowChanged) isSessionUpdate()      {}
func (ImageAttached) isSessionUpdate()             {}
func (TurnStarted) isSessionUpdate()               {}
func (TurnActivityChanged) isSessionUpdate()       {}
func (TurnEnded) isSessionUpdate()                 {}
func (AssistantTextDelta) isSessionUpdate()        {}
func (AssistantMessageRecorded) isSessionUpdate()  {}
func (ReasoningDelta) isSessionUpdate()            {}
func (UsageRecorded) isSessionUpdate()             {}
func (NoticeRaised) isSessionUpdate()              {}
func (CancelRequestAnswered) isSessionUpdate()     {}
func (WorkerStateChanged) isSessionUpdate()        {}
func (ServerReachabilityChanged) isSessionUpdate() {}
func (QueuedMessagesChanged) isSessionUpdate()     {}
func (PlanChanged) isSessionUpdate()               {}
