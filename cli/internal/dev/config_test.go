// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dev

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superdurable/dex/config"
)

func TestAttributeStoreConfigFlagLoadsStandardDexYAML(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "attribute-store.yaml")
	contents := []byte(`attributeStore:
  stores:
    entityStore:
      type: postgres
      dsn: postgres://entity_store:entity_store@localhost:55432/entity_store?sslmode=disable
      tableName: public.user_profiles
  syncBatchSize: 25
`)
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cliConfig, err := parseConfig([]string{"--attribute-store-config", configPath}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	attributeStore, err := cliConfig.loadAttributeStoreConfig()
	if err != nil {
		t.Fatal(err)
	}
	if attributeStore.SyncBatchSize != 25 {
		t.Fatalf("unexpected sync batch size: %d", attributeStore.SyncBatchSize)
	}
	store := attributeStore.Stores["entityStore"]
	if store.Type != config.AttributeStoreTypePostgres ||
		store.DSN != "postgres://entity_store:entity_store@localhost:55432/entity_store?sslmode=disable" ||
		store.TableName != "public.user_profiles" {
		t.Fatalf("unexpected store config: %+v", store)
	}
}

func TestAttributeStoreConfigRejectsUnknownFields(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "attribute-store.yaml")
	if err := os.WriteFile(configPath, []byte("attributeStore:\n  unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliConfig := &Config{AttributeStoreConfigPath: configPath}
	if _, err := cliConfig.loadAttributeStoreConfig(); err == nil {
		t.Fatal("expected unknown Attribute Store field to fail")
	}
}

func TestAttributeStoreConfigRequiresStore(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "attribute-store.yaml")
	if err := os.WriteFile(configPath, []byte("attributeStore: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliConfig := &Config{AttributeStoreConfigPath: configPath}
	if _, err := cliConfig.loadAttributeStoreConfig(); err == nil {
		t.Fatal("expected empty Attribute Store config to fail")
	}
}

func TestServerLogFolderFlag(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	cfg, err := parseConfig([]string{"--server-log-folder", directory}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogDirectory != directory {
		t.Fatalf("unexpected server log folder: %q", cfg.LogDirectory)
	}
}

func TestVerboseEngineLogFlag(t *testing.T) {
	cfg, err := parseConfig([]string{"--verbose-engine-log"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VerboseEngineLog {
		t.Fatal("expected verbose engine logs to be enabled")
	}
}

func TestVerboseEngineLogRejectedWithExternalAddress(t *testing.T) {
	_, err := parseConfig([]string{
		"--external-temporal-address", "127.0.0.1:7233",
		"--verbose-engine-log",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --verbose-engine-log to fail with --external-temporal-address")
	}
}

func TestSQLiteDBFilenameRejectedWithExternalAddress(t *testing.T) {
	_, err := parseConfig([]string{
		"--external-temporal-address", "127.0.0.1:7233",
		"--sqlite-db-filename", "dex.sqlite.db",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --sqlite-db-filename to fail with --external-temporal-address")
	}
}

func TestExternalTemporalNamespaceRequiresAddress(t *testing.T) {
	_, err := parseConfig([]string{"--external-temporal-namespace", "custom"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --external-temporal-namespace to fail without --external-temporal-address")
	}
}

func TestHelpListsFlagsWithTemporalLast(t *testing.T) {
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
	cfg, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OpenBrowser {
		t.Fatal("expected --open to default true")
	}
}

func TestOpenFalseDisablesBrowser(t *testing.T) {
	cfg, err := parseConfig([]string{"--open=false"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenBrowser {
		t.Fatal("expected --open=false to disable Dex Web")
	}
}

func TestOpenFalseSeparateTokenIsRejected(t *testing.T) {
	_, err := parseConfig([]string{"--open", "false"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --open false to fail")
	}
}

func TestRemovedTemporalPortFlagsAreRejected(t *testing.T) {
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

func TestWriteTemporalStartupRecordIncludesPortsAndDatabase(t *testing.T) {
	cfg := testConfig(t)
	cfg.TemporalPort = 7234
	cfg.TemporalUIPort = 8234
	cfg.SQLiteDBFilename = filepath.Join(cfg.StateDirectory, "dev", "7234", localSQLiteFileName)
	var output bytes.Buffer
	if err := cfg.writeTemporalStartupRecord(&output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "127.0.0.1:7234") {
		t.Fatalf("missing Temporal gRPC address: %s", text)
	}
	if !strings.Contains(text, "http://127.0.0.1:8234") {
		t.Fatalf("missing Temporal Web address: %s", text)
	}
	if !strings.Contains(text, filepath.Join(cfg.StateDirectory, "dev", "7234")) {
		t.Fatalf("missing Temporal DB directory: %s", text)
	}
}
