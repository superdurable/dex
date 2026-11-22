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

// Tests run against the Dex server at DEX_FLOW_SERVICE_ADDRESS.

package ordersupport

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// SessionTokenHeader carries the session token that starting a session returns.
const SessionTokenHeader = "X-Agent-Session-Token"

// SessionRoutes serves one agent's sessions over HTTP. Each route is one call on AgentSessionClient:
//
//   - POST /products/agent-harnesses/<harness>/sessions starts a session.
//   - POST .../messages sends a user message.
//   - POST .../interrupts/resolve approves or rejects a tool call.
//   - GET .../snapshot?session_id=... reads the session.
type SessionRoutes struct {
	harnessName string
	sessions    *dexagents.AgentSessionClient
	sessionKey  []byte
}

// NewSessionRoutes returns the routes of harnessName's sessions; sessionKey signs their session tokens.
func NewSessionRoutes(harnessName string, sessions *dexagents.AgentSessionClient, sessionKey []byte) *SessionRoutes {
	return &SessionRoutes{harnessName: harnessName, sessions: sessions, sessionKey: sessionKey}
}

// Register adds the routes to mux.
func (routes *SessionRoutes) Register(mux *http.ServeMux) {
	prefix := "/products/agent-harnesses/" + routes.harnessName
	mux.HandleFunc("POST "+prefix+"/sessions", routes.startSession)
	mux.HandleFunc("POST "+prefix+"/messages", routes.sendUserMessage)
	mux.HandleFunc("POST "+prefix+"/interrupts/resolve", routes.resolveInterrupt)
	mux.HandleFunc("GET "+prefix+"/snapshot", routes.getAgentSnapshot)
}

// SignSessionID returns the session token: an HMAC of the session ID, so no token is stored anywhere.
func SignSessionID(sessionKey []byte, sessionID string) string {
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

func (routes *SessionRoutes) startSession(response http.ResponseWriter, request *http.Request) {
	// The server picks the session ID, so a caller cannot claim an existing session.
	sessionID := fmt.Sprintf("%s-order-support-%s", routes.harnessName, randomID())
	if _, err := routes.sessions.StartSession(request.Context(), sessionID, nil, dex.StartFlowOptions{}); err != nil {
		writeDexError(response, err)
		return
	}
	writeJSON(response, map[string]string{
		"session_id": sessionID, "session_token": SignSessionID(routes.sessionKey, sessionID),
	})
}

func (routes *SessionRoutes) sendUserMessage(response http.ResponseWriter, request *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
	}
	if !routes.readSessionRequest(response, request, &body, &body.SessionID) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		http.Error(response, "text must be a non-empty string", http.StatusBadRequest)
		return
	}
	isAccepted, err := routes.sessions.SendUserMessage(request.Context(), body.SessionID, body.Text)
	if err != nil {
		writeDexError(response, err)
		return
	}
	writeJSON(response, map[string]bool{"accepted": isAccepted})
}

func (routes *SessionRoutes) resolveInterrupt(response http.ResponseWriter, request *http.Request) {
	var body struct {
		SessionID        string  `json:"session_id"`
		InterruptID      string  `json:"interrupt_id"`
		Approved         *bool   `json:"approved"`
		RejectionMessage *string `json:"rejection_message"`
	}
	if !routes.readSessionRequest(response, request, &body, &body.SessionID) {
		return
	}
	if strings.TrimSpace(body.InterruptID) == "" || body.Approved == nil {
		http.Error(response, "interrupt_id must be a non-empty string and approved a boolean", http.StatusBadRequest)
		return
	}
	isAccepted, err := routes.sessions.ResolveInterrupt(
		request.Context(), body.SessionID, body.InterruptID, *body.Approved, body.RejectionMessage)
	if err != nil {
		writeDexError(response, err)
		return
	}
	writeJSON(response, map[string]bool{"accepted": isAccepted})
}

func (routes *SessionRoutes) getAgentSnapshot(response http.ResponseWriter, request *http.Request) {
	sessionID := request.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(response, "session_id is required", http.StatusBadRequest)
		return
	}
	if !routes.isAuthorized(request, sessionID) {
		http.Error(response, "this caller has no token for the agent session", http.StatusForbidden)
		return
	}
	snapshot, err := routes.sessions.GetAgentSnapshot(request.Context(), sessionID)
	var notFound *dex.FlowNotFoundError
	var notActive *dex.FlowNotActiveOrNotFoundError
	if errors.As(err, &notFound) || errors.As(err, &notActive) {
		http.Error(response, "the agent session is missing or no longer retained", http.StatusNotFound)
		return
	}
	if err != nil {
		writeDexError(response, err)
		return
	}
	writeJSON(response, map[string]dexagents.AgentSnapshot{"snapshot": snapshot})
}

// readSessionRequest decodes a JSON body and checks its session token before any Dex call.
func (routes *SessionRoutes) readSessionRequest(
	response http.ResponseWriter, request *http.Request, body any, sessionID *string,
) bool {
	if err := json.NewDecoder(request.Body).Decode(body); err != nil {
		http.Error(response, "the request body must be a JSON object", http.StatusBadRequest)
		return false
	}
	if strings.TrimSpace(*sessionID) == "" {
		http.Error(response, "session_id must be a non-empty string", http.StatusBadRequest)
		return false
	}
	if !routes.isAuthorized(request, *sessionID) {
		http.Error(response, "this caller has no token for the agent session", http.StatusForbidden)
		return false
	}
	return true
}

func (routes *SessionRoutes) isAuthorized(request *http.Request, sessionID string) bool {
	expected := SignSessionID(routes.sessionKey, sessionID)
	return hmac.Equal([]byte(request.Header.Get(SessionTokenHeader)), []byte(expected))
}

func writeDexError(response http.ResponseWriter, err error) {
	var notActive *dex.FlowNotActiveOrNotFoundError
	if errors.As(err, &notActive) {
		http.Error(response, "the agent session is closed or no longer retained; start a new one", http.StatusConflict)
		return
	}
	var lockConflict *dex.RPCLockConflictError
	if errors.As(err, &lockConflict) {
		http.Error(response, "the agent session is busy; try again", http.StatusConflict)
		return
	}
	slog.Error("agent session call failed", "error", err)
	http.Error(response, "the agent session call failed", http.StatusInternalServerError)
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write the response", "error", err)
	}
}

func randomID() string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}
