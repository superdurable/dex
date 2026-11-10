// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex-connectors-library/sdkgo"
	"github.com/superdurable/dex-connectors-library/sdkgo/singledispatch"
)

// fakeSlack is a Slack Web API that counts chat.postMessage requests and answers as each test scripts it.
type fakeSlack struct {
	server       *httptest.Server
	posts        atomic.Int32
	mu           sync.Mutex
	postBehavior func(response http.ResponseWriter, request *http.Request, postNumber int32)
}

func newFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	slack := &fakeSlack{postBehavior: answerPostSent}
	slack.server = httptest.NewServer(http.HandlerFunc(slack.serve))
	t.Cleanup(slack.server.Close)
	return slack
}

func (slack *fakeSlack) serve(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") == "" {
		http.Error(response, "missing token", http.StatusUnauthorized)
		return
	}
	switch request.URL.Path {
	case "/chat.postMessage":
		postNumber := slack.posts.Add(1)
		slack.mu.Lock()
		behavior := slack.postBehavior
		slack.mu.Unlock()
		behavior(response, request, postNumber)
	case "/conversations.replies":
		writeSlackJSON(response, map[string]any{"ok": true, "messages": []map[string]any{{
			"ts": request.URL.Query().Get("oldest"), "thread_ts": request.URL.Query().Get("ts"), "user": "U0456789012", "text": "Approved for dispatch.",
		}}})
	default:
		http.NotFound(response, request)
	}
}

func (slack *fakeSlack) setPostBehavior(behavior func(http.ResponseWriter, *http.Request, int32)) {
	slack.mu.Lock()
	defer slack.mu.Unlock()
	slack.postBehavior = behavior
}

func answerPostSent(response http.ResponseWriter, request *http.Request, _ int32) {
	var payload map[string]string
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	writeSlackJSON(response, map[string]any{"ok": true, "channel": payload["channel"], "ts": "1710000011.000600", "message": map[string]any{
		"ts": "1710000011.000600", "thread_ts": payload["thread_ts"], "user": "U0BOT123456", "text": payload["text"],
	}})
}

func writeSlackJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		panic(err)
	}
}

var threadReplyArguments = map[string]any{"channelId": "C0123456789", "threadTimestamp": "1710000000.000100", "text": "Processing complete."}

func TestToolsListOffersAgentOperationsWithDexMeta(t *testing.T) {
	harness := newTestHarness(t, allConnectorFixtures("http://127.0.0.1:1"), nil)
	tools := harness.listTools(t)
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	require.ElementsMatch(t, []string{
		"slack_list_thread_messages", "slack_get_thread_reply", "slack_post_channel_message", "slack_post_thread_reply",
		"gmail_get_message", "gmail_send_message", "gmail_reply_to_message",
		"hacker-news-daily_list_story_i_ds", "hacker-news-daily_get_item",
		"google-youtube_search_videos", "google-youtube_get_video", "google-youtube_get_channel", "google-youtube_list_playlist_items",
		"reddit_get_post", "spotify_list_playlist_tracks",
	}, names)

	reply := tools["slack_post_thread_reply"]
	require.Equal(t, "Post one reply to an existing Slack channel thread. Supply the channel ID, the parent message timestamp, and the reply text.", reply.Description)
	require.Equal(t, "non_idempotent_write", reply.Meta[metaKeyEffect])
	require.Equal(t, "recommended", reply.Meta[metaKeyApproval])
	require.Equal(t, "slack", reply.Meta[metaKeyConnector])
	require.Equal(t, "postThreadReply", reply.Meta[metaKeyOperation])
	require.Equal(t, "support-slack", reply.Meta[metaKeyConnection])
	require.False(t, reply.Annotations.ReadOnlyHint)
	require.False(t, reply.Annotations.IdempotentHint)
	schema, isObject := reply.InputSchema.(map[string]any)
	require.True(t, isObject)
	require.ElementsMatch(t, []any{"channelId", "text", "threadTimestamp"}, schema["required"])

	read := tools["slack_get_thread_reply"]
	require.Equal(t, "read", read.Meta[metaKeyEffect])
	require.NotContains(t, read.Meta, metaKeyApproval)
	require.True(t, read.Annotations.ReadOnlyHint)
}

func TestReadCallReturnsStructuredContent(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	session := harness.connect(t)
	result, err := callWithKey(t, session, "slack_get_thread_reply", map[string]any{
		"channelId": "C0123456789", "threadTimestamp": "1710000000.000100", "replyTimestamp": "1710000003.000400",
	}, "flow-1/turn-1/call-read")
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
	structured, isObject := result.StructuredContent.(map[string]any)
	require.True(t, isObject)
	message := structured["message"].(map[string]any)
	require.Equal(t, "Approved for dispatch.", message["text"])
	require.Equal(t, "found", result.Meta[metaKeyBranch])
	require.True(t, strings.HasPrefix(resultText(t, result), `{"message":{`))
	entries, err := os.ReadDir(harness.stateDirectory)
	require.NoError(t, err)
	require.Empty(t, entries, "a Query keeps no call state")
}

