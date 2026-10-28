// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package aiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIModelSendsTheConversationAndReadsToolCalls(t *testing.T) {
	var received chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected request %s with %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode the request: %v", err)
		}
		_, err := fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"wait","arguments":"{\"seconds\":5}"}}]}}]}`)
		if err != nil {
			t.Errorf("write the response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	model := &OpenAIModel{BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "test-model", HTTPClient: server.Client()}

	reply, err := model.Complete(context.Background(), ModelRequest{
		SystemPrompt: "Be brief.",
		Messages: []Message{
			{Role: RoleUser, Content: "Read the page"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_0", Name: ToolFetchPage, Arguments: `{"url":"https://example.com"}`}}},
			{Role: RoleTool, ToolCallID: "call_0", ToolName: ToolFetchPage, Outcome: OutcomeDone, Content: "Title: Example"},
		},
		Tools: toolDefinitions,
	})

	require.NoError(t, err)
	require.Equal(t, []ToolCall{{ID: "call_1", Name: ToolWait, Arguments: `{"seconds":5}`}}, reply.ToolCalls)
	require.Equal(t, "test-model", received.Model)
	require.Len(t, received.Tools, len(toolDefinitions))
	require.Len(t, received.Messages, 4)
	require.Equal(t, "system", received.Messages[0].Role)
	require.Equal(t, "call_0", received.Messages[2].ToolCalls[0].ID)
	require.Equal(t, "call_0", received.Messages[3].ToolCallID)
}

func TestOpenAIModelReportsProviderFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
		if _, err := fmt.Fprint(writer, `{"error":{"message":"Rate limit reached."}}`); err != nil {
			t.Errorf("write the response: %v", err)
		}
	}))
	model := &OpenAIModel{BaseURL: server.URL, APIKey: "test-key", Model: "test-model", HTTPClient: server.Client()}

	_, rejected := model.Complete(context.Background(), ModelRequest{})
	server.Close()
	_, unreachable := model.Complete(context.Background(), ModelRequest{})

	var providerError *ModelProviderError
	require.ErrorAs(t, rejected, &providerError)
	require.Equal(t, http.StatusTooManyRequests, providerError.StatusCode)
	require.Equal(t, "model provider API returned HTTP 429: Rate limit reached.", rejected.Error())
	require.ErrorAs(t, unreachable, &providerError)
	require.Zero(t, providerError.StatusCode)
}

func TestMockModelTurnsCommandsIntoToolCalls(t *testing.T) {
	for command, expected := range map[string]ToolCall{
		"/fetch https://example.com": {Name: ToolFetchPage, Arguments: `{"url":"https://example.com"}`},
		"/publish Aurora ships":      {Name: ToolPublishNote, Arguments: `{"text":"Aurora ships"}`},
		"/slack Aurora ships":        {Name: ToolPostToSlack, Arguments: `{"text":"Aurora ships"}`},
		"/wait 30":                   {Name: ToolWait, Arguments: `{"seconds":30}`},
		"/ask Which release?":        {Name: ToolAskUser, Arguments: `{"question":"Which release?"}`},
	} {
		reply, err := MockModel{}.Complete(context.Background(), ModelRequest{Messages: []Message{{Role: RoleUser, Content: command}}})
		require.NoError(t, err)
		require.Len(t, reply.ToolCalls, 1, command)
		require.Equal(t, expected.Name, reply.ToolCalls[0].Name, command)
		require.JSONEq(t, expected.Arguments, reply.ToolCalls[0].Arguments, command)
	}
}

func TestPageTitleAndTextDropsMarkup(t *testing.T) {
	title, text := pageTitleAndText(`<html><head><title>Release &amp; notes</title><style>p{}</style></head>
		<body><script>alert(1)</script><h1>Aurora</h1><p>ships   today.</p></body></html>`)

	require.Equal(t, "Release & notes", title)
	require.Equal(t, "Aurora ships today.", text)
}
