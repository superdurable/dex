// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package anthropicmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
)

type fakeMessagesServer struct {
	mutex      sync.Mutex
	bodies     []map[string]any
	statusCode int
	events     []string
}

func (server *fakeMessagesServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(server.statusCode)
		if _, err := writer.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`)); err != nil {
			return
		}
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

func newTestClient(t *testing.T, server http.Handler) *MessagesModelClient {
	t.Helper()
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return NewMessagesModelClient(anthropic.NewClient(option.WithBaseURL(httpServer.URL), option.WithAPIKey("test-key")), "claude-test")
}

func TestMessagesModelClientStreamsOneRequestAndStoresTheLiteLLMShape(t *testing.T) {
	server := &fakeMessagesServer{events: []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test-2026","content":[],"stop_reason":null,"usage":{"input_tokens":20,"cache_read_input_tokens":5,"cache_creation_input_tokens":1,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Check the order."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig=="}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque=="}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Looking "}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"it up."}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup_order","input":{}}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"order_id\":"}}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":" \"7\"}"}}`,
		`{"type":"content_block_stop","index":3}`,
		`{"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"toolu_2","name":"lookup_order","input":{"order_id":"8"}}}`,
		`{"type":"content_block_stop","index":4}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":30,"thinking_tokens":12}}`,
		`{"type":"message_stop"}`,
	}}
	client := newTestClient(t, server)
	maxOutputTokens := int64(512)
	output := &recordedOutput{}

	response, err := client.CallModel(context.Background(), builtinloop.ModelRequest{
		Instructions: "Help.",
		InputItems: []builtinloop.ModelInputItem{
			builtinloop.UserTextItem{Text: "where is 6?"},
			// The Python core's LiteLLM item for an Anthropic response.
			builtinloop.ModelOutputItem{Provider: "anthropic", API: "chat_completions",
				Item:      `{"role":"assistant","content":"Checking 6.","tool_calls":[{"id":"toolu_0","type":"function","function":{"name":"lookup_order","arguments":"{\"order_id\": \"6\"}"}}],"thinking_blocks":[{"type":"thinking","thinking":"t","signature":"keep=="},{"type":"redacted_thinking","data":"r=="}]}`,
				Text:      ptr("Checking 6."),
				ToolCalls: []builtinloop.ModelToolCall{{ToolCallID: "toolu_0", ToolName: "lookup_order", Arguments: `{"order_id": "6"}`}}},
			builtinloop.ToolResultItem{ToolCallID: "toolu_0", ToolName: "lookup_order", Output: "shipped"},
			builtinloop.UserTextItem{Text: "and 7?"},
			// The TypeScript core's AI SDK item for an Anthropic response.
			builtinloop.ModelOutputItem{Provider: "anthropic.messages", API: "ai-sdk",
				Item:      `{"role":"assistant","content":[{"type":"reasoning","text":"u","providerOptions":{"anthropic":{"signature":"ts=="}}},{"type":"text","text":"Checking."},{"type":"tool-call","toolCallId":"toolu_9","toolName":"lookup_order","input":{"order_id":"7"}}]}`,
				Text:      ptr("Checking."),
				ToolCalls: []builtinloop.ModelToolCall{{ToolCallID: "toolu_9", ToolName: "lookup_order", Arguments: `{"order_id":"7"}`}}},
			builtinloop.ToolResultItem{ToolCallID: "toolu_9", ToolName: "lookup_order", Output: "pending"},
			builtinloop.UserTextItem{Text: "and 8?"},
			// Another provider's item replays as text and tool calls only.
			builtinloop.ModelOutputItem{Provider: "openai", API: "responses", Item: `{}`, Text: ptr("Checking 8."),
				ToolCalls: []builtinloop.ModelToolCall{{ToolCallID: "call_8", ToolName: "lookup_order", Arguments: `{"order_id":"8"}`}}},
			builtinloop.ToolResultItem{ToolCallID: "call_8", ToolName: "lookup_order", Output: "late"},
		},
		Tools: []builtinloop.ModelToolDefinition{{Name: "lookup_order", Description: "Look up.",
			Parameters: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: builtinloop.ToolChoice{Mode: builtinloop.ToolChoiceModeRequired},
		Settings:   builtinloop.ModelSettings{MaxOutputTokens: &maxOutputTokens},
	}, output)
	require.NoError(t, err)

	require.Len(t, server.bodies, 1)
	body := server.bodies[0]
	require.Equal(t, "claude-test", body["model"])
	require.Equal(t, true, body["stream"])
	require.Equal(t, float64(512), body["max_tokens"])
	require.Equal(t, "Help.", body["system"])
	require.Equal(t, map[string]any{"type": "any"}, body["tool_choice"])
	require.Equal(t, []any{map[string]any{"name": "lookup_order", "description": "Look up.", "input_schema": map[string]any{"type": "object"}}}, body["tools"])
	require.Equal(t, []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "where is 6?"}}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "t", "signature": "keep=="},
			map[string]any{"type": "redacted_thinking", "data": "r=="},
			map[string]any{"type": "text", "text": "Checking 6."},
			map[string]any{"type": "tool_use", "id": "toolu_0", "name": "lookup_order", "input": map[string]any{"order_id": "6"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_0", "content": "shipped"},
			map[string]any{"type": "text", "text": "and 7?"},
		}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "u", "signature": "ts=="},
			map[string]any{"type": "text", "text": "Checking."},
			map[string]any{"type": "tool_use", "id": "toolu_9", "name": "lookup_order", "input": map[string]any{"order_id": "7"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_9", "content": "pending"},
			map[string]any{"type": "text", "text": "and 8?"},
		}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "Checking 8."},
			map[string]any{"type": "tool_use", "id": "call_8", "name": "lookup_order", "input": map[string]any{"order_id": "8"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call_8", "content": "late"},
		}},
	}, body["messages"])

	require.Equal(t, []string{"Looking ", "it up."}, output.textDeltas)
	require.Equal(t, []string{"Check the order."}, output.reasoningDeltas)
	require.Len(t, response.OutputItems, 1)
	require.Equal(t, "anthropic", response.OutputItems[0].Provider)
	require.Equal(t, "chat_completions", response.OutputItems[0].API)
	require.Equal(t, `{"role":"assistant","content":"Looking it up.","tool_calls":[`+
		`{"id":"toolu_1","type":"function","function":{"name":"lookup_order","arguments":"{\"order_id\": \"7\"}"}},`+
		`{"id":"toolu_2","type":"function","function":{"name":"lookup_order","arguments":"{\"order_id\":\"8\"}"}}],`+
		`"thinking_blocks":[{"type":"thinking","thinking":"Check the order.","signature":"sig=="},{"type":"redacted_thinking","data":"opaque=="}]}`,
		response.OutputItems[0].Item)
	require.Equal(t, "Looking it up.", response.Text)
	require.Equal(t, []builtinloop.ModelToolCall{
		{ToolCallID: "toolu_1", ToolName: "lookup_order", Arguments: `{"order_id": "7"}`},
		{ToolCallID: "toolu_2", ToolName: "lookup_order", Arguments: `{"order_id":"8"}`},
	}, response.ToolCalls, "input that arrives whole in content_block_start is the call's arguments")
	require.Equal(t, "claude-test-2026", response.ResponseModel)
	require.Equal(t, "tool_use", response.FinishReason)
	require.Equal(t, dexagents.AgentUsage{ModelCalls: 1, InputTokens: 26, OutputTokens: 30, CachedInputTokens: 5, ReasoningTokens: 12}, response.Usage)
}

