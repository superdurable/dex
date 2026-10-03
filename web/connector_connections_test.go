// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/superdurable/dex/web/api"
)

func TestConnectorConnectionsAPIWritesWithoutReturningSecretsAndReloadsAfterRestart(t *testing.T) {
	directory := t.TempDir()
	provider := connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation",
		ConnectionName: "sender", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConfigurationEnabled: true,
	}})
	setup := connectorTestSetup(t, directory, provider)
	installConnectorTestRelease(t, setup, connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1",
	}, "google", []connectorManifestField{
		{Name: "access_token", Type: "secretString", Required: true},
		{Name: "primary_email", Type: "string", Required: true},
	})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	listRecorder := httptest.NewRecorder()
	mux.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-connections", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listRecorder.Code, listRecorder.Body.String())
	}
	var list connectorConnectionListResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Connections) != 1 || list.Connections[0].Status != "Missing" {
		t.Fatalf("connections = %+v", list.Connections)
	}

	body := `{"modulePath":"github.com/superdurable/dex-connectors-library/connectors/google/gmail","moduleVersion":"v0.1.1","provider":"google","configuration":{},"credentials":{"access_token":"never-return-this","primary_email":"owner@example.com"},"credentialExpiresAt":null}`
	putRequest := httptest.NewRequest(http.MethodPut, "/api/v2/connector-connections/gmail/sender", strings.NewReader(body))
	putRequest.Host = "127.0.0.1:8802"
	putRequest.Header.Set("Origin", "http://127.0.0.1:8802")
	putRequest.Header.Set(connectorCSRFHeader, list.CSRFToken)
	putRequest.Header.Set(api.V2DefinitionRevisionHeader, list.DefinitionRevision)
	putRecorder := httptest.NewRecorder()
	mux.ServeHTTP(putRecorder, putRequest)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", putRecorder.Code, putRecorder.Body.String())
	}
	if strings.Contains(putRecorder.Body.String(), "never-return-this") {
		t.Fatal("credential leaked in API response")
	}

	restarted := connectorTestSetup(t, directory, provider)
	installConnectorTestRelease(t, restarted, connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1",
	}, "google", nil)
	views, _, err := restarted.connectionViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Status != "Ready" || views[0].Provider != "google" {
		t.Fatalf("restarted views = %+v", views)
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("never-return-this")) {
		t.Fatal("credential leaked in connection view")
	}
}

