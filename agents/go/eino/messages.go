// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexeino

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
)

// MessageItemType is the "type" of every framework_item this adapter stores.
//
// The item is a JSON object {"type":"eino_message","format":1,"message":{...}}
// whose message is the Eino schema.Message, encoded with encoding/json.
// Since 0.6.16.
const MessageItemType = "eino_message"

// MessageItemFormat is the format number of the stored item; a new number marks an incompatible item.
//
// Since 0.6.16.
const MessageItemFormat = 1

// einoADKMessageIDKey is the Message.Extra key of the random message ID that Eino ADK sets.
const einoADKMessageIDKey = "_eino_msg_id"

// einoADKExtraPrefix starts the other Message.Extra keys that Eino ADK sets for itself.
const einoADKExtraPrefix = "_eino_adk_"

type storedMessageItem struct {
	Type    string          `json:"type"`
	Format  int             `json:"format"`
	Message *schema.Message `json:"message"`
}

// EncodeMessage returns the session message that stores one Eino message.
//
// The framework_item is a MessageItemType envelope of the message as
// encoding/json writes it, so the stored bytes do not depend on the Go
// toolchain. The Extra keys that Eino ADK sets for itself, "_eino_msg_id"
// and keys that start with "_eino_adk_", are dropped, so a retried model call
// stores the same bytes. Other Extra keys are kept, including the replay keys
// of eino-ext model clients, such as the thinking signature of the Claude
// client. The role must be user, assistant, or tool: Dex stores no system messages. The
// Text, ToolCallID, ToolName, ToolArguments, and ToolOutput fields describe the
// message for snapshots; the tool fields are set only when the message carries
// exactly one tool call or result. It returns an error for another role or a
// message that does not encode. Since 0.6.16.
func EncodeMessage(message *schema.Message) (dexagents.FrameworkMessage, error) {
	if message == nil {
		return dexagents.FrameworkMessage{}, fmt.Errorf("cannot store a nil Eino message")
	}
	stored := withoutEinoInternalExtra(message)
	role, err := sessionRole(stored.Role)
	if err != nil {
		return dexagents.FrameworkMessage{}, err
	}
	item, err := json.Marshal(storedMessageItem{Type: MessageItemType, Format: MessageItemFormat, Message: stored})
	if err != nil {
		return dexagents.FrameworkMessage{}, fmt.Errorf("encode the Eino %s message: %w", stored.Role, err)
	}
	framework := dexagents.FrameworkMessage{Role: role, FrameworkItem: string(item)}
	switch role {
	case dexagents.MessageRoleUser:
		framework.Text = ptr(stored.Content)
	case dexagents.MessageRoleAssistant:
		if stored.Content != "" {
			framework.Text = ptr(stored.Content)
		}
		if len(stored.ToolCalls) == 1 {
			call := stored.ToolCalls[0]
			framework.ToolCallID, framework.ToolName = ptr(call.ID), ptr(call.Function.Name)
			framework.ToolArguments = ptr(call.Function.Arguments)
		}
	case dexagents.MessageRoleTool:
		framework.ToolCallID, framework.ToolName, framework.ToolOutput = ptr(stored.ToolCallID), ptr(stored.ToolName), ptr(stored.Content)
	}
	return framework, nil
}

// DecodeMessage returns the Eino message that one stored session message holds.
//
// A message this adapter stored decodes to its Eino message. A message that
// another runner stored, such as the built-in agent loop or an adapter of
// another framework, becomes an Eino message with its text and tool call or
// tool result, so a session that changed runners keeps its history. It
// returns an error for a MessageItemType envelope this version cannot read, a
// role that does not match the envelope, or a message with no role.
// Since 0.6.16.
func DecodeMessage(message dexagents.AgentMessage) (*schema.Message, error) {
	if stored, isEinoItem, err := decodeStoredMessageItem(message.FrameworkItem); isEinoItem {
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", message.Sequence, err)
		}
		if role, roleErr := sessionRole(stored.Role); roleErr != nil || role != message.Role {
			return nil, fmt.Errorf("message %d stores an Eino %q message under role %q", message.Sequence, stored.Role, message.Role)
		}
		return stored, nil
	}
	if item, err := builtinloop.DecodeBuiltinLoopMessage(message); err == nil {
		return builtinLoopItemMessage(item), nil
	}
	return describedMessage(message)
}

