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
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerLogFolderFlag(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	directory := filepath.Join(t.TempDir(), "logs")
	cfg, err := parseConfig([]string{"--server-log-folder", directory}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogDirectory != directory {
		t.Fatalf("unexpected server log folder: %q", cfg.LogDirectory)
	}
}

func TestFlowRenderingDirectoryFlag(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	directory := t.TempDir()
	cfg, err := parseConfig([]string{"--flow-rendering-dir", directory}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FlowRenderingDirectory != directory {
		t.Fatalf("unexpected Flow rendering directory: %q", cfg.FlowRenderingDirectory)
	}
}

func TestConnectorConfigDirectoryFlagResolvesAbsolutePath(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	directory := filepath.Join("testdata", "connector-secrets")
	cfg, err := parseConfig([]string{"--connector-config-dir", directory}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnectorConfigDirectory != expected {
		t.Fatalf("connector config directory = %q, want %q", cfg.ConnectorConfigDirectory, expected)
	}
}

func TestConnectorConfigDirectoryDefaultsUnderDexHome(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	cfg, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(homeDirectory, ".dex", "connectors")
	if cfg.ConnectorConfigDirectory != expected {
		t.Fatalf("connector config directory = %q, want %q", cfg.ConnectorConfigDirectory, expected)
	}
}

func TestConnectorReleaseOverrideFlagResolvesRepeatableDirectories(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	firstDirectory := filepath.Join("testdata", "slack-release")
	secondDirectory := filepath.Join("testdata", "gmail-release")
	cfg, err := parseConfig([]string{
		"--connector-release-override", "slack=" + firstDirectory,
		"--connector-release-override", "gmail=" + secondDirectory,
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	firstAbsolute, _ := filepath.Abs(firstDirectory)
	secondAbsolute, _ := filepath.Abs(secondDirectory)
	if cfg.ConnectorReleaseOverrides["slack"] != firstAbsolute || cfg.ConnectorReleaseOverrides["gmail"] != secondAbsolute {
		t.Fatalf("Connector release overrides = %+v", cfg.ConnectorReleaseOverrides)
	}
}

func TestConnectorReleaseOverrideFlagRejectsInvalidAndDuplicateIDs(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	for _, arguments := range [][]string{
		{"--connector-release-override", "Slack=/tmp/release"},
		{"--connector-release-override", "slack"},
		{"--connector-release-override", "slack=/tmp/one", "--connector-release-override", "slack=/tmp/two"},
	} {
		if _, err := parseConfig(arguments, &bytes.Buffer{}); err == nil {
			t.Fatalf("parseConfig(%v) succeeded", arguments)
		}
	}
}

func TestHelpListsFlagsInOrder(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", filepath.Join(t.TempDir(), "missing"))
	var output bytes.Buffer
	_, err := parseConfig([]string{"-h"}, &output)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	text := output.String()
	last := -1
	for _, name := range flagOrder {
		idx := strings.Index(text, "-"+name)
		if idx < 0 {
			t.Fatalf("missing flag %s in help:\n%s", name, text)
		}
		if idx < last {
			t.Fatalf("flag %s is out of order in help:\n%s", name, text)
		}
		last = idx
	}
	for _, name := range []string{
		"-temporal-port",
		"-temporal-ui-port",
		"-temporal-address",
		"-temporal-db-filename",
		"-temporal-namespace",
		"-temporal-log-file",
		"-log-dir",
		"-external-temporal-address",
		"-external-temporal-namespace",
		"-sqlite-db-filename",
		"-verbose-engine-log",
		"-blob-store-dir",
	} {
		if strings.Contains(text, "  "+name+" ") || strings.Contains(text, "  "+name+"\n") {
			t.Fatalf("removed flag %s still documented:\n%s", name, text)
		}
	}
	if !strings.Contains(text, "open Dex Web after startup (default true)") {
		t.Fatalf("missing --open default in help:\n%s", text)
	}
}

func TestOpenBrowserDefaultsTrue(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	cfg, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OpenBrowser {
		t.Fatal("expected --open to default true")
	}
}

func TestOpenFalseDisablesBrowser(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	cfg, err := parseConfig([]string{"--open=false"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenBrowser {
		t.Fatal("expected --open=false to disable Dex Web")
	}
}

func TestOpenFalseSeparateTokenIsRejected(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	_, err := parseConfig([]string{"--open", "false"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --open false to fail")
	}
}

func TestRemovedTemporalPortFlagsAreRejected(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	for _, name := range []string{
		"temporal-port",
		"temporal-ui-port",
		"temporal-address",
		"temporal-db-filename",
		"temporal-namespace",
		"temporal-log-file",
		"log-dir",
	} {
		if _, err := parseConfig([]string{"--" + name, "1"}, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected unknown flag %s to fail", name)
		}
	}
}

func TestAttributeStoreConfigPathIsPassedToServerZD(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	path := filepath.Join(t.TempDir(), "attributes.yaml")
	if err := os.WriteFile(path, []byte("attributeStore: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseConfig([]string{"--attribute-store-config", "  " + path + "  "}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AttributeStoreConfigPath != path {
		t.Fatalf("Attribute Store path = %q", cfg.AttributeStoreConfigPath)
	}
}
