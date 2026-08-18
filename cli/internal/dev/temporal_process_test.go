// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEphemeralServerLogDirIsRemovedOnClose(t *testing.T) {
	cfg := testConfig(t)
	logDir, err := openServerLogDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !logDir.isEphemeral {
		t.Fatal("expected ephemeral server log directory")
	}
	if cfg.LogDirectory != logDir.directory {
		t.Fatalf("config directory %q does not match %q", cfg.LogDirectory, logDir.directory)
	}
	if _, err := os.Stat(cfg.temporalEngineLogPath()); err != nil {
		t.Fatal(err)
	}
	if err := logDir.Close(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logDir.directory); !os.IsNotExist(err) {
		t.Fatalf("ephemeral server log directory was kept: %v", err)
	}
}

func TestExplicitServerLogDirIsKept(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	cfg := testConfig(t)
	cfg.LogDirectory = directory
	logDir, err := openServerLogDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if logDir.isEphemeral {
		t.Fatal("explicit server log directory must not be ephemeral")
	}
	if err := logDir.Close(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("explicit server log directory was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, temporalEngineLogFileName)); err != nil {
		t.Fatalf("workflow engine log file missing: %v", err)
	}
}

func TestExternalTemporalSkipsEngineLogFile(t *testing.T) {
	cfg := testConfig(t)
	cfg.ExternalTemporalAddress = "127.0.0.1:7233"
	logDir, err := openServerLogDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if logDir.engineLog != nil {
		t.Fatal("external Temporal must not open a workflow engine log file")
	}
	if err := logDir.Close(true); err != nil {
		t.Fatal(err)
	}
}
