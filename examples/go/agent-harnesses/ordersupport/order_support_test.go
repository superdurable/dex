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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS. They read Flow
// history with dexcli (DEXCLI_PATH, DEXCLI, or PATH). Without a server they
// fail, unless DEX_EXAMPLES_ALLOW_SKIP=1.
package ordersupport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex-connectors-library/connectors/slack/go"
	"github.com/superdurable/dex-connectors-library/connectors/slack/go/slackmock"
	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"

	"github.com/superdurable/dex/examples/go/agent-harnesses/demomodel"
	"github.com/superdurable/dex/examples/go/agent-harnesses/orderdesk"
	"github.com/superdurable/dex/examples/go/agent-harnesses/ordersupport"
)

const (
	routePrefix = "/products/agent-harnesses/eino"
	turnTimeout = 90 * time.Second
)

var sessionKey = []byte("order-support-test-key")

func TestOrderSupportRoutesReadAndCancelOrders(t *testing.T) {
	orderDesk := orderdesk.NewOrderDesk(0)
	server := startOrderSupportServer(t, orderDesk, nil)
	session := server.startSession(t)

	server.sendMessage(t, session, "Look up order 1001")
	snapshot := server.waitForTurn(t, session, 1)
	if reply := lastAssistantText(snapshot); reply != demomodel.DemoReplyPrefix+"Order 1001: 2 notebooks, shipped." {
		t.Fatalf("reply = %q, want the order", reply)
	}

	server.sendMessage(t, session, "Cancel order 1002")
	interrupt := server.waitForInterrupt(t, session)
	if interrupt.ToolName != "cancel_order" {
		t.Fatalf("pending tool = %s, want cancel_order", interrupt.ToolName)
	}
	if calls := orderDesk.CancelOrderCalls(); len(calls) != 0 {
		t.Fatalf("cancel_order ran before approval: %q", calls)
	}
	server.resolveInterrupt(t, session, interrupt.InterruptID, true)
	snapshot = server.waitForTurn(t, session, 2)
	if calls := orderDesk.CancelOrderCalls(); !slices.Equal(calls, []string{"1002"}) {
		t.Fatalf("cancel_order calls = %q, want one call for 1002", calls)
	}
	if reply := lastAssistantText(snapshot); !strings.Contains(reply, "Order 1002 is cancelled") {
		t.Fatalf("reply = %q, want the cancellation", reply)
	}

	server.sendMessage(t, session, "Cancel order 1003")
	interrupt = server.waitForInterrupt(t, session)
	server.resolveInterrupt(t, session, interrupt.InterruptID, false)
	server.waitForTurn(t, session, 3)
	if calls := orderDesk.CancelOrderCalls(); !slices.Equal(calls, []string{"1002"}) {
		t.Fatalf("a rejected cancel_order ran: %q", calls)
	}
}

func TestOrderSupportRoutesRefuseCallsWithoutTheSessionToken(t *testing.T) {
	server := startOrderSupportServer(t, orderdesk.NewOrderDesk(0), nil)
	session := server.startSession(t)

	response := server.request(t, http.MethodGet, routePrefix+"/snapshot?session_id="+session.SessionID, nil, "wrong")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("snapshot with a wrong token = %d, want 403", response.StatusCode)
	}
	body := map[string]string{"session_id": session.SessionID, "text": "Cancel order 1002"}
	response = server.request(t, http.MethodPost, routePrefix+"/messages", body, "")
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("message without a token = %d, want 403", response.StatusCode)
	}
}

func TestSlackThreadReplyPostsOnceAfterApproval(t *testing.T) {
	slackMock := slackmock.New(t, ordersupport.SlackConnectionName)
	connection := slackMock.Connection()
	toolSources, err := ordersupport.NewSlackToolSources(&connection)
	if err != nil {
		t.Fatal(err)
	}
	server := startOrderSupportServer(t, orderdesk.NewOrderDesk(0), toolSources)
	session := server.startSession(t)

	server.sendMessage(t, session, "Reply in Slack thread C0123456789 1710000000.000100: Order 1002 ships on Thursday.")
	interrupt := server.waitForInterrupt(t, session)
	if interrupt.ToolName != ordersupport.SlackPostThreadReplyToolName {
		t.Fatalf("pending tool = %s, want %s", interrupt.ToolName, ordersupport.SlackPostThreadReplyToolName)
	}
	if calls := slackMock.PostThreadReply().Calls(); len(calls) != 0 {
		t.Fatalf("Slack posted before approval: %+v", calls)
	}
	server.resolveInterrupt(t, session, interrupt.InterruptID, true)
	server.waitForTurn(t, session, 1)

	calls := slackMock.PostThreadReply().Calls()
	if len(calls) != 1 {
		t.Fatalf("Slack posts = %+v, want one", calls)
	}
	want := slack.PostThreadReplyInput{
		ChannelID: "C0123456789", ThreadTimestamp: "1710000000.000100", Text: "Order 1002 ships on Thursday.",
	}
	if calls[0].Input != want || calls[0].FlowID != session.SessionID {
		t.Fatalf("Slack post = %+v, want %+v in session %s", calls[0], want, session.SessionID)
	}
	history, err := agentstest.ReadStepHistory(context.Background(), dexcliPath(t), flowServiceAddress(t), session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(agentstest.ExecutedStepTypes(history), "CallSingleAttemptTool") {
		t.Fatalf("executed Steps = %q, want the post in CallSingleAttemptTool", agentstest.ExecutedStepTypes(history))
	}
}

type orderSupportServer struct {
	url string
}

type startedSession struct {
	SessionID    string `json:"session_id"`
	SessionToken string `json:"session_token"`
}

func startOrderSupportServer(
	t *testing.T, orderDesk *orderdesk.OrderDesk, toolSources []dexagents.ToolSource,
) *orderSupportServer {
	t.Helper()
	sessionFlow, err := ordersupport.NewEinoOrderSupportFlow(orderDesk, demomodel.NewOrderSupportDemoModel(), toolSources)
	if err != nil {
		t.Fatal(err)
	}
	app, err := ordersupport.StartOrderSupportApp(context.Background(), sessionFlow, flowServiceAddress(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	ordersupport.NewSessionRoutes("eino", app.Sessions, sessionKey).Register(mux)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		httpServer.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Errorf("stop the order support app: %v", err)
		}
	})
	return &orderSupportServer{url: httpServer.URL}
}

