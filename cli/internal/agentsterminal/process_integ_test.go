// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//go:build integration

package agentsterminal

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
)

// --describe needs no terminal, network, or environment, and prints one
// JSON line and nothing else.
func TestDescribeWithAnEmptyEnvironment(t *testing.T) {
	command := exec.Command(buildDexcli(t), "agents-tui", "--describe")
	command.Env = []string{}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	require.NoError(t, command.Run())
	require.Empty(t, stderr.String())
	require.Equal(t, 1, bytes.Count(stdout.Bytes(), []byte("\n")))
	document, err := launchcontract.CheckDescribeOutput(stdout.Bytes())
	require.NoError(t, err)
	require.Contains(t, document.Contracts, "1")
	require.Contains(t, document.Contracts["1"].Modes, launchcontract.ModeChat)
}

// SIGTSTP releases the terminal (leaves the alternate screen, shows the
// cursor) and the chat takes it back in the foreground; typing still works.
// The PTY session leader's process group is orphaned, so the stop itself is
// discarded here; the PTY e2e covers shell job control.
func TestSuspendReleasesAndRetakesTheTerminal(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	require.NoError(t, err)
	dexcli := buildDexcli(t)
	stateDirectory := t.TempDir()
	data, err := os.ReadFile(filepath.Join(transcriptsDirectory, "chat_ready_then_exit.json"))
	require.NoError(t, err)
	var transcript map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(strings.ReplaceAll(string(data),
		"127.0.0.1:8801", "127.0.0.1:"+closedPort(t)), "127.0.0.1:8802", "127.0.0.1:"+closedPort(t))), &transcript))
	transcript["steps"] = append(transcript["steps"].([]any), map[string]any{"close": "tui"})
	encoded, err := json.Marshal(transcript)
	require.NoError(t, err)
	transcriptPath := filepath.Join(stateDirectory, "transcript.json")
	require.NoError(t, os.WriteFile(transcriptPath, encoded, 0o600))
	recordPath := filepath.Join(stateDirectory, "record.jsonl")
	screenPath := filepath.Join(stateDirectory, "screen.log")
	host := exec.Command(pythonPath, contractHostScript, "launcher",
		"--transcript", transcriptPath, "--pty", "100x30", "--screen-log", screenPath, "--record", recordPath,
		"--step-timeout", "60", "--pty-input-from-stdin", raceEnvironmentArgument(filepath.Join(stateDirectory, "race")), "--", dexcli, "agents-tui")
	host.Env = []string{"HOME=" + stateDirectory, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH")}
	keys, err := host.StdinPipe()
	require.NoError(t, err)
	var hostStderr bytes.Buffer
	host.Stderr = &hostStderr
	require.NoError(t, host.Start())
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Wait() }()
	require.Eventually(t, func() bool { return countLines(recordPath) >= 3 }, 10*time.Second, 20*time.Millisecond)
	dexcliPID := childProcessID(t, host.Process.Pid)
	screenSize := func() int {
		screen, _ := os.ReadFile(screenPath)
		return len(screen)
	}
	before := screenSize()
	require.NoError(t, syscall.Kill(dexcliPID, syscall.SIGTSTP))
	require.Eventually(t, func() bool {
		screen, _ := os.ReadFile(screenPath)
		after := string(screen[before:])
		leave := strings.Index(after, "\x1b[?1049l")
		released := after[max(leave, 0):]
		return leave >= 0 && strings.Contains(released, "\x1b[?25h") && strings.Contains(released, "\x1b[?2004l") &&
			strings.Contains(released, "\x1b[?1002l") && strings.Contains(released, "\x1b[?1049h")
	}, 10*time.Second, 50*time.Millisecond, "dexcli left the alternate screen, showed the cursor, and came back")
	_, err = keys.Write([]byte("hello"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		screen, _ := os.ReadFile(screenPath)
		return strings.Contains(terminalEscapes.ReplaceAllString(string(screen), ""), "hello")
	}, 10*time.Second, 50*time.Millisecond, "typing works after the resume")
	_, err = keys.Write([]byte("\x15\x04"))
	require.NoError(t, err)
	select {
	case err := <-hostDone:
		require.NoError(t, err, "host stderr: %s", hostStderr.String())
		requireNoRaceReports(t, filepath.Join(stateDirectory, "race"))
	case <-time.After(20 * time.Second):
		require.NoError(t, host.Process.Kill())
		t.Fatal("dexcli did not exit")
	}
}

// childProcessID returns the one child of parentPID, a process this test
// started through the host.
func childProcessID(t *testing.T, parentPID int) int {
	t.Helper()
	var output []byte
	require.Eventually(t, func() bool {
		var err error
		output, err = exec.Command("pgrep", "-P", strconv.Itoa(parentPID)).Output()
		return err == nil && len(bytes.Fields(output)) == 1
	}, 5*time.Second, 20*time.Millisecond)
	pid, err := strconv.Atoi(string(bytes.TrimSpace(output)))
	require.NoError(t, err)
	return pid
}
