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

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
)

// A SIGTSTP that arrives while the launch check holds the terminal in raw
// mode stops the process only after the mode was restored.
func TestStopDuringTheTerminalCheckWaitsForRestore(t *testing.T) {
	var isRaw atomic.Bool
	var stops atomic.Int32
	var wasRawAtStop atomic.Bool
	handler := newTerminalSignals(os.Stdin, nil)
	handler.stopProcess = func(uint64) {
		wasRawAtStop.Store(isRaw.Load())
		stops.Add(1)
	}
	handler.wait.Add(1)
	go handler.run()
	t.Cleanup(func() {
		signal.Stop(handler.signals)
		close(handler.done)
		handler.wait.Wait()
	})
	entered, release := make(chan struct{}), make(chan struct{})
	calls := terminalModeCalls{
		isTerminal: func(uintptr) bool { return true },
		getSize:    func(uintptr) (int, int, error) { return 80, 24, nil },
		makeRaw: func(uintptr) (*term.State, error) {
			isRaw.Store(true)
			close(entered)
			<-release
			return &term.State{}, nil
		},
		restore: func(uintptr, *term.State) error {
			isRaw.Store(false)
			return nil
		},
	}
	checked := make(chan bool, 1)
	go handler.HoldTerminalMode(func() { checked <- checkTerminal(calls, 0, 1) == nil })
	<-entered
	handler.signals <- syscall.SIGTSTP
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, stops.Load(), "no stop while the terminal is raw")
	close(release)
	require.True(t, <-checked)
	require.Eventually(t, func() bool { return stops.Load() == 1 }, time.Second, 5*time.Millisecond)
	require.False(t, wasRawAtStop.Load(), "the terminal was cooked when the process stopped")
}
