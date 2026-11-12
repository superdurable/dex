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

// SendUserMessage sends a message. While a turn runs, the session queues it.
type SendUserMessage struct {
	// Text is the message text.
	Text string
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

func (SendUserMessage) isUserAction()     {}
func (ResolveApproval) isUserAction()     {}
func (AnswerQuestion) isUserAction()      {}
func (DeclineQuestion) isUserAction()     {}
func (CancelTurn) isUserAction()          {}
func (SteerQueuedMessage) isUserAction()  {}
func (DeleteQueuedMessage) isUserAction() {}
func (RunSlashCommand) isUserAction()     {}