// An empty response stores no item, and an empty stored item is never sent: the API rejects
// empty assistant content on every later request.
func TestMessagesModelClientNeverStoresOrSendsAnEmptyAssistantMessage(t *testing.T) {
	server := &fakeMessagesServer{events: []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":3,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	}}
	client := newTestClient(t, server)
	response, err := client.CallModel(context.Background(), builtinloop.ModelRequest{
		InputItems: []builtinloop.ModelInputItem{
			builtinloop.UserTextItem{Text: "hi"},
			builtinloop.ModelOutputItem{Provider: "anthropic", API: "chat_completions", Item: `{"role":"assistant","content":null}`},
			builtinloop.ModelOutputItem{Provider: "anthropic", API: "chat_completions", Item: `{"role":"assistant","content":""}`},
			builtinloop.UserTextItem{Text: "again"},
		},
	}, &recordedOutput{})
	require.NoError(t, err)
	require.Empty(t, response.OutputItems)
	require.Equal(t, []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "text", "text": "again"},
		}},
	}, server.bodies[0]["messages"])
}

// A tool with no input stores "" as its arguments, as the Python core does; the call reads "{}".
func TestMessagesModelClientStoresEmptyArgumentsAsPythonDoes(t *testing.T) {
	server := &fakeMessagesServer{events: []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":3,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"list_orders","input":{}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
	}}
	client := newTestClient(t, server)
	response, err := client.CallModel(context.Background(), builtinloop.ModelRequest{
		InputItems: []builtinloop.ModelInputItem{builtinloop.UserTextItem{Text: "list them"}},
	}, &recordedOutput{})
	require.NoError(t, err)
	require.Equal(t, `{"role":"assistant","content":null,"tool_calls":[{"id":"toolu_1","type":"function","function":{"name":"list_orders","arguments":""}}]}`,
		response.OutputItems[0].Item)
	require.Equal(t, []builtinloop.ModelToolCall{{ToolCallID: "toolu_1", ToolName: "list_orders", Arguments: "{}"}}, response.ToolCalls)
}

func TestMessagesModelClientMakesNoRetriesOfItsOwn(t *testing.T) {
	server := &fakeMessagesServer{statusCode: http.StatusServiceUnavailable}
	client := newTestClient(t, server)
	_, err := client.CallModel(context.Background(), builtinloop.ModelRequest{
		InputItems: []builtinloop.ModelInputItem{builtinloop.UserTextItem{Text: "hi"}},
	}, &recordedOutput{})
	require.Error(t, err)
	require.Len(t, server.bodies, 1, "RunModel attempts are the retries")
	require.Equal(t, float64(DefaultMaxOutputTokens), server.bodies[0]["max_tokens"])
	require.NotContains(t, server.bodies[0], "tools")
}

func TestMessagesModelClientFailsOnAnErrorEvent(t *testing.T) {
	server := &fakeMessagesServer{events: []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
	}}
	client := newTestClient(t, server)
	_, err := client.CallModel(context.Background(), builtinloop.ModelRequest{
		InputItems: []builtinloop.ModelInputItem{builtinloop.UserTextItem{Text: "hi"}},
	}, &recordedOutput{})
	require.Error(t, err)
}

func ptr[Value any](value Value) *Value {
	return &value
}
