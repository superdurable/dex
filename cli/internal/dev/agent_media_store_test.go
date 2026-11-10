// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// agentMediaStoreLinePattern is the documented format that agent CLI runtimes parse.
var agentMediaStoreLinePattern = regexp.MustCompile(`(?m)^Agent media store: (s3://\S+) endpoint=(http://\S+)$`)

func TestAgentMediaStoreReadyLineHasTheDocumentedFormat(t *testing.T) {
	line := agentMediaStoreReadyLine("http://127.0.0.1:8803")
	match := agentMediaStoreLinePattern.FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("ready line %q does not match %s", line, agentMediaStoreLinePattern)
	}
	if match[1] != "s3://dex-dev-media/agents" || match[2] != "http://127.0.0.1:8803" {
		t.Fatalf("ready line %q announces %q at %q", line, match[1], match[2])
	}
}

func TestAgentMediaStoreFlagsDefaultOnAtPort8803(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	cfg, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsAgentMediaStoreEnabled || cfg.AgentMediaStorePort != 8803 || cfg.isExplicit("agent-media-store-port") {
		t.Fatalf("defaults: enabled=%t port=%d explicit=%t", cfg.IsAgentMediaStoreEnabled, cfg.AgentMediaStorePort,
			cfg.isExplicit("agent-media-store-port"))
	}
	if address := cfg.ownedAddresses()["Agent media store"]; address != "127.0.0.1:8803" {
		t.Fatalf("agent media store address = %q", address)
	}

	cfg, err = parseConfig([]string{"--agent-media-store=false", "--agent-media-store-port", "9903"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IsAgentMediaStoreEnabled || cfg.AgentMediaStorePort != 9903 || !cfg.isExplicit("agent-media-store-port") {
		t.Fatalf("flags: enabled=%t port=%d explicit=%t", cfg.IsAgentMediaStoreEnabled, cfg.AgentMediaStorePort,
			cfg.isExplicit("agent-media-store-port"))
	}
	if _, isOwned := cfg.ownedAddresses()["Agent media store"]; isOwned {
		t.Fatal("a disabled agent media store owns no address")
	}
}

func TestAgentMediaStoreFlagsRejectBadPortAndCollision(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	if _, err := parseConfig([]string{"--agent-media-store-port", "0"}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "agent-media-store-port must be between 1 and 65535") {
		t.Fatalf("expected a port range error, got %v", err)
	}
	if _, err := parseConfig([]string{"--dex-port", "8803"}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "cannot both use 127.0.0.1:8803") {
		t.Fatalf("expected an address collision error, got %v", err)
	}
}

func TestZDDevArgumentsServeTheAgentMediaStoreOnlyWithAnAddress(t *testing.T) {
	cfg := &Config{}
	base := []string{"dev", "--data-dir", "/data", "--listen", "127.0.0.1:8801"}
	if actual := zdDevArguments(cfg, "/data", "127.0.0.1:8801", ""); !reflect.DeepEqual(actual, base) {
		t.Fatalf("arguments without the agent media store = %q, want %q", actual, base)
	}
	expected := append(base,
		"--application-s3-listen", "127.0.0.1:8803", "--application-s3-bucket", "dex-dev-media")
	if actual := zdDevArguments(cfg, "/data", "127.0.0.1:8801", "127.0.0.1:8803"); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("arguments with the agent media store = %q, want %q", actual, expected)
	}
}

func TestOlderZDBinaryTurnsTheAgentMediaStoreOffWithAReason(t *testing.T) {
	cfg := testConfig(t)
	cfg.IsAgentMediaStoreEnabled = true
	cfg.ZDBinary = writeFakeZDWithHelp(t, "Usage: dex-server-zd dev --data-dir <DATA_DIR>")
	reason := newSupervisor(cfg, &bytes.Buffer{}, &bytes.Buffer{}).disableAgentMediaStoreUnlessZDServesIt(context.Background())
	if cfg.IsAgentMediaStoreEnabled || !strings.Contains(reason, "does not serve it") {
		t.Fatalf("older dex-server-zd: enabled=%t reason=%q", cfg.IsAgentMediaStoreEnabled, reason)
	}

	cfg.IsAgentMediaStoreEnabled = true
	cfg.ZDBinary = writeFakeZDWithHelp(t, "      --application-s3-listen <APPLICATION_S3_LISTEN>")
	reason = newSupervisor(cfg, &bytes.Buffer{}, &bytes.Buffer{}).disableAgentMediaStoreUnlessZDServesIt(context.Background())
	if !cfg.IsAgentMediaStoreEnabled || reason != "" {
		t.Fatalf("current dex-server-zd: enabled=%t reason=%q", cfg.IsAgentMediaStoreEnabled, reason)
	}

	cfg.ZDBinary = filepath.Join(t.TempDir(), "missing-dex-server-zd")
	reason = newSupervisor(cfg, &bytes.Buffer{}, &bytes.Buffer{}).disableAgentMediaStoreUnlessZDServesIt(context.Background())
	if cfg.IsAgentMediaStoreEnabled || !strings.Contains(reason, "dev --help") {
		t.Fatalf("unrunnable dex-server-zd: enabled=%t reason=%q", cfg.IsAgentMediaStoreEnabled, reason)
	}
}

// writeFakeZDWithHelp returns a fake dex-server-zd that prints help for every command line.
func writeFakeZDWithHelp(t *testing.T, help string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dex-server-zd")
	script := "#!/bin/sh\ncat <<'EOF'\n" + help + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
