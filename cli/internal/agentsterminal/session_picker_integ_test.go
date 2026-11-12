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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/sdk-go/dex"
)

// Without summaries the picker shows ID, status, and start; search falls
// back to IDs.
func TestSessionsModeWithoutSessionSummaries(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	require.NoError(t, err)
	dexcli := buildDexcli(t)
	const (
		runningSession = "test-20261111-100000-aaaaaa"
		otherSession   = "test-20261111-110000-bbbbbb"
		endedSession   = "test-20261111-120000-cccccc"
	)
	address, client := startEngineWithSessions(t, dexcli, realWorkerFlowType, []string{runningSession, otherSession, endedSession})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, client.StopFlow(ctx, endedSession, dex.StopOptions{Type: dex.TerminateFlow}))
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	stack := &realWorkerStack{t: t, pythonPath: pythonPath, dexcli: dexcli, address: address, home: home}
	picker := stack.openChatWithLaunch(t, "", false, chatLaunch{methods: []string{}, resumeSessionID: runningSession})
	picker.waitForScreen(endedSession)
	picker.waitForScreen("terminated")
	require.NotContains(t, picker.screen(), "TURNS", "no summary columns without summaries")
	require.NotContains(t, picker.screen(), "UPDATED")
	picker.typeText("/cccccc\r")
	picker.waitForScreen("Title search is off here; filtering 3 loaded sessions by ID.")
	picker.typeText("\r")
	picker.waitForScreen("This session is terminated; run dex-agents-test to start a new one.")
	// A pause after Esc keeps the next key from reading as Alt.
	picker.typeText("\x1b")
	time.Sleep(300 * time.Millisecond)
	picker.typeText("/aaaaaa\r")
	time.Sleep(300 * time.Millisecond)
	picker.typeText("\r")
	picker.waitForHostExit()
}

// With summaries the pickers show and search titles; both modes resume
// the picked session.
func TestSessionPickersWithSessionSummaries(t *testing.T) {
	stack := newRealWorkerStackWithSummary(t, true)
	const (
		flakySession = "terminal-ui-picker-flaky"
		otherSession = "terminal-ui-picker-other"
	)
	for sessionID, text := range map[string]string{flakySession: "say fix the flaky retry test", otherSession: "say hello there"} {
		stack.startSession(t, sessionID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := stack.starter.SendUserMessage(ctx, sessionID, text)
		cancel()
		require.NoError(t, err)
		stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	}
	t.Run("SessionsModeSearchesTitles", func(t *testing.T) {
		picker := stack.openChatWithLaunch(t, "", false, chatLaunch{methods: []string{}, resumeSessionID: flakySession})
		picker.waitForScreen(`"say fix the flaky retry test"`)
		picker.waitForScreen("TURNS")
		picker.waitForScreen("UPDATED")
		// The summary values decode: a last activity time and one turn, not dashes.
		require.Regexp(t, flakySession+`\s*● running\s*\d\d-\d\d \d\d:\d\d\s*(now|\d+[mhd] ago)\s*1\b`, picker.screen())
		picker.typeText("/flaky\r")
		picker.waitForScreen("1 sessions whose titles match")
		picker.typeText("\r")
		picker.waitForHostExit()
	})
	t.Run("ChatSwitchesWithSlashSessions", func(t *testing.T) {
		chat := stack.openChat(t, otherSession, true)
		chat.waitForScreen("said: hello there")
		chat.typeText("/sessions\r")
		chat.waitForScreen(`"say fix the flaky retry test"`)
		chat.waitForScreen("(this chat)")
		chat.typeText("/flaky\r")
		chat.waitForScreen("1 sessions whose titles match")
		chat.typeText("\r")
		chat.waitForScreen("── session " + flakySession + " ──")
		chat.waitForScreen("said: fix the flaky retry test")
		chat.exit()
	})
}
