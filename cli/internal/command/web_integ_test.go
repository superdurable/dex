// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebServesDexWebAgainstExistingServer(t *testing.T) {
	_, serverAddress := startTestFlowService(t)
	webCommand := startTestWebCommand(t, "--server", serverAddress, "web", "--web-port", "0")

	healthResponse, err := http.Get(webCommand.webURL + "/healthz")
	require.NoError(t, err)
	require.NoError(t, healthResponse.Body.Close())
	require.Equal(t, http.StatusOK, healthResponse.StatusCode)
	readyResponse, err := http.Get(webCommand.webURL + "/readyz")
	require.NoError(t, err)
	require.NoError(t, readyResponse.Body.Close())
	require.Equal(t, http.StatusOK, readyResponse.StatusCode)
	requireWebSearchReachesFlowService(t, webCommand.webURL)

	require.NoError(t, webCommand.stop())
	require.Contains(t, webCommand.stdout.String(), "Dex Web:       "+webCommand.webURL)
	require.Contains(t, webCommand.stdout.String(), "Dex Server:    "+serverAddress)
	require.NotContains(t, webCommand.stderr.String(), "warning:")
}

func TestWebRequestTimeoutDoesNotBoundRunningDexWeb(t *testing.T) {
	_, serverAddress := startTestFlowService(t)
	webCommand := startTestWebCommand(t, "--server", serverAddress, "--timeout", "1s", "web", "--web-port", "0")

	// Elapsed time past --timeout is the behavior under test.
	time.Sleep(1500 * time.Millisecond)
	requireWebSearchReachesFlowService(t, webCommand.webURL)
	require.NoError(t, webCommand.stop())
}

func TestWebFailsStartupWhenServerIsUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	unreachableAddress := listener.Addr().String()
	require.NoError(t, listener.Close())

	app := NewApp(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	app.openBrowser = func(url string) error {
		t.Errorf("unexpected browser open for %s", url)
		return nil
	}
	err = app.Execute(context.Background(), []string{
		"--server", unreachableAddress, "--timeout", "2s", "web", "--web-port", "0",
	})
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, err.Error(), "--server "+unreachableAddress+" is unreachable")
}

func TestWebRejectsUnexpectedArguments(t *testing.T) {
	stdout := &bytes.Buffer{}
	app := NewApp(bytes.NewReader(nil), stdout, &bytes.Buffer{})
	err := app.Execute(context.Background(), []string{"web", "extra"})
	require.Error(t, err)
	require.Equal(t, 2, ExitCode(err))

	require.NoError(t, app.Execute(context.Background(), []string{"web", "--help"}))
	require.Contains(t, stdout.String(), "Usage: dexcli [global flags] web [flags]")
}

type testWebCommand struct {
	webURL       string
	stdout       *lockedBuffer
	stderr       *lockedBuffer
	cancel       context.CancelFunc
	commandError chan error
}

func startTestWebCommand(t *testing.T, args ...string) *testWebCommand {
	t.Helper()
	stdout := &lockedBuffer{}
	stderr := &lockedBuffer{}
	app := NewApp(bytes.NewReader(nil), stdout, stderr)
	openedURLs := make(chan string, 1)
	app.openBrowser = func(url string) error {
		openedURLs <- url
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	commandError := make(chan error, 1)
	go func() { commandError <- app.Execute(ctx, args) }()
	webCommand := &testWebCommand{stdout: stdout, stderr: stderr, cancel: cancel, commandError: commandError}
	t.Cleanup(func() {
		cancel()
	})
	select {
	case webCommand.webURL = <-openedURLs:
	case err := <-commandError:
		t.Fatalf("dexcli web exited before readiness: %v stderr=%s", err, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("dexcli web did not become ready; stderr=%s", stderr.String())
	}
	return webCommand
}

func (c *testWebCommand) stop() error {
	c.cancel()
	return <-c.commandError
}

func requireWebSearchReachesFlowService(t *testing.T, webURL string) {
	t.Helper()
	response, err := http.Post(webURL+"/api/flows/search", "application/json", strings.NewReader(`{"query":""}`))
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, string(body))
	var searchResult struct {
		Flows []struct {
			FlowID string `json:"flowId"`
		} `json:"flows"`
	}
	require.NoError(t, json.Unmarshal(body, &searchResult))
	require.Len(t, searchResult.Flows, 1)
	require.Equal(t, "flow-1", searchResult.Flows[0].FlowID)
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