// decodeStoredMessageItem reports whether text is this adapter's envelope and, if so, decodes it.
func decodeStoredMessageItem(text string) (*schema.Message, bool, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(text), &probe) != nil || probe.Type != MessageItemType {
		return nil, false, nil
	}
	var stored storedMessageItem
	if err := json.Unmarshal([]byte(text), &stored); err != nil {
		return nil, true, fmt.Errorf("the Eino message item does not decode: %w", err)
	}
	if stored.Format != MessageItemFormat {
		return nil, true, fmt.Errorf("the Eino message item has format %d; this version reads format %d", stored.Format, MessageItemFormat)
	}
	if stored.Message == nil {
		return nil, true, fmt.Errorf("the Eino message item has no message")
	}
	return stored.Message, true, nil
}

func builtinLoopItemMessage(item builtinloop.ModelInputItem) *schema.Message {
	switch typed := item.(type) {
	case builtinloop.UserTextItem:
		return schema.UserMessage(typed.Text)
	case builtinloop.ToolResultItem:
		return schema.ToolMessage(typed.Output, typed.ToolCallID, schema.WithToolName(typed.ToolName))
	case builtinloop.ModelOutputItem:
		calls := make([]schema.ToolCall, 0, len(typed.ToolCalls))
		for _, call := range typed.ToolCalls {
			calls = append(calls, functionCall(call.ToolCallID, call.ToolName, call.Arguments))
		}
		text := ""
		if typed.Text != nil {
			text = *typed.Text
		}
		return schema.AssistantMessage(text, calls)
	default:
		return schema.UserMessage("")
	}
}

// describedMessage rebuilds a message of another framework from its snapshot fields.
func describedMessage(message dexagents.AgentMessage) (*schema.Message, error) {
	text := stringValue(message.Text)
	switch message.Role {
	case dexagents.MessageRoleUser:
		return schema.UserMessage(text), nil
	case dexagents.MessageRoleAssistant:
		var calls []schema.ToolCall
		if message.ToolCallID != nil && message.ToolName != nil {
			calls = append(calls, functionCall(*message.ToolCallID, *message.ToolName, stringValue(message.ToolArguments)))
		}
		return schema.AssistantMessage(text, calls), nil
	case dexagents.MessageRoleTool:
		output := text
		if message.ToolOutput != nil {
			output = *message.ToolOutput
		}
		return schema.ToolMessage(output, stringValue(message.ToolCallID), schema.WithToolName(stringValue(message.ToolName))), nil
	default:
		return nil, fmt.Errorf("message %d has role %q, which is not user, assistant, or tool", message.Sequence, message.Role)
	}
}

func functionCall(id, name, arguments string) schema.ToolCall {
	return schema.ToolCall{ID: id, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: arguments}}
}

func sessionRole(role schema.RoleType) (dexagents.MessageRole, error) {
	switch role {
	case schema.User:
		return dexagents.MessageRoleUser, nil
	case schema.Assistant:
		return dexagents.MessageRoleAssistant, nil
	case schema.Tool:
		return dexagents.MessageRoleTool, nil
	default:
		return "", fmt.Errorf("an Eino %q message cannot be stored: the session stores user, assistant, and tool messages", role)
	}
}

// withoutEinoInternalExtra returns a copy of message without the Extra keys Eino ADK sets for itself.
func withoutEinoInternalExtra(message *schema.Message) *schema.Message {
	copied := *message
	copied.Extra = withoutInternalKeys(message.Extra)
	if len(message.ToolCalls) > 0 {
		copied.ToolCalls = slices.Clone(message.ToolCalls)
		for index := range copied.ToolCalls {
			copied.ToolCalls[index].Extra = withoutInternalKeys(copied.ToolCalls[index].Extra)
		}
	}
	return &copied
}

