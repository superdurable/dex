// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package agentsterminal

import (
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newRunningSignals(t *testing.T, stops *atomic.Int32) *terminalSignals {
	t.Helper()
	handler := newTerminalSignals(os.Stdin, nil)
	handler.stopProcess = func(uint64) { stops.Add(1) }
	handler.wait.Add(1)
	go handler.run()
	t.Cleanup(func() {
		signal.Stop(handler.signals)
		close(handler.done)
		handler.wait.Wait()
	})
	return handler
}

// SIGTSTP before the chat chose the direct stop, then the chat attached
// before the stop took the terminal: only the chat's suspend path runs.
func TestAttachBeforeAnEarlyStopTakesTheChatPath(t *testing.T) {
	var stops atomic.Int32
	handler := newRunningSignals(t, &stops)
	program := &recordingProgram{}
	handler.beforeTerminalModeLock = func() { handler.Attach(program) }
	handler.signals <- syscall.SIGTSTP
	require.Eventually(t, func() bool { return len(program.sent()) == 1 }, time.Second, 5*time.Millisecond)
	_, isSuspendRequest := program.sent()[0].(suspendRequestedMsg)
	require.True(t, isSuspendRequest, "the chat suspends through its event loop")
	time.Sleep(50 * time.Millisecond)
	require.Zero(t, stops.Load(), "no direct stop once the chat owns the terminal")
}

// An exit signal is handled while a stop waits for the terminal check.
func TestExitSignalsDoNotWaitForTheTerminalCheck(t *testing.T) {
	var stops atomic.Int32
	handler := newRunningSignals(t, &stops)
	entered, release := make(chan struct{}), make(chan struct{})
	go handler.HoldTerminalMode(func() {
		close(entered)
		<-release
	})
	<-entered
	handler.signals <- syscall.SIGTSTP
	handler.signals <- syscall.SIGINT
	require.Eventually(t, func() bool { return handler.EarlyExitCode() == exitCodeInterrupted }, time.Second, 5*time.Millisecond,
		"SIGINT is recorded while the terminal check blocks")
	require.Zero(t, stops.Load())
	close(release)
	require.Eventually(t, func() bool { return stops.Load() == 1 }, time.Second, 5*time.Millisecond)
}
