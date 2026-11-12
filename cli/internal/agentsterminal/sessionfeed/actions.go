// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package sessionfeed

// UserAction is one request the chat screen sends to the session. The
// concrete types below are the only implementations.
type UserAction interface {
	isUserAction()
}

// SendUserMessage sends a message. The screen prints it only when no turn
// runs or starts; otherwise UserMessageRecorded does.
type SendUserMessage struct {
	// Text is the message text.
	Text string
	// Images are the attachments from ImageAttached, in order.
	Images []ImageAttachment
	// IsPlanMode asks for a planning turn, after /plan.
	IsPlanMode bool
}

// ExecutePlan approves one plan revision, so the session starts a turn
// that runs it.
type ExecutePlan struct {
	// Revision is the PlanChanged revision the user reviewed.
	Revision int64
}

// ResolveApproval approves or rejects a pending tool call.
type ResolveApproval struct {
	// InterruptID identifies the approval.
	InterruptID string
	// IsApproved grants the approval when true and rejects it when false.
	IsApproved bool
	// Reason explains a rejection to the agent; empty for none.
	Reason string
}

// AnswerQuestion answers the agent's question.
type AnswerQuestion struct {
	// InterruptID identifies the question.
	InterruptID string
	// Answer is the chosen or typed answer.
	Answer string
}

// DeclineQuestion declines to answer the agent's question.
type DeclineQuestion struct {
	// InterruptID identifies the question.
	InterruptID string
}

// CancelTurn asks the session to cancel the running turn. The answer arrives
// as CancelRequestAnswered.
type CancelTurn struct {
	// TurnID identifies the turn to cancel.
	TurnID string
}

// SteerQueuedMessage asks the session to deliver a queued message into the
// running turn instead of after it.
type SteerQueuedMessage struct {
	// MessageID identifies the queued message.
	MessageID string
}

// DeleteQueuedMessage removes a queued message before it is sent.
type DeleteQueuedMessage struct {
	// MessageID identifies the queued message.
	MessageID string
}

// RunSlashCommand runs one of the SlashCommand values the source declared,
// such as "/model gpt-5.2".
type RunSlashCommand struct {
	// Name is the command name without the slash.
	Name string
	// Argument is the rest of the line, trimmed; empty for none.
	Argument string
}

// SlashCommand is one slash command that a source runs. Completion and help
// derive from them.
type SlashCommand struct {
	// Name is the command name without the slash, such as "model".
	Name string
	// ArgumentHint names the argument in help, such as "ID"; empty for none.
	ArgumentHint string
	// Description says what the command does, starting with a verb.
	Description string
}

// DetachImage removes the newest image attached for the next message, after
// the user deleted its chip.
type DetachImage struct{}

// ReadEarlierMessages asks for the stored messages before a sequence, for
// /history. The answer arrives as EarlierMessagesRead.
type ReadEarlierMessages struct {
	// SessionID is the session the screen shows; the answer carries it back.
	SessionID string
	// BeforeSequence is the stored sequence to read before, from
	// HistoryWindowChanged or an earlier EarlierMessagesRead.
	BeforeSequence int64
	// RequestID identifies this read; the answer carries it back.
	RequestID int64
}

func (SendUserMessage) isUserAction()     {}
func (ResolveApproval) isUserAction()     {}
func (AnswerQuestion) isUserAction()      {}
func (DeclineQuestion) isUserAction()     {}
func (CancelTurn) isUserAction()          {}
func (SteerQueuedMessage) isUserAction()  {}
func (DeleteQueuedMessage) isUserAction() {}
func (RunSlashCommand) isUserAction()     {}
func (ExecutePlan) isUserAction()         {}
func (DetachImage) isUserAction()         {}
func (ReadEarlierMessages) isUserAction() {}