func withoutInternalKeys(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	kept := maps.Clone(extra)
	maps.DeleteFunc(kept, func(key string, _ any) bool {
		return key == einoADKMessageIDKey || strings.HasPrefix(key, einoADKExtraPrefix)
	})
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// modelInputMessages returns the Eino run input: every stored message, then the results not stored yet.
//
// Assistant messages that follow each other merge, since another runner may
// store one model response as several items. Every tool call is paired with
// exactly one result in the input only; see pairToolCallsWithResults.
func modelInputMessages(stored []dexagents.AgentMessage, unstoredResults []*schema.Message) ([]*schema.Message, error) {
	messages := make([]*schema.Message, 0, len(stored)+len(unstoredResults))
	for _, message := range stored {
		decoded, err := DecodeMessage(message)
		if err != nil {
			return nil, err
		}
		previous := len(messages) - 1
		if decoded.Role == schema.Assistant && previous >= 0 && messages[previous].Role == schema.Assistant &&
			!isEinoMessageItem(message.FrameworkItem) {
			messages[previous] = mergeAssistantMessages(messages[previous], decoded)
			continue
		}
		messages = append(messages, decoded)
	}
	for _, result := range unstoredResults {
		// Before-model hooks may edit input messages in place, and Dex stores these results unedited.
		copied := *result
		copied.Extra = maps.Clone(result.Extra)
		messages = append(messages, &copied)
	}
	return pairToolCallsWithResults(messages), nil
}

func isEinoMessageItem(text string) bool {
	_, isEinoItem, _ := decodeStoredMessageItem(text)
	return isEinoItem
}

func mergeAssistantMessages(first *schema.Message, second *schema.Message) *schema.Message {
	merged := *first
	merged.Content = strings.TrimSpace(strings.Join([]string{first.Content, second.Content}, "\n"))
	merged.ToolCalls = append(slices.Clone(first.ToolCalls), second.ToolCalls...)
	return &merged
}

// pairToolCallsWithResults gives every tool call exactly one result, right after its assistant message.
//
// A call without a stored result, such as one whose turn an older Worker
// ended, gets dexagents.OutcomeUnknownToolResult in the model input only. A
// result whose call is not open is dropped. Providers reject either orphan.
func pairToolCallsWithResults(messages []*schema.Message) []*schema.Message {
	paired := make([]*schema.Message, 0, len(messages))
	var openCalls []schema.ToolCall
	closeOpenCalls := func() {
		for _, call := range openCalls {
			paired = append(paired, schema.ToolMessage(dexagents.OutcomeUnknownToolResult, call.ID, schema.WithToolName(call.Function.Name)))
		}
		openCalls = nil
	}
	for _, message := range messages {
		if message.Role == schema.Tool {
			index := slices.IndexFunc(openCalls, func(call schema.ToolCall) bool { return call.ID == message.ToolCallID })
			if index >= 0 {
				openCalls = slices.Delete(openCalls, index, index+1)
				paired = append(paired, message)
			}
			continue
		}
		closeOpenCalls()
		paired = append(paired, message)
		if message.Role == schema.Assistant {
			openCalls = slices.Clone(message.ToolCalls)
		}
	}
	closeOpenCalls()
	return paired
}

// unstoredToolResults returns a tool message for each final record of the turn whose result is not stored yet.
func unstoredToolResults(turn dexagents.AgentTurnContext) ([]*schema.Message, error) {
	storedResultIDs := map[string]bool{}
	for _, message := range turn.Messages {
		if message.TurnID == turn.TurnID && message.Role == dexagents.MessageRoleTool && message.ToolCallID != nil {
			storedResultIDs[*message.ToolCallID] = true
		}
	}
	records := slices.Collect(maps.Values(turn.ToolCalls))
	slices.SortStableFunc(records, func(left, right dexagents.AgentToolCall) int {
		return int(left.DeferralOrder - right.DeferralOrder)
	})
	results := []*schema.Message{}
	for _, record := range records {
		if storedResultIDs[record.ToolCallID] {
			continue
		}
		output, err := dexagents.DescribeToolCallOutcome(record)
		if err != nil {
			return nil, err
		}
		results = append(results, schema.ToolMessage(output, record.ToolCallID, schema.WithToolName(record.ToolName)))
	}
	return results, nil
}

func encodeMessages(messages []*schema.Message) ([]dexagents.FrameworkMessage, error) {
	encoded := make([]dexagents.FrameworkMessage, 0, len(messages))
	for _, message := range messages {
		framework, err := EncodeMessage(message)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, framework)
	}
	return encoded, nil
}

func ptr[Value any](value Value) *Value {
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
