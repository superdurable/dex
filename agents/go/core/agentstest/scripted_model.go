// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package agentstest helps test agent sessions without a model key: a scripted model client, a
// private Dex engine, Worker child processes, and Step history.
package agentstest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/agents/go/core/stepwise"
)

// ScriptedModelProvider is the ModelOutputItem.Provider of every item ScriptedModelClient returns.
const ScriptedModelProvider = "dex-scripted"

// ScriptedModelAPI is the ModelOutputItem.API of every item ScriptedModelClient returns.
const ScriptedModelAPI = "scripted"

// ScriptedToolCall describes one tool call of a scripted model reply.
type ScriptedToolCall struct {
	// ToolName is the tool name.
	ToolName string
	// Arguments is the arguments as JSON text. Empty means "{}".
	Arguments string
	// ToolCallID is the tool call ID, or "" for "<step_execution_id>-<index>", which is unique
	// in the session.
	ToolCallID string
}

// ScriptedModelReply describes what one scripted model call returns.
type ScriptedModelReply struct {
	// Text is the visible text, streamed as one text_delta. "" for none.
	Text string
	// ToolCalls are the tool calls, in order.
	ToolCalls []ScriptedToolCall
	// Reasoning is a reasoning summary, streamed as one reasoning_delta and stored as a
	// reasoning item, or nil.
	Reasoning *string
	// Usage is the usage of the call. Nil uses 1 model call, 10 input tokens, and 5 output tokens,
	// as the Python core's scripted client does.
	Usage *dexagents.AgentUsage
}

// ScriptedModelResponder returns the reply for one request. An error fails the RunModel attempt.
type ScriptedModelResponder func(request stepwise.ModelRequest) (ScriptedModelReply, error)

// ScriptedModelClient answers every model call from a script, for tests without a model key.
//
// The script is a function of the whole request, so it can read the user text
// and the tool results the runner replays. Keep it deterministic: a Worker
// that crashes mid-call reruns it in a new process. Its items are byte for
// byte those of the Python core's ScriptedModelClient, so a session scripted
// in one core continues in another.
//
//	model := agentstest.NewScriptedModelClient(func(request stepwise.ModelRequest) (agentstest.ScriptedModelReply, error) {
//		if len(agentstest.ToolResults(request)) > 0 {
//			return agentstest.ScriptedModelReply{Text: "done"}, nil
//		}
//		return agentstest.ScriptedModelReply{ToolCalls: []agentstest.ScriptedToolCall{
//			{ToolName: "lookup_order", Arguments: `{"order_id": "7"}`},
//		}}, nil
//	})
type ScriptedModelClient struct {
	respond  ScriptedModelResponder
	mutex    sync.Mutex
	requests []stepwise.ModelRequest
}

// NewScriptedModelClient creates a client around a script. respond must not be nil.
func NewScriptedModelClient(respond ScriptedModelResponder) *ScriptedModelClient {
	if respond == nil {
		panic("agentstest: NewScriptedModelClient needs a responder")
	}
	return &ScriptedModelClient{respond: respond}
}

// Requests returns every request this client received in this process, in call order.
func (client *ScriptedModelClient) Requests() []stepwise.ModelRequest {
	client.mutex.Lock()
	defer client.mutex.Unlock()
	return append([]stepwise.ModelRequest(nil), client.requests...)
}

