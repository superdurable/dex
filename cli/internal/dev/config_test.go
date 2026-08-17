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

func TestTemporalLogFileFlag(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "logs", "temporal.log")
	cfg, err := parseConfig([]string{"--temporal-log-file", logFile}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TemporalLogFile != logFile {
		t.Fatalf("unexpected Temporal log file: %q", cfg.TemporalLogFile)
	}
}

func TestTemporalLogFileRejectedWithExternalAddress(t *testing.T) {
	_, err := parseConfig([]string{
		"--temporal-address", "127.0.0.1:7233",
		"--temporal-log-file", "temporal.log",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected --temporal-log-file to fail with --temporal-address")
	}
}

func TestWriteTemporalStartupRecordIncludesPortsAndDatabase(t *testing.T) {
	cfg := testConfig(t)
	cfg.TemporalPort = 7234
	cfg.TemporalUIPort = 8234
	cfg.TemporalDBFilename = filepath.Join(cfg.StateDirectory, "dev", "7234", localSQLiteFileName)
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