func TestKeyedWriteRetriedSendsOnce(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	session := harness.connect(t)
	first, err := callWithKey(t, session, "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/call-1")
	require.NoError(t, err)
	require.False(t, first.IsError, resultText(t, first))
	for range 2 {
		retried, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/call-1")
		require.NoError(t, err)
		require.False(t, retried.IsError)
		require.Equal(t, true, retried.Meta[metaKeyReplayed])
		require.Equal(t, resultText(t, first), resultText(t, retried))
	}
	require.Equal(t, int32(1), slack.posts.Load())

	reused, err := callWithKey(t, session, "slack_post_thread_reply", map[string]any{
		"channelId": "C0123456789", "threadTimestamp": "1710000000.000100", "text": "Another reply.",
	}, "flow-1/turn-1/call-1")
	require.NoError(t, err)
	require.True(t, reused.IsError)
	require.Equal(t, "rejected", reused.Meta[metaKeyOutcome])
	require.Contains(t, resultText(t, reused), "different tool call")
	require.Equal(t, int32(1), slack.posts.Load())
}

func TestConcurrentCallsWithOneKeySendOnce(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/concurrent")
			require.NoError(t, err)
			require.False(t, result.IsError)
		})
	}
	group.Wait()
	require.Equal(t, int32(1), slack.posts.Load())
}

func TestUncertainWriteIsOutcomeUnknownAndNeverResent(t *testing.T) {
	slack := newFakeSlack(t)
	slack.setPostBehavior(func(response http.ResponseWriter, request *http.Request, _ int32) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			panic(err)
		}
		connection, _, err := response.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		if err := connection.Close(); err != nil {
			panic(err)
		}
	})
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	for range 2 {
		result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/uncertain")
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "unknown", result.Meta[metaKeyOutcome])
		require.Equal(t, "uncertain", result.Meta[metaKeyBranch])
	}
	require.Equal(t, int32(1), slack.posts.Load())
}

func TestClaimedCheckpointFromLostAttemptNeverResends(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	key := "flow-1/turn-1/lost-attempt"
	stateKey := uuid.NewSHA1(callStateNamespace, []byte(key)).String()
	digest := requestDigest("slack_post_thread_reply", mustMarshal(t, threadReplyArguments))
	heartbeat := mustMarshal(t, singledispatch.Marker{CallID: sdkCallIDOf(key, "slack", "postThreadReply"), Attempt: 1, State: singledispatch.StateClaimed})
	require.NoError(t, harness.server.source.calls.store.write(stateKey, callStateRecord{
		ConnectorID: "slack", OperationID: "postThreadReply", ConnectionName: "support-slack",
		RequestDigest: digest, Attempt: 1, Heartbeat: heartbeat,
	}))
	result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, key)
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "unknown", result.Meta[metaKeyOutcome])
	require.Equal(t, int32(0), slack.posts.Load())
}

func TestRejectedWriteHasRejectedOutcome(t *testing.T) {
	slack := newFakeSlack(t)
	slack.setPostBehavior(func(response http.ResponseWriter, _ *http.Request, _ int32) {
		writeSlackJSON(response, map[string]any{"ok": false, "error": "thread_not_found"})
	})
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/rejected")
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "rejected", result.Meta[metaKeyOutcome])
	require.Equal(t, "providerRejected", result.Meta[metaKeyBranch])
	require.Contains(t, resultText(t, result), "slack postThreadReply returned providerRejected")
}

func TestConnectorRetryIsRetryableErrorAndSameKeySendsAgain(t *testing.T) {
	slack := newFakeSlack(t)
	slack.setPostBehavior(func(response http.ResponseWriter, request *http.Request, postNumber int32) {
		if postNumber == 1 {
			response.Header().Set("Retry-After", "1")
			http.Error(response, "rate limited", http.StatusTooManyRequests)
			return
		}
		answerPostSent(response, request, postNumber)
	})
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	_, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/rate-limited")
	var wireError *jsonrpc.Error
	require.True(t, errors.As(err, &wireError), "error %v", err)
	require.Equal(t, int64(jsonrpc.CodeInternalError), wireError.Code)
	var data map[string]any
	require.NoError(t, json.Unmarshal(wireError.Data, &data))
	require.Equal(t, true, data[errorDataKeyRetryable])
	require.Equal(t, true, data[errorDataKeyNotSent])
	require.Equal(t, float64(1000), data[errorDataKeyRetryAfterMs])

	result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "flow-1/turn-1/rate-limited")
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
	require.Equal(t, int32(2), slack.posts.Load())
}

func TestInvalidArgumentsAreRejectedBeforeAnyState(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", map[string]any{"channelId": "C0123456789"}, "flow-1/turn-1/invalid")
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, "rejected", result.Meta[metaKeyOutcome])
	require.Contains(t, resultText(t, result), "invalid arguments for slack_post_thread_reply")
	entries, err := os.ReadDir(harness.stateDirectory)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Equal(t, int32(0), slack.posts.Load())
}

