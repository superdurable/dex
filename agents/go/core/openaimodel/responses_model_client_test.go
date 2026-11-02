// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package openaimodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/stepwise"
)

// fakeResponsesServer serves one scripted SSE stream and records every request body.
type fakeResponsesServer struct {
	mutex      sync.Mutex
	bodies     []map[string]any
	statusCode int
	events     []string
}

func (server *fakeResponsesServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	server.mutex.Lock()
	server.bodies = append(server.bodies, decoded)
	server.mutex.Unlock()
	if server.statusCode != 0 {
		http.Error(writer, `{"error":{"message":"overloaded"}}`, server.statusCode)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	for _, event := range server.events {
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
}

type recordedOutput struct {
	textDeltas      []string
	reasoningDeltas []string
}

func (output *recordedOutput) WriteTextDelta(text string) error {
	output.textDeltas = append(output.textDeltas, text)
	return nil
}

func (output *recordedOutput) WriteReasoningDelta(text string) error {
	output.reasoningDeltas = append(output.reasoningDeltas, text)
	return nil
}

func newTestClient(t *testing.T, server http.Handler) *ResponsesModelClient {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return NewResponsesModelClient(openai.NewClient(option.WithBaseURL(httpServer.URL), option.WithAPIKey("test-key")), "gpt-test")
}

func TestResponsesModelClientStreamsOneRequestAndStoresReplayFields(t *testing.T) {
	server := &fakeResponsesServer{events: []string{
		`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","status":"in_progress","model":"gpt-test","output":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","sequence_number":1,"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"Thinking"}`,
		`{"type":"response.output_item.done","sequence_number":2,"output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Thinking"}],"encrypted_content":"opaque==","extra":"dropped"}}`,
		`{"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"Looking"}`,
		`{"type":"response.output_item.done","sequence_number":4,"output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup_order","arguments":"{\"order_id\":\"7\"}","status":"completed"}}`,
		`{"type":"response.output_item.done","sequence_number":5,"output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Looking","annotations":[]}]}}`,
		`{"type":"response.completed","sequence_number":6,"response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-test-2026","output":[],"usage":{"input_tokens":30,"output_tokens":12,"input_tokens_details":{"cached_tokens":5},"output_tokens_details":{"reasoning_tokens":4},"total_tokens":42}}}`,
	}}
	client := newTestClient(t, server)
	temperature := 0.2
	output := &recordedOutput{}

	response, err := client.CallModel(context.Background(), stepwise.ModelRequest{
		Instructions: "Help.",
		InputItems: []stepwise.ModelInputItem{
			stepwise.UserTextItem{Text: "where is 7?"},
			stepwise.ModelOutputItem{Provider: "openai", API: "responses", Item: `{"type":"reasoning","id":"rs_0","encrypted_content":"keep=="}`},
			stepwise.ModelOutputItem{Provider: "dex-scripted", API: "scripted", Item: `{"x":1}`, Text: ptr("earlier"),
				ToolCalls: []stepwise.ModelToolCall{{ToolCallID: "c0", ToolName: "lookup_order", Arguments: `{"order_id":"6"}`}}},
			stepwise.ToolResultItem{ToolCallID: "c0", ToolName: "lookup_order", Output: "shipped"},
		},
		Tools: []stepwise.ModelToolDefinition{{Name: "lookup_order", Description: "Look up.",
			Parameters: json.RawMessage(`{"type":"object","properties":{"order_id":{"type":"string"}}}`)}},
		ToolChoice: stepwise.ToolChoice{Mode: stepwise.ToolChoiceModeAuto},
		Settings:   stepwise.ModelSettings{Temperature: &temperature, ProviderOptions: map[string]any{"parallel_tool_calls": false}},
	}, output)
	require.NoError(t, err)

	require.Len(t, server.bodies, 1)
	body := server.bodies[0]
	require.Equal(t, "gpt-test", body["model"])
	require.Equal(t, true, body["stream"])
	require.Equal(t, false, body["store"])
	require.Equal(t, "Help.", body["instructions"])
	require.Equal(t, []any{"reasoning.encrypted_content"}, body["include"])
	require.Equal(t, map[string]any{"summary": "auto"}, body["reasoning"])
	require.Equal(t, 0.2, body["temperature"])
	require.Equal(t, false, body["parallel_tool_calls"])
	require.Equal(t, "auto", body["tool_choice"])
	require.Equal(t, []any{map[string]any{"type": "function", "name": "lookup_order", "description": "Look up.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"order_id": map[string]any{"type": "string"}}},
		"strict":     false}}, body["tools"])
	require.Equal(t, []any{
		map[string]any{"type": "message", "role": "user", "content": "where is 7?"},
		map[string]any{"type": "reasoning", "id": "rs_0", "encrypted_content": "keep=="},
		map[string]any{"type": "message", "role": "assistant", "content": "earlier"},
		map[string]any{"type": "function_call", "call_id": "c0", "name": "lookup_order", "arguments": `{"order_id":"6"}`},
		map[string]any{"type": "function_call_output", "call_id": "c0", "output": "shipped"},
	}, body["input"])

	require.Equal(t, []string{"Looking"}, output.textDeltas)
	require.Equal(t, []string{"Thinking"}, output.reasoningDeltas)
	require.Len(t, response.OutputItems, 3)
	require.Equal(t, `{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"Thinking"}],"encrypted_content":"opaque=="}`,
		response.OutputItems[0].Item, "only replay fields are stored, in order, with opaque content unchanged")
	require.Equal(t, "Looking", *response.OutputItems[1].Text)
	require.Equal(t, "openai", response.OutputItems[2].Provider)
	require.Equal(t, "responses", response.OutputItems[2].API)
	require.Equal(t, []stepwise.ModelToolCall{{ToolCallID: "call_1", ToolName: "lookup_order", Arguments: `{"order_id":"7"}`}}, response.ToolCalls)
	require.Equal(t, "Looking", response.Text)
	require.Equal(t, "gpt-test-2026", response.ResponseModel)
	require.Equal(t, "completed", response.FinishReason)
	require.Equal(t, dexagents.AgentUsage{ModelCalls: 1, InputTokens: 30, OutputTokens: 12, CachedInputTokens: 5, ReasoningTokens: 4}, response.Usage)
}

func TestResponsesModelClientMakesNoRetriesOfItsOwn(t *testing.T) {
	server := &fakeResponsesServer{statusCode: http.StatusTooManyRequests}
	client := newTestClient(t, server)
	_, err := client.CallModel(context.Background(), stepwise.ModelRequest{
		InputItems: []stepwise.ModelInputItem{stepwise.UserTextItem{Text: "hi"}},
	}, &recordedOutput{})
	require.Error(t, err)
	require.Len(t, server.bodies, 1, "RunModel attempts are the retries")
}

func TestResponsesModelClientFailsAStreamWithoutCompletion(t *testing.T) {
	server := &fakeResponsesServer{events: []string{
		`{"type":"response.failed","sequence_number":0,"response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"boom"}}}`,
	}}
	client := newTestClient(t, server)
	_, err := client.CallModel(context.Background(), stepwise.ModelRequest{
		InputItems: []stepwise.ModelInputItem{stepwise.UserTextItem{Text: "hi"}},
		ToolChoice: stepwise.ToolChoice{Mode: stepwise.ToolChoiceModeNamed, ToolName: "x"},
	}, &recordedOutput{})
	require.ErrorContains(t, err, "stream failed")
	require.NotContains(t, server.bodies[0], "tools", "a request without tools sends no tools field")
	require.NotContains(t, server.bodies[0], "tool_choice")
}

func ptr[Value any](value Value) *Value {
	return &value
}
