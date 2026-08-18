// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dev

import "testing"

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDirectory = t.TempDir()
	cfg.OpenBrowser = false
	return cfg
}
