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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closableBuffer records what a log file receives and whether it was closed.
type closableBuffer struct {
	bytes.Buffer
	isClosed bool
}

func (buffer *closableBuffer) Close() error {
	buffer.isClosed = true
	return nil
}

const serverOutputFixture = "2026-11-19T17:22:14.680761Z  INFO dexzd_server::embedded_s3: embedded S3 emulator serving\n" +
	"2026-11-19T17:22:14.804630Z  INFO s3local_server::controls: S3 request operation=PutObject\n" +
	"2026-11-19T17:22:15.000000Z  WARN dexzd_engine::lease: lease renewal was slow duration_ms=900\n" +
	"2026-11-19T17:22:16.000000Z DEBUG dexzd_engine::lease: renewed\n" +
	"2026-11-19T17:22:17.000000Z ERROR dexzd_server: commit failed\n" +
	"thread 'main' panicked at crates/dexzd-server/src/main.rs:10:5:\n" +
	"\x1b[2m2026-11-19T17:22:18.000000Z\x1b[0m \x1b[33m WARN\x1b[0m colored warning\n" +
	"Error: cannot bind 127.0.0.1:8801"

func TestServerLogRouterShowsWarningsErrorsAndNonLogLines(t *testing.T) {
	logFile := &closableBuffer{}
	terminal := &bytes.Buffer{}
	router := newServerLogRouter(logFile, terminal, defaultServerLogLevel)
	// Split writes across line boundaries, as a pipe may deliver them.
	for _, chunk := range []string{serverOutputFixture[:50], serverOutputFixture[50:300], serverOutputFixture[300:]} {
		if _, err := router.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	if logFile.String() != serverOutputFixture || !logFile.isClosed {
		t.Fatalf("log file = %q, closed %t", logFile.String(), logFile.isClosed)
	}
	expected := "2026-11-19T17:22:15.000000Z  WARN dexzd_engine::lease: lease renewal was slow duration_ms=900\n" +
		"2026-11-19T17:22:17.000000Z ERROR dexzd_server: commit failed\n" +
		"thread 'main' panicked at crates/dexzd-server/src/main.rs:10:5:\n" +
		"\x1b[2m2026-11-19T17:22:18.000000Z\x1b[0m \x1b[33m WARN\x1b[0m colored warning\n" +
		"Error: cannot bind 127.0.0.1:8801\n"
	if terminal.String() != expected {
		t.Fatalf("terminal =\n%q\nwant\n%q", terminal.String(), expected)
	}
}

func TestServerLogRouterInfoLevelShowsEveryLogLine(t *testing.T) {
	terminal := &bytes.Buffer{}
	router := newServerLogRouter(nil, terminal, "info")
	if _, err := router.Write([]byte(serverOutputFixture + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := router.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(terminal.String(), "DEBUG") || strings.Count(terminal.String(), "\n") != 7 {
		t.Fatalf("terminal =\n%s", terminal.String())
	}
}

func TestServerLogRouterKeepsTheRecentLines(t *testing.T) {
	router := newServerLogRouter(nil, &bytes.Buffer{}, defaultServerLogLevel)
	for index := 0; index < serverLogTailLines+5; index++ {
		if _, err := fmt.Fprintf(router, "2026-11-19T17:22:14Z  INFO line %d\n", index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := router.Write([]byte("unterminated crash message")); err != nil {
		t.Fatal(err)
	}
	recentLines := router.RecentLines()
	if len(recentLines) != serverLogTailLines || recentLines[0] != "2026-11-19T17:22:14Z  INFO line 6" ||
		recentLines[len(recentLines)-1] != "unterminated crash message" {
		t.Fatalf("recent lines = %q", recentLines)
	}
}

func TestCreateServerLogFileTruncatesWithPrivatePermissions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "dexcli")
	for _, content := range []string{"first start with a longer log\n", "second\n"} {
		file, path, err := createServerLogFile(directory, 18801)
		if err != nil {
			t.Fatal(err)
		}
		if path != filepath.Join(directory, "dev-18801.log") {
			t.Fatalf("log path = %s", path)
		}
		if _, err := file.WriteString(content); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(directory, "dev-18801.log"))
	if err != nil || string(content) != "second\n" {
		t.Fatalf("log content = %q, %v", content, err)
	}
	fileInfo, err := os.Stat(filepath.Join(directory, "dev-18801.log"))
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode = %v, %v", fileInfo, err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil || directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("log directory mode = %v, %v", directoryInfo, err)
	}
}

func TestCreateServerLogFileRefusesASymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(target, filepath.Join(directory, "dev-18801.log")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createServerLogFile(directory, 18801); err == nil {
		t.Fatal("a symlinked log file was opened")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("the symlink target was created: %v", err)
	}
}

func TestCreateServerLogFileFallsBackWhenTheDirectoryIsUnusable(t *testing.T) {
	unusableDirectory := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(unusableDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	fallbackDirectory := filepath.Join(t.TempDir(), "dexcli")
	file, path, err := createServerLogFileWithFallback(unusableDirectory, fallbackDirectory, 18801)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(fallbackDirectory, "dev-18801.log") {
		t.Fatalf("log path = %s", path)
	}
	if _, _, err := createServerLogFileWithFallback(unusableDirectory, "", 18801); err == nil {
		t.Fatal("an unusable directory without a fallback did not fail")
	}
}

func TestServerLogLevelFlag(t *testing.T) {
	t.Setenv("DEX_SERVER_ZD_BINARY", writeFakeExecutable(t, t.TempDir(), "dex-server-zd"))
	config, err := parseConfig([]string{"--server-log-level", "INFO"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if config.ServerLogLevel != "info" {
		t.Fatalf("server log level = %q", config.ServerLogLevel)
	}
	defaults, err := parseConfig(nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.ServerLogLevel != "warn" || defaults.ServerLogFileDirectory != "/tmp/dexcli" {
		t.Fatalf("defaults = %q in %q", defaults.ServerLogLevel, defaults.ServerLogFileDirectory)
	}
	if _, err := parseConfig([]string{"--server-log-level", "verbose"}, &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "server log level must be one of trace, debug, info, warn, error") {
		t.Fatalf("invalid level error = %v", err)
	}
}
