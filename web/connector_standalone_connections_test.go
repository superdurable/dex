// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const standaloneTestModulePath = "github.com/superdurable/dex-connectors-library/connectors/google/gmail/go"

func standaloneTestIdentity() connectorDefinitionIdentity {
	return connectorDefinitionIdentity{
		ConnectorID: "gmail", ConnectionName: "support-mail", ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2",
	}
}

func TestStandaloneConnectionIsAddedAuthorizedListedAndDeletedWithoutAFlow(t *testing.T) {
	directory := t.TempDir()
	setup := connectorTestSetup(t, directory, connectorTestDefinitionProvider(t, nil))
	installConnectorTestReleaseWithAuth(t, setup, standaloneTestIdentity(), "google", connectorManifestAuth{
		Type: "apiKey", Fields: []connectorManifestField{{Name: "access_token", Type: "secretString", Required: true}},
	}, connectorManifestField{Name: "endpoint", Type: "string"})
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	added := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections",
		`{"connectorId":"gmail","connectionName":"support-mail","modulePath":"`+standaloneTestModulePath+`","moduleVersion":"v0.25.2"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", added.Code, added.Body.String())
	}
	var addedView connectorConnectionView
	if err := json.Unmarshal(added.Body.Bytes(), &addedView); err != nil {
		t.Fatal(err)
	}
	if addedView.Status != "Missing" || !addedView.IsStandalone || addedView.ModuleVersion != "v0.25.2" {
		t.Fatalf("added view = %+v", addedView)
	}
	if _, err := os.Stat(filepath.Join(directory, connectorConnectionsFileName)); !os.IsNotExist(err) {
		t.Fatalf("adding a connection must not write the connection file, stat err = %v", err)
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Missing" || !views[0].IsStandalone || views[0].Uses == nil {
		t.Fatalf("pending views = %+v", views)
	}

	session := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-ui-sessions",
		`{"connectorId":"gmail","connectionName":"support-mail"}`)
	if session.Code != http.StatusOK || !strings.Contains(session.Body.String(), `"provider":"google"`) {
		t.Fatalf("UI session status = %d: %s", session.Code, session.Body.String())
	}
	saved := sendStandaloneTestRequest(t, setup, mux, http.MethodPut, "/api/v2/connector-connections/gmail/support-mail",
		`{"modulePath":"`+standaloneTestModulePath+`","moduleVersion":"v0.25.2","provider":"google",`+
			`"configuration":{"endpoint":"https://gmail.example"},"credentials":{"access_token":"standalone-secret"}}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", saved.Code, saved.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "gmail", "support-mail")
	if stored.ModulePath != standaloneTestModulePath || stored.ModuleVersion != "v0.25.2" || stored.Provider != "google" ||
		string(stored.Configuration["endpoint"]) != `"https://gmail.example"` || string(stored.Credentials["access_token"]) != `"standalone-secret"` {
		t.Fatalf("stored connection = %+v", stored)
	}
	if len(setup.pendingConnectionSnapshot()) != 0 {
		t.Fatal("a saved connection must leave the pending set")
	}

	restarted := connectorTestSetup(t, directory, connectorTestDefinitionProvider(t, nil))
	installConnectorTestRelease(t, restarted, standaloneTestIdentity(), "google", nil)
	views = listStandaloneTestViews(t, restarted)
	if len(views) != 1 || views[0].Status != "Ready" || !views[0].IsStandalone || views[0].Provider != "google" ||
		len(views[0].StoredCredentialFields) != 1 {
		t.Fatalf("restarted views = %+v", views)
	}
	restartedMux := http.NewServeMux()
	restarted.registerHandlers(restartedMux)
	deleted := sendStandaloneTestRequest(t, restarted, restartedMux, http.MethodDelete, "/api/v2/connector-connections/gmail/support-mail", "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete status = %d: %s", deleted.Code, deleted.Body.String())
	}
	if views = listStandaloneTestViews(t, restarted); len(views) != 0 {
		t.Fatalf("views after delete = %+v", views)
	}
}

