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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectorLanguageNeutralReleaseLookup(t *testing.T) {
	legacy := connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail", ModuleVersion: "v0.1.1",
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Connection setup</main>"))
	metadata, err := json.Marshal(connectorTestRelease(legacy, archive, connectorUIHostAPIRange))
	if err != nil {
		t.Fatal(err)
	}
	server := connectorReleaseTestServer(t, metadata, archive)
	defer server.Close()
	resolver := &connectorReleaseResolver{baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client()}
	for _, ecosystem := range []string{"go", "pypi", "npm"} {
		t.Run(ecosystem, func(t *testing.T) {
			identity := connectorDefinitionIdentity{ConnectorID: "gmail", ReleaseTag: "connectors/google/gmail/v0.1.1", Package: &connectorPackageIdentity{Ecosystem: ecosystem, Name: "dex-gmail", Version: "1.2.3"}}
			resolved, err := resolver.resolve(context.Background(), identity)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.release.Tag != identity.ReleaseTag || resolved.uiRoot == "" {
				t.Fatalf("release = %+v", resolved)
			}
			identity.ConnectorID = "other"
			if _, err := resolver.releaseMetadata(context.Background(), identity); err == nil {
				t.Fatal("cached release accepted a different Connector ID")
			}
		})
	}
}

func TestConnectorGoReleaseTagModuleIdentity(t *testing.T) {
	for _, scenario := range []struct {
		connectorID   string
		releaseTag    string
		modulePath    string
		moduleVersion string
	}{
		{"stripe", "connectors/stripe/go/v0.25.0", "github.com/superdurable/dex-connectors-library/connectors/stripe/go", "v0.25.0"},
		{"gmail", "connectors/google/gmail/go/v0.25.0-rc.1", "github.com/superdurable/dex-connectors-library/connectors/google/gmail/go", "v0.25.0-rc.1"},
	} {
		t.Run(scenario.connectorID, func(t *testing.T) {
			identity := connectorDefinitionIdentity{
				ConnectorID: scenario.connectorID, ReleaseTag: scenario.releaseTag,
				Package: &connectorPackageIdentity{Ecosystem: "go", Name: scenario.modulePath, Version: scenario.moduleVersion},
			}
			resolved, err := connectorReleaseModuleIdentity(identity)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.ModulePath != scenario.modulePath || resolved.ModuleVersion != scenario.moduleVersion {
				t.Fatalf("module identity = %+v", resolved)
			}
			if _, err := connectorReleaseModuleIdentity(resolved); err != nil {
				t.Fatalf("matching explicit module identity rejected: %v", err)
			}
			for _, change := range []func(*connectorDefinitionIdentity){
				func(candidate *connectorDefinitionIdentity) {
					candidate.ModulePath = strings.TrimSuffix(scenario.modulePath, "/go")
				},
				func(candidate *connectorDefinitionIdentity) { candidate.ModuleVersion = "v0.1.0" },
			} {
				candidate := identity
				change(&candidate)
				if _, err := connectorReleaseModuleIdentity(candidate); err == nil {
					t.Fatalf("conflicting module identity accepted: %+v", candidate)
				}
			}
		})
	}
}

func TestConnectorLanguageNeutralIdentityRejectsAmbiguity(t *testing.T) {
	identity := connectorDefinitionIdentity{ConnectorID: "gmail", ReleaseTag: "connectors/google/gmail/v0.1.1", Package: &connectorPackageIdentity{Ecosystem: "pypi", Name: "dex-gmail", Version: "0.1.1rc1"}}
	for _, change := range []func(*connectorDefinitionIdentity){
		func(identity *connectorDefinitionIdentity) { identity.ReleaseTag = "connectors/../gmail/v0.1.1" },
		func(identity *connectorDefinitionIdentity) { identity.ReleaseTag = "connectors/google/gmail/latest" },
		func(identity *connectorDefinitionIdentity) { identity.Package = nil },
		func(identity *connectorDefinitionIdentity) {
			identity.Package = &connectorPackageIdentity{Ecosystem: "npm", Name: "gmail", Version: "^0.1.1"}
		},
		func(identity *connectorDefinitionIdentity) { identity.ModulePath = "example.com/gmail" },
		func(identity *connectorDefinitionIdentity) { identity.ModuleVersion = "v0.2.0" },
	} {
		candidate := identity
		change(&candidate)
		if _, err := connectorReleaseModuleIdentity(candidate); err == nil {
			t.Fatalf("invalid identity accepted: %+v", candidate)
		}
	}
}

func TestConnectorConnectionsFormLoadsForLanguageNeutralIdentity(t *testing.T) {
	for _, ecosystem := range []string{"pypi", "npm"} {
		t.Run(ecosystem, func(t *testing.T) {
			identity := connectorDefinitionIdentity{ConnectorID: "gmail", OperationID: "sendMessage", OperationKind: "mutation", ConnectionName: "sender", ReleaseTag: "connectors/google/gmail/v0.1.1", Package: &connectorPackageIdentity{Ecosystem: ecosystem, Name: "dex-gmail", Version: "0.1.1"}, ConfigurationEnabled: true}
			setup := connectorTestSetup(t, t.TempDir(), connectorTestDefinitionProvider(t, []connectorDefinitionIdentity{identity}))
			legacy, err := connectorReleaseModuleIdentity(identity)
			if err != nil {
				t.Fatal(err)
			}
			installConnectorTestRelease(t, setup, legacy, "google", []connectorManifestField{{Name: "access_token", Type: "secretString", Required: true}})
			mux := http.NewServeMux()
			setup.registerHandlers(mux)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v2/connector-connections", nil))
			var list connectorConnectionListResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || len(list.Connections) != 1 || list.Connections[0].Status != "Missing" {
				t.Fatalf("connections = %d %s", recorder.Code, recorder.Body.String())
			}
			request := authorizedConnectorRequest(t, setup, http.MethodPost, "/api/v2/connector-ui-sessions", strings.NewReader(`{"connectorId":"gmail","connectionName":"sender"}`))
			recorder = httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "access_token") {
				t.Fatalf("setup form = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestConnectorCatalogLoadsPythonAndTypeScriptDefinitions(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			directory := t.TempDir()
			graph, err := os.ReadFile(filepath.Join("..", "cli", "internal", "flowviz", "testdata", language, "workflow.golden.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "flow.json"), graph, 0o644); err != nil {
				t.Fatal(err)
			}
			provider, err := NewDirectoryFlowDefinitionProvider(directory)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := provider.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			views, err := connectorViewsFromCatalog(snapshot.Response, nil, time.Now(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(views) != 1 || views[0].Status != "Missing" || len(views[0].Uses) != 1 || len(views[0].TriggerUses) != 1 || views[0].ModuleVersion != "v1.2.3" {
				t.Fatalf("views = %+v", views)
			}
		})
	}
}
