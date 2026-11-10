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
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testConnectorsMCPPlatform = "testos-testarch"

func TestConnectorsMCPResolverSelectsNewestSupportedReleaseAndCachesIt(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	releases := newTestConnectorsMCPReleaseServer(t, fakeBinary)
	releases.setIndex(
		releases.release("0.2.0", 2, ""),
		releases.release("0.1.10", 1, ""),
		releases.release("0.1.9", 1, ""),
		releases.release("../escape", 1, ""),
	)
	cfg := releases.config(t)
	logs := &synchronizedBuffer{}
	resolver := newConnectorsMCPReleaseResolver(cfg, releases.server.Client(), testConnectorsMCPPlatform, logs)

	resolved, err := resolver.resolveBinary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.1.10", resolved.version, "launch contract 2 is skipped and 0.1.10 sorts above 0.1.9")
	require.Equal(t, filepath.Join(cfg.ToolsDirectory, "0.1.10", connectorsMCPBinaryName), resolved.path)
	installedBinary, err := os.ReadFile(resolved.path)
	require.NoError(t, err)
	expectedBinary, err := os.ReadFile(fakeBinary)
	require.NoError(t, err)
	require.Equal(t, expectedBinary, installedBinary)
	info, err := os.Stat(resolved.path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	require.Equal(t, int64(1), releases.indexRequests.Load())

	resolvedAgain, err := newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, logs).resolveBinary(context.Background())
	require.NoError(t, err)
	require.Equal(t, resolved, resolvedAgain)
	require.Equal(t, int64(1), releases.indexRequests.Load(), "a fresh cached index is not fetched again")
	require.Equal(t, int64(1), releases.assetRequests.Load(), "a cached release is not downloaded again")
}

func TestConnectorsMCPResolverRejectsArchiveWithWrongSHA256(t *testing.T) {
	releases := newTestConnectorsMCPReleaseServer(t, buildFakeConnectorsMCP(t))
	releases.setIndex(releases.release("0.1.4", 1, strings.Repeat("0", 64)))
	cfg := releases.config(t)
	resolver := newConnectorsMCPReleaseResolver(cfg, releases.server.Client(), testConnectorsMCPPlatform, &synchronizedBuffer{})

	_, err := resolver.resolveBinary(context.Background())
	require.ErrorContains(t, err, "sha256")
	require.ErrorContains(t, err, "refusing to install it")
	_, statErr := os.Stat(filepath.Join(cfg.ToolsDirectory, "0.1.4"))
	require.ErrorIs(t, statErr, os.ErrNotExist, "a rejected archive is never installed")
}

func TestConnectorsMCPResolverFallsBackToCachedReleaseOffline(t *testing.T) {
	releases := newTestConnectorsMCPReleaseServer(t, buildFakeConnectorsMCP(t))
	releases.setIndex(releases.release("0.1.4", 1, ""))
	cfg := releases.config(t)
	logs := &synchronizedBuffer{}
	_, err := newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, logs).resolveBinary(context.Background())
	require.NoError(t, err)

	cfg.IndexCheckInterval = 0
	releases.server.Close()
	resolved, err := newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, logs).resolveBinary(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.1.4", resolved.version)
	require.Contains(t, logs.String(), "using cached release 0.1.4")
}

func TestConnectorsMCPResolverReportsUnsupportedPlatformAndLaunchContract(t *testing.T) {
	releases := newTestConnectorsMCPReleaseServer(t, buildFakeConnectorsMCP(t))
	unsupportedPlatform := releases.release("0.1.4", 1, "")
	unsupportedPlatform.Assets = map[string]connectorsMCPReleaseAsset{"otheros-otherarch": unsupportedPlatform.Assets[testConnectorsMCPPlatform]}
	releases.setIndex(unsupportedPlatform, releases.release("0.3.0", 7, ""))
	cfg := releases.config(t)

	_, err := newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, &synchronizedBuffer{}).resolveBinary(context.Background())
	require.ErrorContains(t, err, "no connectors MCP release is published for "+testConnectorsMCPPlatform)

	cfg.Version = "0.3.0"
	_, err = newConnectorsMCPReleaseResolver(
		cfg, releases.server.Client(), testConnectorsMCPPlatform, &synchronizedBuffer{}).resolveBinary(context.Background())
	require.ErrorContains(t, err, "launch contract 7")
}

