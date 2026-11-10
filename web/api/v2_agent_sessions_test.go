// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superdurable/dex/sdk-go/gen/dexpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testAgentSessionRevision = "sha256:agent"

func TestAgentSessionStartUsesTheSessionStartInputAndSearchMode(t *testing.T) {
	client := &v2TestClient{}
	mux := newAgentSessionTestMux(client)
	response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/start", testAgentSessionRevision, `{
		"flowType":"SupportAgent","flowId":"session-1","initialMessage":"Hello",
		"sessionSettings":{ "model": "mock/dex", "system_prompt": "Be brief", "max_turns": 9007199254740993 }
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if len(client.startRequests) != 1 {
		t.Fatalf("StartFlow requests = %d", len(client.startRequests))
	}
	started := client.startRequests[0]
	if started.GetFlowId() != "session-1" || started.GetFlowType() != "SupportAgent" ||
		started.GetStartStepType() != "StartAgentSession" || !started.GetStepOptions().GetSkipWaitFor() ||
		started.GetRequestId() == "" {
		t.Fatalf("StartFlow request = %+v", started)
	}
	config := started.GetFlowStartOptions().GetFlowConfigOverride()
	if config.GetActiveStepSearchMode() != dexpb.ActiveStepSearchMode_ACTIVE_STEP_SEARCH_MODE_ENABLED_FOR_STEPS_WITH_WAIT_FOR ||
		!config.GetWorkerTarget().GetIsPullMode() {
		t.Fatalf("Flow config override = %+v", config)
	}
	want := `{"initial_message":"Hello","session_settings":{"model":"mock/dex","system_prompt":"Be brief","max_turns":9007199254740993}}`
	if payload := string(started.GetStepInput().GetObjValue().GetPayload()); payload != want {
		t.Fatalf("start input = %s", payload)
	}
}

func TestAgentSessionStartWithoutMessageOrSettingsSendsNulls(t *testing.T) {
	client := &v2TestClient{}
	mux := newAgentSessionTestMux(client)
	response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/start", testAgentSessionRevision,
		`{"flowType":"SupportAgent","flowId":"session-2","initialMessage":"  ","sessionSettings":null}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if payload := string(client.startRequests[0].GetStepInput().GetObjValue().GetPayload()); payload !=
		`{"initial_message":null,"session_settings":null}` {
		t.Fatalf("start input = %s", payload)
	}
}

func TestAgentSessionStartRejectsInvalidRequestsWithoutStarting(t *testing.T) {
	client := &v2TestClient{}
	mux := newAgentSessionTestMux(client)
	for _, testCase := range []struct {
		name     string
		revision string
		body     string
		status   int
	}{
		{"stale revision", "sha256:stale", `{"flowType":"SupportAgent","flowId":"s"}`, http.StatusConflict},
		{"not a session Flow type", testAgentSessionRevision, `{"flowType":"RefundFlow","flowId":"s"}`, http.StatusBadRequest},
		{"unknown Flow type", testAgentSessionRevision, `{"flowType":"Missing","flowId":"s"}`, http.StatusBadRequest},
		{"blank Flow ID", testAgentSessionRevision, `{"flowType":"SupportAgent","flowId":" "}`, http.StatusBadRequest},
		{"reserved Flow ID character", testAgentSessionRevision, `{"flowType":"SupportAgent","flowId":"a/b"}`, http.StatusBadRequest},
		{"array settings", testAgentSessionRevision, `{"flowType":"SupportAgent","flowId":"s","sessionSettings":[1]}`, http.StatusBadRequest},
		{"string settings", testAgentSessionRevision, `{"flowType":"SupportAgent","flowId":"s","sessionSettings":"x"}`, http.StatusBadRequest},
		{"oversized settings", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","sessionSettings":{"p":"` + strings.Repeat("x", 16*1024) + `"}}`,
			http.StatusBadRequest},
		{"unknown field", testAgentSessionRevision, `{"flowType":"SupportAgent","flowId":"s","workerTargetAddress":"w:1"}`, http.StatusBadRequest},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/start", testCase.revision, testCase.body)
			if response.Code != testCase.status {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
		})
	}
	if len(client.startRequests) != 0 {
		t.Fatalf("StartFlow requests = %+v", client.startRequests)
	}
}

func TestAgentSessionMessageInvokesSendUserMessage(t *testing.T) {
	client := &v2TestClient{}
	client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return &dexpb.InvokeRPCResponse{Output: &dexpb.Value{Kind: &dexpb.Value_BoolValue{BoolValue: true}}}, nil
	}
	mux := newAgentSessionTestMux(client)
	response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/messages", testAgentSessionRevision, `{
		"flowType":"SupportAgent","flowId":"session-1","workQueuePermissions":["agent.operate"],"text":"Look up order 7"
	}`)
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"invoked":true,"output":true}` {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	if len(client.rpcRequests) != 1 {
		t.Fatalf("RPC requests = %+v", client.rpcRequests)
	}
	invoked := client.rpcRequests[0]
	if invoked.GetRpcName() != "send_user_message" || invoked.GetFlowId() != "session-1" ||
		invoked.GetIsTransactional() || len(invoked.GetLockAttributeKeys()) != 0 {
		t.Fatalf("InvokeRPC request = %+v", invoked)
	}
	if payload := string(invoked.GetInput().GetObjValue().GetPayload()); payload != `{"plan_mode":false,"text":"Look up order 7"}` {
		t.Fatalf("RPC input = %s", payload)
	}
}

