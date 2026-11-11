// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package builtinloop

import (
	"encoding/json"
	"fmt"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
)

// The framework_item "type" values of stored built-in loop messages, shared with the other cores.
const (
	// UserTextItemType marks stored user text.
	UserTextItemType = "user_text"
	// ModelOutputItemType marks a stored model output item.
	ModelOutputItemType = "model_output"
	// ToolResultItemType marks a stored tool result.
	ToolResultItemType = "tool_result"
)

// EncodeBuiltinLoopMessage returns the session message that stores one model input item.
//
// The framework_item is a compact JSON object with a "type" of user_text,
// model_output, or tool_result, byte for byte as the Python core writes it. A
// model output item keeps the provider's item as a JSON string, so its bytes
// survive storage. It returns an error for an unknown item type.
//
// Since 0.5.0. It replaces stepwise.EncodeStepwiseMessage.
func EncodeBuiltinLoopMessage(item ModelInputItem) (dexagents.FrameworkMessage, error) {
	switch typed := item.(type) {
	case UserTextItem:
		frameworkItem, err := compactJSON(pythonjson.Object{
			{Key: "type", Value: UserTextItemType},
			{Key: "text", Value: typed.Text},
		})
		return dexagents.FrameworkMessage{Role: dexagents.MessageRoleUser, Text: &typed.Text, FrameworkItem: frameworkItem}, err
	case ToolResultItem:
		frameworkItem, err := compactJSON(pythonjson.Object{
			{Key: "type", Value: ToolResultItemType},
			{Key: "tool_call_id", Value: typed.ToolCallID},
			{Key: "tool_name", Value: typed.ToolName},
			{Key: "output", Value: typed.Output},
		})
		return dexagents.FrameworkMessage{
			Role:          dexagents.MessageRoleTool,
			ToolCallID:    &typed.ToolCallID,
			ToolName:      &typed.ToolName,
			ToolOutput:    &typed.Output,
			FrameworkItem: frameworkItem,
		}, err
	case ModelOutputItem:
		toolCalls := make([]any, 0, len(typed.ToolCalls))
		for _, call := range typed.ToolCalls {
			toolCalls = append(toolCalls, pythonjson.Object{
				{Key: "tool_call_id", Value: call.ToolCallID},
				{Key: "tool_name", Value: call.ToolName},
				{Key: "arguments", Value: call.Arguments},
			})
		}
		frameworkItem, err := compactJSON(pythonjson.Object{
			{Key: "type", Value: ModelOutputItemType},
			{Key: "provider", Value: typed.Provider},
			{Key: "api", Value: typed.API},
			{Key: "item", Value: typed.Item},
			{Key: "text", Value: typed.Text},
			{Key: "tool_calls", Value: toolCalls},
		})
		message := dexagents.FrameworkMessage{Role: dexagents.MessageRoleAssistant, Text: typed.Text, FrameworkItem: frameworkItem}
		if len(typed.ToolCalls) == 1 {
			call := typed.ToolCalls[0]
			message.ToolCallID, message.ToolName, message.ToolArguments = &call.ToolCallID, &call.ToolName, &call.Arguments
		}
		return message, err
	default:
		return dexagents.FrameworkMessage{}, fmt.Errorf("unknown model input item %T", item)
	}
}

type storedBuiltinLoopItem struct {
	Type       string  `json:"type"`
	Text       *string `json:"text"`
	ToolCallID *string `json:"tool_call_id"`
	ToolName   *string `json:"tool_name"`
	Output     *string `json:"output"`
	Provider   *string `json:"provider"`
	API        *string `json:"api"`
	Item       *string `json:"item"`
	ToolCalls  []struct {
		ToolCallID *string `json:"tool_call_id"`
		ToolName   *string `json:"tool_name"`
		Arguments  *string `json:"arguments"`
	} `json:"tool_calls"`
}

