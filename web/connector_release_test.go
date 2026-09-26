// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectorReleaseResolverVerifiesAndCachesUI(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConnectionName: "sender", ConfigurationEnabled: true,
	}
	archive := connectorUITestArchive(t, "index.html", []byte("<main>Gmail</main>"))
	release := connectorTestRelease(identity, archive, connectorUIHostAPIRange)
	metadata, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	server := connectorReleaseTestServer(t, metadata, archive)
	defer server.Close()
	resolver := &connectorReleaseResolver{
		baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client(),
	}
	resolved, err := resolver.resolve(context.Background(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.uiRoot == "" {
		t.Fatal("expected cached UI root")
	}
	contents, err := os.ReadFile(filepath.Join(resolved.uiRoot, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "<main>Gmail</main>" {
		t.Fatalf("cached entrypoint = %q", contents)
	}
}

func TestConnectorReleaseResolverRejectsChecksumTraversalAndHostAPIRange(t *testing.T) {
	identity := connectorDefinitionIdentity{
		ConnectorID: "gmail", ModulePath: "github.com/superdurable/dex-connectors-library/connectors/google/gmail",
		ModuleVersion: "v0.1.1", ConnectionName: "sender", ConfigurationEnabled: true,
	}
	tests := []struct {
		name               string
		archive            []byte
		hostRange          string
		corrupt            bool
		capabilityMismatch bool
		want               string
	}{
		{name: "checksum", archive: connectorUITestArchive(t, "index.html", []byte("ok")), hostRange: connectorUIHostAPIRange, corrupt: true, want: "checksum"},
		{name: "traversal", archive: connectorUITestArchive(t, "../escape.html", []byte("bad")), hostRange: connectorUIHostAPIRange, want: "unsafe"},
		{name: "host range", archive: connectorUITestArchive(t, "index.html", []byte("ok")), hostRange: ">=2.0.0", want: "incompatible"},
		{name: "capabilities", archive: connectorUITestArchive(t, "index.html", []byte("ok")), hostRange: connectorUIHostAPIRange, capabilityMismatch: true, want: "incompatible"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release := connectorTestRelease(identity, test.archive, test.hostRange)
			if test.capabilityMismatch {
				release.UI.Capabilities = []string{"configuration.write"}
			}
			metadata, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			servedArchive := test.archive
			if test.corrupt {
				servedArchive = append(append([]byte(nil), test.archive...), 'x')
			}
			server := connectorReleaseTestServer(t, metadata, servedArchive)
			defer server.Close()
			resolver := &connectorReleaseResolver{
				baseURL: server.URL, artifactRoot: filepath.Join(t.TempDir(), "artifacts"), httpClient: server.Client(),
			}
			_, err = resolver.resolve(context.Background(), identity)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolve error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestServeConnectorUIAssetUsesSandboxHeadersAndRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>Gmail</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/connector-ui/index.html", nil)
	response := httptest.NewRecorder()

	serveConnectorUIAsset(response, request, "index.html", root)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Body.String() != "<main>Gmail</main>" {
		t.Fatalf("body = %q", response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'none'") {
		t.Fatalf("CSP = %q", response.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP = %q", response.Header().Get("Content-Security-Policy"))
	}
}

func connectorTestRelease(identity connectorDefinitionIdentity, archive []byte, hostRange string) connectorRelease {
	archiveDigest := sha256.Sum256(archive)
	release := connectorRelease{
		ConnectorID: identity.ConnectorID, ModulePath: identity.ModulePath, Version: identity.ModuleVersion,
		Tag:       strings.TrimPrefix(identity.ModulePath, "github.com/superdurable/dex-connectors-library/") + "/" + identity.ModuleVersion,
		SourceSHA: "source-sha", ManifestSHA256: strings.Repeat("0", sha256.Size*2),
		UI: &connectorUIRelease{
			Artifact: "connector-ui.tgz", SHA256: hex.EncodeToString(archiveDigest[:]),
			Entrypoint: "index.html", HostAPIRange: hostRange,
		},
	}
	release.Manifest.APIVersion = "connectors.dex.dev/v1alpha1"
	release.Manifest.Kind = "Connector"
	release.Manifest.Metadata.Name = identity.ConnectorID
	release.Manifest.Metadata.DisplayName = "Gmail"
	release.Manifest.Spec.Provider = "google"
	release.Manifest.Spec.Studio = &struct {
		Setup connectorManifestStudioSetup  `json:"setup"`
		Units []connectorManifestStudioUnit `json:"units,omitempty"`
	}{Setup: connectorManifestStudioSetup{Entrypoint: "index.html", HostAPIRange: hostRange}}
	return release
}

func connectorReleaseTestServer(t *testing.T, metadata []byte, archive []byte) *httptest.Server {
	t.Helper()
	metadataDigest := sha256.Sum256(metadata)
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch filepath.Base(request.URL.Path) {
		case connectorReleaseDigestName:
			_, err := fmt.Fprintf(response, "%x  %s\n", metadataDigest, connectorReleaseMetadataName)
			if err != nil {
				t.Error(err)
			}
		case connectorReleaseMetadataName:
			_, err := response.Write(metadata)
			if err != nil {
				t.Error(err)
			}
		case "connector-ui.tgz":
			_, err := response.Write(archive)
			if err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(response, request)
		}
	}))
}

func connectorUITestArchive(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
