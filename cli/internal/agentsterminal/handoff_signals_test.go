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

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// A signal before the chat program is attached is recorded with its own
// exit code, stops the handoff once, and reaches the program on Attach: the
// window between the handoff and the chat loses nothing.
func TestSignalBeforeTheChatReachesItOnAttach(t *testing.T) {
	for _, testCase := range []struct {
		received os.Signal
		exitCode int
	}{
		{syscall.SIGINT, exitCodeInterrupted},
		{syscall.SIGTERM, exitCodeTerminated},
		{syscall.SIGHUP, exitCodeHangUp},
	} {
		t.Run(testCase.received.String(), func(t *testing.T) {
			var stops atomic.Int32
			handler := newTerminalSignals(os.Stdin, func() { stops.Add(1) })
			handler.wait.Add(1)
			go handler.run()
			t.Cleanup(func() {
				signal.Stop(handler.signals)
				close(handler.done)
				handler.wait.Wait()
			})
			handler.signals <- testCase.received
			handler.signals <- syscall.SIGTERM
			require.Eventually(t, func() bool { return handler.EarlyExitCode() != 0 }, time.Second, 5*time.Millisecond)
			require.Equal(t, testCase.exitCode, handler.EarlyExitCode(), "the first signal decides")
			require.Eventually(t, func() bool { return stops.Load() == 1 }, time.Second, 5*time.Millisecond)
			time.Sleep(50 * time.Millisecond)
			require.Equal(t, int32(1), stops.Load(), "the handoff stops once")
			program := &recordingProgram{}
			handler.Attach(program)
			require.Eventually(t, func() bool { return len(program.sent()) == 1 }, time.Second, 5*time.Millisecond)
			require.Equal(t, []tea.Msg{externalSignalMsg{exitCode: testCase.exitCode}}, program.sent())
		})
	}
}
