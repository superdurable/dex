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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectorReleaseResolverRemembersMissingReleasesUntilTheyExpire(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v9.9.9", ConnectionName: "sender", ConfigurationEnabled: true,
	}
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	resolver := &connectorReleaseResolver{
		baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client(),
		now: func() time.Time { return now },
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := resolver.releaseMetadata(context.Background(), identity); !errors.Is(err, errConnectorArtifactNotFound) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requests before expiry = %d", requestCount.Load())
	}
	now = now.Add(connectorMissingReleaseTTL)
	if _, err := resolver.releaseMetadata(context.Background(), identity); !errors.Is(err, errConnectorArtifactNotFound) {
		t.Fatalf("error after expiry = %v", err)
	}
	if requestCount.Load() != 2 {
		t.Fatalf("requests after expiry = %d", requestCount.Load())
	}
}

func TestConnectorReleaseResolverBoundsConcurrentMetadataDownloads(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		for {
			observed := maxInFlight.Load()
			if current <= observed || maxInFlight.CompareAndSwap(observed, current) {
				break
			}
		}
		<-unblock
		inFlight.Add(-1)
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	resolver := &connectorReleaseResolver{
		baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client(),
	}
	const releaseCount = 3 * connectorReleaseMetadataConcurrency
	var waitGroup sync.WaitGroup
	for index := 0; index < releaseCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			identity := connectorDefinitionIdentity{
				ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
				ModuleVersion: fmt.Sprintf("v1.0.%d", index), ConnectionName: "sender", ConfigurationEnabled: true,
			}
			if _, err := resolver.releaseMetadata(context.Background(), identity); !errors.Is(err, errConnectorArtifactNotFound) {
				t.Errorf("release %d error = %v", index, err)
			}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < connectorReleaseMetadataConcurrency && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(unblock)
	waitGroup.Wait()
	if maxInFlight.Load() != connectorReleaseMetadataConcurrency {
		t.Fatalf("most downloads in flight = %d, want %d", maxInFlight.Load(), connectorReleaseMetadataConcurrency)
	}
}

func TestFlowDefinitionWithTooManyConnectorStepsIsInvalid(t *testing.T) {
	nodes := make([]string, 0, maxFlowDefinitionConnectorSteps+1)
	for index := 0; index <= maxFlowDefinitionConnectorSteps; index++ {
		nodes = append(nodes, fmt.Sprintf(`{"id":"step:%d","kind":"step","name":"Send%d","metadata":{"connectorFactory":true}}`, index, index))
	}
	graph := strings.Replace(validFlowDefinitionV2("ConnectorFlow", true), `"nodes":[]`, `"nodes":[`+strings.Join(nodes, ",")+`]`, 1)
	definitions := buildFlowDefinitions([]flowDefinitionDocument{{origin: flowDefinitionOriginDirectory, path: "connector.json", data: []byte(graph)}})
	if len(definitions) != 1 || !strings.Contains(definitions[0].Error, "Connector Steps") || definitions[0].Graph != nil {
		t.Fatalf("definitions = %+v", definitions)
	}
}