func TestStandaloneConnectionAddRejectsInvalidAndExistingConnections(t *testing.T) {
	flowIdentity := connectorDefinitionIdentity{
		ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation", ConnectionName: "sender",
		ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2", ConfigurationEnabled: true,
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{flowIdentity}))
	installConnectorTestRelease(t, setup, standaloneTestIdentity(), "google", []connectorManifestField{
		{Name: "access_token", Type: "secretString", Required: true},
	})
	stored := testLocalConnectorConnection("gmail", "stored-mail", "token", nil)
	stored.ModulePath, stored.ModuleVersion = standaloneTestModulePath, "v0.25.2"
	if err := setup.store.put(stored, nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	addBody := func(connectionName string, modulePath string, moduleVersion string) string {
		encoded, err := json.Marshal(connectorConnectionAddRequest{
			ConnectorID: "gmail", ConnectionName: connectionName, ModulePath: modulePath, ModuleVersion: moduleVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, testCase := range []struct {
		name       string
		body       string
		statusCode int
		errorCode  string
	}{
		{"empty name", addBody("", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"surrounding space", addBody(" support ", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"slash", addBody("team/support", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"newline", addBody("support\nmail", standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"too long", addBody(strings.Repeat("a", connectorConnectionNameLimit+1), standaloneTestModulePath, "v0.25.2"), http.StatusBadRequest, "CONNECTOR_CONNECTION_NAME_INVALID"},
		{"unofficial module", addBody("support", "example.com/gmail", "v0.25.2"), http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
		{"inexact version", addBody("support", standaloneTestModulePath, "latest"), http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
		{"used by a Flow", addBody("sender", standaloneTestModulePath, "v0.25.2"), http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS"},
		{"already stored", addBody("stored-mail", standaloneTestModulePath, "v0.25.2"), http.StatusConflict, "CONNECTOR_CONNECTION_EXISTS"},
		{"unknown field", `{"connectorId":"gmail","connectionName":"support","modulePath":"` + standaloneTestModulePath + `","moduleVersion":"v0.25.2","extra":true}`, http.StatusBadRequest, "CONNECTOR_REQUEST_INVALID"},
	} {
		recorder := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", testCase.body)
		if recorder.Code != testCase.statusCode || !strings.Contains(recorder.Body.String(), `"`+testCase.errorCode+`"`) {
			t.Fatalf("%s: status = %d: %s", testCase.name, recorder.Code, recorder.Body.String())
		}
	}

	forged := httptest.NewRequest(http.MethodPost, "/api/v2/connector-connections", strings.NewReader(addBody("support", standaloneTestModulePath, "v0.25.2")))
	forged.Host = "127.0.0.1:8802"
	forged.Header.Set("Origin", "http://attacker.example")
	forged.Header.Set(connectorCSRFHeader, setup.csrfToken)
	forgedRecorder := httptest.NewRecorder()
	mux.ServeHTTP(forgedRecorder, forged)
	if forgedRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin add status = %d: %s", forgedRecorder.Code, forgedRecorder.Body.String())
	}

	first := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.2"))
	again := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.2"))
	other := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections", addBody("support", standaloneTestModulePath, "v0.25.1"))
	if first.Code != http.StatusOK || again.Code != http.StatusOK || other.Code != http.StatusConflict {
		t.Fatalf("repeated add statuses = %d, %d, %d: %s", first.Code, again.Code, other.Code, other.Body.String())
	}
	removed := sendStandaloneTestRequest(t, setup, mux, http.MethodDelete, "/api/v2/connector-connections/gmail/support", "")
	if removed.Code != http.StatusOK || !strings.Contains(removed.Body.String(), `"deleted":true`) {
		t.Fatalf("remove pending status = %d: %s", removed.Code, removed.Body.String())
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 2 || views[0].ConnectionName != "sender" || views[0].IsStandalone ||
		views[1].ConnectionName != "stored-mail" || !views[1].IsStandalone || views[1].Status != "Ready" {
		t.Fatalf("views = %+v", views)
	}
}

func TestStandaloneSlackConnectionCompletesOAuthWithoutAFlow(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "slack", ConnectionName: "support-slack",
		ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack/go", ModuleVersion: "v1.3.0",
	}
	var metadata []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch filepath.Base(request.URL.Path) {
		case connectorReleaseDigestName:
			digest := sha256.Sum256(metadata)
			if _, err := fmt.Fprintf(response, "%x  %s\n", digest, connectorReleaseMetadataName); err != nil {
				t.Error(err)
			}
		case connectorReleaseMetadataName:
			if _, err := response.Write(metadata); err != nil {
				t.Error(err)
			}
		case "token":
			if _, err := response.Write([]byte(`{"ok":true,"access_token":"xoxb-bot","scope":"chat:write",` +
				`"authed_user":{"access_token":"xoxp-user","scope":"channels:history"}}`)); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	release := connectorRelease{
		ConnectorID: identity.ConnectorID, ModulePath: identity.ModulePath, Version: identity.ModuleVersion,
		Tag: "connectors/slack/go/v1.3.0", SourceSHA: "source-sha", ManifestSHA256: strings.Repeat("0", sha256.Size*2),
	}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = "slack"
	release.Manifest.Spec.Provider = "slack"
	release.Manifest.Spec.Auth.Type = "oauth2"
	release.Manifest.Spec.Auth.Fields = []connectorManifestField{
		{Name: "oauth_client_id", Type: "string"},
		{Name: "oauth_client_secret", Type: "secretString"},
		{Name: "bot_token", Type: "secretString", Required: true},
		{Name: "user_token", Type: "secretString", Required: true},
		{Name: "app_token", Type: "secretString"},
	}
	release.Manifest.Spec.Auth.OAuth2 = &connectorManifestOAuth2{
		AuthorizationEndpoint: server.URL + "/authorize", TokenEndpoint: server.URL + "/token",
		ClientIDCredential: "oauth_client_id", ClientSecretCredential: "oauth_client_secret",
		Scopes: []string{"chat:write"}, UserScopes: []string{"channels:history"},
		CredentialMappings: []connectorOAuthCredentialMapping{
			{Credential: "bot_token", Source: "access_token"},
			{Credential: "user_token", Source: "authed_user.access_token"},
		},
	}
	var err error
	metadata, err = json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	setup.releases.baseURL = server.URL
	setup.releases.httpClient = server.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	added := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections",
		`{"connectorId":"slack","connectionName":"support-slack","modulePath":"`+identity.ModulePath+`","moduleVersion":"v1.3.0"}`)
	if added.Code != http.StatusOK {
		t.Fatalf("add status = %d: %s", added.Code, added.Body.String())
	}
	started := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-connections/slack/support-slack/oauth/start",
		`{"clientId":"client","clientSecret":"secret","configuration":{},"credentialValues":{},"credentialSecrets":{"app_token":"xapp-app"}}`)
	if started.Code != http.StatusOK {
		t.Fatalf("OAuth start status = %d: %s", started.Code, started.Body.String())
	}
	var startResponse struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &startResponse); err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(startResponse.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRecorder()
	mux.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/api/v2/connector-oauth/callback?state="+
		url.QueryEscape(authorizationURL.Query().Get("state"))+"&code=authorization-code", nil))
	if callback.Code != http.StatusSeeOther || !strings.Contains(callback.Header().Get("Location"), "connectionName=support-slack") {
		t.Fatalf("OAuth callback status = %d: %s", callback.Code, callback.Body.String())
	}
	stored := getConnectorTestConnection(t, setup, "slack", "support-slack")
	if stored.ModulePath != identity.ModulePath || stored.ModuleVersion != "v1.3.0" || stored.Provider != "slack" {
		t.Fatalf("stored Slack connection = %+v", stored)
	}
	assertConnectorTestCredentials(t, stored, map[string]any{
		"oauth_client_id": "client", "oauth_client_secret": "secret",
		"bot_token": "xoxb-bot", "user_token": "xoxp-user", "app_token": "xapp-app",
	})
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Ready" || !views[0].IsStandalone {
		t.Fatalf("views = %+v", views)
	}
}

func TestStandaloneStoredConnectionWithoutOfficialReleaseIsUnsupported(t *testing.T) {
	setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	connection := testLocalConnectorConnection("custom", "local", "token", nil)
	connection.ModulePath, connection.ModuleVersion = "example.com/custom", "v0.0.0-20260101000000-abcdef"
	if err := setup.store.put(connection, nil); err != nil {
		t.Fatal(err)
	}
	views := listStandaloneTestViews(t, setup)
	if len(views) != 1 || views[0].Status != "Unsupported" || !views[0].IsStandalone {
		t.Fatalf("views = %+v", views)
	}
	mux := http.NewServeMux()
	setup.registerHandlers(mux)
	session := sendStandaloneTestRequest(t, setup, mux, http.MethodPost, "/api/v2/connector-ui-sessions",
		`{"connectorId":"custom","connectionName":"local"}`)
	if session.Code != http.StatusNotFound || !strings.Contains(session.Body.String(), `"CONNECTOR_CONNECTION_UNSUPPORTED"`) {
		t.Fatalf("UI session status = %d: %s", session.Code, session.Body.String())
	}
}

func TestConnectorCatalogListsPublicReleasesAndLocalOverrides(t *testing.T) {
	overrideIdentity := connectorDefinitionIdentity{
		ConnectorID: "slack", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/slack/go", ModuleVersion: "v1.4.0-rc.1",
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Slack</main>"))
	release := connectorTestRelease(overrideIdentity, archive, connectorUIHostAPIRange)
	release.Manifest.Metadata.DisplayName = "Slack (local)"
	overrideDirectory := connectorLocalReleaseTestDirectory(t, release, archive)
	setup, err := newConnectorSetup(&Config{
		BindAddress: "127.0.0.1", ConnectorSetupEnabled: true, ConnectorConfigDirectory: t.TempDir(),
		ConnectorReleaseOverrides: map[string]string{"slack": overrideDirectory},
	}, connectorTestDefinitionProvider(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	catalogServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if _, err := response.Write([]byte(`{"apiVersion":"connectors.dex.dev/catalog/v1alpha1","kind":"ConnectorCatalog","connectors":[` +
			`{"id":"slack","name":"Slack","version":"v1.3.0","languages":{"go":"v1.3.0"},"directory":"connectors/slack"},` +
			`{"id":"gmail","name":"Gmail","description":"Send mail.","version":"v0.25.2","languages":{"go":"v0.25.2","python":"v0.1.0"},"directory":"connectors/google/gmail"},` +
			`{"id":"bad id","name":"Bad","version":"v0.1.0","languages":{"go":"v0.1.0"},"directory":"connectors/bad"},` +
			`{"id":"nogo","name":"No Go","version":"v0.1.0","languages":{},"directory":"connectors/nogo"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer catalogServer.Close()
	setup.publicCatalog.url = catalogServer.URL
	setup.publicCatalog.httpClient = catalogServer.Client()
	mux := http.NewServeMux()
	setup.registerHandlers(mux)

	var listed connectorCatalogListResponse
	for range 2 {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-catalog", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("catalog status = %d: %s", recorder.Code, recorder.Body.String())
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("public catalog requests = %d, want one cached fetch", requests)
	}
	want := []connectorCatalogEntry{
		{ConnectorID: "gmail", DisplayName: "Gmail", Description: "Send mail.", ModulePath: standaloneTestModulePath, ModuleVersion: "v0.25.2"},
		{ConnectorID: "slack", DisplayName: "Slack (local)", ModulePath: overrideIdentity.ModulePath, ModuleVersion: "v1.4.0-rc.1", LocalOverride: true},
	}
	if !listed.IsPublicCatalogAvailable || fmt.Sprint(listed.Connectors) != fmt.Sprint(want) {
		t.Fatalf("catalog = %+v", listed)
	}

	unavailable := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, nil))
	unavailable.publicCatalog.url = catalogServer.URL + "/missing"
	unavailable.publicCatalog.httpClient = &http.Client{Transport: connectorRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("offline")
	})}
	unavailableMux := http.NewServeMux()
	unavailable.registerHandlers(unavailableMux)
	recorder := httptest.NewRecorder()
	unavailableMux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-catalog", nil))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), `"CONNECTOR_CATALOG_UNAVAILABLE"`) {
		t.Fatalf("offline catalog status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func sendStandaloneTestRequest(
	t *testing.T,
	setup *connectorSetup,
	mux *http.ServeMux,
	method string,
	target string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, authorizedConnectorRequest(t, setup, method, target, strings.NewReader(body)))
	return recorder
}

func listStandaloneTestViews(t *testing.T, setup *connectorSetup) []connectorConnectionView {
	t.Helper()
	views, _, err := setup.connectionViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return views
}
