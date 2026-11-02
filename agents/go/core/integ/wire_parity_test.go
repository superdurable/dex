// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/anthropicmodel"
	"github.com/superdurable/dex/agents/go/core/internal/pythonjson"
	"github.com/superdurable/dex/agents/go/core/stepwise"
)

// pythonCoreDir is the Python core, whose locked environment runs the parity scripts.
const pythonCoreDir = "../../../python/core"

type pythonWireValues struct {
	Values map[string]string `json:"values"`
	Events []struct {
		Type            string `json:"type"`
		Payload         string `json:"payload"`
		Attempt         int64  `json:"attempt"`
		StepExecutionID string `json:"step_execution_id"`
	} `json:"events"`
	StepwiseItems   []string `json:"stepwise_items"`
	TurnFailureNote string   `json:"turn_failure_note"`
	Outcomes        []string `json:"outcomes"`
	Anthropic       struct {
		Item     string           `json:"item"`
		Replayed []map[string]any `json:"replayed"`
	} `json:"anthropic"`
	Schema struct {
		Schema    json.RawMessage `json:"schema"`
		Arguments []string        `json:"arguments"`
		Messages  []string        `json:"messages"`
	} `json:"schema"`
}

// Every value the Python core encodes decodes in Go and re-encodes to the same canonical bytes.
// Event payloads, Stepwise framework items, and model-visible texts match byte for byte.
func TestGoReEncodesPythonWireValuesByteForByte(t *testing.T) {
	t.Parallel()
	encoded := runPythonScript(t, "encode_wire_values.py")
	var python pythonWireValues
	require.NoError(t, json.Unmarshal([]byte(encoded), &python))

	decoders := map[string]func() any{
		"AgentSessionStartInput":   func() any { return &dexagents.AgentSessionStartInput{} },
		"AgentState":               func() any { return &dexagents.AgentState{} },
		"AgentMessage":             func() any { return &dexagents.AgentMessage{} },
		"AgentToolCall":            func() any { return &dexagents.AgentToolCall{} },
		"AgentInterrupt":           func() any { return &dexagents.AgentInterrupt{} },
		"PendingInterrupts":        func() any { return &[]dexagents.AgentInterrupt{} },
		"AgentInterruptResolution": func() any { return &dexagents.AgentInterruptResolution{} },
		"ResolveInterruptRequest":  func() any { return &dexagents.ResolveInterruptRequest{} },
		"AgentUsage":               func() any { return &dexagents.AgentUsage{} },
		"AgentUserMessage":         func() any { return &dexagents.AgentUserMessage{} },
		"ToolCallCompleted":        func() any { return &dexagents.ToolCallCompleted{} },
		"AgentEvent":               func() any { return &dexagents.AgentEvent{} },
		"ListAgentMessagesRequest": func() any { return &dexagents.ListAgentMessagesRequest{} },
		"AgentMessagesPage":        func() any { return &dexagents.AgentMessagesPage{} },
		"AgentSnapshot":            func() any { return &dexagents.AgentSnapshot{} },
	}
	toolCalls := []dexagents.AgentToolCall{}
	for name, pythonJSON := range python.Values {
		typeName, _, _ := strings.Cut(name, "/")
		newValue, isKnown := decoders[typeName]
		require.True(t, isKnown, name)
		value := newValue()
		require.NoError(t, json.Unmarshal([]byte(pythonJSON), value), name)
		goJSON, err := json.Marshal(value)
		require.NoError(t, err, name)
		require.Equal(t, pythonJSON, canonicalPythonJSON(t, goJSON), name)
		if toolCall, isToolCall := value.(*dexagents.AgentToolCall); isToolCall {
			toolCalls = append(toolCalls, *toolCall)
		}
	}

	note, err := dexagents.BuildTurnFailureNote(toolCalls)
	require.NoError(t, err)
	require.Equal(t, python.TurnFailureNote, note)
	byOrder := map[int64]dexagents.AgentToolCall{}
	for _, toolCall := range toolCalls {
		byOrder[toolCall.DeferralOrder] = toolCall
	}
	for order, expected := range python.Outcomes {
		outcome, err := dexagents.DescribeToolCallOutcome(byOrder[int64(order)])
		require.NoError(t, err)
		require.Equal(t, expected, outcome)
	}

	goEvents := []dexagents.AgentEvent{}
	writer := dexagents.NewAgentEventWriter(func(event dexagents.AgentEvent) error {
		goEvents = append(goEvents, event)
		return nil
	}, ptr("turn-1"), 1, "RunModel-2")
	require.NoError(t, writer.WriteEvent(dexagents.AgentEventTypeTurnStarted, map[string]any{"sequence": 1}))
	usage := map[string]any{"model_calls": 1, "input_tokens": 10, "output_tokens": 5, "cached_input_tokens": 0, "reasoning_tokens": 0}
	session := map[string]any{"model_calls": 2, "input_tokens": 20, "output_tokens": 10, "cached_input_tokens": 0, "reasoning_tokens": 0}
	require.NoError(t, writer.WriteEvent(dexagents.AgentEventTypeUsageRecorded, map[string]any{"recorded": usage, "session": session}))
	require.NoError(t, writer.WriteEvent(dexagents.AgentEventTypeInterruptRaised, map[string]any{
		"interrupt_id": "call-2", "tool_call_id": "call-2", "tool_name": "refund_order", "deadline": nil,
		"kind": "tool_approval", "question": nil, "choices": []string{}, "allows_free_text": true,
	}))
	require.NoError(t, writer.WriteEvent(dexagents.AgentEventTypeInterruptResolved, map[string]any{
		"interrupt_id": "call-2", "approved": false, "rejection_message": ptr("Ünï <no>"),
		"kind": "tool_approval", "answered": false, "cancellation_reason": nil,
	}))
	require.NoError(t, writer.WriteTextDelta("Ünïcode ✓ \"q\" \\ \t"))
	require.Len(t, goEvents, len(python.Events))
	for index, expected := range python.Events {
		require.Equal(t, expected.Type, goEvents[index].Type)
		require.Equal(t, expected.Payload, goEvents[index].Payload, expected.Type)
		require.Equal(t, expected.Attempt, goEvents[index].Attempt)
		require.Equal(t, expected.StepExecutionID, goEvents[index].StepExecutionID)
	}

	tool, err := stepwise.NewFunctionTool("checked", "Check arguments.", python.Schema.Schema, dexagents.ToolEffectRead,
		func(context.Context, json.RawMessage, stepwise.ToolCallContext) (string, error) { return "ok", nil })
	require.NoError(t, err)
	require.Len(t, python.Schema.Messages, len(python.Schema.Arguments))
	for index, arguments := range python.Schema.Arguments {
		output, err := tool.Invoke(context.Background(), arguments, stepwise.ToolCallContext{})
		if err != nil {
			output = err.Error()
		}
		require.Equal(t, python.Schema.Messages[index], output, arguments)
	}

	anthropicItem := goAnthropicItem(t)
	require.Equal(t, python.Anthropic.Item, anthropicItem.Item, "Go stores an Anthropic response as the Python core does")
	require.Equal(t, "anthropic", anthropicItem.Provider)
	require.Equal(t, "chat_completions", anthropicItem.API)
	require.Len(t, python.Anthropic.Replayed, 1)
	require.Equal(t, []any{
		map[string]any{"type": "thinking", "thinking": "Check the order.", "signature": "sig=="},
		map[string]any{"type": "redacted_thinking", "data": "opaque=="},
	}, python.Anthropic.Replayed[0]["thinking_blocks"], "the Python core replays the item with its thinking")

	items := []stepwise.ModelInputItem{
		stepwise.UserTextItem{Text: "Ünïcode <&> \"q\""},
		stepwise.ToolResultItem{ToolCallID: "call-1", ToolName: "lookup_order", Output: "ok ✓"},
		stepwise.ModelOutputItem{Provider: "openai", API: "responses", Item: `{"type":"message"}`, Text: ptr("hi")},
		stepwise.ModelOutputItem{Provider: "dex-scripted", API: "scripted", Item: `{"type":"function_call"}`,
			ToolCalls: []stepwise.ModelToolCall{{ToolCallID: "c", ToolName: "t", Arguments: `{"a": 1}`}}},
	}
	require.Len(t, python.StepwiseItems, len(items))
	for index, item := range items {
		message, err := stepwise.EncodeStepwiseMessage(item)
		require.NoError(t, err)
		require.Equal(t, python.StepwiseItems[index], message.FrameworkItem)
		decoded, err := stepwise.DecodeStepwiseMessage(dexagents.AgentMessage{FrameworkItem: python.StepwiseItems[index]})
		require.NoError(t, err)
		require.Equal(t, item, decoded)
	}
}

