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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

type recordingProgram struct {
	mutex    sync.Mutex
	messages []tea.Msg
}

func (program *recordingProgram) Send(message tea.Msg) {
	program.mutex.Lock()
	defer program.mutex.Unlock()
	program.messages = append(program.messages, message)
}

func (program *recordingProgram) sent() []tea.Msg {
	program.mutex.Lock()
	defer program.mutex.Unlock()
	return append([]tea.Msg(nil), program.messages...)
}

// newTestSignals runs the handler on a channel the test feeds, with a fake
// stop and foreground probe.
func newTestSignals(t *testing.T, isForeground *atomic.Bool, stops *atomic.Int32) (*terminalSignals, *recordingProgram) {
	t.Helper()
	program := &recordingProgram{}
	handler := newTerminalSignals(os.Stdin, nil)
	handler.Attach(program)
	handler.isForeground = isForeground.Load
	handler.stopProcess = func() { stops.Add(1) }
	handler.wait.Add(1)
	go handler.run()
	t.Cleanup(func() {
		// The suspend command registers SIGTSTP again; leave no handler behind.
		signal.Stop(handler.signals)
		close(handler.done)
		handler.wait.Wait()
	})
	return handler, program
}

// suspendCommandOf waits for the program to get the suspend request.
func suspendCommandOf(t *testing.T, program *recordingProgram) tea.ExecCommand {
	t.Helper()
	var command tea.ExecCommand
	require.Eventually(t, func() bool {
		for _, message := range program.sent() {
			if request, isRequest := message.(suspendRequestedMsg); isRequest {
				command = request.command
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)
	return command
}

// SIGTSTP asks the program to run the suspend command on its event loop; the
// command stops only this process and returns in the foreground.
func TestSuspendInTheForegroundReturnsAfterTheStop(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	isForeground.Store(true)
	handler, program := newTestSignals(t, &isForeground, &stops)
	handler.signals <- syscall.SIGTSTP
	command := suspendCommandOf(t, program)
	require.NoError(t, command.Run())
	require.Equal(t, int32(1), stops.Load())
	handler.signals <- syscall.SIGTSTP
	require.Eventually(t, func() bool { return len(program.sent()) == 2 }, time.Second, 10*time.Millisecond,
		"a later SIGTSTP suspends again")
}

// Continued in the background (bg): the command keeps the terminal released
// until the process is in the foreground again.
func TestContinueInTheBackgroundWaitsForTheForeground(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	handler, program := newTestSignals(t, &isForeground, &stops)
	handler.signals <- syscall.SIGTSTP
	command := suspendCommandOf(t, program)
	returned := make(chan error, 1)
	go func() { returned <- command.Run() }()
	handler.signals <- syscall.SIGCONT
	handler.signals <- syscall.SIGTSTP
	select {
	case <-returned:
		t.Fatal("the command returned in the background")
	case <-time.After(3 * foregroundPollInterval / 2):
	}
	require.Len(t, program.sent(), 1, "no second suspend while suspended")
	isForeground.Store(true)
	select {
	case err := <-returned:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the command did not return in the foreground")
	}
}

// External SIGINT, SIGTERM, and SIGHUP become orderly exits with their codes.
func TestExternalSignalsExitWithTheirCodes(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	handler, program := newTestSignals(t, &isForeground, &stops)
	for _, received := range []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		handler.signals <- received
	}
	require.Eventually(t, func() bool { return len(program.sent()) == 3 }, time.Second, 10*time.Millisecond)
	require.ElementsMatch(t, []tea.Msg{
		externalSignalMsg{exitCode: exitCodeInterrupted},
		externalSignalMsg{exitCode: exitCodeTerminated},
		externalSignalMsg{exitCode: exitCodeHangUp},
	}, program.sent())
	require.Zero(t, stops.Load())
}
