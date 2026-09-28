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
		ConnectorHostedEnvironment: "staging", ConnectorHostedServiceToken: "backend-token",
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
		ConnectorHostedEnvironment: "preview", ConnectorHostedServiceToken: "backend-token",
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
