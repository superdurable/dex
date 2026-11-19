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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// defaultServerLogFileDirectory is fixed, not os.TempDir(), so the log is easy to find on macOS too.
	defaultServerLogFileDirectory = "/tmp/dexcli"
	// defaultServerLogLevel shows only server warnings and errors in the terminal.
	defaultServerLogLevel = "warn"
	// serverLogTailLines is how many recent server lines a startup failure prints.
	serverLogTailLines = 20
	// maxServerLogLineBytes flushes an unterminated line so one huge write cannot grow memory without bound.
	maxServerLogLineBytes = 64 << 10
)

// serverLogLevels lists the --server-log-level values from the most to the least verbose.
var serverLogLevels = []string{"trace", "debug", "info", "warn", "error"}

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// serverLogRouter copies every server output line to a log file and shows selected lines in the terminal.
// Lines that are not tracing log lines, such as panics, are always shown.
type serverLogRouter struct {
	mu sync.Mutex
	// logFile is nil when dex-server-zd writes its own log file through --log-dir.
	logFile              io.WriteCloser
	terminal             io.Writer
	minimumTerminalLevel int
	pendingLine          []byte
	recentLines          []string
	nextRecentLine       int
	writeErr             error
}

func newServerLogRouter(logFile io.WriteCloser, terminal io.Writer, minimumTerminalLevel string) *serverLogRouter {
	if terminal == nil {
		panic("server log terminal writer must not be nil")
	}
	return &serverLogRouter{
		logFile:              logFile,
		terminal:             terminal,
		minimumTerminalLevel: serverLogLevelRank(minimumTerminalLevel),
	}
}

// Write never fails, so the server keeps running when the log file or terminal cannot be written; Close reports it.
func (router *serverLogRouter) Write(output []byte) (int, error) {
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.logFile != nil {
		if _, err := router.logFile.Write(output); err != nil && router.writeErr == nil {
			router.writeErr = fmt.Errorf("write server log file: %w", err)
		}
	}
	router.pendingLine = append(router.pendingLine, output...)
	for {
		lineEnd := bytes.IndexByte(router.pendingLine, '\n')
		if lineEnd < 0 {
			break
		}
		router.routeLine(string(router.pendingLine[:lineEnd]))
		router.pendingLine = router.pendingLine[lineEnd+1:]
	}
	if len(router.pendingLine) > maxServerLogLineBytes {
		router.routeLine(string(router.pendingLine))
		router.pendingLine = nil
	}
	router.pendingLine = append([]byte(nil), router.pendingLine...)
	return len(output), nil
}

// Close routes an unterminated last line and closes the log file; call it after the server process exits.
func (router *serverLogRouter) Close() error {
	router.mu.Lock()
	defer router.mu.Unlock()
	router.routePendingLine()
	err := router.writeErr
	if router.logFile != nil {
		if closeErr := router.logFile.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close server log file: %w", closeErr))
		}
		router.logFile = nil
	}
	return err
}

// RecentLines returns up to serverLogTailLines of the latest server lines, oldest first.
// It first routes an unterminated last line, such as a crash message without a newline.
func (router *serverLogRouter) RecentLines() []string {
	router.mu.Lock()
	defer router.mu.Unlock()
	router.routePendingLine()
	if len(router.recentLines) < serverLogTailLines {
		return append([]string(nil), router.recentLines...)
	}
	return append(append([]string(nil), router.recentLines[router.nextRecentLine:]...), router.recentLines[:router.nextRecentLine]...)
}

func (router *serverLogRouter) routePendingLine() {
	if len(router.pendingLine) > 0 {
		router.routeLine(string(router.pendingLine))
		router.pendingLine = nil
	}
}

func (router *serverLogRouter) routeLine(line string) {
	router.rememberLine(line)
	if !router.isShownInTerminal(line) {
		return
	}
	if _, err := fmt.Fprintln(router.terminal, line); err != nil && router.writeErr == nil {
		router.writeErr = fmt.Errorf("write server log line to the terminal: %w", err)
	}
}

func (router *serverLogRouter) rememberLine(line string) {
	if len(router.recentLines) < serverLogTailLines {
		router.recentLines = append(router.recentLines, line)
		return
	}
	router.recentLines[router.nextRecentLine] = line
	router.nextRecentLine = (router.nextRecentLine + 1) % serverLogTailLines
}

func (router *serverLogRouter) isShownInTerminal(line string) bool {
	level, isLogLine := parseServerLogLevel(line)
	return !isLogLine || serverLogLevelRank(level) >= router.minimumTerminalLevel
}

// parseServerLogLevel returns the lower-case level of a tracing line "<RFC 3339 time> <LEVEL> ...".
func parseServerLogLevel(line string) (string, bool) {
	fields := strings.Fields(ansiEscapePattern.ReplaceAllString(line, ""))
	if len(fields) < 2 {
		return "", false
	}
	if _, err := time.Parse(time.RFC3339Nano, fields[0]); err != nil {
		return "", false
	}
	level := strings.ToLower(fields[1])
	if serverLogLevelRank(level) < 0 {
		return "", false
	}
	return level, true
}

// serverLogLevelRank returns the position of level in serverLogLevels, or -1 for an unknown level.
func serverLogLevelRank(level string) int {
	for rank, candidate := range serverLogLevels {
		if candidate == level {
			return rank
		}
	}
	return -1
}

// createServerLogFile creates <directory>/dev-<dexPort>.log, truncating the log of an earlier start.
// It returns the open file and its path.
func createServerLogFile(directory string, dexPort int) (*os.File, string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("create server log directory %s: %w", directory, err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, "", fmt.Errorf("inspect server log directory %s: %w", directory, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("server log directory %s is not a directory", directory)
	}
	// Chmod succeeds only for the owner, so another user's shared temporary directory is refused.
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("restrict server log directory %s: %w", directory, err)
	}
	path := filepath.Join(directory, fmt.Sprintf("dev-%d.log", dexPort))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("create server log file %s: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, "", errors.Join(fmt.Errorf("restrict server log file %s: %w", path, err), file.Close())
	}
	return file, path, nil
}
