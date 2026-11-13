// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// AgentSessionStatus names what an agent session is doing now.
//
// The session Flow stores it in AgentState.Status. A UI reads it from the
// get_agent_snapshot RPC. The values are JSON strings on the wire, shared
// with the Python and TypeScript cores. Decoding rejects any other string.
type AgentSessionStatus string

const (
	// AgentSessionStatusWaitingForUserMessage means the session waits for the next user message.
	AgentSessionStatusWaitingForUserMessage AgentSessionStatus = "waiting_for_user_message"
	// AgentSessionStatusRunningModel means RunModel makes one model call of the active turn.
	AgentSessionStatusRunningModel AgentSessionStatus = "running_model"
	// AgentSessionStatusRunningTools means the tool call Steps of the active turn run or wait for
	// approval, in parallel, and ResumeTurn waits for their results.
	AgentSessionStatusRunningTools AgentSessionStatus = "running_tools"
	// AgentSessionStatusCompactingContext means CompactContext summarizes earlier messages. Only a
	// Worker with compaction writes it; this Go core reads it.
	AgentSessionStatusCompactingContext AgentSessionStatus = "compacting_context"
	// AgentSessionStatusFailed means the session ended and takes no more messages. Its Flow closed
	// failed, and AgentState.LastTurnError holds the reason. Python and TypeScript Workers write it;
	// the Go core reads it.
	AgentSessionStatusFailed AgentSessionStatus = "failed"
)

// UnmarshalText decodes a status and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (status *AgentSessionStatus) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, status, "AgentSessionStatus",
		AgentSessionStatusWaitingForUserMessage, AgentSessionStatusRunningModel,
		AgentSessionStatusRunningTools, AgentSessionStatusCompactingContext,
		AgentSessionStatusFailed)
}

// MessageRole names the author of one stored agent message.
type MessageRole string

const (
	// MessageRoleUser marks a message the user sent, or a Dex note stored as user text.
	MessageRoleUser MessageRole = "user"
	// MessageRoleAssistant marks model output, including a tool call request.
	MessageRoleAssistant MessageRole = "assistant"
	// MessageRoleTool marks a tool result returned to the model.
	MessageRoleTool MessageRole = "tool"
)

// UnmarshalText decodes a role and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (role *MessageRole) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, role, "MessageRole", MessageRoleUser, MessageRoleAssistant, MessageRoleTool)
}

// AgentMessageOrigin names why the session wrote a user message itself, in AgentMessage.Origin.
//
// A later release may add values, and decoding keeps a value this release does not know. Show a
// message with an unknown value as a note from the session, never as text the user typed.
// Since 0.6.0.
//
//	if message.Origin != nil && *message.Origin == dexagents.AgentMessageOriginPlanExecution {
//		fmt.Println("Running the approved plan")
//	}
type AgentMessageOrigin string

const (
	// AgentMessageOriginPlanExecution marks the message with which execute_plan started the turn.
	AgentMessageOriginPlanExecution AgentMessageOrigin = "plan_execution"
	// AgentMessageOriginTurnFailureNote marks the note in which RecordTurnFailure told the model
	// which tool calls of the failed turn ran.
	AgentMessageOriginTurnFailureNote AgentMessageOrigin = "turn_failure_note"
	// AgentMessageOriginTurnSteeredNote marks the note that lists the tool calls that ran before
	// steer_user_message ended the turn.
	AgentMessageOriginTurnSteeredNote AgentMessageOrigin = "turn_steered_note"
	// AgentMessageOriginTurnCancelledNote marks the note that lists the tool calls that ran before
	// cancel_turn ended the turn.
	AgentMessageOriginTurnCancelledNote AgentMessageOrigin = "turn_cancelled_note"
)

// ToolEffect classifies what a tool call does to the world outside the session.
//
// Every tool call runs in its own Step, and the effect picks that Step.
// ToolEffectRead and ToolEffectIdempotentWrite calls run in CallTool with up
// to three attempts and a stable idempotency key. ToolEffectNonIdempotentWrite
// and ToolEffectUnclassified calls run in CallSingleAttemptTool once; a crash
// during the call records ToolCallStatusOutcomeUnknown.
type ToolEffect string

const (
	// ToolEffectRead means the tool only reads. Running it again is harmless.
	ToolEffectRead ToolEffect = "read"
	// ToolEffectIdempotentWrite means the tool writes, and repeating it with the same idempotency
	// key has no further effect.
	ToolEffectIdempotentWrite ToolEffect = "idempotent_write"
	// ToolEffectNonIdempotentWrite means the tool writes, and repeating it can repeat the effect.
	ToolEffectNonIdempotentWrite ToolEffect = "non_idempotent_write"
	// ToolEffectUnclassified means nothing is known about the tool. It is treated like a
	// non-idempotent write.
	ToolEffectUnclassified ToolEffect = "unclassified"
)

// UnmarshalText decodes an effect and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (effect *ToolEffect) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, effect, "ToolEffect",
		ToolEffectRead, ToolEffectIdempotentWrite, ToolEffectNonIdempotentWrite, ToolEffectUnclassified)
}

// IsValid reports whether the effect is one of the four contract values.
//
// It returns false for the empty string and for any other text.
func (effect ToolEffect) IsValid() bool {
	return slices.Contains([]ToolEffect{
		ToolEffectRead, ToolEffectIdempotentWrite, ToolEffectNonIdempotentWrite, ToolEffectUnclassified,
	}, effect)
}

// isMultiAttempt reports whether calls with this effect run in CallTool.
func (effect ToolEffect) isMultiAttempt() bool {
	return effect == ToolEffectRead || effect == ToolEffectIdempotentWrite
}

// ToolCallStatus names the lifecycle state of one deferred tool call record.
//
// ToolCallStatusSucceeded, ToolCallStatusFailed, ToolCallStatusRejected, and
// ToolCallStatusOutcomeUnknown are final. A runner resuming a turn reads final
// records and returns their results to the model instead of running the tools
// again. Every other status is non-final, and runners never see a non-final
// record. The set of final statuses never grows.
type ToolCallStatus string

const (
	// ToolCallStatusAwaitingApproval means the call waits for the resolve_interrupt RPC.
	ToolCallStatusAwaitingApproval ToolCallStatus = "awaiting_approval"
	// ToolCallStatusApproved means the call may run and its tool Step has not finished.
	ToolCallStatusApproved ToolCallStatus = "approved"
	// ToolCallStatusAwaitingAnswer means the call waits for an answer. This Go core reads it only.
	ToolCallStatusAwaitingAnswer ToolCallStatus = "awaiting_answer"
	// ToolCallStatusWaitingForTimer means a durable_wait call waits. This Go core reads it only.
	ToolCallStatusWaitingForTimer ToolCallStatus = "waiting_for_timer"
	// ToolCallStatusWaitingForTask means a remote task runs. This Go core reads it only.
	ToolCallStatusWaitingForTask ToolCallStatus = "waiting_for_task"
	// ToolCallStatusRejected means the user or an expired deadline rejected the call. It never ran.
	ToolCallStatusRejected ToolCallStatus = "rejected"
	// ToolCallStatusSucceeded means the tool ran and returned AgentToolCall.Output.
	ToolCallStatusSucceeded ToolCallStatus = "succeeded"
	// ToolCallStatusFailed means the tool returned a ToolExecutionError, a read call's attempts
	// ran out, or the tool did not run (Error starts with ToolCallNotRunErrorPrefix);
	// AgentToolCall.Error holds the message.
	ToolCallStatusFailed ToolCallStatus = "failed"
	// ToolCallStatusOutcomeUnknown means a write's tool Step ended without a result, so the effect
	// may or may not have happened.
	ToolCallStatusOutcomeUnknown ToolCallStatus = "outcome_unknown"
)