func TestAgentSessionSettingsInvokesUpdateSessionSettings(t *testing.T) {
	client := &v2TestClient{}
	mux := newAgentSessionTestMux(client)
	response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/settings", testAgentSessionRevision, `{
		"flowType":"SupportAgent","flowId":"session-1","workQueuePermissions":["agent.operate"],
		"sessionSettings":{ "model": "mock/other" }
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	invoked := client.rpcRequests[0]
	if invoked.GetRpcName() != "update_session_settings" {
		t.Fatalf("InvokeRPC request = %+v", invoked)
	}
	if payload := string(invoked.GetInput().GetObjValue().GetPayload()); payload != `{"session_settings":{"model":"mock/other"}}` {
		t.Fatalf("RPC input = %s", payload)
	}
}

func TestAgentSessionRPCsEnforceTheSessionActionChecks(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		path       string
		revision   string
		body       string
		flowStatus dexpb.FlowStatus
		status     int
	}{
		{"stale revision", "/api/v2/agent-sessions/messages", "sha256:stale",
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"text":"hi"}`, 0, http.StatusConflict},
		{"missing permission", "/api/v2/agent-sessions/messages", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["refund.manage"],"text":"hi"}`, 0, http.StatusForbidden},
		{"not a session Flow type", "/api/v2/agent-sessions/settings", testAgentSessionRevision,
			`{"flowType":"RefundFlow","flowId":"s","workQueuePermissions":["refund.manage"],"sessionSettings":{}}`, 0, http.StatusBadRequest},
		{"blank text", "/api/v2/agent-sessions/messages", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"text":"  "}`, 0, http.StatusBadRequest},
		{"array settings", "/api/v2/agent-sessions/settings", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"sessionSettings":[]}`, 0, http.StatusBadRequest},
		{"null settings", "/api/v2/agent-sessions/settings", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"sessionSettings":null}`, 0, http.StatusBadRequest},
		{"oversized settings", "/api/v2/agent-sessions/settings", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"sessionSettings":{"p":"` +
				strings.Repeat("x", 16*1024) + `"}}`, 0, http.StatusBadRequest},
		{"closed session", "/api/v2/agent-sessions/settings", testAgentSessionRevision,
			`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"sessionSettings":{}}`,
			dexpb.FlowStatus_FLOW_STATUS_COMPLETED, http.StatusConflict},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := &v2TestClient{flowStatus: testCase.flowStatus}
			mux := newAgentSessionTestMux(client)
			response := performAgentSessionRequest(mux, testCase.path, testCase.revision, testCase.body)
			if response.Code != testCase.status {
				t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
			}
			if len(client.rpcRequests) != 0 {
				t.Fatalf("RPC requests = %+v", client.rpcRequests)
			}
		})
	}
}

func TestAgentSessionRPCReportsAMissingWorker(t *testing.T) {
	client := &v2TestClient{}
	client.invokeRPCHandler = func(context.Context, *dexpb.InvokeRPCRequest) (*dexpb.InvokeRPCResponse, error) {
		return nil, status.Error(codes.Unavailable, "no Worker polls this Flow type")
	}
	mux := newAgentSessionTestMux(client)
	response := performAgentSessionRequest(mux, "/api/v2/agent-sessions/messages", testAgentSessionRevision,
		`{"flowType":"SupportAgent","flowId":"s","workQueuePermissions":["agent.operate"],"text":"hi"}`)
	if response.Code == http.StatusOK || !strings.Contains(response.Body.String(), `"grpcCode":14`) {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
}

func newAgentSessionTestMux(client *v2TestClient) *http.ServeMux {
	client.summaryFlowType = "SupportAgent"
	session := V2Definition{Actions: []V2Action{{
		RPCName: "resolve_interrupt", Label: "Resolve interrupt", RequiredPermission: "agent.operate",
		Input: V2ActionInput{Kind: "object"},
	}, {
		RPCName: "steer_user_message", Label: "Steer turn", RequiredPermission: "agent.operate",
		Input: V2ActionInput{Kind: "object"},
	}}}
	mux := http.NewServeMux()
	RegisterDynamicV2Handlers(mux, client, func(context.Context) (V2DefinitionSnapshot, error) {
		return V2DefinitionSnapshot{
			Definitions: map[string]V2Definition{"SupportAgent": session, "RefundFlow": testV2Definition()},
			Revision:    testAgentSessionRevision,
		}, nil
	}, V2HandlerConfig{WorkerHealthChecker: healthyV2WorkerHealthChecker})
	return mux
}

func performAgentSessionRequest(mux *http.ServeMux, path string, revision string, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(V2DefinitionRevisionHeader, revision)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