func TestConnectorsMCPManagerStartsServerAndProxiesMCPWithHostChecks(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	cfg := testConnectorsMCPConfig(t, buildFakeConnectorsMCP(t))
	cfg.Connections = []string{"slack/work=post_thread_reply"}
	manager, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	result := waitForProxiedMCPResult(t, proxyServer.URL, "")
	require.Equal(t, "tools/list", result.Method)
	require.Equal(t, strings.TrimPrefix(proxyServer.URL, "http://"), result.Host, "the proxy keeps the loopback Host")
	require.Equal(t, []string{"slack/work=post_thread_reply"}, result.Connections)
	starts := readFakeConnectorsMCPStarts(t, recordDirectory)
	require.Len(t, starts, 1)
	require.Equal(t, []string{
		"--listen", "127.0.0.1:0",
		"--connections-file", cfg.ConnectionsFile,
		"--state-dir", cfg.StateDirectory,
		"--output", "json",
		"--exit-on-stdin-close",
		"--connection", "slack/work=post_thread_reply",
	}, starts[0].Args)
	require.Contains(t, logs.String(), "Connectors MCP: started server 0.1.0 with 1 tools")

	for name, mutate := range map[string]func(*http.Request){
		"DNS name Host":       func(request *http.Request) { request.Host = "attacker.example" },
		"LAN IP Host":         func(request *http.Request) { request.Host = "192.168.1.20:8802" },
		"cross-origin Origin": func(request *http.Request) { request.Header.Set("Origin", "http://attacker.example") },
		"https Origin":        func(request *http.Request) { request.Header.Set("Origin", "https://localhost") },
	} {
		status, body := postMCP(t, proxyServer.URL, mutate)
		require.Equal(t, http.StatusForbidden, status, "%s: %s", name, body)
	}
	status, body := postMCP(t, proxyServer.URL, func(request *http.Request) {
		request.Host = "localhost:8802"
		request.Header.Set("Origin", "http://localhost:8802")
	})
	require.Equal(t, http.StatusOK, status, "a same-origin loopback request passes both Origin checks: %s", body)

	require.NoError(t, manager.Stop())
	require.False(t, isProcessAlive(starts[0].PID), "stopping dexcli dev stops the server it started")
	_, err := os.Stat(filepath.Join(cfg.StateDirectory, connectorsMCPServerFileName))
	require.ErrorIs(t, err, os.ErrNotExist, "closing stdin lets the server exit cleanly and remove server.json")
	status, body = postMCP(t, proxyServer.URL, nil)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
}

func TestConnectorsMCPManagersOfDifferentConnectionsFilesUseSeparateStateDirectories(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	dexStateDirectory := t.TempDir()
	defaultConfig := testConnectorsMCPConfig(t, fakeBinary)
	defaultConfig.StateDirectory = ""
	defaultConfig.ConnectionsFile = ""
	require.NoError(t, defaultConfig.resolvePaths(dexStateDirectory, filepath.Join(dexStateDirectory, "connectors")))
	otherConfig := testConnectorsMCPConfig(t, fakeBinary)
	otherConfig.StateDirectory = ""
	otherConfig.ConnectionsFile = ""
	require.NoError(t, otherConfig.resolvePaths(dexStateDirectory, t.TempDir()))
	require.Equal(t, filepath.Join(dexStateDirectory, "connectors-mcp"), defaultConfig.StateDirectory)
	require.NotEqual(t, defaultConfig.StateDirectory, otherConfig.StateDirectory)

	_, defaultProxy, _ := startTestConnectorsMCPManager(t, defaultConfig)
	_, otherProxy, _ := startTestConnectorsMCPManager(t, otherConfig)
	defaultResult := waitForProxiedMCPResult(t, defaultProxy.URL, "")
	otherResult := waitForProxiedMCPResult(t, otherProxy.URL, "")
	require.NotEqual(t, defaultResult.InstanceID, otherResult.InstanceID, "each connections file gets its own server")
}

