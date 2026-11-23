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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/sdk-go/dex"
)

// fakeOpenAIServer answers chat completions by the last user text and counts the requests per text.
type fakeOpenAIServer struct {
	mutex    sync.Mutex
	requests map[string]int
}

func newFakeOpenAIServer(t *testing.T) (*fakeOpenAIServer, string) {
	t.Helper()
	fake := &fakeOpenAIServer{requests: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(fake.serveChatCompletion))
	t.Cleanup(server.Close)
	return fake, server.URL + "/v1"
}

func (fake *fakeOpenAIServer) requestCount(lastUserText string) int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	return fake.requests[lastUserText]
}

func (fake *fakeOpenAIServer) serveChatCompletion(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	lastUser := ""
	for _, message := range body.Messages {
		if message.Role == "user" {
			lastUser = message.Content
		}
	}
	fake.mutex.Lock()
	fake.requests[lastUser]++
	count := fake.requests[lastUser]
	fake.mutex.Unlock()
	response.Header().Set("Content-Type", "application/json")
	switch {
	case lastUser == "rate limited once" && count == 1:
		response.Header().Set("Retry-After", "1")
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = response.Write([]byte(`{"error":{"message":"slow down","type":"rate_limit_exceeded"}}`))
	case lastUser == "bad request":
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"error":{"message":"the request is malformed","type":"invalid_request_error"}}`))
	default:
		_, _ = response.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-test",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"OpenAI says: ` + lastUser + `"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`))
	}
}

// An eino-ext OpenAI 429 backs off and succeeds on the next RunModel attempt, without a failed turn.
func TestOpenAIRateLimitBacksOffAndSucceeds(t *testing.T) {
	t.Parallel()
	fake, baseURL := newFakeOpenAIServer(t)
	stack := newAgentTestStackWithOpenAI(t, baseURL)
	stack.startWorker()
	sessions := stack.sessions(einoOpenAIAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "rate-limited", ptr("rate limited once"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoOpenAIAgentFlowType, "rate-limited", 1)
	require.Nil(t, snapshot.State.LastTurnError)
	require.Equal(t, "OpenAI says: rate limited once", *snapshot.Messages[1].Text)
	require.Equal(t, 2, fake.requestCount("rate limited once"))
	require.Equal(t, 2, stack.finalAttempt("rate-limited", "RunModel"))
	require.Equal(t, int64(1), snapshot.Usage.ModelCalls)
	require.Equal(t, int64(11), snapshot.Usage.InputTokens)
}

// An eino-ext OpenAI 400 ends the turn at once: a retry would get the same answer.
func TestOpenAIBadRequestEndsTheTurnWithoutRetries(t *testing.T) {
	t.Parallel()
	fake, baseURL := newFakeOpenAIServer(t)
	stack := newAgentTestStackWithOpenAI(t, baseURL)
	stack.startWorker()
	sessions := stack.sessions(einoOpenAIAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "bad-request", ptr("bad request"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(einoOpenAIAgentFlowType, "bad-request", 1)
	require.NotNil(t, snapshot.State.LastTurnError)
	require.Contains(t, *snapshot.State.LastTurnError, "400")
	require.Equal(t, 1, fake.requestCount("bad request"), "a 400 is not retried")
	require.Equal(t, []string{"StartAgentSession", "RunModel", "RecordTurnFailure"}, stack.executedStepTypes("bad-request"))
	require.Equal(t, 1, stack.finalAttempt("bad-request", "RunModel"))
}
