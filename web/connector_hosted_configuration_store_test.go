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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/superdurable/dex/web/api"
)

func TestHostedConnectorConfigurationStoreScopesRequestsAndUsesRevisionCAS(t *testing.T) {
	t.Helper()
	var mutationBody string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer backend-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if request.URL.Path == "/api/system/projects/project-1/environments/staging/connector-configuration" && request.Method == http.MethodGet {
			writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{
				ConfigurationRevision: "revision-1", ConfigurationState: "Valid",
				Connections: []localConnectorConnection{{
					ConnectorID: "gmail", ConnectionName: "sender", ModulePath: "example/gmail", ModuleVersion: "v0.1.0",
					Provider: "google", Configuration: map[string]json.RawMessage{}, Credentials: map[string]json.RawMessage{},
				}},
			})
			return
		}
		if request.URL.Path == "/api/system/projects/project-1/environments/staging/connector-configuration/connections/gmail/sender" && request.Method == http.MethodPut {
			if request.Header.Get("If-Match") != "revision-1" {
				t.Fatalf("If-Match = %q", request.Header.Get("If-Match"))
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			mutationBody = string(encoded)
			writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{
				ConfigurationRevision: "revision-2", ConfigurationState: "Draft",
			})
			return
		}
		http.NotFound(response, request)
	}))
	defer backend.Close()

	store, err := newHostedConnectorConfigurationStore(&Config{
		ConnectorHostedBaseURL: backend.URL, ConnectorHostedProjectID: "project-1",
		ConnectorHostedEnvironment: "staging", ConnectorHostedReleaseID: "release-1",
		ConnectorHostedServiceToken: "backend-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	connections, err := store.list()
	if err != nil || len(connections) != 1 {
		t.Fatalf("connections = %+v, err = %v", connections, err)
	}
	err = store.put(localConnectorConnection{
		ConnectorID: "gmail", ConnectionName: "sender", ModulePath: "example/gmail", ModuleVersion: "v0.1.0",
		Provider: "google", Configuration: map[string]json.RawMessage{},
		Credentials: map[string]json.RawMessage{"access_token": json.RawMessage(`"write-only-token"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mutationBody, "write-only-token") {
		t.Fatalf("mutation body = %s", mutationBody)
	}
	state := store.state()
	if state.ConfigurationRevision != "revision-2" || state.ConfigurationState != "Draft" {
		t.Fatalf("state = %+v", state)
	}
}

func TestHostedConnectorSetupAllowsBlobStoreAndHidesLocalPaths(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{
			ConfigurationRevision: "revision-ready", ConfigurationState: "Ready to deploy",
			ApplicationRevision: "revision-running", Connections: []localConnectorConnection{},
		})
	}))
	defer backend.Close()
	setup, err := newConnectorSetup(&Config{
		BindAddress: "0.0.0.0", FlowRenderingSource: FlowRenderingSourceBlobStore,
		WorkQueuePermissionMode: api.V2PermissionModeTrustedHeader, TrustForwardedEmbeddingHeaders: true,
		ConnectorSetupEnabled: true, ConnectorSetupMode: ConnectorSetupModeHosted,
		ConnectorCacheDirectory: t.TempDir(), ConnectorHostedBaseURL: backend.URL,
		ConnectorHostedProjectID: "project-1", ConnectorHostedEnvironment: "production",
		ConnectorHostedReleaseID:    "release-1",
		ConnectorHostedServiceToken: "backend-token",
	}, connectorTestDefinitionProvider(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	setup.handleListConnections(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-connections", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var result connectorConnectionListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Mode != ConnectorSetupModeHosted || result.ConfigurationRevision != "revision-ready" {
		t.Fatalf("result = %+v", result)
	}
	if result.Directory != "" || result.FilePath != "" || result.UseConfigurationsFilePath != "" || result.LaunchCommand != "" {
		t.Fatalf("hosted response exposed local paths: %+v", result)
	}
}

func TestHostedConnectorConfigurationConflictIsSafe(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{ConfigurationRevision: "revision-1"})
			return
		}
		response.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer backend.Close()
	store, err := newHostedConnectorConfigurationStore(&Config{
		ConnectorHostedBaseURL: backend.URL, ConnectorHostedProjectID: "project-1",
		ConnectorHostedEnvironment: "preview", ConnectorHostedReleaseID: "release-1",
		ConnectorHostedServiceToken: "backend-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.put(localConnectorConnection{
		ConnectorID: "gmail", ConnectionName: "sender", ModulePath: "example/gmail", ModuleVersion: "v0.1.0",
		Provider: "google", Configuration: map[string]json.RawMessage{}, Credentials: map[string]json.RawMessage{},
	})
	if !errors.Is(err, errConnectorConfigurationRevisionConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestHostedConnectorOAuthDelegatesStartAndCallbackToReleaseScope(t *testing.T) {
	var paths []string
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if request.Header.Get("Authorization") != "Bearer backend-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/api/system/projects/project-1/environments/staging/connector-configuration":
			writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{
				ConfigurationRevision: "revision-1",
			})
		case "/api/system/projects/project-1/environments/staging/connector-configuration/releases/release-1/connections/gmail/sender/oauth/start":
			if request.Header.Get("If-Match") != "revision-1" {
				t.Fatalf("If-Match = %q", request.Header.Get("If-Match"))
			}
			writeWebJSON(response, http.StatusOK, hostedConnectorOAuthStartResponse{
				AuthorizationURL: "https://accounts.example.test/authorize?state=opaque",
				ExpiresAt:        time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
			})
		case "/api/system/projects/project-1/environments/staging/connector-configuration/releases/release-1/oauth/callback":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["state"] != "opaque" || body["code"] != "authorization-code" {
				t.Fatalf("callback body = %v", body)
			}
			writeWebJSON(response, http.StatusOK, hostedConnectorOAuthCallbackResponse{
				ConnectorID: "gmail", ConnectionName: "sender",
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer backend.Close()

	store, err := newHostedConnectorConfigurationStore(&Config{
		ConnectorHostedBaseURL: backend.URL, ConnectorHostedProjectID: "project-1",
		ConnectorHostedEnvironment: "staging", ConnectorHostedReleaseID: "release-1",
		ConnectorHostedServiceToken: "backend-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.startOAuth("gmail", "sender", hostedConnectorOAuthStartRequest{
		DefinitionRevision: "definition-1", ModulePath: "example/gmail", ModuleVersion: "v0.14.0",
		Provider: "google", AuthMethodID: "google-oauth",
		OAuth2:   connectorManifestOAuth2{AuthorizationEndpoint: "https://accounts.example.test/authorize"},
		ClientID: "client-id", ClientSecret: "client-secret",
		RedirectURI: "https://studio.example.test/dex/callback",
	})
	if err != nil || started.AuthorizationURL == "" {
		t.Fatalf("start result = %+v, error = %v", started, err)
	}
	completed, err := store.completeOAuth("opaque", "authorization-code", "")
	if err != nil || completed.ConnectorID != "gmail" || completed.ConnectionName != "sender" {
		t.Fatalf("callback result = %+v, error = %v", completed, err)
	}
	if len(paths) != 3 {
		t.Fatalf("backend paths = %v", paths)
	}
}

func TestHostedConnectorSetupCommandUsesReleaseScopeAndRevisionCAS(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/system/projects/project-1/environments/staging/connector-configuration":
			writeWebJSON(response, http.StatusOK, hostedConnectorConfigurationSnapshot{
				ConfigurationRevision: "revision-1",
			})
		case "/api/system/projects/project-1/environments/staging/connector-configuration/releases/release-1/connections/slack/workspace/setup-commands/listChannels":
			if request.Header.Get("If-Match") != "revision-1" {
				t.Fatalf("If-Match = %q", request.Header.Get("If-Match"))
			}
			var body hostedConnectorStudioCommandRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Command.ID != "listChannels" || body.Command.Capability != "slack.channels-list" {
				t.Fatalf("command = %+v", body.Command)
			}
			if body.Command.Request.Credential.Field != "bot_token" || body.Parameters["cursor"] != "next" {
				t.Fatalf("body = %+v", body)
			}
			writeWebJSON(response, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(response, request)
		}
	}))
	defer backend.Close()

	store, err := newHostedConnectorConfigurationStore(&Config{
		ConnectorHostedBaseURL: backend.URL, ConnectorHostedProjectID: "project-1",
		ConnectorHostedEnvironment: "staging", ConnectorHostedReleaseID: "release-1",
		ConnectorHostedServiceToken: "backend-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.executeProviderCommand(
		"slack",
		"workspace",
		connectorManifestStudioCommand{
			ID: "listChannels", Capability: "slack.channels-list",
			Request: connectorManifestStudioHTTPRequest{
				Method: http.MethodGet, URL: "https://slack.com/api/conversations.list",
				Credential: connectorManifestStudioCredential{Field: "bot_token", Scheme: "bearer"},
			},
		},
		map[string]string{"cursor": "next"},
	)
	if err != nil || value["ok"] != true {
		t.Fatalf("value = %+v, err = %v", value, err)
	}
}
