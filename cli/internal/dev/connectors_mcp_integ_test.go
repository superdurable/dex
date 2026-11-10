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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const slackConnectionsFile = `{
  "schemaVersion": "connectors.dex.dev/local-connections/v1alpha1",
  "connections": [{
    "connectorId": "slack",
    "modulePath": "github.com/superdurable/dex-connectors-library/connectors/slack/go",
    "moduleVersion": "v1.3.0",
    "provider": "slack",
    "connectionName": "support-slack",
    "configuration": {},
    "credentials": {"bot_token": "xoxb-test-bot", "user_token": "xoxp-test-user"}
  }]
}`

func TestDevMountsConnectorsMCPBesideDexWeb(t *testing.T) {
	ports := freePorts(t, 2)
	cfg := testConfig(t)
	cfg.DexPort = ports[0]
	cfg.WebPort = ports[1]
	cfg.explicitLocalFlags["dex-port"] = true
	cfg.explicitLocalFlags["web-port"] = true
	if err := os.WriteFile(filepath.Join(cfg.ConnectorConfigDirectory, "connections.json"), []byte(slackConnectionsFile), 0o600); err != nil {
		t.Fatal(err)
	}
	webURL := "http://127.0.0.1:" + strconv.Itoa(ports[1])
	stateDirectory := cfg.StateDirectory
	output := &synchronizedBuffer{}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runFinished := make(chan error, 1)
	go func() {
		runFinished <- newSupervisor(cfg, output, output).Run(runCtx)
	}()
	waitForHealthyWeb(t, webURL+"/healthz", runFinished)
	waitForReadyStack(t, output, runFinished)
	if !strings.Contains(output.String(), "Connectors MCP: "+webURL+"/mcp") {
		t.Fatalf("ready output does not name the MCP endpoint: %s", output.String())
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "dev-mcp-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: webURL + "/mcp", MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(context.Background(), nil)
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "slack_post_thread_reply") {
		t.Fatalf("tools/list = %v, want slack_post_thread_reply", names)
	}

	cancel()
	if err := <-runFinished; err != nil {
		t.Fatalf("local stack stopped with error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDirectory, "connectors-mcp", "calls")); err != nil {
		t.Fatalf("MCP state directory was not created under the Dex state directory: %v", err)
	}
}
