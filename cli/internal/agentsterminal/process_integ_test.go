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
	"fmt"
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

// SIGTSTP under a job-control shell releases the terminal once and stops
// dexcli; fg retakes the terminal, and typing works. Two rounds check that
// the second stop works too.
func TestSuspendUnderAJobControlShell(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	require.NoError(t, err)
	bashPath, err := exec.LookPath("bash")
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
	// The host appends --channel-fd N, which become the shell's $2 and $3.
	host := exec.Command(pythonPath, contractHostScript, "launcher",
		"--transcript", transcriptPath, "--pty", "100x30", "--screen-log", screenPath, "--record", recordPath,
		"--step-timeout", "60", "--pty-input-from-stdin", raceEnvironmentArgument(filepath.Join(stateDirectory, "race")),
		"--extra-env=DEXCLI="+dexcli, "--extra-env=PS1=job-shell$ ", "--extra-env=BASH_SILENCE_DEPRECATION_WARNING=1",
		"--", bashPath, "--norc", "--noprofile", "-i", "-s", "job-shell")
	host.Env = []string{"HOME=" + stateDirectory, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH")}
	keys, err := host.StdinPipe()
	require.NoError(t, err)
	var hostStderr bytes.Buffer
	host.Stderr = &hostStderr
	require.NoError(t, host.Start())
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Wait() }()
	screen := func() string {
		data, _ := os.ReadFile(screenPath)
		return string(data)
	}
	frame := func() string { return strings.Join(terminalFrame(screen(), 100, 30), "\n") }
	typeKeys := func(text string) {
		_, err := keys.Write([]byte(text))
		require.NoError(t, err)
	}
	waitForFrame := func(text string) {
		t.Helper()
		require.Eventually(t, func() bool { return strings.Contains(frame(), text) }, 20*time.Second, 50*time.Millisecond,
			"the frame shows %q:\n%s", text, frame())
	}
	waitForFrame("job-shell$")
	typeKeys("\"$DEXCLI\" agents-tui \"$2\" \"$3\"\r")
	require.Eventually(t, func() bool { return countLines(recordPath) >= 3 }, 20*time.Second, 20*time.Millisecond)
	waitForFrame("Ctrl-D exit")
	dexcliPID := childProcessID(t, childProcessID(t, host.Process.Pid))
	for round := 1; round <= 2; round++ {
		before := len(screen())
		require.NoError(t, syscall.Kill(dexcliPID, syscall.SIGTSTP))
		require.Eventually(t, func() bool { return strings.Contains(processState(t, dexcliPID), "T") }, 20*time.Second, 50*time.Millisecond,
			"round %d: dexcli is stopped", round)
		waitForFrame("job-shell$")
		released := screen()[before:]
		require.Contains(t, released, "\x1b[?1049l\x1b[?2004l\x1b[?1002l", "round %d: dexcli left the alternate screen, paste, and mouse modes", round)
		require.NotContains(t, released, "\x1b[?1049h", "round %d: a stopped dexcli draws nothing", round)
		typeKeys("fg\r")
		waitForFrame("Ctrl-D exit")
		typed := fmt.Sprintf("round%d", round)
		typeKeys(typed)
		waitForFrame("› " + typed)
		typeKeys("\x15")
		waitForFrame("type a message")
		require.Equal(t, 1, strings.Count(screen()[before:], "\x1b[?1049l"), "round %d: one SIGTSTP releases the terminal once", round)
		require.Equal(t, 1, strings.Count(screen()[before:], "\x1b[?1049h"), "round %d: fg retakes it once", round)
	}
	typeKeys("\x04")
	waitForFrame("job-shell$")
	typeKeys("exit\r")
	select {
	case err := <-hostDone:
		requireNoRaceReports(t, filepath.Join(stateDirectory, "race"))
		require.NoError(t, err, "host stderr: %s", hostStderr.String())
	case <-time.After(20 * time.Second):
		require.NoError(t, host.Process.Kill())
		t.Fatalf("the shell did not exit; frame:\n%s", frame())
	}
}

// processState returns the ps state letters of pid, such as "T" when
// stopped.
func processState(t *testing.T, pid int) string {
	t.Helper()
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
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
