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
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConnectorsMCPCommandStartsServerAndStopsItWhenStdinCloses(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	cfg := testConnectorsMCPCommandConfig(t, buildFakeConnectorsMCP(t))
	command := startTestConnectorsMCPCommand(t, cfg)

	ready := command.readReadyLine(t)
	require.Equal(t, "ready", ready.Type)
	require.Equal(t, os.Getpid(), ready.PID, "the pid is the dexcli process the caller owns")
	require.Equal(t, cfg.ConnectorsMCP.ConnectionsFile, ready.ConnectionsFile)
	require.True(t, strings.HasPrefix(ready.URL, "http://127.0.0.1:") && strings.HasSuffix(ready.URL, "/mcp"), ready.URL)
	baseURL := strings.TrimSuffix(ready.URL, "/mcp")
	result := waitForProxiedMCPResult(t, baseURL, "")
	require.Equal(t, "tools/list", result.Method)
	starts := readFakeConnectorsMCPStarts(t, recordDirectory)
	require.Len(t, starts, 1)
	require.Equal(t, result.PID, starts[0].PID)
	status, body := postMCP(t, baseURL, func(request *http.Request) { request.Host = "attacker.example" })
	require.Equal(t, http.StatusForbidden, status, body)

	require.NoError(t, command.closeStdinAndWait(t))
	require.False(t, isProcessAlive(result.PID), "the command stops the server it started")
	_, err := os.Stat(filepath.Join(cfg.ConnectorsMCP.StateDirectory, connectorsMCPServerFileName))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestConnectorsMCPCommandReusesRunningServerAndLeavesItRunning(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	cfg := testConnectorsMCPCommandConfig(t, fakeBinary)
	require.NoError(t, cfg.ConnectorsMCP.resolvePaths(cfg.StateDirectory, cfg.ConnectorConfigDirectory))
	external := startExternalFakeConnectorsMCP(t, fakeBinary, &cfg.ConnectorsMCP)
	command := startTestConnectorsMCPCommand(t, cfg)

	ready := command.readReadyLine(t)
	reused := waitForProxiedMCPResult(t, strings.TrimSuffix(ready.URL, "/mcp"), "")
	require.Equal(t, external.announcement.InstanceID, reused.InstanceID)
	require.Contains(t, command.logs.String(), "reusing running server")
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 1, "the command started no second server")

	require.NoError(t, command.closeStdinAndWait(t))
	require.True(t, isProcessAlive(external.announcement.PID), "the command never stops a server it reused")
}

func TestConnectorsMCPCommandReusesTheServerOfARunningDexcliDev(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	devConfig := testConfig(t)
	devConfig.ConnectorsMCP.IsEnabled = true
	devConfig.ConnectorsMCP.BinaryPath = fakeBinary
	devConfig.ConnectorConfigDirectory = filepath.Join(devConfig.StateDirectory, "connectors")
	webURL, _, _ := startTestDev(t, devConfig)
	devResult := waitForProxiedMCPResult(t, webURL, "")

	cfg := testConnectorsMCPCommandConfig(t, fakeBinary)
	cfg.StateDirectory = devConfig.StateDirectory
	cfg.ConnectorConfigDirectory = devConfig.ConnectorConfigDirectory
	command := startTestConnectorsMCPCommand(t, cfg)
	ready := command.readReadyLine(t)
	commandResult := waitForProxiedMCPResult(t, strings.TrimSuffix(ready.URL, "/mcp"), "")
	require.Equal(t, devResult.InstanceID, commandResult.InstanceID, "one server serves both commands")
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 1)

	require.NoError(t, command.closeStdinAndWait(t))
	afterExit := waitForProxiedMCPResult(t, webURL, "")
	require.Equal(t, devResult.InstanceID, afterExit.InstanceID, "dexcli dev keeps its server")
}

func TestDexcliDevReusesTheServerOfARunningConnectorsMCPCommand(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	devConfig := testConfig(t)
	cfg := testConnectorsMCPCommandConfig(t, fakeBinary)
	cfg.StateDirectory = devConfig.StateDirectory
	cfg.ConnectorConfigDirectory = filepath.Join(devConfig.StateDirectory, "connectors")
	command := startTestConnectorsMCPCommand(t, cfg)
	ready := command.readReadyLine(t)
	commandResult := waitForProxiedMCPResult(t, strings.TrimSuffix(ready.URL, "/mcp"), "")

	devConfig.ConnectorsMCP = *testConnectorsMCPConfig(t, fakeBinary)
	devConfig.ConnectorsMCP.StateDirectory = ""
	devConfig.ConnectorsMCP.ConnectionsFile = ""
	devConfig.ConnectorsMCP.IsEnabled = true
	devConfig.ConnectorConfigDirectory = cfg.ConnectorConfigDirectory
	webURL, output, _ := startTestDev(t, devConfig)
	devResult := waitForProxiedMCPResult(t, webURL, "")
	require.Equal(t, commandResult.InstanceID, devResult.InstanceID, "one server serves both commands")
	require.Contains(t, output.String(), "reusing running server")
	require.Len(t, readFakeConnectorsMCPStarts(t, recordDirectory), 1)

	require.NoError(t, command.closeStdinAndWait(t))
	restarted := waitForProxiedMCPResult(t, webURL, commandResult.InstanceID)
	require.NotEqual(t, commandResult.InstanceID, restarted.InstanceID, "dexcli dev starts its own server after the command exits")
}