func TestConnectorsMCPManagerRestartsCrashedServer(t *testing.T) {
	cfg := testConnectorsMCPConfig(t, buildFakeConnectorsMCP(t))
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)
	first := waitForProxiedMCPResult(t, proxyServer.URL, "")

	require.NoError(t, syscall.Kill(first.PID, syscall.SIGKILL))
	second := waitForProxiedMCPResult(t, proxyServer.URL, first.InstanceID)
	require.NotEqual(t, first.PID, second.PID)
	require.Contains(t, logs.String(), "server stopped (exited")
}

func TestConnectorsMCPManagerReusesRunningServerFromServerJSON(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	cfg := testConnectorsMCPConfig(t, fakeBinary)
	external := startExternalFakeConnectorsMCP(t, fakeBinary, cfg)
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	reused := waitForProxiedMCPResult(t, proxyServer.URL, "")
	require.Equal(t, external.announcement.InstanceID, reused.InstanceID)
	require.Contains(t, logs.String(), "reusing running server")
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 1, "dexcli started no second server")

	external.stop(t)
	started := waitForProxiedMCPResult(t, proxyServer.URL, external.announcement.InstanceID)
	require.NotEqual(t, external.announcement.InstanceID, started.InstanceID)
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 2, "dexcli starts its own server when the reused one stops")
}

func TestConnectorsMCPManagerReusesRunningServerWithSameSelection(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	cfg := testConnectorsMCPConfig(t, fakeBinary)
	cfg.Connections = []string{"slack/work=post_thread_reply,list_thread_messages", "hacker-news-daily/default"}
	external := startExternalFakeConnectorsMCP(t, fakeBinary, cfg)
	require.NotNil(t, external.announcement.ConnectionsConfigDigest)
	// The same selection in another order and tool order has the same digest.
	cfg.Connections = []string{"hacker-news-daily/default", "slack/work=list_thread_messages,post_thread_reply"}
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	reused := waitForProxiedMCPResult(t, proxyServer.URL, "")
	require.Equal(t, external.announcement.InstanceID, reused.InstanceID)
	require.Contains(t, logs.String(), "reusing running server")
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 1, "dexcli started no second server")
}

func TestConnectorsMCPManagerReportsLockedStateDirectoryWithOtherSelection(t *testing.T) {
	fakeBinary := buildFakeConnectorsMCP(t)
	cfg := testConnectorsMCPConfig(t, fakeBinary)
	external := startExternalFakeConnectorsMCP(t, fakeBinary, cfg)
	cfg.Connections = []string{"slack/personal"}
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	waitForLog(t, logs, "Connectors MCP: unavailable: another Connectors MCP server holds "+cfg.StateDirectory)
	require.Contains(t, logs.String(), external.announcement.URL+" offers another connection selection")
	status, body := postMCP(t, proxyServer.URL, nil)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, "unavailable")
}

func TestConnectorsMCPManagerGivesUpAfterRepeatedStartFailures(t *testing.T) {
	t.Setenv("FAKE_CONNECTORS_MCP_MODE", "error")
	cfg := testConnectorsMCPConfig(t, buildFakeConnectorsMCP(t))
	cfg.MaxConsecutiveStartFailures = 2
	_, proxyServer, logs := startTestConnectorsMCPManager(t, cfg)

	waitForLog(t, logs, "Connectors MCP: unavailable: Connectors MCP server failed to start: fake startup failure")
	require.Contains(t, logs.String(), "retrying in")
	require.Contains(t, logs.String(), connectorsMCPHint)
	status, body := postMCP(t, proxyServer.URL, nil)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, "fake startup failure")
}

