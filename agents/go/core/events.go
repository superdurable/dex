// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"fmt"

	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// AgentEventType names an event the session writes to AgentEvents.
//
// AgentEvent.Type holds one of these values. Payloads are JSON objects with
// snake_case keys. This Go core writes the types its Steps produce; the other
// contract types come from Workers with later contract additions.
type AgentEventType string

const (
	// AgentEventTypeTurnStarted means a user message started a turn. Payload {"sequence"}, the
	// user message sequence.
	AgentEventTypeTurnStarted AgentEventType = "turn_started"
	// AgentEventTypeTurnCompleted means the turn produced its final answer. Payload
	// {"message_count"}, the messages the turn appended after its user message.
	AgentEventTypeTurnCompleted AgentEventType = "turn_completed"
	// AgentEventTypeTurnFailed means RecordTurnFailure ended the turn. Payload {"error"}.
	AgentEventTypeTurnFailed AgentEventType = "turn_failed"
	// AgentEventTypeTextDelta is visible model text. Payload {"text"}.
	AgentEventTypeTextDelta AgentEventType = "text_delta"
	// AgentEventTypeReasoningDelta is a provider-authored reasoning summary. Payload {"text"}.
	AgentEventTypeReasoningDelta AgentEventType = "reasoning_delta"
	// AgentEventTypeToolCallStarted means a tool started. Payload {"tool_call_id", "tool_name"}.
	AgentEventTypeToolCallStarted AgentEventType = "tool_call_started"
	// AgentEventTypeToolCallFinished means a tool call's record became final, rejections included.
	// Payload {"tool_call_id", "tool_name", "status"}.
	AgentEventTypeToolCallFinished AgentEventType = "tool_call_finished"
	// AgentEventTypeToolCallDeferred means the model asked for a tool call, which runs in its own
	// Step. Payload {"tool_call_id", "tool_name", "effect", "requires_approval"}.
	AgentEventTypeToolCallDeferred AgentEventType = "tool_call_deferred"
	// AgentEventTypeInterruptRaised means a tool call waits for approval. Payload
	// {"interrupt_id", "tool_call_id", "tool_name", "deadline", "kind", "question", "choices",
	// "allows_free_text", "display"}; "display" is null when the source gave none, since 0.6.0.
	AgentEventTypeInterruptRaised AgentEventType = "interrupt_raised"
	// AgentEventTypeInterruptResolved means an interrupt was answered or expired. Payload
	// {"interrupt_id", "approved", "rejection_message", "kind", "answered", "cancellation_reason"}.
	AgentEventTypeInterruptResolved AgentEventType = "interrupt_resolved"
	// AgentEventTypeUsageRecorded means the session added one model call's usage. Payload
	// {"recorded", "session"}, each an AgentUsage object.
	AgentEventTypeUsageRecorded AgentEventType = "usage_recorded"
)

// AgentEventWriter writes live events for one Step attempt to AgentEvents.
//
// The session gives every runner call a writer bound to the current turn ID,
// Step execution ID, and Step attempt. Events are best effort: a retry can
// repeat them and a consumer can miss them, so never put durable results only
// in events. A writer is safe to use from several goroutines; the Dex Stream
// serializes the writes. Writes after the runner call returns fail, because
// the Step attempt is over.
//
//	func (runner *MyRunner) RunModelStep(ctx context.Context, turn dexagents.AgentTurnContext) (dexagents.AgentTurnOutcome, error) {
//		if err := turn.Events.WriteTextDelta("Looking that up"); err != nil {
//			return nil, err
//		}
//		// ...
//	}
type AgentEventWriter struct {
	writeAgentEvent func(AgentEvent) error
	turnID          *string
	attempt         int64
	stepExecutionID string
}

// NewAgentEventWriter creates a writer that stamps every event with its turn and Step attempt.
//
// The session constructs writers. Construct one yourself only to test a runner
// without a Dex Server. writeAgentEvent receives every event and must not be
// nil; turnID may be nil for events outside a turn.
func NewAgentEventWriter(
	writeAgentEvent func(AgentEvent) error,
	turnID *string,
	attempt int64,
	stepExecutionID string,
) *AgentEventWriter {
	if writeAgentEvent == nil {
		panic("dexagents: NewAgentEventWriter needs a writeAgentEvent function")
	}
	return &AgentEventWriter{
		writeAgentEvent: writeAgentEvent,
		turnID:          turnID,
		attempt:         attempt,
		stepExecutionID: stepExecutionID,
	}
}

// TurnID returns the turn stamped on every event, or nil outside a turn.
func (writer *AgentEventWriter) TurnID() *string {
	return writer.turnID
}

// Attempt returns the one-based Step attempt stamped on every event.
func (writer *AgentEventWriter) Attempt() int64 {
	return writer.attempt
}

// StepExecutionID returns the Dex Step execution ID stamped on every event.
func (writer *AgentEventWriter) StepExecutionID() string {
	return writer.stepExecutionID
}

// WriteEvent writes one event with a JSON object payload.
//
// Prefer the typed methods for the standard event types. Use this method for
// an adapter-specific type, with an adapter prefix such as
// "myframework.handoff", so it never collides with a standard type. The
// payload is written with sorted keys, exactly as the Python core writes it.
// payload values may be strings, booleans, numbers, nil, slices, string-keyed
// maps, or pythonjson-compatible values.
//
// It returns an error when the payload cannot be written as JSON or the
// Stream write fails.
func (writer *AgentEventWriter) WriteEvent(eventType AgentEventType, payload map[string]any) error {
	payloadText, err := pythonjson.Marshal(payload, pythonjson.Options{ShouldSortKeys: true})
	if err != nil {
		return fmt.Errorf("encode %s event payload: %w", eventType, err)
	}
	return writer.writeAgentEvent(AgentEvent{
		Type:            string(eventType),
		TurnID:          writer.turnID,
		Attempt:         writer.attempt,
		Payload:         payloadText,
		StepExecutionID: writer.stepExecutionID,
	})
}

// WriteTextDelta writes one chunk of visible model text.
//
// Every non-empty call writes one Stream message; an empty text writes
// nothing. It returns an error when the Stream write fails.
func (writer *AgentEventWriter) WriteTextDelta(text string) error {
	if text == "" {
		return nil
	}
	return writer.WriteEvent(AgentEventTypeTextDelta, map[string]any{"text": text})
}

// WriteReasoningDelta writes one chunk of a provider-authored reasoning summary.
//
// Call it only for an explicit provider reasoning-summary event; never label
// ordinary model text as reasoning. An empty text writes nothing. It returns
// an error when the Stream write fails.
func (writer *AgentEventWriter) WriteReasoningDelta(text string) error {
	if text == "" {
		return nil
	}
	return writer.WriteEvent(AgentEventTypeReasoningDelta, map[string]any{"text": text})
}

// WriteToolCallStarted reports that a tool started running.
//
// The session writes it when a tool call Step starts. A runner writes it only
// for a tool its framework cannot defer. It returns an error when the Stream
// write fails.
func (writer *AgentEventWriter) WriteToolCallStarted(toolCallID string, toolName string) error {
	return writer.WriteEvent(AgentEventTypeToolCallStarted, map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
	})
}

// WriteToolCallFinished reports that a tool call's record became final.
//
// It returns an error when the Stream write fails.
func (writer *AgentEventWriter) WriteToolCallFinished(toolCallID string, toolName string, status ToolCallStatus) error {
	return writer.WriteEvent(AgentEventTypeToolCallFinished, map[string]any{
		"tool_call_id": toolCallID,
		"tool_name":    toolName,
		"status":       string(status),
	})
}
