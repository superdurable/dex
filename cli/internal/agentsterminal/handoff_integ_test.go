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
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SIGINT, SIGTERM, and SIGHUP while dexcli waits for the launch line end it
// with 130, 143, and 1, and it writes nothing on stderr.
func TestSignalsWhileTheLaunchLineBlocks(t *testing.T) {
	dexcli := buildDexcli(t)
	for _, testCase := range []struct {
		received syscall.Signal
		exitCode int
	}{
		{syscall.SIGINT, exitCodeInterrupted},
		{syscall.SIGTERM, exitCodeTerminated},
		{syscall.SIGHUP, exitCodeHangUp},
	} {
		t.Run(testCase.received.String(), func(t *testing.T) {
			t.Parallel()
			fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
			require.NoError(t, err)
			launcherEnd, terminalUIEnd := os.NewFile(uintptr(fds[0]), "launcher"), os.NewFile(uintptr(fds[1]), "tui")
			t.Cleanup(func() {
				if closeErr := launcherEnd.Close(); closeErr != nil {
					t.Logf("close the launcher end: %v", closeErr)
				}
			})
			racePrefix := t.TempDir() + "/race"
			command := exec.Command(dexcli, "agents-tui", "--channel-fd", "3")
			command.ExtraFiles = []*os.File{terminalUIEnd}
			command.Env = []string{"HOME=" + t.TempDir(), "TERM=xterm-256color", "GORACE=log_path=" + racePrefix}
			var stderr bytes.Buffer
			command.Stdout, command.Stderr = &stderr, &stderr
			require.NoError(t, command.Start())
			require.NoError(t, terminalUIEnd.Close())
			// The process installs its handlers at start; give it that moment.
			time.Sleep(500 * time.Millisecond)
			require.NoError(t, command.Process.Signal(testCase.received))
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				var exitError *exec.ExitError
				require.True(t, errors.As(err, &exitError) || err == nil, "exit: %v", err)
				require.Equal(t, testCase.exitCode, command.ProcessState.ExitCode())
			case <-time.After(10 * time.Second):
				require.NoError(t, command.Process.Kill())
				t.Fatal("dexcli kept waiting for the launch line")
			}
			require.Empty(t, stderr.String())
			requireNoRaceReports(t, racePrefix)
		})
	}
}
