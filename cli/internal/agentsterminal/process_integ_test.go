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
	"io"
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
	require.Equal(t, []string{}, document.Contracts["1"].Modes[launchcontract.ModeSessions].UsesMethods)
	require.Equal(t, []string{launchcontract.MethodListConnectors, launchcontract.MethodOpenURL},
		document.Contracts["1"].Modes[launchcontract.ModeConnectors].UsesMethods)
}

// suspendHost runs one command on the contract host's PTY for the suspend
// tests.
type suspendHost struct {
	t              *testing.T
	host           *exec.Cmd
	keys           io.WriteCloser
	hostStderr     *bytes.Buffer
	hostDone       chan error
	stateDirectory string
	screenPath     string
	recordPath     string
}

func startSuspendHost(t *testing.T, extraEnvironment []string, command ...string) *suspendHost {
	t.Helper()
	pythonPath, err := exec.LookPath("python3")
	require.NoError(t, err)
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
	suspend := &suspendHost{
		t: t, stateDirectory: stateDirectory, hostStderr: &bytes.Buffer{}, hostDone: make(chan error, 1),
		screenPath: filepath.Join(stateDirectory, "screen.log"), recordPath: filepath.Join(stateDirectory, "record.jsonl"),
	}
	arguments := []string{contractHostScript, "launcher",
		"--transcript", transcriptPath, "--pty", "100x30", "--screen-log", suspend.screenPath, "--record", suspend.recordPath,
		"--step-timeout", "60", "--pty-input-from-stdin", raceEnvironmentArgument(filepath.Join(stateDirectory, "race"))}
	for _, assignment := range extraEnvironment {
		arguments = append(arguments, "--extra-env="+assignment)
	}
	suspend.host = exec.Command(pythonPath, append(append(arguments, "--"), command...)...)
	suspend.host.Env = []string{"HOME=" + stateDirectory, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH")}
	suspend.keys, err = suspend.host.StdinPipe()
	require.NoError(t, err)
	suspend.host.Stderr = suspend.hostStderr
	require.NoError(t, suspend.host.Start())
	go func() { suspend.hostDone <- suspend.host.Wait() }()
	return suspend
}

func (suspend *suspendHost) screen() string {
	data, _ := os.ReadFile(suspend.screenPath)
	return string(data)
}

func (suspend *suspendHost) frame() string {
	return strings.Join(terminalFrame(suspend.screen(), 100, 30), "\n")
}

func (suspend *suspendHost) typeKeys(text string) {
	suspend.t.Helper()
	_, err := suspend.keys.Write([]byte(text))
	require.NoError(suspend.t, err)
}

func (suspend *suspendHost) waitForFrame(text string) {
	suspend.t.Helper()
	require.Eventually(suspend.t, func() bool { return strings.Contains(suspend.frame(), text) }, 20*time.Second, 50*time.Millisecond,
		"the frame shows %q:\n%s\nraw tail: %q", text, suspend.frame(), lastBytes(suspend.screen(), 1500))
}

func lastBytes(text string, count int) string {
	return text[max(len(text)-count, 0):]
}

// waitForRetake waits until dexcli entered the alternate screen again after
// offset, which it does once raw mode is back.
func (suspend *suspendHost) waitForRetake(offset int) {
	suspend.t.Helper()
	require.Eventually(suspend.t, func() bool { return strings.Contains(suspend.screen()[offset:], "\x1b[?1049h") }, 20*time.Second, 20*time.Millisecond,
		"dexcli retook the terminal")
	suspend.waitForFrame("Ctrl-D exit")
}

func (suspend *suspendHost) waitForReady() {
	suspend.t.Helper()
	require.Eventually(suspend.t, func() bool { return countLines(suspend.recordPath) >= 3 }, 20*time.Second, 20*time.Millisecond)
	suspend.waitForFrame("Ctrl-D exit")
}

// checkTypingWorks types a word, sees it in the input, and clears it.
func (suspend *suspendHost) checkTypingWorks(word string) {
	suspend.t.Helper()
	suspend.typeKeys(word)
	suspend.waitForFrame("› " + word)
	suspend.typeKeys("\x15")
	suspend.waitForFrame("type a message")
}

func (suspend *suspendHost) waitForExit() {
	suspend.t.Helper()
	select {
	case err := <-suspend.hostDone:
		requireNoRaceReports(suspend.t, filepath.Join(suspend.stateDirectory, "race"))
		require.NoError(suspend.t, err, "host stderr: %s", suspend.hostStderr.String())
	case <-time.After(20 * time.Second):
		require.NoError(suspend.t, suspend.host.Process.Kill())
		suspend.t.Fatalf("the host did not exit; frame:\n%s", suspend.frame())
	}
}

// stopForegroundGroup sends SIGTSTP to pid's group, as a tty Ctrl-Z does;
// raw mode reads Ctrl-Z as a key.
func stopForegroundGroup(t *testing.T, pid int) {
	t.Helper()
	groupID, err := syscall.Getpgid(pid)
	require.NoError(t, err)
	require.NoError(t, syscall.Kill(-groupID, syscall.SIGTSTP))
}

// Under a job-control shell, SIGTSTP stops the job and fg brings the chat
// back, round after round.
func TestSuspendUnderAJobControlShell(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	require.NoError(t, err)
	// The host appends --channel-fd N, which become the shell's $2 and $3.
	suspend := startSuspendHost(t,
		[]string{"DEXCLI=" + buildDexcli(t), "PS1=job-shell$ ", "BASH_SILENCE_DEPRECATION_WARNING=1"},
		bashPath, "--norc", "--noprofile", "-i", "-s", "job-shell")
	suspend.waitForFrame("job-shell$")
	// sh stays as dexcli's parent in the job's group, as a launcher does.
	suspend.typeKeys("sh -c '\"$DEXCLI\" agents-tui \"$1\" \"$2\"; :' launcher \"$2\" \"$3\"\r")
	suspend.waitForReady()
	launcherPID := childProcessID(t, childProcessID(t, suspend.host.Process.Pid))
	dexcliPID := childProcessID(t, launcherPID)
	for round := 1; round <= 10; round++ {
		before := len(suspend.screen())
		stopForegroundGroup(t, dexcliPID)
		require.Eventually(t, func() bool {
			return strings.Contains(processState(t, dexcliPID), "T") && strings.Contains(processState(t, launcherPID), "T")
		}, 20*time.Second, 20*time.Millisecond, "round %d: the job is stopped", round)
		suspend.typeKeys("fg\r")
		suspend.waitForRetake(before)
		suspend.checkTypingWorks(fmt.Sprintf("round%d", round))
		// Released before the stop or retaken after fg: one switch each way.
		require.Equal(t, 1, strings.Count(suspend.screen()[before:], "\x1b[?1049l"), "round %d: the terminal is released once", round)
		require.Equal(t, 1, strings.Count(suspend.screen()[before:], "\x1b[?1049h"), "round %d: fg retakes it once", round)
	}
	suspend.typeKeys("\x04")
	suspend.waitForFrame("job-shell$")
	suspend.typeKeys("exit\r")
	suspend.waitForExit()
}

// In an orphaned process group no shell could continue the chat, so it keeps
// running.
func TestSuspendInAnOrphanedProcessGroupKeepsRunning(t *testing.T) {
	suspend := startSuspendHost(t, nil, buildDexcli(t), "agents-tui")
	suspend.waitForReady()
	dexcliPID := childProcessID(t, suspend.host.Process.Pid)
	before := len(suspend.screen())
	stopForegroundGroup(t, dexcliPID)
	require.Eventually(t, func() bool { return strings.Count(suspend.screen()[before:], "\x1b[?1049h") == 1 }, 20*time.Second, 20*time.Millisecond,
		"dexcli released and retook the terminal")
	require.NotContains(t, processState(t, dexcliPID), "T", "dexcli is not stopped")
	suspend.checkTypingWorks("still-here")
	require.NotContains(t, processState(t, dexcliPID), "T")
	require.Equal(t, 1, strings.Count(suspend.screen()[before:], "\x1b[?1049l"), "one SIGTSTP releases the terminal once")
	suspend.typeKeys("\x04")
	suspend.waitForExit()
}

// When the shell already owns the terminal, dexcli stops without writing to
// it.
func TestSuspendAfterTheShellTookTheTerminal(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	require.NoError(t, err)
	suspend := startSuspendHost(t,
		[]string{"DEXCLI=" + buildDexcli(t), "PS1=job-shell$ ", "BASH_SILENCE_DEPRECATION_WARNING=1"},
		bashPath, "--norc", "--noprofile", "-i", "-s", "job-shell")
	suspend.waitForFrame("job-shell$")
	// tostop makes a background write stop the writer, which SIGTTOU would show.
	suspend.typeKeys("stty tostop; sh -c '\"$DEXCLI\" agents-tui \"$1\" \"$2\"; :' launcher \"$2\" \"$3\"\r")
	suspend.waitForReady()
	launcherPID := childProcessID(t, childProcessID(t, suspend.host.Process.Pid))
	dexcliPID := childProcessID(t, launcherPID)
	before := len(suspend.screen())
	require.NoError(t, syscall.Kill(launcherPID, syscall.SIGTSTP))
	require.Eventually(t, func() bool {
		after := suspend.screen()[before:]
		return strings.Contains(after, "Stopped") && strings.HasSuffix(after, "job-shell$ ")
	}, 20*time.Second, 20*time.Millisecond, "the shell took the terminal back")
	shellOwned := len(suspend.screen())
	require.NoError(t, syscall.Kill(dexcliPID, syscall.SIGTSTP))
	require.Eventually(t, func() bool { return strings.Contains(processState(t, dexcliPID), "T") }, 20*time.Second, 20*time.Millisecond,
		"dexcli is stopped")
	require.NotContains(t, suspend.screen()[shellOwned:], "\x1b", "dexcli wrote nothing to the shell's terminal")
	suspend.typeKeys("fg\r")
	suspend.waitForRetake(shellOwned)
	suspend.checkTypingWorks("back-again")
	suspend.typeKeys("\x04")
	suspend.waitForFrame("job-shell$")
	suspend.typeKeys("exit\r")
	suspend.waitForExit()
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
