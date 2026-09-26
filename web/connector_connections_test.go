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
	if err := setup.store.put(testLocalConnectorConnection("gmail", "sender", "token", nil)); err != nil {
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

func TestConnectorSetupRejectsNonLoopbackAndBlobStore(t *testing.T) {
	provider := connectorTestDefinitionProvider(t, nil)
	_, err := newConnectorSetup(&Config{
		BindAddress: "0.0.0.0", ConnectorSetupEnabled: true, ConnectorConfigDirectory: t.TempDir(),
	}, provider)
	if err == nil {
		t.Fatal("expected non-loopback Connector setup to fail")
	}
	_, err = newConnectorSetup(&Config{
		BindAddress: "127.0.0.1", ConnectorSetupEnabled: true,
		ConnectorConfigDirectory: t.TempDir(), FlowRenderingSource: FlowRenderingSourceBlobStore,
	}, provider)
	if err == nil {
		t.Fatal("expected blobstore Connector setup to fail")
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
	release := connectorRelease{
		ConnectorID: identity.ConnectorID, ModulePath: identity.ModulePath, Version: identity.ModuleVersion,
		Tag:       strings.TrimPrefix(identity.ModulePath, "github.com/superdurable/dex-connectors-library/") + "/" + identity.ModuleVersion,
		SourceSHA: "source-sha", ManifestSHA256: strings.Repeat("0", 64),
	}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = identity.ConnectorID
	release.Manifest.Spec.Provider = provider
	release.Manifest.Spec.Auth.Type = "apiKey"
	release.Manifest.Spec.Auth.Fields = authFields
	metadata, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	server := connectorReleaseTestServer(t, metadata, nil)
	t.Cleanup(server.Close)
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
}
