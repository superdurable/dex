// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDirectory = t.TempDir()
	cfg.OpenBrowser = false
	// Test servers skip fsync; a config file also works with the released bundled dex-server-zd.
	cfg.ZDConfigPath = filepath.Join(t.TempDir(), "zd-overrides.yaml")
	if err := os.WriteFile(cfg.ZDConfigPath, []byte("dev:\n  durabilityMode: processCrashSafe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}