// UnmarshalText decodes a status and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (status *ToolCallStatus) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, status, "ToolCallStatus",
		ToolCallStatusAwaitingApproval, ToolCallStatusApproved, ToolCallStatusAwaitingAnswer,
		ToolCallStatusWaitingForTimer, ToolCallStatusWaitingForTask, ToolCallStatusRejected,
		ToolCallStatusSucceeded, ToolCallStatusFailed, ToolCallStatusOutcomeUnknown)
}

// IsFinal reports whether the record has a result the model can see.
//
// It returns true for rejected, succeeded, failed, and outcome_unknown, and
// false while the call waits or may still run.
func (status ToolCallStatus) IsFinal() bool {
	switch status {
	case ToolCallStatusRejected, ToolCallStatusSucceeded, ToolCallStatusFailed, ToolCallStatusOutcomeUnknown:
		return true
	default:
		return false
	}
}

// InterruptKind names why a turn paused for a person.
type InterruptKind string

const (
	// InterruptKindToolApproval means a tool call needs approval before it runs. Answer it with
	// the resolve_interrupt RPC.
	InterruptKindToolApproval InterruptKind = "tool_approval"
	// InterruptKindQuestion means the model or a tool asked the user a question. This Go core reads
	// it only; the Go core never raises questions.
	InterruptKindQuestion InterruptKind = "question"
)

// UnmarshalText decodes a kind and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (kind *InterruptKind) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, kind, "InterruptKind", InterruptKindToolApproval, InterruptKindQuestion)
}

// InteractionMode names what kind of turn the session runs.
//
// This Go core starts only InteractionModeChat turns. It decodes the other
// values, which plan mode writes.
type InteractionMode string

const (
	// InteractionModeChat is an ordinary turn. The runner offers every tool.
	InteractionModeChat InteractionMode = "chat"
	// InteractionModePlanning is a turn that only plans.
	InteractionModePlanning InteractionMode = "planning"
	// InteractionModeExecuting is a turn that runs an approved plan.
	InteractionModeExecuting InteractionMode = "executing"
)

// UnmarshalText decodes a mode and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (mode *InteractionMode) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, mode, "InteractionMode",
		InteractionModeChat, InteractionModePlanning, InteractionModeExecuting)
}

// PlanStatus names where an AgentPlan stands.
type PlanStatus string

const (
	// PlanStatusDraft means a planning turn wrote the plan and it waits for approval.
	PlanStatusDraft PlanStatus = "draft"
	// PlanStatusActive means the plan runs.
	PlanStatusActive PlanStatus = "active"
	// PlanStatusCompleted means every task is completed.
	PlanStatusCompleted PlanStatus = "completed"
	// PlanStatusCleared means the plan was replaced with no tasks.
	PlanStatusCleared PlanStatus = "cleared"
)

// UnmarshalText decodes a plan status and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (status *PlanStatus) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, status, "PlanStatus", PlanStatusDraft, PlanStatusActive, PlanStatusCompleted, PlanStatusCleared)
}

// PlanTaskStatus names where one task of an AgentPlan stands.
type PlanTaskStatus string

const (
	// PlanTaskStatusPending means the task has not started.
	PlanTaskStatusPending PlanTaskStatus = "pending"
	// PlanTaskStatusInProgress means the model works on the task.
	PlanTaskStatusInProgress PlanTaskStatus = "in_progress"
	// PlanTaskStatusCompleted means the task is done.
	PlanTaskStatusCompleted PlanTaskStatus = "completed"
)

// UnmarshalText decodes a task status and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (status *PlanTaskStatus) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, status, "PlanTaskStatus", PlanTaskStatusPending, PlanTaskStatusInProgress, PlanTaskStatusCompleted)
}

// ToolCallCancellationReason names why steering or cancellation abandoned a tool call.
type ToolCallCancellationReason string

const (
	// ToolCallCancellationReasonSteered means a steered user message superseded the turn.
	ToolCallCancellationReasonSteered ToolCallCancellationReason = "steered"
	// ToolCallCancellationReasonTurnCancelled means the cancel_turn RPC stopped the turn.
	ToolCallCancellationReasonTurnCancelled ToolCallCancellationReason = "turn_cancelled"
)

// UnmarshalText decodes a reason and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (reason *ToolCallCancellationReason) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, reason, "ToolCallCancellationReason",
		ToolCallCancellationReasonSteered, ToolCallCancellationReasonTurnCancelled)
}

// TurnControlKind names the RPC that published one TurnControlRequest.
type TurnControlKind string

const (
	// TurnControlKindSteer means steer_user_message published the request.
	TurnControlKindSteer TurnControlKind = "steer"
	// TurnControlKindCancelTurn means cancel_turn published the request.
	TurnControlKindCancelTurn TurnControlKind = "cancel_turn"
)

// UnmarshalText decodes a kind and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (kind *TurnControlKind) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, kind, "TurnControlKind", TurnControlKindSteer, TurnControlKindCancelTurn)
}

// QuestionResolution names how a question that a tool or its task asked was resolved.
type QuestionResolution string

const (
	// QuestionResolutionAnswered means the user answered.
	QuestionResolutionAnswered QuestionResolution = "answered"
	// QuestionResolutionDeclined means the user declined.
	QuestionResolutionDeclined QuestionResolution = "declined"
	// QuestionResolutionDeadlinePassed means the question's deadline passed.
	QuestionResolutionDeadlinePassed QuestionResolution = "deadline_passed"
)

// UnmarshalText decodes a resolution and rejects a value outside the contract.
//
// It returns an error that names the unknown value.
func (resolution *QuestionResolution) UnmarshalText(text []byte) error {
	return decodeWireEnum(text, resolution, "QuestionResolution",
		QuestionResolutionAnswered, QuestionResolutionDeclined, QuestionResolutionDeadlinePassed)
}

// AgentSessionStartInput starts one agent session Flow, optionally with its first user message.
//
// Pass it as the input of Client.StartFlow, or use AgentSessionClient.StartSession.
// The Flow ID you choose is the session ID.
//
//	initialMessage := "Summarize my open tickets"
//	_, err := client.StartFlow(ctx, flow, "session-42", dexagents.AgentSessionStartInput{
//		InitialMessage:  &initialMessage,
//		SessionSettings: map[string]any{"model": "gpt-5-mini"},
//	}, dex.StartFlowOptions{})
type AgentSessionStartInput struct {
	// InitialMessage is the text of the first user message. Nil or whitespace-only text starts
	// the session in AwaitUserMessage.
	InitialMessage *string `json:"initial_message"`
	// SessionSettings are the session's settings, a JSON object that every turn's
	// AgentTurnContext.SessionSettings carries until update_session_settings replaces them, such
	// as the model to use. Nil starts without settings. Settings are stored in the session and
	// get_agent_snapshot returns them, so never put a secret in them; put a reference, such as a
	// credential name, that the Worker resolves. Settings the session refuses fail the session at
	// once; check them first with ValidateSessionSettings. Start sessions with settings only after
	// every Worker of the Flow type runs dex-agents 0.2.0.
	SessionSettings map[string]any `json:"session_settings"`
	// StartEnvironment says where the program that started the session ran, as string keys and
	// values, such as a host name and a workspace path. Nil records nothing. The session never
	// reads it: it stays in the Flow's start input, so a run shows the environment that started
	// it. The Dex Agents CLI records its host, user, configuration file, and workspace here. Never
	// put a secret in it. Workers before dex-agents 0.5.0 ignore it. Since 0.5.0.
	StartEnvironment map[string]string `json:"start_environment"`
}

// UpdateSessionSettingsRequest replaces the session's settings through the update_session_settings RPC.
//
// The new settings apply from the next turn that starts; a running turn keeps
// the settings it started with.
type UpdateSessionSettingsRequest struct {
	// SessionSettings are the complete new settings. They replace the old settings; nothing is
	// merged, and an empty map clears them. Never put a secret in them. Nil, which a missing
	// field or JSON null decodes to, makes the RPC return false and change nothing.
	SessionSettings map[string]any `json:"session_settings"`
}