func TestConnectorsMCPCommandAliasesOfOneConnectionsFileShareOneServer(t *testing.T) {
	recordDirectory := t.TempDir()
	t.Setenv("FAKE_CONNECTORS_MCP_RECORD_DIR", recordDirectory)
	fakeBinary := buildFakeConnectorsMCP(t)
	realConfig := testConnectorsMCPCommandConfig(t, fakeBinary)
	realConfig.ConnectorConfigDirectory = filepath.Join(t.TempDir(), "connectors")
	require.NoError(t, os.MkdirAll(realConfig.ConnectorConfigDirectory, 0o700))
	aliasDirectory := filepath.Join(t.TempDir(), "connectors-alias")
	require.NoError(t, os.Symlink(realConfig.ConnectorConfigDirectory, aliasDirectory))
	aliasConfig := testConnectorsMCPCommandConfig(t, fakeBinary)
	aliasConfig.StateDirectory = realConfig.StateDirectory
	aliasConfig.ConnectorConfigDirectory = aliasDirectory

	realCommand := startTestConnectorsMCPCommand(t, realConfig)
	aliasCommand := startTestConnectorsMCPCommand(t, aliasConfig)
	realReady := realCommand.readReadyLine(t)
	aliasReady := aliasCommand.readReadyLine(t)

	require.Equal(t, realConfig.ConnectorsMCP.StateDirectory, aliasConfig.ConnectorsMCP.StateDirectory)
	require.Equal(t, realReady.ConnectionsFile, aliasReady.ConnectionsFile)
	realResult := waitForProxiedMCPResult(t, strings.TrimSuffix(realReady.URL, "/mcp"), "")
	aliasResult := waitForProxiedMCPResult(t, strings.TrimSuffix(aliasReady.URL, "/mcp"), "")
	require.Equal(t, realResult.InstanceID, aliasResult.InstanceID, "one server serves both paths")
	require.Len(t, uniqueRunningStarts(t, recordDirectory), 1, "only one server runs")
}

func TestConnectorsMCPCommandPrintsErrorLineWhenNoServerStarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stdout := &synchronizedBuffer{}

	err := ExecuteConnectors(context.Background(), []string{
		"mcp", "--connectors-mcp-binary", filepath.Join(t.TempDir(), "missing-dex-connectors-mcp"),
	}, strings.NewReader(""), stdout, io.Discard)

	require.ErrorContains(t, err, "Connectors MCP server is unavailable")
	errorLine := decodeOnlyConnectorsMCPErrorLine(t, stdout.String())
	require.Contains(t, errorLine.Message, "is not executable")
}

func TestConnectorsMCPCommandPrintsErrorLineForInvalidFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, arguments := range map[string][]string{
		"invalid version":          {"mcp", "--connectors-mcp-version=not-a-version"},
		"empty config directory":   {"mcp", "--connector-config-dir="},
		"unknown flag":             {"mcp", "--no-such-flag"},
		"version and binary given": {"mcp", "--connectors-mcp-version=0.1.4", "--connectors-mcp-binary=x"},
	} {
		t.Run(name, func(t *testing.T) {
			stdout := &synchronizedBuffer{}
			err := ExecuteConnectors(context.Background(), arguments, strings.NewReader(""), stdout, io.Discard)
			require.Error(t, err)
			errorLine := decodeOnlyConnectorsMCPErrorLine(t, stdout.String())
			require.Equal(t, err.Error(), errorLine.Message)
		})
	}
}

func TestConnectorsMCPCommandHelpPrintsNoProtocolLine(t *testing.T) {
	stdout := &synchronizedBuffer{}
	require.NoError(t, ExecuteConnectors(context.Background(), []string{"mcp", "--help"}, strings.NewReader(""), stdout, io.Discard))
	require.Empty(t, stdout.String())
}

// decodeOnlyConnectorsMCPErrorLine requires output to be exactly one error line and returns it.
func decodeOnlyConnectorsMCPErrorLine(t *testing.T, output string) connectorsMCPCommandErrorLine {
	t.Helper()
	require.Equal(t, 1, strings.Count(output, "\n"), output)
	var errorLine connectorsMCPCommandErrorLine
	require.NoError(t, json.Unmarshal([]byte(output), &errorLine), output)
	require.Equal(t, "error", errorLine.Type)
	return errorLine
}