func TestWriteWithoutKeyRunsOnceWithoutState(t *testing.T) {
	slack := newFakeSlack(t)
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", slack.server.URL)}, nil)
	result, err := callWithKey(t, harness.connect(t), "slack_post_thread_reply", threadReplyArguments, "")
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
	require.Equal(t, int32(1), slack.posts.Load())
	entries, err := os.ReadDir(harness.stateDirectory)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestMissingOrAmbiguousConnectionsAreSkipped(t *testing.T) {
	t.Run("selected connection is not authorized", func(t *testing.T) {
		harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", "")},
			[]ConnectionSelector{{ConnectorID: "slack", ConnectionName: "absent"}})
		require.Empty(t, harness.listTools(t))
		require.Contains(t, harness.logs.String(), "connection is not authorized in Dex Web")
	})
	t.Run("two connections of one connector without a selection", func(t *testing.T) {
		harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", ""), slackFixture("ops-slack", "")}, nil)
		require.Empty(t, harness.listTools(t))
		require.Contains(t, harness.logs.String(), "select one with --connection")
	})
	t.Run("explicit selection picks one of two", func(t *testing.T) {
		harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", ""), slackFixture("ops-slack", "")},
			[]ConnectionSelector{{ConnectorID: "slack", ConnectionName: "ops-slack"}})
		tools := harness.listTools(t)
		require.Len(t, tools, 4)
		require.Equal(t, "ops-slack", tools["slack_post_thread_reply"].Meta[metaKeyConnection])
	})
	t.Run("no connections file, then one appears", func(t *testing.T) {
		harness := newTestHarness(t, nil, nil)
		require.Empty(t, harness.listTools(t))
		writeConnectionsFile(t, harness.configDirectory, []fixtureConnection{slackFixture("support-slack", "")})
		require.Len(t, harness.listTools(t), 4)
	})
	t.Run("unreadable connections file", func(t *testing.T) {
		harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", "")}, nil)
		require.NoError(t, os.Chmod(filepath.Join(harness.configDirectory, connectionsFileName), 0o644))
		require.Empty(t, harness.listTools(t))
		require.Contains(t, harness.logs.String(), "permissions must be 0600")
	})
}

func TestSelectorsMustNameOneCompiledConnectionPerConnector(t *testing.T) {
	_, err := NewServer(&Config{
		ConnectorConfigDirectory: t.TempDir(), StateDirectory: t.TempDir(),
		Connections: []ConnectionSelector{{ConnectorID: "jira", ConnectionName: "work"}},
	}, discardLogger())
	require.ErrorContains(t, err, `Connector "jira" is not compiled into dexcli`)
	_, err = NewServer(&Config{
		ConnectorConfigDirectory: t.TempDir(), StateDirectory: t.TempDir(),
		Connections: []ConnectionSelector{{ConnectorID: "slack", ConnectionName: "a"}, {ConnectorID: "slack", ConnectionName: "b"}},
	}, discardLogger())
	require.ErrorContains(t, err, "select one Connector twice")
}

func TestHostCheckRefusesRebindingNames(t *testing.T) {
	harness := newTestHarness(t, []fixtureConnection{slackFixture("support-slack", "")}, nil, "tunnel.example.com")
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	for _, testCase := range []struct {
		host       string
		wantStatus int
	}{
		{host: "attacker.example", wantStatus: http.StatusForbidden},
		{host: "localhost:8803", wantStatus: http.StatusOK},
		{host: "tunnel.example.com", wantStatus: http.StatusOK},
		{host: "127.0.0.1", wantStatus: http.StatusOK},
	} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, harness.endpoint, strings.NewReader(initialize))
		require.NoError(t, err)
		request.Host = testCase.host
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, testCase.wantStatus, response.StatusCode, testCase.host)
	}
}

func TestListenAddressMustBeLoopback(t *testing.T) {
	require.NoError(t, validateListenAddress("127.0.0.1:8803"))
	require.NoError(t, validateListenAddress("[::1]:8803"))
	require.NoError(t, validateListenAddress("localhost:8803"))
	require.ErrorContains(t, validateListenAddress("0.0.0.0:8803"), "loopback")
	require.ErrorContains(t, validateListenAddress("192.168.1.10:8803"), "loopback")
}

func mustMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// sdkCallIDOf repeats the connector SDK's Call ID derivation for a local call with idempotencyKey.
func sdkCallIDOf(idempotencyKey string, connectorID string, operationID string) sdkgo.CallID {
	namespace := uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://superdurable.dev/dex-connectors/call-id/v1"))
	var payload []byte
	for _, field := range []string{localFlowID, idempotencyKey, connectorID, operationID} {
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(field)))
		payload = append(payload, field...)
	}
	return sdkgo.CallID(uuid.NewSHA1(namespace, payload).String())
}
