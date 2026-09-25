// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superdurable/dex/web/api"
)

func TestSlackResourceAPIsUseBotTokenWithoutReturningIt(t *testing.T) {
	provider := connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{{
		ConnectorID: "slack", OperationID: "postThreadReply", OperationKind: "mutation",
		ConnectionName: "slack-workspace", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}})
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "unused", nil)
	connection.Credentials = map[string]json.RawMessage{"bot_token": json.RawMessage(`"xoxb-never-return"`)}
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	providerServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer xoxb-never-return" {
			t.Error("Slack bot token was not used")
		}
		switch request.URL.Path {
		case "/conversations.list":
			_, _ = response.Write([]byte(`{"ok":true,"channels":[{"id":"C123","name":"approvals","is_private":true,"is_member":true}]}`))
		case "/users.list":
			_, _ = response.Write([]byte(`{"ok":true,"members":[{"id":"U123","name":"ada","profile":{"display_name":"Ada","image_48":"https://avatars.slack-edge.com/ada.png"}},{"id":"UBOT","is_bot":true,"profile":{"display_name":"Bot"}}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer providerServer.Close()
	setup.slackAPIBaseURL = providerServer.URL
	setup.slackHTTPClient = providerServer.Client()

	for _, resource := range []string{"channels", "users"} {
		request := authorizedConnectorRequest(t, setup, http.MethodGet, "/api/v2/connector-connections/slack/slack-workspace/slack/"+resource, nil)
		request.SetPathValue("connectorId", "slack")
		request.SetPathValue("connectionName", "slack-workspace")
		recorder := httptest.NewRecorder()
		if resource == "channels" {
			setup.handleListSlackChannels(recorder, request)
		} else {
			setup.handleListSlackUsers(recorder, request)
		}
		if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "xoxb-never-return") {
			t.Fatalf("%s response = %d %s", resource, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTriggerBindingAPIWritesOnlyDeclaredBinding(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","threadTriggerMatcher":{"messageContains":"approval"}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/channelThreadCreated/slack-thread-approval-start", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "channelThreadCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-start")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("write status = %d: %s", recorder.Code, recorder.Body.String())
	}
	bindings, err := setup.store.listTriggerBindings("slack", "slack-workspace")
	if err != nil || len(bindings) != 1 || string(bindings[0].Configuration["channelId"]) != `"C123"` {
		t.Fatalf("bindings = %+v, err = %v", bindings, err)
	}

	request = authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/channelThreadCreated/undeclared", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "channelThreadCreated")
	request.SetPathValue("bindingName", "undeclared")
	recorder = httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("undeclared binding status = %d", recorder.Code)
	}
}

func TestTriggerBindingAPIRequiresSlackApprover(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","threadReplyMatcher":{"messageContains":"approve"}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/threadReplyCreated/slack-thread-approval-reply", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "threadReplyCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-reply")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("missing approver status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestTriggerBindingAPIRejectsConfigurableRPCName(t *testing.T) {
	provider := connectorTriggerTestDefinitionProvider(t)
	setup := connectorTestSetup(t, t.TempDir(), provider)
	connection := testLocalConnectorConnection("slack", "slack-workspace", "token", nil)
	if err := setup.store.put(connection); err != nil {
		t.Fatal(err)
	}
	body := `{"configuration":{"channelId":"C123","rpcName":"ApproveRequest","threadReplyMatcher":{"posterUserIds":["U123"]}}}`
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-trigger-bindings/slack/slack-workspace/threadReplyCreated/slack-thread-approval-reply", strings.NewReader(body))
	request.SetPathValue("connectorId", "slack")
	request.SetPathValue("connectionName", "slack-workspace")
	request.SetPathValue("triggerName", "threadReplyCreated")
	request.SetPathValue("bindingName", "slack-thread-approval-reply")
	recorder := httptest.NewRecorder()
	setup.handlePutTriggerBinding(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("configurable RPC name status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func authorizedConnectorRequest(t *testing.T, setup *connectorSetup, method string, target string, body *strings.Reader) *http.Request {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, body)
	}
	request.Host = "127.0.0.1:8802"
	request.Header.Set("Origin", "http://127.0.0.1:8802")
	request.Header.Set(connectorCSRFHeader, setup.csrfToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, "sha256:test")
	return request
}

func connectorTriggerTestDefinitionProvider(t *testing.T) FlowDefinitionProvider {
	t.Helper()
	identity := connectorDefinitionIdentity{
		ConnectorID: "slack", OperationID: "postThreadReply", OperationKind: "mutation",
		ConnectionName: "slack-workspace", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}
	node := map[string]any{
		"id": "step:PostSlackCompletion", "name": "PostSlackCompletion", "kind": "step",
		"metadata": map[string]any{"connectorFactory": true, "connector": identity},
	}
	binding := connectorCatalogTriggerBinding{
		ConnectorID: "slack", TriggerName: "channelThreadCreated",
		ConnectionName: "slack-workspace", BindingName: "slack-thread-approval-start",
		ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, ConfigurationEnabled: true,
	}
	replyBinding := connectorCatalogTriggerBinding{
		ConnectorID: "slack", TriggerName: "threadReplyCreated",
		ConnectionName: "slack-workspace", BindingName: "slack-thread-approval-reply",
		ModulePath: identity.ModulePath, ModuleVersion: identity.ModuleVersion, ConfigurationEnabled: true,
	}
	catalog, err := json.Marshal(map[string]any{
		"configured": true, "definitionRevision": "sha256:test", "definitions": []any{map[string]any{
			"flowName": "SlackEmailApprovalFlow", "graph": map[string]any{
				"nodes": []any{node}, "v2": map[string]any{"connectorTriggerBindings": []any{binding, replyBinding}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return staticFlowDefinitionProvider{snapshot: &FlowDefinitionSnapshot{
		Response: catalog, DefinitionRevision: "sha256:test", Source: "local", DefinitionCount: 1,
	}}
}