// TurnSessionSettings holds the settings one turn started with, in the TurnSessionSettings Attribute.
//
// The Step that starts a turn writes it in the commit that starts the turn.
// The session reads it only while TurnID is AgentState.ActiveTurnID, so a
// copy that an earlier turn left, such as when a Worker without session
// settings began the current turn, reads as no settings.
type TurnSessionSettings struct {
	// TurnID is "turn-<n>" of the turn that started with these settings.
	TurnID string `json:"turn_id"`
	// SessionSettings are the settings, a non-empty JSON object.
	SessionSettings map[string]any `json:"session_settings"`
}

// AgentState describes the current position of one agent session.
//
// The AgentState Attribute holds one value. Every Step that changes the
// session position replaces it. Decoding fills the contract defaults for
// absent fields: TurnCount 0, NextMessageSequence 1, InteractionMode chat.
type AgentState struct {
	// Status is what the session is doing now.
	Status AgentSessionStatus `json:"status"`
	// TurnCount is the number of turns that completed or failed.
	TurnCount int64 `json:"turn_count"`
	// ActiveTurnID is "turn-<n>" of the running turn, or nil between turns. n is TurnCount + 1
	// when the turn starts.
	ActiveTurnID *string `json:"active_turn_id"`
	// NextMessageSequence is the sequence the next stored message receives. Sequences start at 1
	// and have no gaps.
	NextMessageSequence int64 `json:"next_message_sequence"`
	// LastTurnError is the error text of the last finished turn when it failed, or nil when it
	// completed.
	LastTurnError *string `json:"last_turn_error"`
	// InteractionMode is the kind of the active turn, or of the last turn between turns. This Go
	// core writes InteractionModeChat.
	InteractionMode InteractionMode `json:"interaction_mode"`
	// AcceptsTurnControl reports whether steering may end the active turn. This Go core has no
	// steering, so it always writes false, which makes steer_user_message and cancel_turn
	// refuse a turn it started or advanced.
	AcceptsTurnControl bool `json:"accepts_turn_control"`
}

// UnmarshalJSON decodes the wire object, filling contract defaults for absent fields.
//
// It returns an error for a missing status, a wrong JSON type, or an unknown enum value.
func (state *AgentState) UnmarshalJSON(data []byte) error {
	type wireAgentState AgentState
	decoded := wireAgentState{NextMessageSequence: 1, InteractionMode: InteractionModeChat}
	if err := decodeWireObject(data, &decoded, "AgentState", "status"); err != nil {
		return err
	}
	if decoded.InteractionMode == "" {
		decoded.InteractionMode = InteractionModeChat
	}
	*state = AgentState(decoded)
	return nil
}

// AgentMessage stores one framework item of the session conversation.
//
// The AgentMessages AttributeMap holds one value per message of the newest
// messages, keyed by MessageSequenceKey(Sequence); older messages are in
// AgentMessageChunk values. FrameworkItem is the replay truth: the runner
// writes it and reads it back unchanged. The other fields are a display
// projection.
type AgentMessage struct {
	// Sequence is the position in the session, starting at 1.
	Sequence int64 `json:"sequence"`
	// TurnID is "turn-<n>" of the turn that produced the message.
	TurnID string `json:"turn_id"`
	// Role is the author of the message.
	Role MessageRole `json:"role"`
	// Text is the display text, or nil for an item without text.
	Text *string `json:"text"`
	// ToolCallID is the tool call ID of a tool request or result item, or nil.
	ToolCallID *string `json:"tool_call_id"`
	// ToolName is the tool name of a tool request or result item, or nil.
	ToolName *string `json:"tool_name"`
	// ToolArguments is the tool arguments as JSON text for a tool request item, or nil.
	ToolArguments *string `json:"tool_arguments"`
	// ToolOutput is the tool result text for a tool result item, or nil.
	ToolOutput *string `json:"tool_output"`
	// FrameworkItem is the framework-native JSON text, replayed unchanged.
	FrameworkItem string `json:"framework_item"`
	// CreatedAt is the RFC 3339 time when the session stored the message, in UTC with
	// milliseconds and "Z".
	CreatedAt string `json:"created_at"`
	// Images are the item's images, for display. The Go core never sets them; it keeps the field
	// so a message written by another core round-trips.
	Images []AgentImageReference `json:"images"`
	// Origin is why the session wrote this user message itself, or nil for a message that a user
	// or the start input sent, for assistant and tool messages, and for every message stored
	// before 0.6.0. Show a message with an Origin as a session note, not as text the user typed.
	// Only the session sets it. The Go core sets AgentMessageOriginTurnFailureNote; the other
	// values come from Workers of another core. Since 0.6.0.
	Origin *AgentMessageOrigin `json:"origin"`
	// SenderName is the display name that the sender of this user message declared, from
	// AgentUserMessage.SenderName, or nil. The sender chose it; it is not an authenticated
	// identity. Text holds the message without the name, and the model saw it as
	// "<SenderName>: <Text>". Encoding omits it when nil. Since 0.6.2.
	SenderName *string `json:"sender_name,omitempty"`
}