// DecodeBuiltinLoopMessage returns the model input item that one stored session message holds.
//
// A model output item's provider JSON is returned unchanged. It returns an
// error when the message was not stored by the built-in agent loop in any core.
//
// Since 0.5.0. It replaces stepwise.DecodeStepwiseMessage.
func DecodeBuiltinLoopMessage(message dexagents.AgentMessage) (ModelInputItem, error) {
	var stored storedBuiltinLoopItem
	if err := json.Unmarshal([]byte(message.FrameworkItem), &stored); err != nil {
		return nil, fmt.Errorf("message %d is not a built-in agent loop item: %w", message.Sequence, err)
	}
	missing := func(field string) error {
		return fmt.Errorf("message %d is not a built-in agent loop item: missing %s", message.Sequence, field)
	}
	switch stored.Type {
	case UserTextItemType:
		if stored.Text == nil {
			return nil, missing("text")
		}
		return UserTextItem{Text: *stored.Text}, nil
	case ToolResultItemType:
		if stored.ToolCallID == nil || stored.ToolName == nil || stored.Output == nil {
			return nil, missing("tool_call_id, tool_name, or output")
		}
		return ToolResultItem{ToolCallID: *stored.ToolCallID, ToolName: *stored.ToolName, Output: *stored.Output}, nil
	case ModelOutputItemType:
		if stored.Provider == nil || stored.API == nil || stored.Item == nil {
			return nil, missing("provider, api, or item")
		}
		item := ModelOutputItem{Provider: *stored.Provider, API: *stored.API, Item: *stored.Item, Text: stored.Text}
		for _, call := range stored.ToolCalls {
			if call.ToolCallID == nil || call.ToolName == nil || call.Arguments == nil {
				return nil, missing("a tool call field")
			}
			item.ToolCalls = append(item.ToolCalls, ModelToolCall{
				ToolCallID: *call.ToolCallID, ToolName: *call.ToolName, Arguments: *call.Arguments,
			})
		}
		return item, nil
	default:
		return nil, fmt.Errorf("message %d has unknown built-in agent loop item type %q", message.Sequence, stored.Type)
	}
}

// PairToolCallsWithResults returns the items with exactly one result after every tool call.
//
// A tool call without a stored result, such as one whose turn failed before
// the result was stored, gets dexagents.OutcomeUnknownToolResult as its
// result, placed before the next user text. A result whose call is not in the
// items is dropped. Providers reject either kind of orphan.
func PairToolCallsWithResults(items []ModelInputItem) []ModelInputItem {
	paired := make([]ModelInputItem, 0, len(items))
	var openCalls []ModelToolCall
	closeOpenCalls := func() {
		for _, call := range openCalls {
			paired = append(paired, ToolResultItem{
				ToolCallID: call.ToolCallID, ToolName: call.ToolName, Output: dexagents.OutcomeUnknownToolResult,
			})
		}
		openCalls = nil
	}
	for _, item := range items {
		switch typed := item.(type) {
		case ModelOutputItem:
			paired = append(paired, typed)
			for _, call := range typed.ToolCalls {
				// A repeated ID replaces the open call in place, as Python's dict update does.
				if index := indexOfCall(openCalls, call.ToolCallID); index >= 0 {
					openCalls[index] = call
					continue
				}
				openCalls = append(openCalls, call)
			}
		case ToolResultItem:
			if index := indexOfCall(openCalls, typed.ToolCallID); index >= 0 {
				openCalls = append(openCalls[:index], openCalls[index+1:]...)
				paired = append(paired, typed)
			}
		default:
			closeOpenCalls()
			paired = append(paired, item)
		}
	}
	closeOpenCalls()
	return paired
}

func indexOfCall(calls []ModelToolCall, toolCallID string) int {
	for index, call := range calls {
		if call.ToolCallID == toolCallID {
			return index
		}
	}
	return -1
}

func compactJSON(value any) (string, error) {
	return pythonjson.Marshal(value, pythonjson.Options{IsCompact: true})
}