// CallModel runs the script for one request and streams its reply.
//
// It returns one reasoning item, one message item, and one item per tool call,
// for the parts the reply has.
func (client *ScriptedModelClient) CallModel(
	_ context.Context,
	request stepwise.ModelRequest,
	output stepwise.ModelOutputWriter,
) (stepwise.ModelResponse, error) {
	client.mutex.Lock()
	client.requests = append(client.requests, request)
	client.mutex.Unlock()
	reply, err := client.respond(request)
	if err != nil {
		return stepwise.ModelResponse{}, err
	}
	items := []stepwise.ModelOutputItem{}
	if reply.Reasoning != nil {
		if err := output.WriteReasoningDelta(*reply.Reasoning); err != nil {
			return stepwise.ModelResponse{}, err
		}
		item, err := scriptedItem(pythonjson.Object{{Key: "type", Value: "reasoning"}, {Key: "summary", Value: *reply.Reasoning}}, nil, nil)
		if err != nil {
			return stepwise.ModelResponse{}, err
		}
		items = append(items, item)
	}
	if reply.Text != "" {
		if err := output.WriteTextDelta(reply.Text); err != nil {
			return stepwise.ModelResponse{}, err
		}
		text := reply.Text
		item, err := scriptedItem(pythonjson.Object{{Key: "type", Value: "message"}, {Key: "text", Value: text}}, &text, nil)
		if err != nil {
			return stepwise.ModelResponse{}, err
		}
		items = append(items, item)
	}
	toolCalls := []stepwise.ModelToolCall{}
	for index, scripted := range reply.ToolCalls {
		call := stepwise.ModelToolCall{ToolCallID: scripted.ToolCallID, ToolName: scripted.ToolName, Arguments: scripted.Arguments}
		if call.ToolCallID == "" {
			call.ToolCallID = fmt.Sprintf("%s-%d", request.Metadata.StepExecutionID, index)
		}
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		toolCalls = append(toolCalls, call)
		item, err := scriptedItem(pythonjson.Object{
			{Key: "type", Value: "function_call"},
			{Key: "call_id", Value: call.ToolCallID},
			{Key: "name", Value: call.ToolName},
			{Key: "arguments", Value: call.Arguments},
		}, nil, []stepwise.ModelToolCall{call})
		if err != nil {
			return stepwise.ModelResponse{}, err
		}
		items = append(items, item)
	}
	usage := dexagents.AgentUsage{ModelCalls: 1, InputTokens: 10, OutputTokens: 5}
	if reply.Usage != nil {
		usage = *reply.Usage
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return stepwise.ModelResponse{
		OutputItems: items, Text: reply.Text, ToolCalls: toolCalls, Usage: usage,
		ResponseModel: "scripted", FinishReason: finishReason,
	}, nil
}

// EstimateInputTokens estimates four characters per token over the request text.
func (client *ScriptedModelClient) EstimateInputTokens(request stepwise.ModelRequest) int64 {
	characters := len([]rune(request.Instructions))
	for _, item := range request.InputItems {
		switch typed := item.(type) {
		case stepwise.UserTextItem:
			characters += len([]rune(typed.Text))
		case stepwise.ToolResultItem:
			characters += len([]rune(typed.Output))
		case stepwise.ModelOutputItem:
			characters += len([]rune(typed.Item))
		}
	}
	return int64(characters / 4)
}

// UserTexts returns the text of every user item in a request, oldest first, Dex notes included.
func UserTexts(request stepwise.ModelRequest) []string {
	texts := []string{}
	for _, item := range request.InputItems {
		if userText, isUserText := item.(stepwise.UserTextItem); isUserText {
			texts = append(texts, userText.Text)
		}
	}
	return texts
}

// ToolResults returns the tool results after the last user item of a request, oldest first.
func ToolResults(request stepwise.ModelRequest) []stepwise.ToolResultItem {
	results := []stepwise.ToolResultItem{}
	for _, item := range request.InputItems {
		switch typed := item.(type) {
		case stepwise.UserTextItem:
			results = []stepwise.ToolResultItem{}
		case stepwise.ToolResultItem:
			results = append(results, typed)
		}
	}
	return results
}

// JSONArguments returns arguments as JSON text with Python's default separators, as the Python
// scripted client writes a dict. It returns an error when arguments cannot be encoded.
func JSONArguments(arguments map[string]any) (string, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	return pythonjson.Marshal(json.RawMessage(encoded), pythonjson.Options{ShouldEscapeNonASCII: true})
}

func scriptedItem(item pythonjson.Object, text *string, toolCalls []stepwise.ModelToolCall) (stepwise.ModelOutputItem, error) {
	encoded, err := pythonjson.Marshal(item, pythonjson.Options{IsCompact: true, ShouldEscapeNonASCII: true})
	if err != nil {
		return stepwise.ModelOutputItem{}, err
	}
	return stepwise.ModelOutputItem{
		Provider: ScriptedModelProvider, API: ScriptedModelAPI, Item: encoded, Text: text, ToolCalls: toolCalls,
	}, nil
}