// MarshalJSON encodes the wire object, writing an empty Images as [] rather than null.
//
// It returns an error only when a field cannot be encoded.
func (message AgentMessage) MarshalJSON() ([]byte, error) {
	type wireAgentMessage AgentMessage
	encoded := wireAgentMessage(message)
	encoded.Images = nonNilSlice(encoded.Images)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field, a wrong JSON type, or an unknown role.
func (message *AgentMessage) UnmarshalJSON(data []byte) error {
	type wireAgentMessage AgentMessage
	var decoded wireAgentMessage
	if err := decodeWireObject(data, &decoded, "AgentMessage",
		"sequence", "turn_id", "role", "framework_item", "created_at"); err != nil {
		return err
	}
	decoded.Images = nonNilSlice(decoded.Images)
	*message = AgentMessage(decoded)
	return nil
}

// AgentImageReference points to one image in a media store by the SHA-256 of its bytes.
//
// The Go core has no media store; it keeps references so values written by another core
// round-trip.
type AgentImageReference struct {
	// Type is always "image". A missing type decodes as "image".
	Type string `json:"type"`
	// URI is where the store keeps the bytes, ending in "/sha256/<SHA256>".
	URI string `json:"uri"`
	// MimeType is "image/png", "image/jpeg", "image/gif", or "image/webp".
	MimeType string `json:"mime_type"`
	// SHA256 is the SHA-256 of the bytes as 64 lowercase hex digits.
	SHA256 string `json:"sha256"`
	// SizeBytes is the size of the bytes.
	SizeBytes int64 `json:"size_bytes"`
}

// UnmarshalJSON decodes the wire object; a missing type decodes as "image".
//
// It returns an error for a missing required field or a wrong JSON type.
func (reference *AgentImageReference) UnmarshalJSON(data []byte) error {
	type wireAgentImageReference AgentImageReference
	decoded := wireAgentImageReference{Type: "image"}
	if err := decodeWireObject(data, &decoded, "AgentImageReference",
		"uri", "mime_type", "sha256", "size_bytes"); err != nil {
		return err
	}
	*reference = AgentImageReference(decoded)
	return nil
}

// AgentMessageChunk holds consecutive archived messages of a session.
//
// The AgentMessageChunks AttributeMap holds one value per archive chunk, keyed
// by MessageSequenceKey of its first message. A chunk is written once, when
// the oldest messages of the AgentMessages window move into it, or when an
// oversized message is stored straight into a chunk of its own, and never
// changes. It holds the messages its AgentMessageChunkRange in
// AgentMessageIndex.Chunks names: at most the message chunk size, and at most
// the message chunk max bytes unless one message is larger by itself.
type AgentMessageChunk struct {
	// Messages are the chunk's messages in sequence order, without gaps.
	Messages []AgentMessage `json:"messages"`
}

// MarshalJSON encodes the wire object, writing an empty Messages as [].
//
// It returns an error only when a field cannot be encoded.
func (chunk AgentMessageChunk) MarshalJSON() ([]byte, error) {
	type wireAgentMessageChunk AgentMessageChunk
	encoded := wireAgentMessageChunk(chunk)
	encoded.Messages = nonNilSlice(encoded.Messages)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (chunk *AgentMessageChunk) UnmarshalJSON(data []byte) error {
	type wireAgentMessageChunk AgentMessageChunk
	var decoded wireAgentMessageChunk
	if err := decodeWireObject(data, &decoded, "AgentMessageChunk", "messages"); err != nil {
		return err
	}
	decoded.Messages = nonNilSlice(decoded.Messages)
	*chunk = AgentMessageChunk(decoded)
	return nil
}

// AgentMessageChunkRange names the messages of one archive chunk.
//
// The chunk is the AgentMessageChunks instance keyed by
// MessageSequenceKey(FirstSequence). It holds every message from
// FirstSequence through LastSequence.
type AgentMessageChunkRange struct {
	// FirstSequence is the sequence of the chunk's first message and its instance key.
	FirstSequence int64 `json:"first_sequence"`
	// LastSequence is the sequence of the chunk's last message.
	LastSequence int64 `json:"last_sequence"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (chunkRange *AgentMessageChunkRange) UnmarshalJSON(data []byte) error {
	type wireAgentMessageChunkRange AgentMessageChunkRange
	var decoded wireAgentMessageChunkRange
	if err := decodeWireObject(data, &decoded, "AgentMessageChunkRange",
		"first_sequence", "last_sequence"); err != nil {
		return err
	}
	*chunkRange = AgentMessageChunkRange(decoded)
	return nil
}

// AgentMessageIndex says where each stored message of a session lives.
//
// The AgentMessageIndex Attribute holds one value, and a Step writes it only
// when it adds or deletes a chunk. Messages from WindowFirstSequence on are in
// the AgentMessages window, one instance per message, except an oversized
// message, which is stored straight into a single-message chunk. Older
// messages, from FirstSequence on, are in the AgentMessageChunks instances that
// Chunks lists, in order and without gaps. A session without an index, a new
// one or one that a dex-agents 0.1.0 Worker wrote, has every message in
// AgentMessages from sequence 1.
type AgentMessageIndex struct {
	// Chunks lists every kept chunk in sequence order. The chunks before WindowFirstSequence
	// cover FirstSequence up to it without gaps; a chunk from WindowFirstSequence on holds one
	// oversized message.
	Chunks []AgentMessageChunkRange `json:"chunks"`
	// FirstSequence is the sequence of the oldest stored message: the first chunk's first
	// message, or WindowFirstSequence when no chunk is kept. Retention in a core with compaction
	// advances it when it deletes the oldest chunk.
	FirstSequence int64 `json:"first_sequence"`
	// WindowFirstSequence is the sequence of the first message in the window: every older
	// message is in a chunk.
	WindowFirstSequence int64 `json:"window_first_sequence"`
}

// MarshalJSON encodes the wire object, writing an empty Chunks as [].
//
// It returns an error only when a field cannot be encoded.
func (index AgentMessageIndex) MarshalJSON() ([]byte, error) {
	type wireAgentMessageIndex AgentMessageIndex
	encoded := wireAgentMessageIndex(index)
	encoded.Chunks = nonNilSlice(encoded.Chunks)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (index *AgentMessageIndex) UnmarshalJSON(data []byte) error {
	type wireAgentMessageIndex AgentMessageIndex
	var decoded wireAgentMessageIndex
	if err := decodeWireObject(data, &decoded, "AgentMessageIndex",
		"chunks", "first_sequence", "window_first_sequence"); err != nil {
		return err
	}
	decoded.Chunks = nonNilSlice(decoded.Chunks)
	*index = AgentMessageIndex(decoded)
	return nil
}

// TurnReplySequence records where the replies of a turn start.
//
// The TurnReplySequence Attribute holds one value. The Step that starts a turn
// writes it with the first sequence after the turn's user messages, so turn
// events count the turn's replies without reading its older messages. A value
// for another turn, as a dex-agents 0.1.0 Worker leaves it, does not apply.
type TurnReplySequence struct {
	// TurnID is "turn-<n>" of the turn.
	TurnID string `json:"turn_id"`
	// Sequence is the first sequence after the turn's user messages.
	Sequence int64 `json:"sequence"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (replySequence *TurnReplySequence) UnmarshalJSON(data []byte) error {
	type wireTurnReplySequence TurnReplySequence
	var decoded wireTurnReplySequence
	if err := decodeWireObject(data, &decoded, "TurnReplySequence", "turn_id", "sequence"); err != nil {
		return err
	}
	*replySequence = TurnReplySequence(decoded)
	return nil
}

// QuestionAnswer records how one question that a tool asked was resolved.
//
// This Go core carries it on records unchanged; it never asks tool questions.
type QuestionAnswer struct {
	// InterruptID is "<tool_call_id>-input-<n>" of the resolved question.
	InterruptID string `json:"interrupt_id"`
	// Question is the question text the tool asked.
	Question string `json:"question"`
	// Answer is the user's answer, or the decline or deadline text.
	Answer string `json:"answer"`
	// Resolution is how the question was resolved. It defaults to answered.
	Resolution QuestionResolution `json:"resolution"`
}

// UnmarshalJSON decodes the wire object, filling Resolution when absent.
//
// It returns an error for a missing required field, a wrong JSON type, or an unknown resolution.
func (answer *QuestionAnswer) UnmarshalJSON(data []byte) error {
	type wireQuestionAnswer QuestionAnswer
	decoded := wireQuestionAnswer{Resolution: QuestionResolutionAnswered}
	if err := decodeWireObject(data, &decoded, "QuestionAnswer", "interrupt_id", "question", "answer"); err != nil {
		return err
	}
	if decoded.Resolution == "" {
		decoded.Resolution = QuestionResolutionAnswered
	}
	*answer = QuestionAnswer(decoded)
	return nil
}

// AgentToolCall records one tool call that a turn deferred to a Dex Step.
//
// The AgentToolCalls AttributeMap holds one record per deferred call, keyed by
// tool call ID. Records exist only while their turn runs; the session deletes
// them when the turn ends, because the results are already messages. The
// fields after TraceContext belong to contract additions that this Go core
// keeps unchanged when it rewrites a record.
type AgentToolCall struct {
	// ToolCallID is the framework tool call ID. It is non-empty and has no "/".
	ToolCallID string `json:"tool_call_id"`
	// TurnID is "turn-<n>" of the turn that deferred the call.
	TurnID string `json:"turn_id"`
	// ToolName is the name of the tool the model asked for.
	ToolName string `json:"tool_name"`
	// Arguments is the tool arguments as JSON text.
	Arguments string `json:"arguments"`
	// Effect decides which Step runs the call.
	Effect ToolEffect `json:"effect"`
	// RequiresApproval reports whether resolve_interrupt must approve the call.
	RequiresApproval bool `json:"requires_approval"`
	// Status is the lifecycle state of the call.
	Status ToolCallStatus `json:"status"`
	// Output is the tool output text after succeeded, or nil.
	Output *string `json:"output"`
	// Error is the ToolExecutionError message after failed, or the recovery detail of the failed
	// Step after outcome_unknown, or nil. A tool Step that failed before the tool ran records failed
	// with ToolCallNotRunErrorPrefix, ": ", and the recovery detail.
	Error *string `json:"error"`
	// RejectionMessage is the message after rejected, or nil when the person gave none.
	RejectionMessage *string `json:"rejection_message"`
	// DeferralOrder is the zero-based order in which the turn deferred the call.
	DeferralOrder int64 `json:"deferral_order"`
	// ToolSourceReference is opaque text from DeferredToolCall.ToolSourceReference, or nil.
	ToolSourceReference *string `json:"tool_source_reference"`
	// TraceContext is the W3C traceparent of the activation root that the call's tool spans join,
	// or nil.
	TraceContext *string `json:"trace_context"`
	// WaitUntil is the RFC 3339 end of a durable_wait call's Timer, or nil.
	WaitUntil *string `json:"wait_until"`
	// InputContinuation is opaque text from the tool's last input request, or nil.
	InputContinuation *string `json:"input_continuation"`
	// InputAnswers are the answers to the tool's questions, oldest first.
	InputAnswers []QuestionAnswer `json:"input_answers"`
	// InputQuestion is the tool's pending question, or nil.
	InputQuestion *string `json:"input_question"`
	// InputChoices are the choices of that pending question.
	InputChoices []string `json:"input_choices"`
	// InputAllowsFreeText reports whether that pending question accepts free text. It defaults to
	// true.
	InputAllowsFreeText bool `json:"input_allows_free_text"`
	// CancellationReason is why steering abandoned the call, on a rejected record, or nil.
	CancellationReason *ToolCallCancellationReason `json:"cancellation_reason"`
	// TaskID is the remote task ID of a call that started one, or nil.
	TaskID *string `json:"task_id"`
	// TaskPollIntervalSeconds is the whole seconds until the task's next poll, or nil.
	TaskPollIntervalSeconds *int64 `json:"task_poll_interval_seconds"`
	// TaskAnswerPending reports whether the last answer has not reached the task yet.
	TaskAnswerPending bool `json:"task_answer_pending"`
	// OrderedWrite reports whether the call is a connector write that runs in the model's order.
	// The Go core never sets it; it keeps the field so a record written by another core round-trips.
	OrderedWrite bool `json:"ordered_write"`
	// WriteQueued reports whether the call waits for an earlier ordered write of its turn.
	WriteQueued bool `json:"write_queued"`
	// Skipped reports whether write ordering skipped the call after an earlier write failed.
	Skipped bool `json:"skipped"`
	// TruncatedOutputBytes is the size of the full output when Output holds a truncated copy, or nil.
	TruncatedOutputBytes *int64 `json:"truncated_output_bytes"`
	// Images are the images a tool returned with its output. The Go core never sets them; it keeps
	// the field so a record written by another core round-trips.
	Images []AgentImageReference `json:"images"`
	// RunnerToolState is opaque text from ToolExecutionResult.RunnerToolState after succeeded, which only
	// the runner reads, or nil. The session never shows it to the model or in trace attributes. A record
	// written before 0.5.0 has none, and a Worker before 0.5.0 ignores it. Since 0.5.0.
	RunnerToolState *string `json:"runner_tool_state"`
	// ApprovalDisplay is what a call that needs approval acts on and does, from its tool's source, or
	// nil. The Go core never sets it; it keeps the field so a record written by another core
	// round-trips. Since 0.6.0.
	ApprovalDisplay *AgentInterruptDisplay `json:"approval_display"`
}

// MarshalJSON encodes the wire object, writing empty lists as [] rather than null.
//
// It returns an error only when a field cannot be encoded.
func (toolCall AgentToolCall) MarshalJSON() ([]byte, error) {
	type wireAgentToolCall AgentToolCall
	encoded := wireAgentToolCall(toolCall)
	encoded.InputAnswers = nonNilSlice(encoded.InputAnswers)
	encoded.InputChoices = nonNilSlice(encoded.InputChoices)
	encoded.Images = nonNilSlice(encoded.Images)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object, filling contract defaults for absent fields.
//
// It returns an error for a missing required field, a wrong JSON type, or an unknown enum value.
func (toolCall *AgentToolCall) UnmarshalJSON(data []byte) error {
	type wireAgentToolCall AgentToolCall
	decoded := wireAgentToolCall{InputAllowsFreeText: true}
	if err := decodeWireObject(data, &decoded, "AgentToolCall",
		"tool_call_id", "turn_id", "tool_name", "arguments", "effect", "requires_approval", "status"); err != nil {
		return err
	}
	decoded.InputAnswers = nonNilSlice(decoded.InputAnswers)
	decoded.InputChoices = nonNilSlice(decoded.InputChoices)
	decoded.Images = nonNilSlice(decoded.Images)
	*toolCall = AgentToolCall(decoded)
	return nil
}

// AgentInterruptDisplay says what one tool approval acts on and what the call does.
//
// A tool's source builds it from the call's concrete arguments. An approval UI shows Target and
// Summary above the arguments, and Preview when it is set. Without one, show the concrete arguments
// only. The text never promises how often the call runs or what it achieves, and never holds a
// credential. Since 0.6.0.
//
//	if interrupt.Display != nil {
//		fmt.Println(interrupt.Display.Summary)
//	}
type AgentInterruptDisplay struct {
	// Target is what the call acts on, such as a workspace path, a shell command's working
	// directory, or a connector and its connection ("slack/support-slack").
	Target string `json:"target"`
	// Summary is one sentence that says what the call does to Target, built from the call's
	// concrete arguments.
	Summary string `json:"summary"`
	// Preview is longer text to show with the summary, such as a unified diff of a local file
	// write against the file as it was when the call was requested, or nil.
	Preview *string `json:"preview"`
}

// UnmarshalJSON decodes the wire object, requiring target and summary.
//
// It returns an error for a missing required field or a wrong JSON type.
func (display *AgentInterruptDisplay) UnmarshalJSON(data []byte) error {
	type wireAgentInterruptDisplay AgentInterruptDisplay
	var decoded wireAgentInterruptDisplay
	if err := decodeWireObject(data, &decoded, "AgentInterruptDisplay", "target", "summary"); err != nil {
		return err
	}
	*display = AgentInterruptDisplay(decoded)
	return nil
}

// AgentInterrupt describes one decision the session waits for a person to make.
//
// The PendingInterrupts Attribute holds the unresolved interrupts. Answer a
// tool approval with the resolve_interrupt RPC.
type AgentInterrupt struct {
	// InterruptID is the ID passed to resolve_interrupt. It equals the tool call ID for a tool
	// approval.
	InterruptID string `json:"interrupt_id"`
	// Kind is why the turn paused.
	Kind InterruptKind `json:"kind"`
	// ToolCallID is the tool call that waits.
	ToolCallID string `json:"tool_call_id"`
	// ToolName is the name of that tool.
	ToolName string `json:"tool_name"`
	// Arguments is the tool arguments as JSON text.
	Arguments string `json:"arguments"`
	// CreatedAt is the RFC 3339 time when the interrupt was raised.
	CreatedAt string `json:"created_at"`
	// Deadline is the RFC 3339 time after which the session rejects the call with
	// "approval deadline passed", or nil for no deadline.
	Deadline *string `json:"deadline"`
	// Question is the question text of a question interrupt, or nil.
	Question *string `json:"question"`
	// Choices are the answers to offer for a question interrupt. Empty means free text only.
	Choices []string `json:"choices"`
	// AllowsFreeText reports whether an answer may be outside Choices. It defaults to true.
	AllowsFreeText bool `json:"allows_free_text"`
	// Display is what a tool approval acts on and does, from its tool's source, or nil when the
	// source gave none and for a question. The Go core never sets it; it keeps the field so an
	// interrupt written by another core round-trips. Since 0.6.0.
	Display *AgentInterruptDisplay `json:"display"`
}

// MarshalJSON encodes the wire object, writing an empty Choices as [].
//
// It returns an error only when a field cannot be encoded.
func (interrupt AgentInterrupt) MarshalJSON() ([]byte, error) {
	type wireAgentInterrupt AgentInterrupt
	encoded := wireAgentInterrupt(interrupt)
	encoded.Choices = nonNilSlice(encoded.Choices)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object, filling contract defaults for absent fields.
//
// It returns an error for a missing required field, a wrong JSON type, or an unknown kind.
func (interrupt *AgentInterrupt) UnmarshalJSON(data []byte) error {
	type wireAgentInterrupt AgentInterrupt
	decoded := wireAgentInterrupt{AllowsFreeText: true}
	if err := decodeWireObject(data, &decoded, "AgentInterrupt",
		"interrupt_id", "kind", "tool_call_id", "tool_name", "arguments", "created_at"); err != nil {
		return err
	}
	decoded.Choices = nonNilSlice(decoded.Choices)
	*interrupt = AgentInterrupt(decoded)
	return nil
}

// AgentInterruptResolution carries one answer to an interrupt through InterruptResolutions.
//
// resolve_interrupt publishes it to the ChannelMap instance named by the
// interrupt ID, and AwaitToolApproval consumes it.
type AgentInterruptResolution struct {
	// Approved reports whether the tool call may run.
	Approved bool `json:"approved"`
	// RejectionMessage is the message the model sees after a rejection, or nil.
	RejectionMessage *string `json:"rejection_message"`
	// Answer is the answer to a question interrupt, or nil.
	Answer *string `json:"answer"`
}

// UnmarshalJSON decodes the wire object and checks its required field.
//
// It returns an error for a missing approved field or a wrong JSON type.
func (resolution *AgentInterruptResolution) UnmarshalJSON(data []byte) error {
	type wireAgentInterruptResolution AgentInterruptResolution
	var decoded wireAgentInterruptResolution
	if err := decodeWireObject(data, &decoded, "AgentInterruptResolution", "approved"); err != nil {
		return err
	}
	*resolution = AgentInterruptResolution(decoded)
	return nil
}

// ResolveInterruptRequest answers one pending interrupt through the resolve_interrupt RPC.
type ResolveInterruptRequest struct {
	// InterruptID is AgentInterrupt.InterruptID of a pending interrupt.
	InterruptID string `json:"interrupt_id"`
	// Approved is true to run the tool call and false to reject it.
	Approved bool `json:"approved"`
	// RejectionMessage is the message the model sees after a rejection. Nil makes the model see
	// "The user rejected this tool call."
	RejectionMessage *string `json:"rejection_message"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (request *ResolveInterruptRequest) UnmarshalJSON(data []byte) error {
	type wireResolveInterruptRequest ResolveInterruptRequest
	var decoded wireResolveInterruptRequest
	if err := decodeWireObject(data, &decoded, "ResolveInterruptRequest", "interrupt_id", "approved"); err != nil {
		return err
	}
	*request = ResolveInterruptRequest(decoded)
	return nil
}

// AgentUsage counts model usage, for one model call or for the whole session.
//
// A runner returns the usage of one model call in TurnCompleted or
// ToolCallsRequested, and the session adds it to the AgentUsage Attribute.
// Report zero for a count the framework does not expose.
type AgentUsage struct {
	// ModelCalls is the number of model requests.
	ModelCalls int64 `json:"model_calls"`
	// InputTokens counts input tokens, including cached input tokens.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens counts output tokens, including reasoning tokens.
	OutputTokens int64 `json:"output_tokens"`
	// CachedInputTokens counts input tokens served from a provider cache.
	CachedInputTokens int64 `json:"cached_input_tokens"`
	// ReasoningTokens counts output tokens spent on reasoning.
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// CombinedWith returns the field-by-field sum of this usage and another.
//
// Neither input changes.
func (usage AgentUsage) CombinedWith(other AgentUsage) AgentUsage {
	return AgentUsage{
		ModelCalls:        usage.ModelCalls + other.ModelCalls,
		InputTokens:       usage.InputTokens + other.InputTokens,
		OutputTokens:      usage.OutputTokens + other.OutputTokens,
		CachedInputTokens: usage.CachedInputTokens + other.CachedInputTokens,
		ReasoningTokens:   usage.ReasoningTokens + other.ReasoningTokens,
	}
}

// AgentUserMessage sends one user message through the send_user_message RPC.
//
// The RPC queues the message in QueuedUserMessages. The session consumes
// queued messages in order, one per turn, after the current turn ends.
type AgentUserMessage struct {
	// Text is the message text. Whitespace-only text is rejected.
	Text string `json:"text"`
	// PlanMode asks for a planning turn. This Go core has no plan mode, so send_user_message
	// returns false for a message with PlanMode set.
	PlanMode bool `json:"plan_mode"`
	// Images are the images the user attached. This Go core has no image input, so
	// send_user_message returns false for a message with images.
	Images []AgentImageReference `json:"images"`
	// SenderName is a display name the sender declared, such as a person who sends from Dex
	// Web, or nil for none. The model sees the message as "<SenderName>: <Text>", and UIs show
	// the name next to the message. The sender chooses it: it is not authenticated.
	// send_user_message returns false for a name that IsValidSenderName refuses. Encoding
	// omits it when nil. A Worker before 0.6.2 ignores it. Since 0.6.2.
	SenderName *string `json:"sender_name,omitempty"`
}

// MarshalJSON encodes the wire object, writing an empty Images as [] rather than null.
//
// It returns an error only when a field cannot be encoded.
func (message AgentUserMessage) MarshalJSON() ([]byte, error) {
	type wireAgentUserMessage AgentUserMessage
	encoded := wireAgentUserMessage(message)
	encoded.Images = nonNilSlice(encoded.Images)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object; a null plan_mode decodes as false.
//
// It returns an error for a missing text field or a wrong JSON type.
func (message *AgentUserMessage) UnmarshalJSON(data []byte) error {
	type wireAgentUserMessage AgentUserMessage
	var decoded wireAgentUserMessage
	if err := decodeWireObject(data, &decoded, "AgentUserMessage", "text"); err != nil {
		return err
	}
	decoded.Images = nonNilSlice(decoded.Images)
	*message = AgentUserMessage(decoded)
	return nil
}

// AgentPlanTask describes one task of an AgentPlan.
type AgentPlanTask struct {
	// Content is what the task does.
	Content string `json:"content"`
	// Status is where the task stands.
	Status PlanTaskStatus `json:"status"`
}

// AgentPlan holds the session's plan. This Go core reads it in snapshots only.
type AgentPlan struct {
	// Revision is the plan revision, starting at 1.
	Revision int64 `json:"revision"`
	// Status is where the plan stands.
	Status PlanStatus `json:"status"`
	// Tasks are every task in order.
	Tasks []AgentPlanTask `json:"tasks"`
	// UpdatedAt is the RFC 3339 time of the last change.
	UpdatedAt string `json:"updated_at"`
}

// MarshalJSON encodes the wire object, writing an empty Tasks as [].
//
// It returns an error only when a field cannot be encoded.
func (plan AgentPlan) MarshalJSON() ([]byte, error) {
	type wireAgentPlan AgentPlan
	encoded := wireAgentPlan(plan)
	encoded.Tasks = nonNilSlice(encoded.Tasks)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field, a wrong JSON type, or an unknown status.
func (plan *AgentPlan) UnmarshalJSON(data []byte) error {
	type wireAgentPlan AgentPlan
	var decoded wireAgentPlan
	if err := decodeWireObject(data, &decoded, "AgentPlan", "revision", "status", "updated_at"); err != nil {
		return err
	}
	decoded.Tasks = nonNilSlice(decoded.Tasks)
	*plan = AgentPlan(decoded)
	return nil
}

// PlanExecutionRequest carries one approved plan execution. This Go core never writes it.
type PlanExecutionRequest struct {
	// Revision is the approved AgentPlan.Revision.
	Revision int64 `json:"revision"`
}

// ExecutePlanRequest approves one plan revision through the execute_plan RPC.
//
// This Go core does not serve execute_plan; Workers of a core with plan mode do.
// Since 0.6.0.
type ExecutePlanRequest struct {
	// Revision is the AgentPlan.Revision that the user reviewed.
	Revision int64 `json:"revision"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing or null revision field, or a wrong JSON type.
func (request *ExecutePlanRequest) UnmarshalJSON(data []byte) error {
	type wireExecutePlanRequest ExecutePlanRequest
	var decoded wireExecutePlanRequest
	if err := decodeNonNullWireObject(data, &decoded, "ExecutePlanRequest", "revision"); err != nil {
		return err
	}
	*request = ExecutePlanRequest(decoded)
	return nil
}

// SteerUserMessageRequest steers the running turn through the steer_user_message RPC.
//
// Set exactly one field: MessageID moves a queued message, and Text steers
// with new text. The RPC returns false when both or neither are set. This Go
// core does not serve steer_user_message; Workers of a core with steering do.
// Since 0.6.0.
type SteerUserMessageRequest struct {
	// MessageID is PendingUserMessage.MessageID of a queued message, or nil.
	MessageID *string `json:"message_id"`
	// Text is the new message text, or nil.
	Text *string `json:"text"`
	// SenderName is the declared display name of the sender of Text, as
	// AgentUserMessage.SenderName describes it, or nil. The RPC refuses it together with
	// MessageID: a queued message keeps its own name. Encoding omits it when nil. Since 0.6.2.
	SenderName *string `json:"sender_name,omitempty"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing message_id or text field, or a wrong JSON type. Either
// field may be null.
func (request *SteerUserMessageRequest) UnmarshalJSON(data []byte) error {
	type wireSteerUserMessageRequest SteerUserMessageRequest
	var decoded wireSteerUserMessageRequest
	if err := decodeWireObject(data, &decoded, "SteerUserMessageRequest", "message_id", "text"); err != nil {
		return err
	}
	*request = SteerUserMessageRequest(decoded)
	return nil
}

// DeleteQueuedMessageRequest deletes one queued user message through the delete_queued_message RPC.
//
// This Go core does not serve delete_queued_message; Workers of a core with
// steering do. Since 0.6.0.
type DeleteQueuedMessageRequest struct {
	// MessageID is PendingUserMessage.MessageID from get_agent_snapshot.
	MessageID string `json:"message_id"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing or null message_id field, or a wrong JSON type.
func (request *DeleteQueuedMessageRequest) UnmarshalJSON(data []byte) error {
	type wireDeleteQueuedMessageRequest DeleteQueuedMessageRequest
	var decoded wireDeleteQueuedMessageRequest
	if err := decodeNonNullWireObject(data, &decoded, "DeleteQueuedMessageRequest", "message_id"); err != nil {
		return err
	}
	*request = DeleteQueuedMessageRequest(decoded)
	return nil
}

// CancelTurnRequest stops the running turn through the cancel_turn RPC.
//
// This Go core does not serve cancel_turn; Workers of a core with turn
// control do. Since 0.6.0.
type CancelTurnRequest struct {
	// TurnID is the turn to stop, or nil for the active turn. The RPC returns false when it names
	// another turn.
	TurnID *string `json:"turn_id"`
}

// UnmarshalJSON decodes the wire object; a missing turn_id decodes as nil.
//
// It returns an error for a wrong JSON type.
func (request *CancelTurnRequest) UnmarshalJSON(data []byte) error {
	type wireCancelTurnRequest CancelTurnRequest
	var decoded wireCancelTurnRequest
	if err := decodeWireObject(data, &decoded, "CancelTurnRequest"); err != nil {
		return err
	}
	*request = CancelTurnRequest(decoded)
	return nil
}

// AnswerQuestionRequest answers one pending question interrupt through the answer_question RPC.
//
// Decline a question with resolve_interrupt and Approved false instead. This
// Go core does not serve answer_question; Workers of a core with questions
// do. Since 0.6.0.
type AnswerQuestionRequest struct {
	// InterruptID is AgentInterrupt.InterruptID of a pending question interrupt.
	InterruptID string `json:"interrupt_id"`
	// Answer is the answer text. The RPC returns false for blank text, and for text outside
	// AgentInterrupt.Choices when AgentInterrupt.AllowsFreeText is false.
	Answer string `json:"answer"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing or null interrupt_id or answer field, or a wrong JSON type.
func (request *AnswerQuestionRequest) UnmarshalJSON(data []byte) error {
	type wireAnswerQuestionRequest AnswerQuestionRequest
	var decoded wireAnswerQuestionRequest
	if err := decodeNonNullWireObject(data, &decoded, "AnswerQuestionRequest", "interrupt_id", "answer"); err != nil {
		return err
	}
	*request = AnswerQuestionRequest(decoded)
	return nil
}

// ToolCallCompleted reports that one tool call of the active turn has a final record.
//
// Every tool call publishes exactly one to the internal ToolCallResults
// Channel, in the commit that makes its record final. ResumeTurn waits for
// one per tool call the model requested.
type ToolCallCompleted struct {
	// ToolCallID is the tool call whose AgentToolCall record is final.
	ToolCallID string `json:"tool_call_id"`
}

// PendingUserMessage shows one user message that waits in a Channel, with its Channel message ID.
type PendingUserMessage struct {
	// MessageID is the Channel message ID that Dex assigned when the message was published.
	MessageID string `json:"message_id"`
	// Message is the user message.
	Message AgentUserMessage `json:"message"`
}

// TurnControlRequest wakes a waiting session Step. This Go core never publishes it.
type TurnControlRequest struct {
	// Kind is the RPC that published it.
	Kind TurnControlKind `json:"kind"`
}

// TurnCancellationRequest records that cancel_turn asked to stop one turn. This Go core never
// writes it.
type TurnCancellationRequest struct {
	// TurnID is the turn to stop.
	TurnID string `json:"turn_id"`
	// RequestedAt is the RFC 3339 time of the request.
	RequestedAt string `json:"requested_at"`
}

// ToolCallCancellation wakes the Step that waits on one tool call. This Go core never publishes
// it.
type ToolCallCancellation struct {
	// Reason is why the turn ends.
	Reason ToolCallCancellationReason `json:"reason"`
}

// AgentEvent carries one live update on the AgentEvents Stream.
//
// Streams are best effort. A Step retry can repeat events, and a lost
// consumer can miss them. Rebuild durable state from get_agent_snapshot. Group
// live text by (TurnID, StepExecutionID, Attempt): within one StepExecutionID,
// keep only the events of the highest Attempt seen. After turn_completed or
// turn_failed, replace the turn's live text with the stored messages.
type AgentEvent struct {
	// Type is one of the AgentEventType values, or an adapter-prefixed type.
	Type string `json:"type"`
	// TurnID is the turn the event belongs to, or nil.
	TurnID *string `json:"turn_id"`
	// Attempt is the one-based attempt of the Step that wrote the event.
	Attempt int64 `json:"attempt"`
	// Payload is the event-specific JSON object text, with sorted keys.
	Payload string `json:"payload"`
	// StepExecutionID is the Dex Step execution ID of the Step that wrote the event, shared by
	// every attempt. Events written before this field existed decode with "".
	StepExecutionID string `json:"step_execution_id"`
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (event *AgentEvent) UnmarshalJSON(data []byte) error {
	type wireAgentEvent AgentEvent
	var decoded wireAgentEvent
	if err := decodeWireObject(data, &decoded, "AgentEvent", "type", "turn_id", "attempt", "payload"); err != nil {
		return err
	}
	*event = AgentEvent(decoded)
	return nil
}

// ContextSummary holds the summary that compaction wrote. This Go core reads it in snapshots only.
type ContextSummary struct {
	// Generation counts the summaries the session has written, starting at 1.
	Generation int64 `json:"generation"`
	// FirstSequence is the sequence of the first summarized message. Always 1.
	FirstSequence int64 `json:"first_sequence"`
	// LastSequence is the sequence of the last summarized message.
	LastSequence int64 `json:"last_sequence"`
	// Text is the summary text.
	Text string `json:"text"`
	// CreatedAt is the RFC 3339 time when the summary was written.
	CreatedAt string `json:"created_at"`
}

// CompactContextInput names the messages one CompactContext Step summarizes.
//
// RunModel and RecordTurnFailure take a pointer to it as input, because a
// Python Registry requires a failure route to share its Step's input type.
// This Go core always passes nil, which is JSON null.
type CompactContextInput struct {
	// ThroughSequence is the sequence of the last message to summarize.
	ThroughSequence int64 `json:"through_sequence"`
}

// AgentSnapshot returns everything a UI needs to render one agent session.
//
// The get_agent_snapshot RPC returns it from committed state, so a retained
// closed session stays readable.
type AgentSnapshot struct {
	// State is the current session position, or nil before StartAgentSession committed.
	State *AgentState `json:"state"`
	// Messages are the last 200 messages in sequence order.
	Messages []AgentMessage `json:"messages"`
	// PendingInterrupts are the unresolved interrupts.
	PendingInterrupts []AgentInterrupt `json:"pending_interrupts"`
	// ToolCalls are the active turn's records, in DeferralOrder.
	ToolCalls []AgentToolCall `json:"tool_calls"`
	// Usage is the model usage of the whole session.
	Usage AgentUsage `json:"usage"`
	// QueuedMessageCount is the number of user messages queued for later turns.
	QueuedMessageCount int64 `json:"queued_message_count"`
	// Plan is the session's plan, or nil.
	Plan *AgentPlan `json:"plan"`
	// QueuedMessages are the queued messages with their Channel message IDs, oldest first.
	QueuedMessages []PendingUserMessage `json:"queued_messages"`
	// SteeredMessages are steered messages not applied yet, oldest first. This Go core reads them
	// from the SteeredUserMessages Channel, which only a Worker with steering consumes.
	SteeredMessages []PendingUserMessage `json:"steered_messages"`
	// ContextSummary is the current summary, or nil.
	ContextSummary *ContextSummary `json:"context_summary"`
	// SessionSettings are the settings the next turn starts with, or nil when the session has
	// none. The active turn may still run with the settings it started with.
	SessionSettings map[string]any `json:"session_settings"`
}

// MarshalJSON encodes the wire object, writing empty lists as [].
//
// It returns an error only when a field cannot be encoded.
func (snapshot AgentSnapshot) MarshalJSON() ([]byte, error) {
	type wireAgentSnapshot AgentSnapshot
	encoded := wireAgentSnapshot(snapshot)
	encoded.Messages = nonNilSlice(encoded.Messages)
	encoded.PendingInterrupts = nonNilSlice(encoded.PendingInterrupts)
	encoded.ToolCalls = nonNilSlice(encoded.ToolCalls)
	encoded.QueuedMessages = nonNilSlice(encoded.QueuedMessages)
	encoded.SteeredMessages = nonNilSlice(encoded.SteeredMessages)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object, filling empty lists for absent fields.
//
// It returns an error for a missing state field or a wrong JSON type.
func (snapshot *AgentSnapshot) UnmarshalJSON(data []byte) error {
	type wireAgentSnapshot AgentSnapshot
	var decoded wireAgentSnapshot
	if err := decodeWireObject(data, &decoded, "AgentSnapshot", "state"); err != nil {
		return err
	}
	decoded.Messages = nonNilSlice(decoded.Messages)
	decoded.PendingInterrupts = nonNilSlice(decoded.PendingInterrupts)
	decoded.ToolCalls = nonNilSlice(decoded.ToolCalls)
	decoded.QueuedMessages = nonNilSlice(decoded.QueuedMessages)
	decoded.SteeredMessages = nonNilSlice(decoded.SteeredMessages)
	*snapshot = AgentSnapshot(decoded)
	return nil
}

// ListAgentMessagesRequest selects one page of stored messages for the list_agent_messages RPC.
type ListAgentMessagesRequest struct {
	// BeforeSequence returns messages with a smaller sequence. Nil starts after the newest message.
	BeforeSequence *int64 `json:"before_sequence"`
	// Limit is the maximum number of messages, clamped to 1 through 200. It defaults to 50 when
	// absent from the wire.
	Limit int64 `json:"limit"`
}

// UnmarshalJSON decodes the wire object, filling Limit 50 when absent.
//
// It returns an error for a wrong JSON type.
func (request *ListAgentMessagesRequest) UnmarshalJSON(data []byte) error {
	type wireListAgentMessagesRequest ListAgentMessagesRequest
	decoded := wireListAgentMessagesRequest{Limit: defaultMessagePageLimit}
	if err := decodeWireObject(data, &decoded, "ListAgentMessagesRequest"); err != nil {
		return err
	}
	*request = ListAgentMessagesRequest(decoded)
	return nil
}

// AgentMessagesPage returns one page of stored messages, oldest first.
type AgentMessagesPage struct {
	// Messages are the page's messages in sequence order.
	Messages []AgentMessage `json:"messages"`
	// NextBeforeSequence is the BeforeSequence of the previous page, or nil when this page starts
	// at the oldest stored message.
	NextBeforeSequence *int64 `json:"next_before_sequence"`
	// DeletedBeforeSequence is the oldest stored message when retention deleted every earlier
	// one, or nil when the session keeps every message from sequence 1. A page from a 0.1.0
	// session reads as nil.
	DeletedBeforeSequence *int64 `json:"deleted_before_sequence"`
}

// MarshalJSON encodes the wire object, writing an empty Messages as [].
//
// It returns an error only when a field cannot be encoded.
func (page AgentMessagesPage) MarshalJSON() ([]byte, error) {
	type wireAgentMessagesPage AgentMessagesPage
	encoded := wireAgentMessagesPage(page)
	encoded.Messages = nonNilSlice(encoded.Messages)
	return json.Marshal(encoded)
}

// UnmarshalJSON decodes the wire object and checks its required fields.
//
// It returns an error for a missing required field or a wrong JSON type.
func (page *AgentMessagesPage) UnmarshalJSON(data []byte) error {
	type wireAgentMessagesPage AgentMessagesPage
	var decoded wireAgentMessagesPage
	if err := decodeWireObject(data, &decoded, "AgentMessagesPage", "messages", "next_before_sequence"); err != nil {
		return err
	}
	decoded.Messages = nonNilSlice(decoded.Messages)
	*page = AgentMessagesPage(decoded)
	return nil
}

// decodeWireObject decodes a JSON object into target after checking its required keys.
func decodeWireObject(data []byte, target any, typeName string, requiredKeys ...string) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", typeName, err)
	}
	if keys == nil {
		return fmt.Errorf("%s must be a JSON object, not null", typeName)
	}
	for _, key := range requiredKeys {
		if _, isPresent := keys[key]; !isPresent {
			return fmt.Errorf("%s missing required JSON field %q", typeName, key)
		}
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", typeName, err)
	}
	return nil
}

// decodeNonNullWireObject is decodeWireObject whose required fields must also not be JSON null.
func decodeNonNullWireObject(data []byte, target any, typeName string, requiredKeys ...string) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("%s must be a JSON object: %w", typeName, err)
	}
	for _, key := range requiredKeys {
		if value, isPresent := keys[key]; isPresent && string(bytes.TrimSpace(value)) == "null" {
			return fmt.Errorf("%s field %q must not be null", typeName, key)
		}
	}
	return decodeWireObject(data, target, typeName, requiredKeys...)
}

func decodeWireEnum[Enum ~string](text []byte, target *Enum, typeName string, allowed ...Enum) error {
	value := Enum(text)
	if !slices.Contains(allowed, value) {
		return fmt.Errorf("%s has unknown value %q", typeName, string(text))
	}
	*target = value
	return nil
}

func nonNilSlice[Item any](items []Item) []Item {
	if items == nil {
		return []Item{}
	}
	return items
}
