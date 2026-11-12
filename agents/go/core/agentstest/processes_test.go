// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentstest

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const processTestReadyLine = "child ready"

// A child that exits on its own after its ready line is reaped, and then no signal is sent to its group.
func TestKillableProcessSendsNoSignalAfterTheChildExitedOnItsOwn(t *testing.T) {
	process := NewKillableProcess("exiting child", filepath.Join(t.TempDir(), "child.log"))
	require.NoError(t, process.Start(context.Background(), []string{"sh", "-c", "echo " + processTestReadyLine},
		nil, processTestReadyLine, 10*time.Second))
	require.Eventually(t, func() bool { return !process.IsRunning() }, 10*time.Second, 10*time.Millisecond)

	require.EqualError(t, process.SignalProcessGroup(syscall.SIGCONT), "exiting child is not running")
	process.Kill()
	require.EqualError(t, process.SignalProcessGroup(syscall.SIGCONT), "exiting child is not running")

	// The exited child does not block another Start.
	require.NoError(t, process.Start(context.Background(), []string{"sh", "-c", "echo " + processTestReadyLine + "; exec sleep 60"},
		nil, processTestReadyLine, 10*time.Second))
	require.True(t, process.IsRunning())
	process.Kill()
	require.False(t, process.IsRunning())
}

// A stopped child is still running: SIGSTOP and SIGCONT reach it, and Kill ends it.
func TestKillableProcessSignalsARunningChildGroup(t *testing.T) {
	process := NewKillableProcess("sleeping child", filepath.Join(t.TempDir(), "child.log"))
	require.NoError(t, process.Start(context.Background(), []string{"sh", "-c", "echo " + processTestReadyLine + "; exec sleep 60"},
		nil, processTestReadyLine, 10*time.Second))
	t.Cleanup(process.Kill)
	require.NoError(t, process.SignalProcessGroup(syscall.SIGSTOP))
	require.True(t, process.IsRunning(), "a stopped child has not exited")
	require.NoError(t, process.SignalProcessGroup(syscall.SIGCONT))
	process.Kill()
	require.False(t, process.IsRunning())
	require.Error(t, process.SignalProcessGroup(syscall.SIGCONT))
}

func TestKillableProcessReportsAnExitBeforeTheReadyLine(t *testing.T) {
	process := NewKillableProcess("failing child", filepath.Join(t.TempDir(), "child.log"))
	err := process.Start(context.Background(), []string{"sh", "-c", "echo boom; exit 3"}, nil, processTestReadyLine, 10*time.Second)
	require.ErrorContains(t, err, "failing child exited before it was ready (exit status 3)")
	require.ErrorContains(t, err, "boom")
	require.False(t, process.IsRunning())
}
