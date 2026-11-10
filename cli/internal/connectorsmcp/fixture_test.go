// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package connectorsmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// fixtureConnection is one connections.json record as Dex Web writes it.
type fixtureConnection struct {
	ConnectorID    string         `json:"connectorId"`
	ModulePath     string         `json:"modulePath"`
	ModuleVersion  string         `json:"moduleVersion"`
	Provider       string         `json:"provider"`
	ConnectionName string         `json:"connectionName"`
	Configuration  map[string]any `json:"configuration"`
	Credentials    map[string]any `json:"credentials"`
}

const connectorModulePrefix = "github.com/superdurable/dex-connectors-library/connectors/"

// allConnectorFixtures returns one connection per compiled connector; slackEndpoint receives Slack calls.
func allConnectorFixtures(slackEndpoint string) []fixtureConnection {
	return []fixtureConnection{
		slackFixture("support-slack", slackEndpoint),
		{
			ConnectorID: "gmail", ModulePath: connectorModulePrefix + "google/gmail/go", ModuleVersion: "v0.25.2",
			Provider: "google", ConnectionName: "inbox", Configuration: map[string]any{},
			Credentials: map[string]any{
				"auth_method": "google-oauth", "oauth_client_id": "client", "oauth_client_secret": "secret",
				"access_token": "access", "refresh_token": "refresh", "primary_email": "agent@example.com",
			},
		},
		{
			ConnectorID: "hacker-news-daily", ModulePath: connectorModulePrefix + "hackernews/go", ModuleVersion: "v0.23.0",
			Provider: "hacker-news", ConnectionName: "news", Configuration: map[string]any{}, Credentials: map[string]any{},
		},
		{
			ConnectorID: "google-youtube", ModulePath: connectorModulePrefix + "google/youtube/go", ModuleVersion: "v0.3.0",
			Provider: "google", ConnectionName: "videos", Configuration: map[string]any{},
			Credentials: map[string]any{
				"auth_method": "google-oauth-readonly", "oauth_client_id": "client", "oauth_client_secret": "secret",
				"access_token": "access", "refresh_token": "refresh",
			},
		},
		{
			ConnectorID: "reddit", ModulePath: connectorModulePrefix + "reddit/go", ModuleVersion: "v0.1.0",
			Provider: "reddit", ConnectionName: "posts", Configuration: map[string]any{"redditUsername": "dex-agent"},
			Credentials: map[string]any{"oauth_client_id": "client", "oauth_client_secret": "secret", "access_token": "access", "refresh_token": "refresh"},
		},
		{
			ConnectorID: "spotify", ModulePath: connectorModulePrefix + "spotify/go", ModuleVersion: "v0.24.0",
			Provider: "spotify", ConnectionName: "music", Configuration: map[string]any{},
			Credentials: map[string]any{"oauth_client_id": "client", "oauth_client_secret": "secret", "access_token": "access", "refresh_token": "refresh"},
		},
	}
}

func slackFixture(connectionName string, endpoint string) fixtureConnection {
	configuration := map[string]any{}
	if endpoint != "" {
		configuration["endpoint"] = endpoint
	}
	return fixtureConnection{
		ConnectorID: "slack", ModulePath: connectorModulePrefix + "slack/go", ModuleVersion: "v1.3.0",
		Provider: "slack", ConnectionName: connectionName, Configuration: configuration,
		Credentials: map[string]any{"bot_token": "xoxb-test-bot", "user_token": "xoxp-test-user"},
	}
}

// writeConnectionsFile writes connections.json with the owner-only mode the SDK loader requires.
func writeConnectionsFile(t *testing.T, directory string, connections []fixtureConnection) {
	t.Helper()
	contents, err := json.Marshal(map[string]any{
		"schemaVersion": "connectors.dex.dev/local-connections/v1alpha1",
		"connections":   connections,
	})
	require.NoError(t, err)
	path := filepath.Join(directory, connectionsFileName)
	temporaryPath := path + ".tmp"
	require.NoError(t, os.WriteFile(temporaryPath, contents, 0o600))
	require.NoError(t, os.Chmod(temporaryPath, 0o600))
	require.NoError(t, os.Rename(temporaryPath, path))
}

// testHarness serves a Connectors MCP Server over loopback HTTP for one test.
type testHarness struct {
	configDirectory string
	stateDirectory  string
	server          *Server
	endpoint        string
	logs            *synchronizedBuffer
}

func newTestHarness(t *testing.T, connections []fixtureConnection, selectors []ConnectionSelector, allowedHosts ...string) *testHarness {
	t.Helper()
	configDirectory := t.TempDir()
	if connections != nil {
		writeConnectionsFile(t, configDirectory, connections)
	}
	logs := &synchronizedBuffer{}
	server, err := NewServer(&Config{
		ConnectorConfigDirectory: configDirectory,
		StateDirectory:           t.TempDir(),
		Connections:              selectors,
		AllowedHosts:             allowedHosts,
		Version:                  "test",
	}, slog.New(slog.NewTextHandler(logs, nil)))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(MountPath, server.Handler())
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return &testHarness{
		configDirectory: configDirectory,
		stateDirectory:  server.source.calls.store.directory,
		server:          server,
		endpoint:        httpServer.URL + MountPath,
		logs:            logs,
	}
}

func (harness *testHarness) connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "connectors-mcp-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: harness.endpoint, MaxRetries: -1}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session
}

func (harness *testHarness) listTools(t *testing.T) map[string]*mcp.Tool {
	t.Helper()
	result, err := harness.connect(t).ListTools(context.Background(), nil)
	require.NoError(t, err)
	tools := make(map[string]*mcp.Tool, len(result.Tools))
	for _, tool := range result.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func callWithKey(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any, key string) (*mcp.CallToolResult, error) {
	t.Helper()
	params := &mcp.CallToolParams{Name: name, Arguments: arguments}
	if key != "" {
		params.Meta = mcp.Meta{metaKeyIdempotencyKey: key}
	}
	return session.CallTool(context.Background(), params)
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, result.Content, 1)
	text, isText := result.Content[0].(*mcp.TextContent)
	require.True(t, isText)
	return text.Text
}

// synchronizedBuffer collects log output written from request goroutines.
type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(contents []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(contents)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

var _ io.Writer = (*synchronizedBuffer)(nil)
