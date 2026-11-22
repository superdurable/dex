// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino_test

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	dexeino "github.com/superdurable/dex/agents/go/eino"
)

// A stored item is a versioned envelope of the Eino message, without Eino's internal Extra keys.
func TestEncodeMessageStoresAVersionedEnvelope(t *testing.T) {
	message := schema.AssistantMessage("checking", []schema.ToolCall{call("call_1", "lookup_order", `{"id":"1"}`)})
	message.Extra = map[string]any{"_eino_msg_id": "random", "signature": "keep me"}
	stored, err := dexeino.EncodeMessage(message)
	require.NoError(t, err)
	require.Equal(t, dexagents.MessageRoleAssistant, stored.Role)
	require.Equal(t, "checking", *stored.Text)
	require.Equal(t, "call_1", *stored.ToolCallID)
	require.Equal(t, "lookup_order", *stored.ToolName)
	require.Equal(t, `{"id":"1"}`, *stored.ToolArguments)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal([]byte(stored.FrameworkItem), &envelope))
	require.Equal(t, dexeino.MessageItemType, envelope["type"])
	require.Equal(t, float64(dexeino.MessageItemFormat), envelope["format"])
	require.NotContains(t, stored.FrameworkItem, "_eino_msg_id")
	require.Contains(t, stored.FrameworkItem, "keep me")
	require.Equal(t, "random", message.Extra["_eino_msg_id"], "the caller's message is not changed")

	again, err := dexeino.EncodeMessage(message)
	require.NoError(t, err)
	require.Equal(t, stored.FrameworkItem, again.FrameworkItem, "encoding is deterministic")

	decoded, err := dexeino.DecodeMessage(dexagents.AgentMessage{Role: stored.Role, FrameworkItem: stored.FrameworkItem})
	require.NoError(t, err)
	require.Equal(t, "keep me", decoded.Extra["signature"])
	require.Equal(t, message.ToolCalls, decoded.ToolCalls)

	_, err = dexeino.EncodeMessage(schema.SystemMessage("You are a bot."))
	require.ErrorContains(t, err, "cannot be stored")
	_, err = dexeino.EncodeMessage(&schema.Message{Content: "no role"})
	require.ErrorContains(t, err, "cannot be stored")
	_, err = dexeino.DecodeMessage(dexagents.AgentMessage{Role: dexagents.MessageRoleUser, FrameworkItem: stored.FrameworkItem})
	require.ErrorContains(t, err, "under role")
	_, err = dexeino.DecodeMessage(dexagents.AgentMessage{Role: dexagents.MessageRoleUser,
		FrameworkItem: `{"type":"eino_message","format":2,"message":{"role":"user","content":"x"}}`})
	require.ErrorContains(t, err, "format 2")
}

// Messages another runner stored replay as Eino text and tool calls, and every call keeps exactly one result.
func TestForeignHistoryReplaysAsTextAndToolCalls(t *testing.T) {
	var stored []dexagents.AgentMessage
	add := func(message dexagents.FrameworkMessage, turnID string) {
		stored = append(stored, dexagents.AgentMessage{Sequence: int64(len(stored) + 1), TurnID: turnID, Role: message.Role,
			Text: message.Text, ToolCallID: message.ToolCallID, ToolName: message.ToolName, ToolArguments: message.ToolArguments,
			ToolOutput: message.ToolOutput, FrameworkItem: message.FrameworkItem})
	}
	builtin := func(item builtinloop.ModelInputItem) dexagents.FrameworkMessage {
		message, err := builtinloop.EncodeBuiltinLoopMessage(item)
		require.NoError(t, err)
		return message
	}
	text := "Let me check."
	add(builtin(builtinloop.UserTextItem{Text: "lookup 1 and 2"}), "turn-1")
	add(builtin(builtinloop.ModelOutputItem{Provider: "openai", API: "responses", Item: `{"type":"message"}`, Text: &text}), "turn-1")
	add(builtin(builtinloop.ModelOutputItem{Provider: "openai", API: "responses", Item: `{"type":"function_call"}`,
		ToolCalls: []builtinloop.ModelToolCall{{ToolCallID: "call_1", ToolName: "lookup_order", Arguments: `{"id":"1"}`}}}), "turn-1")
	add(builtin(builtinloop.ModelOutputItem{Provider: "openai", API: "responses", Item: `{"type":"function_call"}`,
		ToolCalls: []builtinloop.ModelToolCall{{ToolCallID: "call_2", ToolName: "lookup_order", Arguments: `{"id":"2"}`}}}), "turn-1")
	add(builtin(builtinloop.ToolResultItem{ToolCallID: "call_1", ToolName: "lookup_order", Output: "order 1 is shipped"}), "turn-1")
	add(builtin(builtinloop.ToolResultItem{ToolCallID: "call_stale", ToolName: "lookup_order", Output: "orphan"}), "turn-1")
	answer := "Order 1 shipped."
	add(dexagents.FrameworkMessage{Role: dexagents.MessageRoleAssistant, Text: &answer, FrameworkItem: `{"kind":"other framework"}`}, "turn-2")
	scripted := echoModel()
	runner, err := dexeino.NewRunner(&adk.ChatModelAgentConfig{Name: "agent", Description: "Test agent.", Model: scripted})
	require.NoError(t, err)
	user, err := runner.BuildUserMessage("and now?")
	require.NoError(t, err)
	add(user, "turn-3")
	state := &turnState{t: t, turnID: "turn-3", messages: stored, records: map[string]dexagents.AgentToolCall{}, attempt: 1}
	state.runModel(runner)

	input := scripted.lastInput()
	require.Equal(t, []schema.RoleType{schema.User, schema.Assistant, schema.Tool, schema.Tool, schema.Assistant, schema.User}, roles(input))
	require.Equal(t, "Let me check.", input[1].Content)
	require.Len(t, input[1].ToolCalls, 2, "one model response stored as several items replays as one message")
	require.Equal(t, "order 1 is shipped", input[2].Content)
	require.Equal(t, "call_2", input[3].ToolCallID)
	require.Equal(t, dexagents.OutcomeUnknownToolResult, input[3].Content, "an unanswered call gets the unknown outcome")
	require.Equal(t, "Order 1 shipped.", input[4].Content)
}

// History whose Extra holds JSON-decoded values still lets the run stop at its tool calls.
func TestJSONDecodedExtraInHistoryKeepsTheToolCalls(t *testing.T) {
	scripted := &scriptedChatModel{respond: func([]*schema.Message) (*schema.Message, error) {
		return toolCallMessage(call("call_1", "lookup_order", `{"id":"1"}`)), nil
	}}
	runner := readRunner(t, scripted)
	earlier := schema.AssistantMessage("earlier answer", nil)
	earlier.Extra = map[string]any{"provider_blocks": []any{map[string]any{"type": "thinking", "n": 1.5}}}
	stored, err := dexeino.EncodeMessage(earlier)
	require.NoError(t, err)
	state := newTurnState(t, runner, "first")
	state.store(stored)
	user, err := runner.BuildUserMessage("lookup 1")
	require.NoError(t, err)
	state.store(user)
	requested, isRequested := state.runModel(runner).(*dexagents.ToolCallsRequested)
	require.True(t, isRequested)
	require.Len(t, requested.ToolCalls, 1)
}
