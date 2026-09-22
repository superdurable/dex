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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testReleaseID = "550e8400-e29b-41d4-a716-446655440000"
const secondTestReleaseID = "550e8400-e29b-41d4-a716-446655440001"
const thirdTestReleaseID = "550e8400-e29b-41d4-a716-446655440002"

func TestDirectoryFlowDefinitionProviderReloadsDirectDefinitions(t *testing.T) {
	directory := t.TempDir()
	writeFlowDefinitionTestFile(t, directory, "flow.json", validFlowDefinitionV2("FirstFlow", true))
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeFlowDefinitionTestFile(t, directory, "flow.json", validFlowDefinitionV2("SecondFlow", true))
	second, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.DefinitionRevision == second.DefinitionRevision {
		t.Fatal("definition revision did not change")
	}
	if _, found := second.V2Definitions["SecondFlow"]; !found {
		t.Fatalf("reloaded definitions = %+v", second.V2Definitions)
	}
}

func TestDirectoryFlowDefinitionProviderValidatesManifestDigestAndCount(t *testing.T) {
	directory := t.TempDir()
	releaseDirectory := filepath.Join(directory, "releases", testReleaseID)
	if err := os.MkdirAll(releaseDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	graph := []byte(validFlowDefinitionV2("RefundFlow", true))
	if err := os.WriteFile(filepath.Join(releaseDirectory, "refund.json"), graph, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := flowDefinitionDigest([]flowDefinitionFile{{path: "refund.json", data: graph}})
	writeManifest(t, directory, flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: testReleaseID,
		BundlePrefix: "releases/" + testReleaseID + "/",
		BundleDigest: digest, DefinitionCount: 1,
	})
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.DefinitionRevision != digest || snapshot.DefinitionCount != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	writeManifest(t, directory, flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: testReleaseID,
		BundlePrefix: "releases/" + testReleaseID + "/",
		BundleDigest: digest, DefinitionCount: 2,
	})
	if _, err := provider.Load(context.Background()); err == nil {
		t.Fatal("count mismatch loaded successfully")
	}
}

