// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConnectorsMCPResolverDiscardsAndRedownloadsTamperedCache(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	expectedBinary, err := os.ReadFile(fakeBinary)
	require.NoError(t, err)
	for name, tamper := range map[string]func(t *testing.T, releaseDirectory string){
		"binary content": func(t *testing.T, releaseDirectory string) {
			require.NoError(t, os.WriteFile(filepath.Join(releaseDirectory, connectorsMCPBinaryName), []byte("#!/bin/sh\necho tampered\n"), 0o755))
		},
		"release.json launch contract": func(t *testing.T, releaseDirectory string) {
			rewriteInstalledRelease(t, releaseDirectory, func(record *connectorsMCPInstalledRelease) { record.LaunchContract = 2 })
		},
		"release.json binary sha256": func(t *testing.T, releaseDirectory string) {
			rewriteInstalledRelease(t, releaseDirectory, func(record *connectorsMCPInstalledRelease) {
				record.BinarySHA256 = strings.Repeat("a", 64)
			})
		},
		"release.json archive sha256": func(t *testing.T, releaseDirectory string) {
			rewriteInstalledRelease(t, releaseDirectory, func(record *connectorsMCPInstalledRelease) {
				record.SHA256 = strings.Repeat("b", 64)
			})
		},
		"symlinked release directory": func(t *testing.T, releaseDirectory string) {
			require.NoError(t, os.Rename(releaseDirectory, releaseDirectory+"-elsewhere"))
			require.NoError(t, os.Symlink(releaseDirectory+"-elsewhere", releaseDirectory))
		},
		"symlinked binary": func(t *testing.T, releaseDirectory string) {
			binaryPath := filepath.Join(releaseDirectory, connectorsMCPBinaryName)
			require.NoError(t, os.Rename(binaryPath, binaryPath+"-elsewhere"))
			require.NoError(t, os.Symlink(binaryPath+"-elsewhere", binaryPath))
		},
	} {
		t.Run(name, func(t *testing.T) {
			releases := newTestConnectorsMCPReleaseServer(t, fakeBinary)
			releases.setIndex(releases.release("0.1.4", 1, ""))
			cfg := releases.config(t)
			logs := &synchronizedBuffer{}
			resolver := newConnectorsMCPReleaseResolver(cfg, releases.server.Client(), testConnectorsMCPPlatform, logs)
			installed, err := resolver.resolveBinary(context.Background())
			require.NoError(t, err)

			tamper(t, filepath.Dir(installed.path))
			resolved, err := resolver.resolveBinary(context.Background())
			require.NoError(t, err)
			require.Contains(t, logs.String(), "discarding cached release 0.1.4")
			require.Equal(t, int64(2), releases.assetRequests.Load(), "the tampered release is downloaded again")
			reinstalledBinary, err := os.ReadFile(resolved.path)
			require.NoError(t, err)
			require.Equal(t, expectedBinary, reinstalledBinary)
		})
	}
}

func TestConnectorsMCPResolverRejectsTamperedCacheOffline(t *testing.T) {
	releases := newTestConnectorsMCPReleaseServer(t, buildFakeConnectorsMCP(t))
	releases.setIndex(releases.release("0.1.4", 1, ""))
	cfg := releases.config(t)
	logs := &synchronizedBuffer{}
	installed, err := newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, logs).resolveBinary(context.Background())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(installed.path, []byte("#!/bin/sh\necho tampered\n"), 0o755))

	cfg.IndexCheckInterval = 0
	releases.server.Close()
	_, err = newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, logs).resolveBinary(context.Background())
	require.ErrorContains(t, err, "no compatible release is cached")
	require.Contains(t, logs.String(), "discarding cached release 0.1.4")
	_, statErr := os.Stat(filepath.Dir(installed.path))
	require.ErrorIs(t, statErr, os.ErrNotExist, "the tampered release is removed from the cache")
}