func TestDevProxiesConnectorsMCPOnDexWebPort(t *testing.T) {
	cfg := testConfig(t)
	cfg.ConnectorsMCP.IsEnabled = true
	cfg.ConnectorsMCP.BinaryPath = buildFakeConnectorsMCP(t)
	cfg.ConnectorConfigDirectory = t.TempDir()
	webURL, output, stop := startTestDev(t, cfg)

	require.Contains(t, output.String(), "Connectors MCP: "+webURL+"/mcp")
	result := waitForProxiedMCPResult(t, webURL, "")
	require.Equal(t, "tools/list", result.Method)
	status, body := postMCP(t, webURL, func(request *http.Request) { request.Host = "attacker.example" })
	require.Equal(t, http.StatusForbidden, status, body)

	require.NoError(t, stop())
	require.False(t, isProcessAlive(result.PID), "stopping dexcli dev stops the Connectors MCP server")
}

func TestDevStartsWithHintWhenConnectorsMCPIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.ConnectorsMCP.IsEnabled = true
	cfg.ConnectorsMCP.BinaryPath = ""
	cfg.ConnectorConfigDirectory = t.TempDir()
	// Nothing listens on port 1, so the release index cannot be fetched, as when offline.
	cfg.ConnectorsMCP.IndexURL = "https://127.0.0.1:1/connectors-mcp/index.json"
	webURL, output, stop := startTestDev(t, cfg)

	waitForLog(t, output, "Connectors MCP: unavailable: fetch connectors MCP release index")
	require.Contains(t, output.String(), connectorsMCPHint)
	status, body := postMCP(t, webURL, nil)
	require.Equal(t, http.StatusServiceUnavailable, status, body)
	healthResponse, err := http.Get(webURL + "/healthz")
	require.NoError(t, err)
	require.NoError(t, healthResponse.Body.Close())
	require.Equal(t, http.StatusOK, healthResponse.StatusCode, "Dex Web keeps serving")
	require.NoError(t, stop())
}

// startTestDev runs dexcli dev for cfg until it is ready and returns Dex Web's URL, its output, and a stop function.
func startTestDev(t *testing.T, cfg *Config) (string, *synchronizedBuffer, func() error) {
	t.Helper()
	ports := freePorts(t, 2)
	cfg.DexPort = ports[0]
	cfg.WebPort = ports[1]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	output := &synchronizedBuffer{}
	runCtx, cancel := context.WithCancel(context.Background())
	runFinished := make(chan error, 1)
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = newSupervisor(cfg, output, output).Run(runCtx)
		close(runDone)
		runFinished <- runErr
	}()
	isStopped := false
	stop := func() error {
		if isStopped {
			return nil
		}
		isStopped = true
		cancel()
		<-runDone
		return runErr
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("dexcli dev stopped with error: %v", err)
		}
	})
	waitForReadyStack(t, output, runFinished)
	return "http://127.0.0.1:" + strconv.Itoa(cfg.WebPort), output, stop
}

func buildFakeConnectorsMCP(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), connectorsMCPBinaryName)
	command := exec.Command("go", "build", "-o", binary, filepath.Join("testdata", "fake-dex-connectors-mcp", "main.go"))
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake dex-connectors-mcp: %v\n%s", err, output)
	}
	return binary
}

func testConnectorsMCPConfig(t *testing.T, binaryPath string) *ConnectorsMCPConfig {
	t.Helper()
	cfg := defaultConnectorsMCPConfig()
	cfg.BinaryPath = binaryPath
	cfg.RestartBackoffBase = 10 * time.Millisecond
	cfg.RestartBackoffMax = 50 * time.Millisecond
	cfg.HealthCheckInterval = 50 * time.Millisecond
	cfg.ShutdownTimeout = 5 * time.Second
	stateDirectory := t.TempDir()
	require.NoError(t, cfg.resolvePaths(stateDirectory, filepath.Join(stateDirectory, "connectors")))
	return &cfg
}