func (server *orderSupportServer) startSession(t *testing.T) startedSession {
	t.Helper()
	var session startedSession
	decodeResponse(t, server.request(t, http.MethodPost, routePrefix+"/sessions", map[string]string{}, ""), &session)
	if session.SessionToken != ordersupport.SignSessionID(sessionKey, session.SessionID) {
		t.Fatalf("session token %q does not sign %s", session.SessionToken, session.SessionID)
	}
	return session
}

func (server *orderSupportServer) sendMessage(t *testing.T, session startedSession, text string) {
	t.Helper()
	body := map[string]string{"session_id": session.SessionID, "text": text}
	var accepted struct{ Accepted bool }
	decodeResponse(t, server.request(t, http.MethodPost, routePrefix+"/messages", body, session.SessionToken), &accepted)
	if !accepted.Accepted {
		t.Fatalf("the session did not accept %q", text)
	}
}

func (server *orderSupportServer) resolveInterrupt(t *testing.T, session startedSession, interruptID string, isApproved bool) {
	t.Helper()
	body := map[string]any{"session_id": session.SessionID, "interrupt_id": interruptID, "approved": isApproved}
	var accepted struct{ Accepted bool }
	decodeResponse(t, server.request(t, http.MethodPost, routePrefix+"/interrupts/resolve", body, session.SessionToken), &accepted)
	if !accepted.Accepted {
		t.Fatalf("the session did not accept the answer to %s", interruptID)
	}
}

func (server *orderSupportServer) readSnapshot(t *testing.T, session startedSession) dexagents.AgentSnapshot {
	t.Helper()
	var body struct {
		Snapshot dexagents.AgentSnapshot `json:"snapshot"`
	}
	response := server.request(t, http.MethodGet, routePrefix+"/snapshot?session_id="+session.SessionID, nil, session.SessionToken)
	decodeResponse(t, response, &body)
	return body.Snapshot
}

func (server *orderSupportServer) waitForTurn(t *testing.T, session startedSession, turnCount int64) dexagents.AgentSnapshot {
	t.Helper()
	return server.waitFor(t, session, func(snapshot dexagents.AgentSnapshot) bool {
		state := snapshot.State
		return state != nil && state.TurnCount >= turnCount &&
			state.Status == dexagents.AgentSessionStatusWaitingForUserMessage
	})
}

func (server *orderSupportServer) waitForInterrupt(t *testing.T, session startedSession) dexagents.AgentInterrupt {
	t.Helper()
	snapshot := server.waitFor(t, session, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.PendingInterrupts) > 0
	})
	return snapshot.PendingInterrupts[0]
}

func (server *orderSupportServer) waitFor(
	t *testing.T, session startedSession, isDone func(dexagents.AgentSnapshot) bool,
) dexagents.AgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(turnTimeout)
	for {
		snapshot := server.readSnapshot(t, session)
		if snapshot.State != nil && snapshot.State.Status == dexagents.AgentSessionStatusFailed {
			t.Fatalf("the session failed: %v", snapshot.State.LastTurnError)
		}
		if isDone(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session did not get there in %s; last snapshot state %+v", turnTimeout, snapshot.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (server *orderSupportServer) request(t *testing.T, method string, path string, body any, token string) *http.Response {
	t.Helper()
	var encoded bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(method, server.url+path, &encoded)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(ordersupport.SessionTokenHeader, token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func decodeResponse(t *testing.T, response *http.Response, value any) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s %s = %d", response.Request.Method, response.Request.URL.Path, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatal(err)
	}
}

func lastAssistantText(snapshot dexagents.AgentSnapshot) string {
	for index := len(snapshot.Messages) - 1; index >= 0; index-- {
		message := snapshot.Messages[index]
		if message.Role == dexagents.MessageRoleAssistant && message.Text != nil && *message.Text != "" {
			return *message.Text
		}
	}
	return ""
}

func flowServiceAddress(t *testing.T) string {
	t.Helper()
	address := os.Getenv("DEX_FLOW_SERVICE_ADDRESS")
	if address == "" {
		if os.Getenv("DEX_EXAMPLES_ALLOW_SKIP") == "1" {
			t.Skip("DEX_FLOW_SERVICE_ADDRESS is not set")
		}
		t.Fatal("set DEX_FLOW_SERVICE_ADDRESS to a running Dex server, or DEX_EXAMPLES_ALLOW_SKIP=1")
	}
	return address
}

func dexcliPath(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"DEXCLI_PATH", "DEXCLI"} {
		if path := os.Getenv(name); path != "" {
			return path
		}
	}
	path, err := exec.LookPath("dexcli")
	if err != nil {
		t.Fatal("set DEXCLI_PATH or DEXCLI to the dexcli executable, or put dexcli on PATH")
	}
	return path
}
