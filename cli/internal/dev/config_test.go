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