func startTestConnectorsMCPManager(
	t *testing.T,
	cfg *ConnectorsMCPConfig,
) (*connectorsMCPManager, *httptest.Server, *synchronizedBuffer) {
	t.Helper()
	logs := &synchronizedBuffer{}
	proxy := newConnectorsMCPProxy()
	proxyServer := httptest.NewServer(proxy)
	t.Cleanup(proxyServer.Close)
	resolver := newConnectorsMCPReleaseResolver(cfg, http.DefaultClient, testConnectorsMCPPlatform, logs)
	manager := newConnectorsMCPManager(cfg, resolver, newLoopbackHTTPClient(), proxy, logs, proxyServer.URL+"/mcp")
	manager.Start(context.Background())
	t.Cleanup(func() {
		if err := manager.Stop(); err != nil {
			t.Errorf("stop connectors MCP manager: %v", err)
		}
	})
	return manager, proxyServer, logs
}

type fakeConnectorsMCPResult struct {
	InstanceID  string   `json:"instanceId"`
	PID         int      `json:"pid"`
	Method      string   `json:"method"`
	Host        string   `json:"host"`
	Connections []string `json:"connections"`
}

// waitForProxiedMCPResult polls baseURL/mcp until a server other than previousInstanceID answers.
func waitForProxiedMCPResult(t *testing.T, baseURL string, previousInstanceID string) fakeConnectorsMCPResult {
	t.Helper()
	var result fakeConnectorsMCPResult
	lastResponse := ""
	require.Eventually(t, func() bool {
		status, body := postMCP(t, baseURL, nil)
		lastResponse = fmt.Sprintf("HTTP %d %s", status, body)
		if status != http.StatusOK {
			return false
		}
		var envelope struct {
			Result fakeConnectorsMCPResult `json:"result"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &envelope))
		result = envelope.Result
		return result.InstanceID != previousInstanceID
	}, 30*time.Second, 20*time.Millisecond, "last /mcp response: %s", lastResponse)
	return result
}

func postMCP(t *testing.T, baseURL string, mutate func(*http.Request)) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(request)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	body, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, string(body)
}

func waitForLog(t *testing.T, logs *synchronizedBuffer, text string) {
	t.Helper()
	require.Eventually(t, func() bool { return strings.Contains(logs.String(), text) },
		30*time.Second, 20*time.Millisecond, "missing %q in:\n%s", text, logs)
}

type fakeConnectorsMCPStart struct {
	PID  int      `json:"pid"`
	Args []string `json:"args"`
}

func readFakeConnectorsMCPStarts(t *testing.T, recordDirectory string) []fakeConnectorsMCPStart {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(recordDirectory, "starts.jsonl"))
	require.NoError(t, err)
	var starts []fakeConnectorsMCPStart
	for _, line := range bytes.Split(bytes.TrimSpace(content), []byte("\n")) {
		var start fakeConnectorsMCPStart
		require.NoError(t, json.Unmarshal(line, &start))
		starts = append(starts, start)
	}
	return starts
}

type externalFakeConnectorsMCP struct {
	command      *exec.Cmd
	stdin        io.WriteCloser
	announcement connectorsMCPAnnouncement
	isStopped    bool
}

// startExternalFakeConnectorsMCP runs a server for cfg's state directory that the manager did not start.
func startExternalFakeConnectorsMCP(t *testing.T, binaryPath string, cfg *ConnectorsMCPConfig) *externalFakeConnectorsMCP {
	t.Helper()
	command := exec.Command(binaryPath, connectorsMCPServeArguments(cfg)...)
	stdin, err := command.StdinPipe()
	require.NoError(t, err)
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	command.Stderr = io.Discard
	require.NoError(t, command.Start())
	external := &externalFakeConnectorsMCP{command: command, stdin: stdin}
	t.Cleanup(func() { external.stop(t) })
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	require.NoError(t, err)
	external.announcement, err = parseConnectorsMCPReadyLine(line)
	require.NoError(t, err)
	return external
}

func (external *externalFakeConnectorsMCP) stop(t *testing.T) {
	t.Helper()
	if external.isStopped {
		return
	}
	external.isStopped = true
	require.NoError(t, external.stdin.Close())
	require.NoError(t, external.command.Wait())
}

type testConnectorsMCPReleaseServer struct {
	server        *httptest.Server
	archive       []byte
	archiveSHA256 string
	index         atomic.Pointer[[]byte]
	indexRequests atomic.Int64
	assetRequests atomic.Int64
}

func newTestConnectorsMCPReleaseServer(t *testing.T, fakeBinary string) *testConnectorsMCPReleaseServer {
	t.Helper()
	releases := &testConnectorsMCPReleaseServer{archive: buildConnectorsMCPArchive(t, fakeBinary)}
	digest := sha256.Sum256(releases.archive)
	releases.archiveSHA256 = hex.EncodeToString(digest[:])
	releases.server = httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/connectors-mcp/index.json":
			releases.indexRequests.Add(1)
			response.Header().Set("Content-Type", "application/json")
			if _, err := response.Write(*releases.index.Load()); err != nil {
				t.Errorf("write index: %v", err)
			}
		case strings.HasPrefix(request.URL.Path, "/download/"):
			releases.assetRequests.Add(1)
			if _, err := response.Write(releases.archive); err != nil {
				t.Errorf("write archive: %v", err)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(releases.server.Close)
	return releases
}

// release returns an index entry whose asset is the fake archive; an empty sha256Override uses its real digest.
func (releases *testConnectorsMCPReleaseServer) release(version string, launchContract int, sha256Override string) connectorsMCPRelease {
	digest := releases.archiveSHA256
	if sha256Override != "" {
		digest = sha256Override
	}
	return connectorsMCPRelease{
		Version:        version,
		LaunchContract: launchContract,
		Tag:            "connectorsmcp/v" + version,
		Assets: map[string]connectorsMCPReleaseAsset{testConnectorsMCPPlatform: {
			URL:    releases.server.URL + "/download/connectorsmcp%2Fv" + version + "/dex-connectors-mcp.tar.gz",
			SHA256: digest,
		}},
	}
}

func (releases *testConnectorsMCPReleaseServer) setIndex(entries ...connectorsMCPRelease) {
	content, err := json.Marshal(connectorsMCPReleaseIndex{SchemaVersion: connectorsMCPReleaseIndexSchemaVersion, Releases: entries})
	if err != nil {
		panic(err)
	}
	releases.index.Store(&content)
}

func (releases *testConnectorsMCPReleaseServer) config(t *testing.T) *ConnectorsMCPConfig {
	t.Helper()
	cfg := testConnectorsMCPConfig(t, "")
	cfg.IndexURL = releases.server.URL + "/connectors-mcp/index.json"
	return cfg
}

// buildConnectorsMCPArchive packs binaryPath as a release archive with a top-level directory and a decoy entry.
func buildConnectorsMCPArchive(t *testing.T, binaryPath string) []byte {
	t.Helper()
	binary, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	entries := tar.NewWriter(compressed)
	for name, content := range map[string][]byte{
		"dex-connectors-mcp_test/README.md":          []byte("decoy"),
		"dex-connectors-mcp_test/dex-connectors-mcp": binary,
	} {
		require.NoError(t, entries.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}))
		_, err := entries.Write(content)
		require.NoError(t, err)
	}
	require.NoError(t, entries.Close())
	require.NoError(t, compressed.Close())
	return archive.Bytes()
}
