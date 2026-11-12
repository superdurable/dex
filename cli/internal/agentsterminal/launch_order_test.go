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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
)

const bearerFileLaunch = `{"type":"launch","contract":1,"mode":"chat",` +
	`"command":{"name":"dex-agents-test","harness":"test","language":"python","version":"0.0.0"},` +
	`"dex":{"server_address":"127.0.0.1:1","tls":{"enabled":false,"ca_file":null,"server_name":null},"auth":{"mode":"bearer","source":"file"},"web_url":null},` +
	`"session":{"flow_type":"Test","session_id":"test-1","is_resumed":false},` +
	`"methods":[],"capabilities":{},"notices":[],"log_path":"~/.dex/agents/test.log"}`

// The contract's checks come first, the terminal last of them; only then is
// the Dex client set up, so its failure never hides an earlier check.
func TestLaunchCheckPrecedence(t *testing.T) {
	brokenTerminal := func() *launchcontract.LaunchRejection {
		return launchcontract.TerminalFailed("stdin is not a terminal")
	}
	goodTerminal := func() *launchcontract.LaunchRejection { return nil }
	var setUps atomic.Int32
	brokenDex := func(launchcontract.LaunchDocument) (dexConnection, *launchcontract.LaunchRejection) {
		setUps.Add(1)
		return dexConnection{}, launchcontract.DexSetupFailed("The credential that DEX_CREDENTIAL_FILE names is not usable.")
	}
	withFile := func(name string) (string, bool) {
		if name == launchcontract.CredentialFileVariable {
			return "/home/user/missing-token", true
		}
		return "", false
	}
	for _, testCase := range []struct {
		name      string
		lookupEnv func(string) (string, bool)
		terminal  func() *launchcontract.LaunchRejection
		code      string
		exitCode  int
		isSetUp   bool
	}{
		{"auth missing beats a broken terminal", func(string) (string, bool) { return "", false }, brokenTerminal, launchcontract.RejectionAuthMissing, 2, false},
		{"a broken terminal beats an unusable credential file", withFile, brokenTerminal, launchcontract.RejectionTerminalFailed, 1, false},
		{"an unusable credential file after every check", withFile, goodTerminal, launchcontract.RejectionDexSetupFailed, 1, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			setUps.Store(0)
			_, _, rejection := runLaunchChecks([]byte(bearerFileLaunch), testCase.lookupEnv, testCase.terminal, brokenDex)
			require.NotNil(t, rejection)
			require.Equal(t, testCase.code, rejection.Code)
			require.Equal(t, testCase.exitCode, rejection.ExitCode)
			require.Equal(t, testCase.isSetUp, setUps.Load() == 1)
		})
	}
}

// The real setup reports an unreadable token file as dex_setup_failed,
// without the path or the token.
func TestDexSetupRejectsAnUnreadableCredentialFile(t *testing.T) {
	launch, rejection := launchcontract.CheckLaunchLine([]byte(bearerFileLaunch), supportedModes, func(name string) (string, bool) {
		return "/nonexistent/token", name == launchcontract.CredentialFileVariable
	})
	require.Nil(t, rejection)
	t.Setenv(launchcontract.CredentialFileVariable, "/nonexistent/token")
	_, rejection = newDexConnection(launch)
	require.NotNil(t, rejection)
	require.Equal(t, launchcontract.RejectionDexSetupFailed, rejection.Code)
	require.NotContains(t, rejection.Message, "/nonexistent")
}

type blockingTerminal struct {
	release chan struct{}
	writes  atomic.Int32
}

func (terminal *blockingTerminal) Write(data []byte) (int, error) {
	<-terminal.release
	terminal.writes.Add(1)
	return len(data), nil
}

func (terminal *blockingTerminal) Read([]byte) (int, error) { return 0, os.ErrClosed }
func (terminal *blockingTerminal) Close() error             { return nil }
func (terminal *blockingTerminal) Fd() uintptr              { return ^uintptr(0) }

// ready waits until the first sized frame was written: a terminal that
// blocks the write holds ready back.
func TestReadyWaitsForTheFirstFrameWrite(t *testing.T) {
	terminal := &blockingTerminal{release: make(chan struct{})}
	readySent := make(chan struct{})
	writer := newFirstFrameWriter(terminal, func() { close(readySent) })
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, err := writer.Write([]byte("setup sequences"))
		require.NoError(t, err)
	}()
	close(terminal.release)
	<-writeDone
	select {
	case <-readySent:
		t.Fatal("ready before any frame was rendered")
	case <-time.After(100 * time.Millisecond):
	}
	terminal.release = make(chan struct{})
	writer.markFrameRendered()
	frameDone := make(chan struct{})
	go func() {
		defer close(frameDone)
		_, err := writer.Write([]byte("first frame"))
		require.NoError(t, err)
	}()
	select {
	case <-readySent:
		t.Fatal("ready while the frame write is blocked")
	case <-time.After(200 * time.Millisecond):
	}
	close(terminal.release)
	<-frameDone
	select {
	case <-readySent:
	case <-time.After(time.Second):
		t.Fatal("no ready after the frame was written")
	}
	_, err := writer.Write([]byte("second frame"))
	require.NoError(t, err)
	require.Equal(t, int32(3), terminal.writes.Load())
}
