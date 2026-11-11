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
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestConnectorDisplayNamesLookUpEachReleaseOnceWithinTheCap(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	setup := &connectorSetup{releases: &connectorReleaseResolver{
		baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client(),
	}}
	const modulePath = "github.com/superdurable/dex-connectors-library/connectors/google/gmail"
	views := make([]connectorConnectionView, 0)
	for repeat := 0; repeat < 3; repeat++ {
		views = append(views, connectorConnectionView{ConnectorID: "gmail", ModulePath: modulePath, ModuleVersion: "v1.0.0"})
	}
	for index := 0; index < 2*maxConnectorDisplayNameLookups; index++ {
		views = append(views, connectorConnectionView{ConnectorID: "gmail", ModulePath: modulePath, ModuleVersion: fmt.Sprintf("v2.0.%d", index)})
	}
	setup.applyConnectorDisplayNames(context.Background(), views)
	if requestCount.Load() != maxConnectorDisplayNameLookups {
		t.Fatalf("release requests = %d, want %d", requestCount.Load(), maxConnectorDisplayNameLookups)
	}
}

func TestConnectorCatalogCacheParsesEachRevisionOnce(t *testing.T) {
	cache := &connectorCatalogCache{}
	decoded, err := cache.decode(&FlowDefinitionSnapshot{DefinitionRevision: "sha256:one", Response: []byte(`{"definitions":[{"flowName":"A"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.decode(&FlowDefinitionSnapshot{DefinitionRevision: "sha256:one", Response: []byte(`not JSON`)})
	if err != nil || again != decoded {
		t.Fatalf("the same revision was parsed again: %v", err)
	}
	changed, err := cache.decode(&FlowDefinitionSnapshot{DefinitionRevision: "sha256:two", Response: []byte(`{"definitions":[]}`)})
	if err != nil || changed == decoded || len(changed.Definitions) != 0 {
		t.Fatalf("changed revision = %+v, err = %v", changed, err)
	}
}