// uniqueRunningStarts returns the recorded server starts whose process still runs.
func uniqueRunningStarts(t *testing.T, recordDirectory string) []fakeConnectorsMCPStart {
	t.Helper()
	var running []fakeConnectorsMCPStart
	for _, start := range readFakeConnectorsMCPStarts(t, recordDirectory) {
		if isProcessAlive(start.PID) {
			running = append(running, start)
		}
	}
	return running
}

func TestConnectorsMCPCommandParsesSharedFlags(t *testing.T) {
	connectorDirectory := t.TempDir()
	cfg, err := parseConnectorsMCPCommandConfig([]string{
		"--connector-config-dir", connectorDirectory,
		"--connectors-mcp-version", "0.1.4",
		"--connectors-mcp-connection", "slack/work=post_thread_reply",
		"--exit-on-stdin-close",
	}, io.Discard)
	require.NoError(t, err)
	require.Equal(t, connectorDirectory, cfg.ConnectorConfigDirectory)
	require.Equal(t, "0.1.4", cfg.ConnectorsMCP.Version)
	require.Equal(t, []string{"slack/work=post_thread_reply"}, cfg.ConnectorsMCP.Connections)
	require.True(t, cfg.IsExitingOnStdinClose)

	_, err = parseConnectorsMCPCommandConfig([]string{"--connectors-mcp-version", "0.1.4", "--connectors-mcp-binary", "x"}, io.Discard)
	require.ErrorContains(t, err, "cannot be combined")
}

func testConnectorsMCPCommandConfig(t *testing.T, binaryPath string) *ConnectorsMCPCommandConfig {
	t.Helper()
	mcpConfig := testConnectorsMCPConfig(t, binaryPath)
	stateDirectory := t.TempDir()
	mcpConfig.StateDirectory = ""
	mcpConfig.ConnectionsFile = ""
	mcpConfig.ToolsDirectory = ""
	return &ConnectorsMCPCommandConfig{
		StateDirectory:           stateDirectory,
		ConnectorConfigDirectory: filepath.Join(stateDirectory, "connectors"),
		ConnectorsMCP:            *mcpConfig,
		IsExitingOnStdinClose:    true,
		ShutdownTimeout:          5 * time.Second,
	}
}

type testConnectorsMCPCommand struct {
	stdinClosed    chan struct{}
	closeStdinOnce sync.Once
	stdout         *bufio.Reader
	logs           *synchronizedBuffer
	done           chan struct{}
	err            error
}

// startTestConnectorsMCPCommand runs dexcli connectors mcp for cfg in this process, with a piped stdout.
func startTestConnectorsMCPCommand(t *testing.T, cfg *ConnectorsMCPCommandConfig) *testConnectorsMCPCommand {
	t.Helper()
	stdoutReader, stdoutWriter := io.Pipe()
	command := &testConnectorsMCPCommand{
		stdinClosed: make(chan struct{}),
		stdout:      bufio.NewReader(stdoutReader),
		logs:        &synchronizedBuffer{},
		done:        make(chan struct{}),
	}
	output := &connectorsMCPCommandOutput{stdout: stdoutWriter}
	go func() {
		command.err = runConnectorsMCPCommand(context.Background(), cfg, command.stdinClosed, output, command.logs)
		// Closing a pipe writer always succeeds; it only ends the test's reads.
		_ = stdoutWriter.Close()
		close(command.done)
	}()
	t.Cleanup(func() {
		command.closeStdin()
		<-command.done
	})
	return command
}

func (command *testConnectorsMCPCommand) closeStdin() {
	command.closeStdinOnce.Do(func() { close(command.stdinClosed) })
}

func (command *testConnectorsMCPCommand) readLine(t *testing.T) []byte {
	t.Helper()
	type readResult struct {
		line []byte
		err  error
	}
	results := make(chan readResult, 1)
	go func() {
		line, err := command.stdout.ReadBytes('\n')
		results <- readResult{line: line, err: err}
	}()
	select {
	case result := <-results:
		require.NoError(t, result.err, "logs:\n%s", command.logs)
		return result.line
	case <-time.After(30 * time.Second):
		t.Fatalf("no stdout line within 30 seconds; logs:\n%s", command.logs)
		return nil
	}
}

func (command *testConnectorsMCPCommand) readReadyLine(t *testing.T) connectorsMCPCommandReadyLine {
	t.Helper()
	var ready connectorsMCPCommandReadyLine
	line := command.readLine(t)
	require.NoError(t, json.Unmarshal(line, &ready), string(line))
	require.Equal(t, "ready", ready.Type, string(line))
	return ready
}

func (command *testConnectorsMCPCommand) closeStdinAndWait(t *testing.T) error {
	t.Helper()
	command.closeStdin()
	return command.wait(t)
}

func (command *testConnectorsMCPCommand) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-command.done:
		return command.err
	case <-time.After(30 * time.Second):
		t.Fatalf("the command did not exit within 30 seconds; logs:\n%s", command.logs)
		return nil
	}
}