// canonicalPythonJSON writes Go's JSON as Python's codec does: sorted keys, compact, ASCII only.
func canonicalPythonJSON(t *testing.T, goJSON []byte) string {
	t.Helper()
	text, err := pythonjson.Marshal(json.RawMessage(goJSON),
		pythonjson.Options{IsCompact: true, ShouldSortKeys: true, ShouldEscapeNonASCII: true})
	require.NoError(t, err)
	return text
}

func runPythonScript(t *testing.T, script string, arguments ...string) string {
	t.Helper()
	scriptPath, err := filepath.Abs(filepath.Join("testdata", "python", script))
	require.NoError(t, err)
	command := exec.CommandContext(context.Background(), "uv",
		append([]string{"run", "--project", pythonCoreDir, "--frozen", "python", scriptPath}, arguments...)...)
	output, err := command.Output()
	if exitError, isExitError := err.(*exec.ExitError); isExitError {
		t.Fatalf("%s failed: %v\n%s", script, err, exitError.Stderr)
	}
	require.NoError(t, err)
	return string(output)
}

// goAnthropicItem streams the response that the Python script assembles through the Go client.
func goAnthropicItem(t *testing.T) stepwise.ModelOutputItem {
	t.Helper()
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Check the order."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig=="}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque=="}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Looking it up."}}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup_order","input":{}}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"order_id\": \"7\"}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			var typed struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(event), &typed); err != nil {
				panic(err)
			}
			if _, err := fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", typed.Type, event); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	client := anthropicmodel.NewMessagesModelClient(
		anthropic.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test-key")), "claude-test")
	response, err := client.CallModel(context.Background(), stepwise.ModelRequest{
		InputItems: []stepwise.ModelInputItem{stepwise.UserTextItem{Text: "where is 7?"}},
	}, discardedOutput{})
	require.NoError(t, err)
	require.Len(t, response.OutputItems, 1)
	return response.OutputItems[0]
}

type discardedOutput struct{}

func (discardedOutput) WriteTextDelta(string) error { return nil }

func (discardedOutput) WriteReasoningDelta(string) error { return nil }