func TestConnectorsMCPResolverRefusesRedirectToHTTP(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	var plainRequests atomic.Int64
	plainServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		plainRequests.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plainServer.Close)
	var indexContent atomic.Pointer[[]byte]
	var isIndexRedirected atomic.Bool
	// One TLS server serves the index and redirects to plain HTTP, so the client trusts every https hop.
	redirectServer := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/connectors-mcp/index.json" && !isIndexRedirected.Load() {
			if _, err := response.Write(*indexContent.Load()); err != nil {
				t.Errorf("write index: %v", err)
			}
			return
		}
		http.Redirect(response, request, plainServer.URL+request.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirectServer.Close)
	archive := buildConnectorsMCPArchive(t, fakeBinary)
	digest := sha256.Sum256(archive)
	index, err := json.Marshal(connectorsMCPReleaseIndex{
		SchemaVersion: connectorsMCPReleaseIndexSchemaVersion,
		Releases: []connectorsMCPRelease{{
			Version: "0.1.4", LaunchContract: 1, Tag: "connectorsmcp/v0.1.4",
			Assets: map[string]connectorsMCPReleaseAsset{testConnectorsMCPPlatform: {
				URL: redirectServer.URL + "/download/dex-connectors-mcp.tar.gz", SHA256: hex.EncodeToString(digest[:]),
			}},
		}},
	})
	require.NoError(t, err)
	indexContent.Store(&index)

	for name, isIndexRedirect := range map[string]bool{"index": true, "asset": false} {
		t.Run(name, func(t *testing.T) {
			isIndexRedirected.Store(isIndexRedirect)
			cfg := testConnectorsMCPConfig(t, "")
			cfg.IndexURL = redirectServer.URL + "/connectors-mcp/index.json"
			_, err := newConnectorsMCPReleaseResolver(
				cfg, redirectServer.Client(), testConnectorsMCPPlatform, &synchronizedBuffer{}).resolveBinary(context.Background())
			require.ErrorContains(t, err, "refusing redirect to non-https URL")
			_, statErr := os.Stat(filepath.Join(cfg.ToolsDirectory, "0.1.4"))
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
	require.Zero(t, plainRequests.Load(), "no request reaches the plain HTTP server")
}

func TestConnectorsMCPDiscoveryRequiresConsistentServerIdentity(t *testing.T) {
	var decoyRequests atomic.Int64
	decoy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		decoyRequests.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(decoy.Close)
	for name, testCase := range map[string]struct {
		isLockHeld bool
		edit       func(announcement map[string]any, health map[string]any, healthServerURL string)
		result     connectorsMCPDiscoveryResult
		reason     string
	}{
		"consistent server": {
			isLockHeld: true,
			edit:       func(map[string]any, map[string]any, string) {},
			result:     connectorsMCPServerReusable,
		},
		"url on another local server": {
			isLockHeld: true,
			edit: func(announcement map[string]any, _ map[string]any, _ string) {
				announcement["url"] = decoy.URL + "/mcp"
			},
			result: connectorsMCPServerAbsent,
			reason: "name different addresses",
		},
		"unexpected mcp path": {
			isLockHeld: true,
			edit: func(announcement map[string]any, _ map[string]any, healthServerURL string) {
				announcement["url"] = healthServerURL + "/other"
			},
			result: connectorsMCPServerAbsent,
			reason: "must end in /mcp and /healthz",
		},
		"other state directory": {
			isLockHeld: true,
			edit: func(announcement map[string]any, _ map[string]any, _ string) {
				announcement["stateDir"] = "/tmp/another-state"
			},
			result: connectorsMCPServerAbsent,
			reason: "names state directory",
		},
		"healthz launch contract differs": {
			isLockHeld: true,
			edit: func(_ map[string]any, health map[string]any, _ string) {
				health["launchContract"] = 2
			},
			result: connectorsMCPServerAbsent,
			reason: "does not confirm",
		},
		"healthz version differs": {
			isLockHeld: true,
			edit: func(_ map[string]any, health map[string]any, _ string) {
				health["version"] = "9.9.9"
			},
			result: connectorsMCPServerAbsent,
			reason: "does not confirm",
		},
		"healthz version missing": {
			isLockHeld: true,
			edit: func(_ map[string]any, health map[string]any, _ string) {
				delete(health, "version")
			},
			result: connectorsMCPServerAbsent,
			reason: "does not confirm",
		},
		"healthz version empty": {
			isLockHeld: true,
			edit: func(_ map[string]any, health map[string]any, _ string) {
				health["version"] = ""
			},
			result: connectorsMCPServerAbsent,
			reason: "does not confirm",
		},
		"server.json without connectionsConfigDigest for the default selection": {
			isLockHeld: true,
			edit: func(announcement map[string]any, _ map[string]any, _ string) {
				delete(announcement, "connectionsConfigDigest")
			},
			result: connectorsMCPServerIncompatible,
			reason: "does not record connectionsConfigDigest",
		},
		"stale file with a live unrelated pid and no lock holder": {
			isLockHeld: false,
			edit:       func(map[string]any, map[string]any, string) {},
			result:     connectorsMCPServerAbsent,
			reason:     "server.json is stale",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := testConnectorsMCPConfig(t, "")
			require.NoError(t, os.MkdirAll(cfg.StateDirectory, 0o700))
			health := map[string]any{"status": "ok", "version": "0.1.0", "launchContract": 1, "instanceId": "instance-1", "toolCount": 1}
			healthServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/healthz" {
					http.NotFound(response, request)
					return
				}
				require.NoError(t, json.NewEncoder(response).Encode(health))
			}))
			t.Cleanup(healthServer.Close)
			// The test process stands in for a server: its pid is alive and it can hold the lock.
			announcement := map[string]any{
				"type": "ready", "url": healthServer.URL + "/mcp", "healthUrl": healthServer.URL + "/healthz",
				"version": "0.1.0", "launchContract": 1, "instanceId": "instance-1", "pid": os.Getpid(),
				"stateDir": cfg.StateDirectory, "connectionsFile": cfg.ConnectionsFile, "tools": []string{},
				"connectionsConfigDigest": nil,
			}
			testCase.edit(announcement, health, healthServer.URL)
			content, err := json.Marshal(announcement)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(cfg.StateDirectory, connectorsMCPServerFileName), content, 0o600))
			lockFile, err := os.OpenFile(filepath.Join(cfg.StateDirectory, connectorsMCPLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, lockFile.Close()) })
			if testCase.isLockHeld {
				require.NoError(t, syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
			}

			_, result, reason := discoverConnectorsMCPServer(context.Background(), cfg, newLoopbackHTTPClient())
			require.Equal(t, testCase.result, result, reason)
			require.Contains(t, reason, testCase.reason)
		})
	}
	require.Zero(t, decoyRequests.Load(), "discovery never contacts the forged url")
}

func TestConnectorsMCPManagerWithDefaultSelectionDoesNotReuseRestrictedServer(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	cfg := testConnectorsMCPConfig(t, fakeBinary)
	cfg.Connections = []string{"slack/work"}
	external := startExternalFakeConnectorsMCP(t, fakeBinary, cfg)
	cfg.Connections = nil
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	waitForLog(t, logs, "Connectors MCP: unavailable: another Connectors MCP server holds "+cfg.StateDirectory)
	require.Contains(t, logs.String(), external.announcement.URL+" offers another connection selection")
	status, _ := postMCP(t, proxyServer.URL, nil)
	require.Equal(t, http.StatusServiceUnavailable, status)
}

func rewriteInstalledRelease(t *testing.T, releaseDirectory string, edit func(*connectorsMCPInstalledRelease)) {
	t.Helper()
	recordPath := filepath.Join(releaseDirectory, connectorsMCPInstalledReleaseFileName)
	content, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	var record connectorsMCPInstalledRelease
	require.NoError(t, json.Unmarshal(content, &record))
	edit(&record)
	content, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(recordPath, content, 0o600))
}