func TestDirectoryFlowDefinitionProviderFailsWhenManifestChangesTwice(t *testing.T) {
	directory := t.TempDir()
	graph := []byte(validFlowDefinitionV2("RefundFlow", true))
	digest := flowDefinitionDigest([]flowDefinitionFile{{path: "refund.json", data: graph}})
	manifestBytes := make([][]byte, 0, 3)
	for _, releaseID := range []string{testReleaseID, secondTestReleaseID, thirdTestReleaseID} {
		if releaseID != thirdTestReleaseID {
			releaseDirectory := filepath.Join(directory, "releases", releaseID)
			if err := os.MkdirAll(releaseDirectory, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(releaseDirectory, "refund.json"), graph, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal(flowDefinitionManifest{
			SchemaVersion: "1.0", ReleaseID: releaseID,
			BundlePrefix: "releases/" + releaseID + "/",
			BundleDigest: digest, DefinitionCount: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		manifestBytes = append(manifestBytes, data)
	}
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	provider.readManifest = func(string) ([]byte, error) {
		index := read
		if index >= len(manifestBytes) {
			index = len(manifestBytes) - 1
		}
		read++
		return manifestBytes[index], nil
	}
	if _, err := provider.Load(context.Background()); err == nil {
		t.Fatal("provider accepted a manifest that changed twice")
	}
}

func TestDirectoryFlowDefinitionProviderRetriesOneManifestChange(t *testing.T) {
	directory := t.TempDir()
	manifests := make([][]byte, 0, 2)
	for _, release := range []struct {
		id       string
		flowType string
	}{
		{id: testReleaseID, flowType: "FirstFlow"},
		{id: secondTestReleaseID, flowType: "SecondFlow"},
	} {
		graph := []byte(validFlowDefinitionV2(release.flowType, true))
		releaseDirectory := filepath.Join(directory, "releases", release.id)
		if err := os.MkdirAll(releaseDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(releaseDirectory, "flow.json"), graph, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, err := json.Marshal(flowDefinitionManifest{
			SchemaVersion: "1.0", ReleaseID: release.id,
			BundlePrefix:    "releases/" + release.id + "/",
			BundleDigest:    flowDefinitionDigest([]flowDefinitionFile{{path: "flow.json", data: graph}}),
			DefinitionCount: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		manifests = append(manifests, manifest)
	}
	provider, err := NewDirectoryFlowDefinitionProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	provider.readManifest = func(string) ([]byte, error) {
		reads++
		if reads == 1 {
			return manifests[0], nil
		}
		return manifests[1], nil
	}
	snapshot, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reads != 3 {
		t.Fatalf("manifest reads = %d", reads)
	}
	if _, found := snapshot.V2Definitions["SecondFlow"]; !found {
		t.Fatalf("retried definitions = %+v", snapshot.V2Definitions)
	}
}

func TestS3FlowDefinitionProviderPaginatesAndReusesUnchangedSnapshot(t *testing.T) {
	prefix := "_superverse/dex-web/flow-definitions"
	bundlePrefix := prefix + "/releases/" + testReleaseID + "/"
	firstGraph := []byte(validFlowDefinitionV2("FirstFlow", true))
	secondGraph := []byte(validFlowDefinitionV2("SecondFlow", true))
	files := []flowDefinitionFile{
		{path: "a.json", data: firstGraph},
		{path: "z.json", data: secondGraph},
	}
	manifest, err := json.Marshal(flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: testReleaseID, BundlePrefix: bundlePrefix,
		BundleDigest: flowDefinitionDigest(files), DefinitionCount: len(files),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeFlowDefinitionObjectStore{
		etag: "manifest-a",
		objects: map[string][]byte{
			prefix + "/active-manifest": manifest,
			bundlePrefix + "a.json":     firstGraph,
			bundlePrefix + "z.json":     secondGraph,
		},
		pages: []FlowDefinitionObjectPage{
			{Keys: []string{bundlePrefix + "z.json"}, ContinuationToken: "next"},
			{Keys: []string{bundlePrefix + "a.json"}},
		},
	}
	provider, err := NewS3FlowDefinitionProvider(store, prefix)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("unchanged manifest did not reuse the validated snapshot")
	}
	if store.listCalls != 2 || store.graphReads != 2 {
		t.Fatalf("list calls = %d graph reads = %d", store.listCalls, store.graphReads)
	}
}

func TestS3FlowDefinitionProviderDoesNotFallBackAfterChangedInvalidManifest(t *testing.T) {
	prefix := "_superverse/dex-web/flow-definitions"
	firstBundlePrefix := prefix + "/releases/" + testReleaseID + "/"
	firstGraph := []byte(validFlowDefinitionV2("FirstFlow", true))
	firstManifest, err := json.Marshal(flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: testReleaseID, BundlePrefix: firstBundlePrefix,
		BundleDigest:    flowDefinitionDigest([]flowDefinitionFile{{path: "flow.json", data: firstGraph}}),
		DefinitionCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeFlowDefinitionObjectStore{
		etag: "manifest-a",
		objects: map[string][]byte{
			prefix + "/active-manifest":     firstManifest,
			firstBundlePrefix + "flow.json": firstGraph,
		},
		pages: []FlowDefinitionObjectPage{{Keys: []string{firstBundlePrefix + "flow.json"}}},
	}
	provider, err := NewS3FlowDefinitionProvider(store, prefix)
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	secondBundlePrefix := prefix + "/releases/" + secondTestReleaseID + "/"
	secondGraph := []byte(validFlowDefinitionV2("SecondFlow", true))
	secondManifest, err := json.Marshal(flowDefinitionManifest{
		SchemaVersion: "1.0", ReleaseID: secondTestReleaseID, BundlePrefix: secondBundlePrefix,
		BundleDigest:    flowDefinitionDigest([]flowDefinitionFile{{path: "flow.json", data: secondGraph}}),
		DefinitionCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	store.etag = "manifest-b"
	store.objects[prefix+"/active-manifest"] = secondManifest
	store.pages = []FlowDefinitionObjectPage{{Keys: nil}}
	if snapshot, loadErr := provider.Load(context.Background()); loadErr == nil || snapshot != nil {
		t.Fatalf("changed incomplete release returned snapshot=%+v error=%v", snapshot, loadErr)
	}
	if provider.cached != first {
		t.Fatal("diagnostic cache did not retain the previous validated snapshot")
	}

	store.objects[secondBundlePrefix+"flow.json"] = secondGraph
	store.pages = []FlowDefinitionObjectPage{{Keys: []string{secondBundlePrefix + "flow.json"}}}
	second, err := provider.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second == first || second.DefinitionRevision == first.DefinitionRevision {
		t.Fatalf("recovered snapshot = %+v", second)
	}
}

func writeManifest(t *testing.T, directory string, manifest flowDefinitionManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, flowDefinitionManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type fakeFlowDefinitionObjectStore struct {
	etag       string
	objects    map[string][]byte
	pages      []FlowDefinitionObjectPage
	listCalls  int
	graphReads int
}

func (s *fakeFlowDefinitionObjectStore) Get(
	_ context.Context,
	key string,
	ifNoneMatch string,
) (FlowDefinitionObject, error) {
	if strings.HasSuffix(key, flowDefinitionManifestName) && ifNoneMatch == s.etag {
		return FlowDefinitionObject{NotModified: true}, nil
	}
	if !strings.HasSuffix(key, flowDefinitionManifestName) {
		s.graphReads++
	}
	return FlowDefinitionObject{Data: s.objects[key], ETag: s.etag}, nil
}

func (s *fakeFlowDefinitionObjectStore) List(
	_ context.Context,
	_ string,
	continuationToken string,
) (FlowDefinitionObjectPage, error) {
	s.listCalls++
	if continuationToken == "" {
		return s.pages[0], nil
	}
	return s.pages[1], nil
}
