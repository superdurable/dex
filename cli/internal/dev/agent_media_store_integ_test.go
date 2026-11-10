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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDevAgentMediaStoreKeepsObjectsAcrossRestartsApartFromServerBucket(t *testing.T) {
	stateDirectory := t.TempDir()
	mediaPort := freePorts(t, 1)[0]
	cfg := agentMediaStoreTestConfig(t, stateDirectory, mediaPort)
	_, output, stop := startTestDev(t, cfg)

	match := agentMediaStoreLinePattern.FindStringSubmatch(output.String())
	require.NotNil(t, match, "ready output announces the agent media store:\n%s", output.String())
	require.Equal(t, "s3://dex-dev-media/agents", match[1])
	endpoint := "http://127.0.0.1:" + strconv.Itoa(mediaPort)
	require.Equal(t, endpoint, match[2])

	image := []byte("\x89PNG\r\n\x1a\nagent image bytes")
	digest := sha256.Sum256(image)
	objectURL := endpoint + "/dex-dev-media/agents/sha256/" + hex.EncodeToString(digest[:])
	status, _ := sendAgentMediaStoreRequest(t, http.MethodPut, objectURL, image)
	require.Equal(t, http.StatusOK, status)
	status, body := sendAgentMediaStoreRequest(t, http.MethodGet, objectURL, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, image, body)
	status, _ = sendAgentMediaStoreRequest(t, http.MethodPut, endpoint+"/dex-dev/written-by-an-application", image)
	require.Equal(t, http.StatusNotFound, status, "the server's own bucket is not on the agent media store endpoint")
	require.NoError(t, stop())

	_, restartedOutput, stopRestarted := startTestDev(t, agentMediaStoreTestConfig(t, stateDirectory, mediaPort))
	require.Contains(t, restartedOutput.String(), agentMediaStoreReadyLine(endpoint))
	status, body = sendAgentMediaStoreRequest(t, http.MethodGet, objectURL, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, image, body, "the object survives a restart on the same state directory")
	require.NoError(t, stopRestarted())
}

func TestDevAgentMediaStoreFlagTurnsItOff(t *testing.T) {
	mediaPort := freePorts(t, 1)[0]
	cfg := agentMediaStoreTestConfig(t, t.TempDir(), mediaPort)
	cfg.IsAgentMediaStoreEnabled = false
	_, output, stop := startTestDev(t, cfg)

	require.NotContains(t, output.String(), "Agent media store")
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mediaPort)), time.Second)
	if err == nil {
		require.NoError(t, connection.Close())
		t.Fatalf("port %d accepts connections with --agent-media-store=false", mediaPort)
	}
	_, err = os.Stat(filepath.Join(cfg.zdDataDirectory(), "application-s3"))
	require.True(t, os.IsNotExist(err), "no application bucket data directory: %v", err)
	require.NoError(t, stop())
}

func agentMediaStoreTestConfig(t *testing.T, stateDirectory string, mediaPort int) *Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.StateDirectory = stateDirectory
	// A fixed data directory keeps state across restarts that startTestDev runs on new Dex ports.
	cfg.ZDDataDirectory = filepath.Join(stateDirectory, "zd")
	cfg.explicitLocalFlags["zd-data-dir"] = true
	cfg.IsAgentMediaStoreEnabled = true
	cfg.AgentMediaStorePort = mediaPort
	cfg.explicitLocalFlags["agent-media-store-port"] = true
	return cfg
}

// sendAgentMediaStoreRequest sends an unsigned request, which the loopback store accepts, and returns its status and body.
func sendAgentMediaStoreRequest(t *testing.T, method string, url string, body []byte) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewReader(body))
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "image/png")
	}
	response, err := newLoopbackHTTPClient().Do(request)
	require.NoError(t, err)
	responseBody, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.NoError(t, response.Body.Close())
	return response.StatusCode, responseBody
}