func TestConnectorConnectionsAPISavesNonOAuthMethodOfMultiMethodManifest(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation",
		ConnectionName: "sender", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConfigurationEnabled: true,
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	installConnectorTestReleaseWithAuth(t, setup, identity, "google", connectorManifestAuth{
		DefaultMethod: "apiKey",
		Methods: []connectorManifestAuthMethod{
			{ID: "apiKey", Type: "apiKey", Fields: []connectorManifestField{
				{Name: "api_key", Type: "secretString", Required: true},
			}},
			{ID: "workspaceServiceAccount", Type: "serviceAccount", Fields: []connectorManifestField{
				{Name: "service_account_key", Type: "secretString", Required: true},
				{Name: "delegated_user", Type: "string", Required: true},
			}},
		},
	})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	const moduleFields = `"modulePath":"github.com/superdurable/dex-connectors-library/connectors/google/gmail","moduleVersion":"v0.1.1","provider":"google","configuration":{}`

	for _, testCase := range []struct {
		authMethodID string
		credentials  string
		expected     map[string]string
	}{
		{
			authMethodID: "workspaceServiceAccount",
			credentials:  `{"service_account_key":"service-account-secret","delegated_user":"owner@example.com"}`,
			expected: map[string]string{
				"auth_method": "workspaceServiceAccount", "service_account_key": "service-account-secret", "delegated_user": "owner@example.com",
			},
		},
		{
			authMethodID: "apiKey",
			credentials:  `{"api_key":"api-key-secret"}`,
			expected:     map[string]string{"auth_method": "apiKey", "api_key": "api-key-secret"},
		},
	} {
		recorder := putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/gmail/sender", `{`+moduleFields+`,"authMethodId":"`+testCase.authMethodID+`","credentials":`+testCase.credentials+`}`)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s put status = %d: %s", testCase.authMethodID, recorder.Code, recorder.Body.String())
		}
		stored, found, err := setup.store.get("gmail", "sender")
		if err != nil || !found || stored.AuthMethodID != testCase.authMethodID {
			t.Fatalf("stored = %+v, found = %v, err = %v", stored, found, err)
		}
		credentials := make(map[string]string, len(stored.Credentials))
		for name, value := range stored.Credentials {
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				t.Fatal(err)
			}
			credentials[name] = text
		}
		if !reflect.DeepEqual(credentials, testCase.expected) {
			t.Fatalf("%s credentials = %+v", testCase.authMethodID, credentials)
		}
	}

	for _, credentials := range []string{
		`{"api_key":"api-key-secret","auth_method":"apiKey"}`,
		`{"api_key":"api-key-secret","auth_methods":["apiKey"]}`,
	} {
		recorder := putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/gmail/sender", `{`+moduleFields+`,"authMethodId":"apiKey","credentials":`+credentials+`}`)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"CONNECTOR_REQUEST_INVALID"`) {
			t.Fatalf("client-set auth method status = %d: %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestConnectorConnectionsAPIStoresSeveralAuthMethodsKeepsAndDropsTheirFields(t *testing.T) {
	setup, mux := connectorLLMTestSetup(t, t.TempDir())
	recorder := putConnectorLLMTestConnection(t, setup, mux, `["anthropic","openai"]`,
		`{"anthropicWorkspaceId":"ws_1"}`, `{"anthropic_api_key":"anthropic-secret","openai_api_key":"openai-secret"}`, `[]`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "llm", "default")
	if stored.AuthMethodID != "" || !reflect.DeepEqual(stored.AuthMethodIDs, []string{"anthropic", "openai"}) {
		t.Fatalf("stored auth methods = %q / %v", stored.AuthMethodID, stored.AuthMethodIDs)
	}
	assertConnectorTestCredentials(t, stored, map[string]any{
		"auth_methods": []any{"anthropic", "openai"}, "anthropic_api_key": "anthropic-secret", "openai_api_key": "openai-secret",
	})

	listRecorder := httptest.NewRecorder()
	mux.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-connections", nil))
	if strings.Contains(listRecorder.Body.String(), "-secret") {
		t.Fatalf("credential leaked in connection list: %s", listRecorder.Body.String())
	}
	var list struct {
		Connections []map[string]any `json:"connections"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &list); err != nil || len(list.Connections) != 1 {
		t.Fatalf("list = %s, err = %v", listRecorder.Body.String(), err)
	}
	view := list.Connections[0]
	if !reflect.DeepEqual(view["authMethodIds"], []any{"anthropic", "openai"}) ||
		!reflect.DeepEqual(view["storedCredentialFields"], []any{"anthropic_api_key", "openai_api_key"}) ||
		!reflect.DeepEqual(view["configuration"], map[string]any{"anthropicWorkspaceId": "ws_1"}) || view["authMethodId"] != nil {
		t.Fatalf("connection view = %+v", view)
	}

	recorder = putConnectorLLMTestConnection(t, setup, mux, `["anthropic","openai"]`,
		`{"anthropicWorkspaceId":"ws_1","model":"anthropic/claude-sonnet-5"}`, `{}`, `["anthropic_api_key","openai_api_key"]`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("keep status = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored = getConnectorTestConnection(t, setup, "llm", "default")
	assertConnectorTestCredentials(t, stored, map[string]any{
		"auth_methods": []any{"anthropic", "openai"}, "anthropic_api_key": "anthropic-secret", "openai_api_key": "openai-secret",
	})
	if string(stored.Configuration["model"]) != `"anthropic/claude-sonnet-5"` {
		t.Fatalf("stored configuration = %s", stored.Configuration)
	}

	recorder = putConnectorLLMTestConnection(t, setup, mux, `["openai","gemini"]`,
		`{"model":"anthropic/claude-sonnet-5"}`, `{"gemini_api_key":"gemini-secret"}`, `["openai_api_key"]`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("remove status = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored = getConnectorTestConnection(t, setup, "llm", "default")
	assertConnectorTestCredentials(t, stored, map[string]any{
		"auth_methods": []any{"openai", "gemini"}, "openai_api_key": "openai-secret", "gemini_api_key": "gemini-secret",
	})
	if _, found := stored.Configuration["anthropicWorkspaceId"]; found || !reflect.DeepEqual(stored.AuthMethodIDs, []string{"openai", "gemini"}) {
		t.Fatalf("removed method fields remain: %+v", stored)
	}
}

func TestConnectorConnectionsAPIRejectsInvalidAuthMethodSelectionsAndKeptFields(t *testing.T) {
	setup, mux := connectorLLMTestSetup(t, t.TempDir())
	recorder := putConnectorLLMTestConnection(t, setup, mux, `["anthropic","openai"]`,
		`{"anthropicWorkspaceId":"ws_1"}`, `{"anthropic_api_key":"anthropic-secret","openai_api_key":"openai-secret"}`, `[]`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("baseline status = %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, testCase := range []struct {
		name          string
		authMethodIDs string
		configuration string
		credentials   string
		keep          string
		code          string
	}{
		{"empty selection", `[]`, `{}`, `{}`, `[]`, "CONNECTOR_AUTH_METHOD_INVALID"},
		{"repeated method", `["openai","openai"]`, `{}`, `{"openai_api_key":"k"}`, `[]`, "CONNECTOR_AUTH_METHOD_INVALID"},
		{"undeclared method", `["mistral"]`, `{}`, `{}`, `[]`, "CONNECTOR_AUTH_METHOD_INVALID"},
		{"selected method key missing", `["openai","gemini"]`, `{}`, `{"openai_api_key":"k"}`, `[]`, "CONNECTOR_REQUEST_INVALID"},
		{"selected method configuration missing", `["anthropic"]`, `{}`, `{"anthropic_api_key":"k"}`, `[]`, "CONNECTOR_REQUEST_INVALID"},
		{"unselected method configuration", `["openai"]`, `{"anthropicWorkspaceId":"ws_1"}`, `{"openai_api_key":"k"}`, `[]`, "CONNECTOR_REQUEST_INVALID"},
		{"unselected method credential", `["openai"]`, `{}`, `{"openai_api_key":"k","anthropic_api_key":"k"}`, `[]`, "CONNECTOR_REQUEST_INVALID"},
		{"client-set auth_methods", `["openai"]`, `{}`, `{"openai_api_key":"k","auth_methods":["openai"]}`, `[]`, "CONNECTOR_REQUEST_INVALID"},
		{"keep unselected method field", `["openai"]`, `{}`, `{}`, `["openai_api_key","anthropic_api_key"]`, "CONNECTOR_REQUEST_INVALID"},
		{"keep field also set", `["openai"]`, `{}`, `{"openai_api_key":"k"}`, `["openai_api_key"]`, "CONNECTOR_REQUEST_INVALID"},
		{"keep unstored field", `["gemini"]`, `{}`, `{}`, `["gemini_api_key"]`, "CONNECTOR_REQUEST_INVALID"},
		{"keep undeclared field", `["openai"]`, `{}`, `{}`, `["openai_api_key","auth_methods"]`, "CONNECTOR_REQUEST_INVALID"},
	} {
		recorder := putConnectorLLMTestConnection(t, setup, mux, testCase.authMethodIDs, testCase.configuration, testCase.credentials, testCase.keep)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"`+testCase.code+`"`) {
			t.Fatalf("%s status = %d: %s", testCase.name, recorder.Code, recorder.Body.String())
		}
	}
	singleIDRequest := `{"modulePath":"` + connectorLLMTestModulePath + `","moduleVersion":"v0.2.0","provider":"llm","authMethodId":"openai","configuration":{},"credentials":{"openai_api_key":"k"}}`
	recorder = putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/llm/default", singleIDRequest)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"CONNECTOR_AUTH_METHOD_INVALID"`) {
		t.Fatalf("authMethodId for multiple selection status = %d: %s", recorder.Code, recorder.Body.String())
	}
	assertConnectorTestCredentials(t, getConnectorTestConnection(t, setup, "llm", "default"), map[string]any{
		"auth_methods": []any{"anthropic", "openai"}, "anthropic_api_key": "anthropic-secret", "openai_api_key": "openai-secret",
	})

	emptySetup, emptyMux := connectorLLMTestSetup(t, t.TempDir())
	recorder = putConnectorLLMTestConnection(t, emptySetup, emptyMux, `["openai"]`, `{}`, `{}`, `["openai_api_key"]`)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"CONNECTOR_REQUEST_INVALID"`) {
		t.Fatalf("keep without a stored record status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestConnectorConnectionsAPIKeepsStoredCredentialsForSingleSelection(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "stripe", OperationID: "createPayment", OperationKind: "mutation",
		ConnectionName: "payments", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/stripe",
		ModuleVersion: "v0.1.0", ConfigurationEnabled: true,
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	installConnectorTestReleaseWithAuth(t, setup, identity, "stripe", connectorManifestAuth{Type: "apiKey", Fields: []connectorManifestField{
		{Name: "secret_key", Type: "secretString", Required: true},
		{Name: "webhook_secret", Type: "secretString", Required: true},
	}}, connectorManifestField{Name: "endpoint", Type: "url"})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	const modulePrefix = `{"modulePath":"github.com/superdurable/dex-connectors-library/connectors/stripe","moduleVersion":"v0.1.0","provider":"stripe","authMethodId":"",`
	recorder := putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/stripe/payments",
		modulePrefix+`"configuration":{"endpoint":"https://api.stripe.test"},"credentials":{"secret_key":"sk-old","webhook_secret":"wh-old"}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", recorder.Code, recorder.Body.String())
	}
	recorder = putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/stripe/payments",
		modulePrefix+`"configuration":{},"credentials":{"webhook_secret":"wh-new"},"keepCredentialFields":["secret_key"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("keep status = %d: %s", recorder.Code, recorder.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "stripe", "payments")
	assertConnectorTestCredentials(t, stored, map[string]any{"secret_key": "sk-old", "webhook_secret": "wh-new"})
	if len(stored.Configuration) != 0 || stored.AuthMethodID != "" || len(stored.AuthMethodIDs) != 0 {
		t.Fatalf("stored = %+v", stored)
	}
	for _, body := range []string{
		modulePrefix + `"configuration":{},"credentials":{},"keepCredentialFields":["secret_key"]}`,
		modulePrefix + `"authMethodIds":["apiKey"],"configuration":{},"credentials":{},"keepCredentialFields":["secret_key","webhook_secret"]}`,
	} {
		recorder = putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/stripe/payments", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid single-selection write status = %d: %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestConnectorConnectionsAPIValidatesSingleSelectionMethodConfigurationAndKeeps(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation",
		ConnectionName: "sender", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConfigurationEnabled: true,
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	installConnectorTestReleaseWithAuth(t, setup, identity, "google", connectorManifestAuth{
		DefaultMethod: "apiKey",
		Methods: []connectorManifestAuthMethod{
			{ID: "apiKey", Type: "apiKey", Fields: []connectorManifestField{{Name: "api_key", Type: "secretString", Required: true}}},
			{ID: "workspaceServiceAccount", Type: "serviceAccount", Fields: []connectorManifestField{
				{Name: "service_account_key", Type: "secretString", Required: true},
			}, Configuration: &connectorManifestAuthMethodConfiguration{Fields: []connectorManifestField{
				{Name: "delegatedUser", Type: "string", Required: true},
			}}},
		},
	})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	const modulePrefix = `{"modulePath":"github.com/superdurable/dex-connectors-library/connectors/google/gmail","moduleVersion":"v0.1.1","provider":"google",`
	for _, testCase := range []struct {
		name   string
		body   string
		status int
	}{
		{"unselected method configuration", `"authMethodId":"apiKey","configuration":{"delegatedUser":"owner@example.com"},"credentials":{"api_key":"k"}}`, http.StatusBadRequest},
		{"selected method configuration missing", `"authMethodId":"workspaceServiceAccount","configuration":{},"credentials":{"service_account_key":"k"}}`, http.StatusBadRequest},
		{"selected method configuration", `"authMethodId":"workspaceServiceAccount","configuration":{"delegatedUser":"owner@example.com"},"credentials":{"service_account_key":"k"}}`, http.StatusOK},
		{"keep another method's field", `"authMethodId":"apiKey","configuration":{},"credentials":{},"keepCredentialFields":["service_account_key"]}`, http.StatusBadRequest},
	} {
		recorder := putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/gmail/sender", modulePrefix+testCase.body)
		if recorder.Code != testCase.status {
			t.Fatalf("%s status = %d: %s", testCase.name, recorder.Code, recorder.Body.String())
		}
	}
	stored := getConnectorTestConnection(t, setup, "gmail", "sender")
	if stored.AuthMethodID != "workspaceServiceAccount" || string(stored.Configuration["delegatedUser"]) != `"owner@example.com"` {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestConnectorUISessionCarriesMultipleAuthMethodManifestFields(t *testing.T) {
	identity := connectorLLMTestIdentity()
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	metadata := []byte(`{"connectorId":"llm","modulePath":"` + connectorLLMTestModulePath + `","version":"v0.2.0","tag":"connectors/llm/v0.2.0",` +
		`"sourceSha":"source-sha","manifestSha256":"` + strings.Repeat("0", 64) + `","manifest":{"apiVersion":"connectors.dex.dev/v1alpha1","kind":"Connector",` +
		`"metadata":{"name":"llm","displayName":"LLM","description":"Several model providers"},"spec":{"provider":"llm",` +
		`"configuration":{"fields":[{"name":"model","type":"string","description":"Default model.","required":false,"studioUnit":{"unit":"modelPicker","port":"model"}}]},` +
		`"auth":{"selection":"multiple","methodLabel":"Provider","methods":[` +
		`{"id":"openai","displayName":"OpenAI","description":"OpenAI key.","type":"apiKey","connectionKind":"apiKey","fields":[{"name":"openai_api_key","type":"secretString","description":"","required":true}]},` +
		`{"id":"anthropic","displayName":"Claude","description":"Claude key.","type":"apiKey","connectionKind":"apiKey","fields":[{"name":"anthropic_api_key","type":"secretString","description":"","required":true}],` +
		`"configuration":{"fields":[{"name":"anthropicWorkspaceId","type":"string","description":"Claude workspace.","required":false}]}}]}}}}`)
	server := connectorReleaseTestServer(t, metadata, nil)
	t.Cleanup(server.Close)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-ui-sessions",
		strings.NewReader(`{"connectorId":"llm","connectionName":"default"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("session status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var session struct {
		Manifest struct {
			Spec struct {
				Configuration struct {
					Fields []map[string]any `json:"fields"`
				} `json:"configuration"`
				Auth map[string]any `json:"auth"`
			} `json:"spec"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	auth := session.Manifest.Spec.Auth
	if auth["selection"] != "multiple" || auth["methodLabel"] != "Provider" {
		t.Fatalf("session auth = %+v", auth)
	}
	methods, isList := auth["methods"].([]any)
	if !isList || len(methods) != 2 {
		t.Fatalf("session methods = %+v", auth["methods"])
	}
	anthropic, _ := methods[1].(map[string]any)
	expectedMethodConfiguration := map[string]any{"fields": []any{map[string]any{
		"name": "anthropicWorkspaceId", "type": "string", "description": "Claude workspace.", "required": false,
	}}}
	if !reflect.DeepEqual(anthropic["configuration"], expectedMethodConfiguration) {
		t.Fatalf("anthropic method configuration = %+v", anthropic["configuration"])
	}
	fields := session.Manifest.Spec.Configuration.Fields
	if len(fields) != 1 || !reflect.DeepEqual(fields[0]["studioUnit"], map[string]any{"unit": "modelPicker", "port": "model"}) {
		t.Fatalf("configuration fields = %+v", fields)
	}
}

func TestConnectorConnectionsAPIRejectsOriginRevisionAndVersionConflict(t *testing.T) {
	provider := connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{
		{ConnectorID: "github", OperationID: "getAuthenticatedProfile", OperationKind: "query", ConnectionName: "reviewer", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/github", ModuleVersion: "v0.1.1", ConfigurationEnabled: true},
		{ConnectorID: "github", OperationID: "listPublicRepositories", OperationKind: "query", ConnectionName: "reviewer", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/github", ModuleVersion: "v0.2.0", ConfigurationEnabled: true},
	})
	setup := connectorTestSetup(t, t.TempDir(), provider)
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	body := `{"modulePath":"github.com/superdurable/dex-connectors-library/connectors/github","moduleVersion":"v0.1.1","provider":"github","configuration":{},"credentials":{"access_token":"secret"}}`

	request := httptest.NewRequest(http.MethodPut, "/api/v2/connector-connections/github/reviewer", strings.NewReader(body))
	request.Host = "127.0.0.1:8802"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set(connectorCSRFHeader, setup.csrfToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, "sha256:test")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid origin status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v2/connector-connections/github/reviewer", strings.NewReader(body))
	request.Host = "127.0.0.1:8802"
	request.Header.Set("Origin", "http://127.0.0.1:8802")
	request.Header.Set(connectorCSRFHeader, setup.csrfToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, "sha256:stale")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("stale revision status = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPut, "/api/v2/connector-connections/github/reviewer", strings.NewReader(body))
	request.Host = "127.0.0.1:8802"
	request.Header.Set("Origin", "http://127.0.0.1:8802")
	request.Header.Set(connectorCSRFHeader, setup.csrfToken)
	request.Header.Set(api.V2DefinitionRevisionHeader, "sha256:test")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("version conflict status = %d", recorder.Code)
	}
}

func TestConnectorUseConfigurationAPIWritesFlowStepScopedSidecar(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation",
		ConnectionName: "sender", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConfigurationEnabled: true,
		ConfigurationUI: api.V2ConnectorConfigurationUI{Units: []api.V2ConnectorUIUnit{{
			ID: "message", UnitID: "textInput", Label: "Message",
			Bindings: []api.V2ConnectorUIBinding{{Port: "text", JSONPointer: "/message/text"}},
		}}},
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	installConnectorTestRelease(t, setup, identity, "google", nil)
	if err := setup.store.put(testLocalConnectorConnection("gmail", "sender", "token", nil), nil); err != nil {
		t.Fatal(err)
	}
	request := authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-use-configurations/gmail/sender/sendMessage/TestFlow/TestStep", strings.NewReader(`{"configuration":{"message":{"text":"Done"}}}`))
	request.SetPathValue("connectorId", "gmail")
	request.SetPathValue("connectionName", "sender")
	request.SetPathValue("operationId", "sendMessage")
	request.SetPathValue("flowType", "TestFlow")
	request.SetPathValue("stepType", "TestStep")
	recorder := httptest.NewRecorder()
	setup.handlePutUseConfiguration(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("write status = %d: %s", recorder.Code, recorder.Body.String())
	}
	configurations, err := setup.store.listUseConfigurations("gmail", "sender")
	if err != nil || len(configurations) != 1 {
		t.Fatalf("configurations = %+v, err = %v", configurations, err)
	}
	var message map[string]string
	if err := json.Unmarshal(configurations[0].Configuration["message"], &message); err != nil || message["text"] != "Done" {
		t.Fatalf("message = %+v, err = %v", message, err)
	}
	contents, err := os.ReadFile(setup.store.useConfigurationsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(contents, []byte(connectorUseConfigurationsSchema)) || bytes.Contains(contents, []byte("token")) {
		t.Fatalf("sidecar = %s", contents)
	}
	views, _, err := setup.connectionViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || len(views[0].Uses) != 1 || !views[0].Uses[0].Configured {
		t.Fatalf("configured use view = %+v", views)
	}

	request = authorizedConnectorRequest(t, setup, http.MethodPut, "/api/v2/connector-use-configurations/gmail/sender/sendMessage/TestFlow/TestStep", strings.NewReader(`{"configuration":{"undeclared":{}}}`))
	request.SetPathValue("connectorId", "gmail")
	request.SetPathValue("connectionName", "sender")
	request.SetPathValue("operationId", "sendMessage")
	request.SetPathValue("flowType", "TestFlow")
	request.SetPathValue("stepType", "TestStep")
	recorder = httptest.NewRecorder()
	setup.handlePutUseConfiguration(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("undeclared empty path status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestConnectorSetupRejectsNonLoopback(t *testing.T) {
	provider := connectorTestDefinitionProvider(t, nil)
	_, err := newConnectorSetup(&Config{
		BindAddress: "0.0.0.0", ConnectorSetupEnabled: true, ConnectorConfigDirectory: t.TempDir(),
	}, provider)
	if err == nil {
		t.Fatal("expected non-loopback Connector setup to fail")
	}

}

func TestConnectorConnectionViewsUseLocalReleaseOverrideForWorkspaceFlow(t *testing.T) {
	releaseIdentity := connectorDefinitionIdentity{
		ConnectorID: "slack", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack",
		ModuleVersion: "v0.7.0", ConnectionName: "workspace", ConfigurationEnabled: true,
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Slack</main>"))
	release := connectorTestRelease(releaseIdentity, archive, connectorUIHostAPIRange)
	release.Manifest.Spec.Provider = "slack"
	release.Manifest.Metadata.DisplayName = "Slack"
	overrideDirectory := connectorLocalReleaseTestDirectory(t, release, archive)
	flowIdentity := connectorDefinitionIdentity{
		ConnectorID: "slack", OperationID: "postMessage", OperationKind: "mutation",
		ConnectionName: "workspace", ConfigurationEnabled: false,
		ConfigurationUI: api.V2ConnectorConfigurationUI{Units: []api.V2ConnectorUIUnit{{
			ID: "message", UnitID: "textInput", Label: "Message",
			Bindings: []api.V2ConnectorUIBinding{{Port: "text", JSONPointer: "/text"}},
		}}},
	}
	directory := t.TempDir()
	setup, err := newConnectorSetup(&Config{
		BindAddress: "127.0.0.1", ConnectorSetupEnabled: true, ConnectorConfigDirectory: directory,
		ConnectorReleaseOverrides: map[string]string{"slack": overrideDirectory},
	}, connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{flowIdentity}))
	if err != nil {
		t.Fatal(err)
	}
	connection := testLocalConnectorConnection("slack", "workspace", "token", nil)
	connection.ModulePath = releaseIdentity.ModulePath
	connection.ModuleVersion = "v0.6.1"
	connection.Provider = "slack"
	if err := setup.store.put(connection, nil); err != nil {
		t.Fatal(err)
	}
	views, _, err := setup.connectionViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Status != "Ready" || !views[0].LocalOverride || views[0].ModuleVersion != "v0.7.0" ||
		views[0].DisplayName != "Slack" {
		t.Fatalf("Connector views = %+v", views)
	}
}

func TestConnectorConnectionListCarriesReleaseDisplayNamesAndOmitsUnresolvedOnes(t *testing.T) {
	gmail := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation", ConnectionName: "sender",
		ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail", ModuleVersion: "v0.1.1",
		ConfigurationEnabled: true,
	}
	stripe := connectorDefinitionIdentity{
		ConnectorID: "stripe", OperationID: "createRefund", OperationKind: "mutation", ConnectionName: "payments",
		ModulePath: "github.com/superdurable/dex-connectors-library/connectors/stripe", ModuleVersion: "v0.3.0",
		ConfigurationEnabled: true,
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Gmail</main>"))
	metadata, err := json.Marshal(connectorTestRelease(gmail, archive, connectorUIHostAPIRange))
	if err != nil {
		t.Fatal(err)
	}
	gmailReleases := connectorReleaseTestServer(t, metadata, archive)
	defer gmailReleases.Close()
	releases := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.URL.Path, "/connectors/google/gmail/v0.1.1/") {
			http.NotFound(response, request)
			return
		}
		gmailReleases.Config.Handler.ServeHTTP(response, request)
	}))
	defer releases.Close()
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{gmail, stripe}))
	setup.releases.baseURL = releases.URL
	setup.releases.httpClient = releases.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-connections", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var list struct {
		Connections []map[string]any `json:"connections"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil || len(list.Connections) != 2 {
		t.Fatalf("list = %s, err = %v", recorder.Body.String(), err)
	}
	if list.Connections[0]["connectorId"] != "gmail" || list.Connections[0]["displayName"] != "Gmail" {
		t.Fatalf("resolved connection view = %+v", list.Connections[0])
	}
	if _, hasDisplayName := list.Connections[1]["displayName"]; list.Connections[1]["connectorId"] != "stripe" || hasDisplayName {
		t.Fatalf("unresolved connection view = %+v", list.Connections[1])
	}
}

func putConnectorTestConnection(
	t *testing.T,
	setup *connectorSetup,
	mux *http.ServeMux,
	target string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, authorizedConnectorRequest(t, setup, http.MethodPut, target, strings.NewReader(body)))
	return recorder
}

const connectorLLMTestModulePath = "github.com/superdurable/dex-connectors-library/connectors/llm"

func connectorLLMTestIdentity() connectorDefinitionIdentity {
	return connectorDefinitionIdentity{
		ConnectorID: "llm", OperationID: "generateText", OperationKind: "mutation", ConnectionName: "default",
		ModulePath: connectorLLMTestModulePath, ModuleVersion: "v0.2.0", ConfigurationEnabled: true,
	}
}

func connectorLLMTestAuth() connectorManifestAuth {
	return connectorManifestAuth{
		Selection: connectorAuthSelectionMultiple, MethodLabel: "Provider",
		Methods: []connectorManifestAuthMethod{
			{ID: "openai", DisplayName: "OpenAI", Type: "apiKey", Fields: []connectorManifestField{
				{Name: "openai_api_key", Type: "secretString", Required: true},
			}},
			{ID: "anthropic", DisplayName: "Claude", Type: "apiKey", Fields: []connectorManifestField{
				{Name: "anthropic_api_key", Type: "secretString", Required: true},
			}, Configuration: &connectorManifestAuthMethodConfiguration{Fields: []connectorManifestField{
				{Name: "anthropicWorkspaceId", Type: "string", Required: true},
			}}},
			{ID: "gemini", DisplayName: "Gemini", Type: "apiKey", Fields: []connectorManifestField{
				{Name: "gemini_api_key", Type: "secretString", Required: true},
			}},
		},
	}
}

func connectorLLMTestSetup(t *testing.T, directory string) (*connectorSetup, *http.ServeMux) {
	t.Helper()
	identity := connectorLLMTestIdentity()
	setup := connectorTestSetup(t, directory, connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
	installConnectorTestReleaseWithAuth(t, setup, identity, "llm", connectorLLMTestAuth(), connectorManifestField{
		Name: "model", Type: "string", StudioUnit: &connectorManifestFieldStudioUnit{Unit: "modelPicker", Port: "model"},
	})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	return setup, mux
}

func putConnectorLLMTestConnection(
	t *testing.T,
	setup *connectorSetup,
	mux *http.ServeMux,
	authMethodIDs string,
	configuration string,
	credentials string,
	keepCredentialFields string,
) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"modulePath":"` + connectorLLMTestModulePath + `","moduleVersion":"v0.2.0","provider":"llm","authMethodIds":` + authMethodIDs +
		`,"configuration":` + configuration + `,"credentials":` + credentials + `,"keepCredentialFields":` + keepCredentialFields + `}`
	return putConnectorTestConnection(t, setup, mux, "/api/v2/connector-connections/llm/default", body)
}

func getConnectorTestConnection(t *testing.T, setup *connectorSetup, connectorID string, connectionName string) storedConnectorConnection {
	t.Helper()
	stored, found, err := setup.store.get(connectorID, connectionName)
	if err != nil || !found {
		t.Fatalf("stored connection found = %v, err = %v", found, err)
	}
	return stored
}

func assertConnectorTestCredentials(t *testing.T, stored storedConnectorConnection, expected map[string]any) {
	t.Helper()
	decoded := make(map[string]any, len(stored.Credentials))
	for name, value := range stored.Credentials {
		var decodedValue any
		if err := json.Unmarshal(value, &decodedValue); err != nil {
			t.Fatal(err)
		}
		decoded[name] = decodedValue
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Fatalf("stored credentials = %+v, expected %+v", decoded, expected)
	}
}

func connectorTestSetup(t *testing.T, directory string, provider FlowDefinitionProvider) *connectorSetup {
	t.Helper()
	setup, err := newConnectorSetup(&Config{
		BindAddress: "127.0.0.1", ConnectorSetupEnabled: true, ConnectorConfigDirectory: directory,
	}, provider)
	if err != nil {
		t.Fatal(err)
	}
	return setup
}

func connectorTestDefinitionProvider(t *testing.T, identities []connectorDefinitionIdentity) FlowDefinitionProvider {
	t.Helper()
	nodes := make([]map[string]any, 0, len(identities))
	for index, identity := range identities {
		nodes = append(nodes, map[string]any{
			"id": "step:test-" + identity.OperationID, "name": "TestStep", "kind": "step",
			"metadata": map[string]any{"connectorFactory": true, "connector": identity},
			"index":    index,
		})
	}
	catalog, err := json.Marshal(map[string]any{
		"configured": true, "definitionRevision": "sha256:test", "definitions": []any{map[string]any{
			"flowName": "TestFlow", "graph": map[string]any{"nodes": nodes},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return staticFlowDefinitionProvider{snapshot: &FlowDefinitionSnapshot{
		Response: catalog, DefinitionRevision: "sha256:test", Source: "local", DefinitionCount: 1,
	}}
}

func installConnectorTestRelease(
	t *testing.T,
	setup *connectorSetup,
	identity connectorDefinitionIdentity,
	provider string,
	authFields []connectorManifestField,
) {
	t.Helper()
	installConnectorTestReleaseWithAuth(t, setup, identity, provider, connectorManifestAuth{Type: "apiKey", Fields: authFields})
}

func installConnectorTestReleaseWithAuth(
	t *testing.T,
	setup *connectorSetup,
	identity connectorDefinitionIdentity,
	provider string,
	auth connectorManifestAuth,
	configurationFields ...connectorManifestField,
) {
	t.Helper()
	release := connectorRelease{
		ConnectorID: identity.ConnectorID, ModulePath: identity.ModulePath, Version: identity.ModuleVersion,
		Tag:       strings.TrimPrefix(identity.ModulePath, "github.com/superdurable/dex-connectors-library/") + "/" + identity.ModuleVersion,
		SourceSHA: "source-sha", ManifestSHA256: strings.Repeat("0", 64),
	}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = identity.ConnectorID
	release.Manifest.Spec.Provider = provider
	release.Manifest.Spec.Auth = auth
	release.Manifest.Spec.Configuration.Fields = configurationFields
	metadata, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	server := connectorReleaseTestServer(t, metadata, nil)
	t.Cleanup(server.Close)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
}
