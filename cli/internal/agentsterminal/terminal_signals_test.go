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
	events   []string
	messages []tea.Msg
}

func (program *recordingProgram) record(event string) {
	program.mutex.Lock()
	defer program.mutex.Unlock()
	program.events = append(program.events, event)
}

func (program *recordingProgram) Send(message tea.Msg) {
	program.mutex.Lock()
	defer program.mutex.Unlock()
	program.messages = append(program.messages, message)
}

func (program *recordingProgram) ReleaseTerminal() error {
	program.record("release")
	return nil
}

func (program *recordingProgram) RestoreTerminal() error {
	program.record("restore")
	return nil
}

func (program *recordingProgram) snapshot() ([]string, []tea.Msg) {
	program.mutex.Lock()
	defer program.mutex.Unlock()
	return append([]string(nil), program.events...), append([]tea.Msg(nil), program.messages...)
}

type countingRefresher struct {
	count atomic.Int32
}

func (refresher *countingRefresher) RequestRefresh() {
	refresher.count.Add(1)
}

// newTestSignals runs the handler on a channel the test feeds, with a fake
// stop and foreground probe.
func newTestSignals(t *testing.T, isForeground *atomic.Bool, stops *atomic.Int32) (*terminalSignals, *recordingProgram, *countingRefresher) {
	t.Helper()
	program := &recordingProgram{}
	refresher := &countingRefresher{}
	handler := newTerminalSignals(program, os.Stdin, nil, refresher)
	handler.isForeground = isForeground.Load
	handler.stopProcess = func() { stops.Add(1) }
	handler.wait.Add(1)
	go handler.run()
	t.Cleanup(func() {
		// suspend registers SIGTSTP again; leave no handler behind.
		signal.Stop(handler.signals)
		close(handler.done)
		handler.wait.Wait()
	})
	return handler, program, refresher
}

// SIGTSTP in the foreground: release, stop only this process, then take the
// terminal back and read the session again.
func TestSuspendInTheForegroundRestoresAfterContinue(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	isForeground.Store(true)
	handler, program, refresher := newTestSignals(t, &isForeground, &stops)
	handler.signals <- syscall.SIGTSTP
	require.Eventually(t, func() bool { return refresher.count.Load() == 1 }, time.Second, 10*time.Millisecond)
	events, _ := program.snapshot()
	require.Equal(t, []string{"release", "restore"}, events)
	require.Equal(t, int32(1), stops.Load())
}

// Continued in the background (bg): the terminal stays untouched until the
// process is in the foreground again.
func TestContinueInTheBackgroundLeavesTheTerminalAlone(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	handler, program, refresher := newTestSignals(t, &isForeground, &stops)
	handler.signals <- syscall.SIGTSTP
	handler.signals <- syscall.SIGCONT
	time.Sleep(3 * foregroundPollInterval / 2)
	events, _ := program.snapshot()
	require.Equal(t, []string{"release"}, events, "no raw mode, drawing, or reading in the background")
	require.Zero(t, refresher.count.Load())
	isForeground.Store(true)
	require.Eventually(t, func() bool { return refresher.count.Load() == 1 }, 2*time.Second, 10*time.Millisecond)
	events, _ = program.snapshot()
	require.Equal(t, []string{"release", "restore"}, events)
}

// External SIGINT, SIGTERM, and SIGHUP become orderly exits with their codes.
func TestExternalSignalsExitWithTheirCodes(t *testing.T) {
	var isForeground atomic.Bool
	var stops atomic.Int32
	handler, program, _ := newTestSignals(t, &isForeground, &stops)
	handler.signals <- syscall.SIGINT
	handler.signals <- syscall.SIGTERM
	handler.signals <- syscall.SIGHUP
	require.Eventually(t, func() bool {
		_, messages := program.snapshot()
		return len(messages) == 3
	}, time.Second, 10*time.Millisecond)
	_, messages := program.snapshot()
	require.Equal(t, []tea.Msg{
		externalSignalMsg{exitCode: exitCodeInterrupted},
		externalSignalMsg{exitCode: exitCodeTerminated},
		externalSignalMsg{exitCode: exitCodeHangUp},
	}, messages)
	require.Zero(t, stops.Load())
}
