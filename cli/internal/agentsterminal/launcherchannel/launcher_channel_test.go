// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package launcherchannel

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
)

const testWait = 5 * time.Second

var allMethods = []string{
	launchcontract.MethodPutImage, launchcontract.MethodStartSession, launchcontract.MethodListConnectors,
	launchcontract.MethodOpenURL, launchcontract.MethodGetRequestResult,
}

// fakeLauncher is the launcher end of a real socketpair.
type fakeLauncher struct {
	t          *testing.T
	connection net.Conn
	received   chan map[string]any
}

func newChannelPair(t *testing.T, methods []string) (*LauncherChannel, *fakeLauncher) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	connect := func(fd int, name string) net.Conn {
		file := os.NewFile(uintptr(fd), name)
		connection, err := net.FileConn(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		return connection
	}
	timeouts := RequestTimeouts{
		MethodWaits:  map[string]time.Duration{launchcontract.MethodGetRequestResult: 150 * time.Millisecond},
		CancelAnswer: 300 * time.Millisecond,
		Recovery:     1500 * time.Millisecond,
		RunningPoll:  50 * time.Millisecond,
	}
	for _, method := range allMethods[:4] {
		timeouts.MethodWaits[method] = 200 * time.Millisecond
	}
	timeouts.MethodWaits[launchcontract.MethodListConnectors] = 2 * time.Second
	channel := NewLauncherChannel(connect(fds[0], "tui"), timeouts)
	launcher := &fakeLauncher{t: t, connection: connect(fds[1], "launcher"), received: make(chan map[string]any, 64)}
	go launcher.read()
	channel.Start(methods)
	t.Cleanup(func() {
		require.NoError(t, channel.Close())
		if err := launcher.connection.Close(); err != nil {
			t.Logf("close the fake launcher: %v", err)
		}
	})
	return channel, launcher
}

func (launcher *fakeLauncher) read() {
	defer close(launcher.received)
	scanner := bufio.NewScanner(launcher.connection)
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			launcher.t.Errorf("dexcli wrote a line that is not JSON: %v", err)
			return
		}
		launcher.received <- message
	}
}

// next returns the next message dexcli sent, checked against the channel schema.
func (launcher *fakeLauncher) next() map[string]any {
	launcher.t.Helper()
	select {
	case message, isOpen := <-launcher.received:
		require.True(launcher.t, isOpen, "the channel ended")
		decoded, err := launchcontract.DecodeChannelLine(mustMarshal(launcher.t, message))
		require.NoError(launcher.t, err)
		require.Nil(launcher.t, launchcontract.CheckTerminalUIMessage(decoded), "dexcli sent a message the schema rejects: %v", message)
		return message
	case <-time.After(testWait):
		launcher.t.Fatal("dexcli sent nothing")
		return nil
	}
}

func (launcher *fakeLauncher) expectSilence(duration time.Duration) {
	launcher.t.Helper()
	select {
	case message := <-launcher.received:
		launcher.t.Fatalf("dexcli sent %v", message)
	case <-time.After(duration):
	}
}

func (launcher *fakeLauncher) send(message map[string]any) {
	launcher.t.Helper()
	_, err := launcher.connection.Write(append(mustMarshal(launcher.t, message), '\n'))
	require.NoError(launcher.t, err)
}

func (launcher *fakeLauncher) respond(request map[string]any, result map[string]any) {
	launcher.send(map[string]any{"type": "response", "id": request["id"], "result": result})
}

func (launcher *fakeLauncher) fail(request map[string]any, code string) {
	launcher.send(map[string]any{"type": "response", "id": request["id"], "error": map[string]any{"code": code, "message": "Test."}})
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func requireRequest(t *testing.T, message map[string]any, method string, id float64) {
	t.Helper()
	require.Equal(t, "request", message["type"])
	require.Equal(t, method, message["method"])
	require.Equal(t, id, message["id"])
}

var imageResult = map[string]any{"image": map[string]any{"uri": "file:///home/user/a", "mime_type": "image/png", "size_bytes": 10, "sha256": "ab"}, "name": "a.png"}

func callAsync(channel *LauncherChannel, method string, key string, params any) <-chan RequestOutcome {
	outcome := make(chan RequestOutcome, 1)
	go func() {
		outcome <- channel.CallWithRecoveryKey(context.Background(), method, key, params, nil)
	}()
	return outcome
}

func newKey(t *testing.T) string {
	t.Helper()
	key, err := launchcontract.NewIdempotencyKey()
	require.NoError(t, err)
	return key
}

func waitOutcome(t *testing.T, outcome <-chan RequestOutcome) RequestOutcome {
	t.Helper()
	select {
	case result := <-outcome:
		return result
	case <-time.After(testWait):
		t.Fatal("the request did not end")
		return RequestOutcome{}
	}
}

// A lost response: dexcli cancels after its wait, then reads the remembered
// outcome with the same key.
func TestLostResponseRecoversWithGetRequestResult(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	key := newKey(t)
	outcome := callAsync(channel, launchcontract.MethodPutImage, key, map[string]any{"path": "/home/user/a.png"})
	request := launcher.next()
	requireRequest(t, request, launchcontract.MethodPutImage, 1)
	require.Equal(t, key, request["idempotency_key"])
	cancel := launcher.next()
	require.Equal(t, map[string]any{"type": "cancel", "id": float64(1)}, cancel)
	launcher.fail(request, "cancelled")
	query := launcher.next()
	requireRequest(t, query, launchcontract.MethodGetRequestResult, 2)
	require.NotContains(t, query, "idempotency_key")
	require.Equal(t, map[string]any{"idempotency_key": key}, query["params"])
	launcher.respond(query, map[string]any{"state": "done", "result": imageResult})
	result := waitOutcome(t, outcome)
	require.Equal(t, OutcomeDone, result.Kind)
	require.JSONEq(t, string(mustMarshal(t, imageResult)), string(result.Result))
}

// A result that arrives right after the cancel wins; no recovery follows.
func TestCancelRacingCompletionTakesTheResult(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	outcome := callAsync(channel, launchcontract.MethodPutImage, newKey(t), map[string]any{"path": "/home/user/a.png"})
	request := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.respond(request, imageResult)
	require.Equal(t, OutcomeDone, waitOutcome(t, outcome).Kind)
	launcher.expectSilence(400 * time.Millisecond)
}

// A forgotten key: unknown makes dexcli resend once with the same key and params.
func TestForgottenKeyResendsOnceWithTheSameKey(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	key := newKey(t)
	outcome := callAsync(channel, launchcontract.MethodStartSession, key, map[string]any{})
	first := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(first, "cancelled")
	query := launcher.next()
	launcher.respond(query, map[string]any{"state": "unknown"})
	resent := launcher.next()
	requireRequest(t, resent, launchcontract.MethodStartSession, 3)
	require.Equal(t, key, resent["idempotency_key"])
	require.Equal(t, first["params"], resent["params"])
	launcher.respond(resent, map[string]any{"session_id": "openai-sdk-20261111-142507-d3fbbe"})
	require.Equal(t, OutcomeDone, waitOutcome(t, outcome).Kind)
}

// A start_session resent after the Flow started returns the session that
// key already started, and a second unknown answer ends recovery.
func TestResendAfterFlowStartedEndsRecovery(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	key := newKey(t)
	outcome := callAsync(channel, launchcontract.MethodStartSession, key, map[string]any{})
	first := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(first, "timeout")
	query := launcher.next()
	launcher.respond(query, map[string]any{"state": "unknown"})
	resent := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(resent, "cancelled")
	result := waitOutcome(t, outcome)
	require.Equal(t, OutcomeUnknown, result.Kind)
	launcher.expectSilence(400 * time.Millisecond)
}

// get_request_result answers running until done, once per RunningPoll.
func TestRunningThenDonePolls(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	stillRunning := 0
	outcome := make(chan RequestOutcome, 1)
	go func() {
		outcome <- channel.CallWithRecoveryKey(context.Background(), launchcontract.MethodStartSession, newKey(t), map[string]any{}, func() { stillRunning++ })
	}()
	first := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(first, "cancelled")
	for id := float64(2); id <= 3; id++ {
		query := launcher.next()
		requireRequest(t, query, launchcontract.MethodGetRequestResult, id)
		launcher.respond(query, map[string]any{"state": "running"})
	}
	query := launcher.next()
	launcher.respond(query, map[string]any{"state": "done", "result": map[string]any{"session_id": "openai-sdk-20261111-142507-d3fbbe"}})
	require.Equal(t, OutcomeDone, waitOutcome(t, outcome).Kind)
	require.Equal(t, 1, stillRunning)
}

// A key that keeps running past the recovery window stays unknown and is
// never resent.
func TestRunningPastTheRecoveryWindowStaysUnknown(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	outcome := callAsync(channel, launchcontract.MethodStartSession, newKey(t), map[string]any{})
	first := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(first, "cancelled")
	for {
		select {
		case result := <-outcome:
			require.Equal(t, OutcomeUnknown, result.Kind)
			launcher.expectSilence(200 * time.Millisecond)
			return
		case message := <-launcher.received:
			require.Equal(t, launchcontract.MethodGetRequestResult, message["method"])
			launcher.respond(message, map[string]any{"state": "running"})
		case <-time.After(testWait):
			t.Fatal("recovery did not end")
		}
	}
}

// The Worker host stopped in the middle of a request: no resend, no query.
func TestHostStoppedMidRequestNeverResends(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	outcome := callAsync(channel, launchcontract.MethodPutImage, newKey(t), map[string]any{"path": "/home/user/a.png"})
	request := launcher.next()
	launcher.send(map[string]any{"type": "worker", "state": "stopped", "exit_code": 137})
	launcher.fail(request, "host_stopped")
	require.Equal(t, OutcomeHostStopped, waitOutcome(t, outcome).Kind)
	notification := <-channel.Notifications()
	require.Equal(t, "stopped", notification.Worker.State)
	require.Equal(t, 137, notification.Worker.ExitCode)
	launcher.expectSilence(400 * time.Millisecond)
}

// The launcher died in the middle of a request: the wait ends at once.
func TestLauncherKilledMidRequestEndsTheWait(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	outcome := callAsync(channel, launchcontract.MethodStartSession, newKey(t), map[string]any{})
	launcher.next()
	require.NoError(t, launcher.connection.Close())
	require.Equal(t, OutcomeChannelEnded, waitOutcome(t, outcome).Kind)
	<-channel.Ended()
	require.ErrorIs(t, channel.EndErr(), ErrChannelEnded)
	_, isOpen := <-channel.Notifications()
	require.False(t, isOpen)
}

// Without get_request_result, dexcli resends once with the same key.
func TestWithoutGetRequestResultResendsOnce(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods[:4])
	key := newKey(t)
	outcome := callAsync(channel, launchcontract.MethodPutImage, key, map[string]any{"path": "/home/user/a.png"})
	first := launcher.next()
	require.Equal(t, "cancel", launcher.next()["type"])
	launcher.fail(first, "cancelled")
	resent := launcher.next()
	requireRequest(t, resent, launchcontract.MethodPutImage, 2)
	require.Equal(t, key, resent["idempotency_key"])
	launcher.respond(resent, imageResult)
	require.Equal(t, OutcomeDone, waitOutcome(t, outcome).Kind)
}

// unknown_method for get_request_result turns it off for the process.
func TestGetRequestResultUnknownMethodTurnsItOff(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	for round := 0; round < 2; round++ {
		outcome := callAsync(channel, launchcontract.MethodPutImage, newKey(t), map[string]any{"path": "/home/user/a.png"})
		first := launcher.next()
		require.Equal(t, "cancel", launcher.next()["type"])
		launcher.fail(first, "cancelled")
		next := launcher.next()
		if round == 0 {
			require.Equal(t, launchcontract.MethodGetRequestResult, next["method"])
			launcher.fail(next, "unknown_method")
			next = launcher.next()
		}
		require.Equal(t, launchcontract.MethodPutImage, next["method"], "round %d resends without asking", round)
		launcher.respond(next, imageResult)
		require.Equal(t, OutcomeDone, waitOutcome(t, outcome).Kind)
	}
}

// A fifth waiting request is busy before it reaches the channel, and
// get_request_result never counts.
func TestFifthRequestIsBusyLocally(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	var wait sync.WaitGroup
	outcomes := make(chan RequestOutcome, 4)
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			outcomes <- channel.Call(context.Background(), launchcontract.MethodListConnectors, newKey(t), map[string]any{})
		}()
	}
	var requests []map[string]any
	for range 4 {
		requests = append(requests, launcher.next())
	}
	busy := channel.Call(context.Background(), launchcontract.MethodListConnectors, newKey(t), map[string]any{})
	require.Equal(t, OutcomeFailed, busy.Kind)
	require.Equal(t, launchcontract.ErrorCodeBusy, busy.Error.Code)
	query := make(chan RequestOutcome, 1)
	go func() {
		query <- channel.Call(context.Background(), launchcontract.MethodGetRequestResult, "", map[string]any{"idempotency_key": newKey(t)})
	}()
	queryRequest := launcher.next()
	require.Equal(t, launchcontract.MethodGetRequestResult, queryRequest["method"])
	launcher.respond(queryRequest, map[string]any{"state": "unknown"})
	require.Equal(t, OutcomeDone, waitOutcome(t, query).Kind)
	for _, request := range requests {
		launcher.respond(request, map[string]any{"mcp_url": "http://127.0.0.1:1/mcp", "tools": []any{}})
	}
	wait.Wait()
	close(outcomes)
	for outcome := range outcomes {
		require.Equal(t, OutcomeDone, outcome.Kind)
	}
}

// Notices and Worker states arrive in order; a later message type is skipped.
func TestNotificationsAndLaterMessageTypes(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	launcher.send(map[string]any{"type": "worker_progress", "percent": 40})
	launcher.send(map[string]any{"type": "notice", "level": "warning", "text": "Lost the Dex server; retrying.", "source": "x"})
	launcher.send(map[string]any{"type": "worker", "state": "reconnecting"})
	first := <-channel.Notifications()
	require.Equal(t, "Lost the Dex server; retrying.", first.Notice.Text)
	second := <-channel.Notifications()
	require.Equal(t, "reconnecting", second.Worker.State)
}

// A malformed known type ends the contract: dexcli closes the channel.
func TestProtocolErrorClosesTheChannel(t *testing.T) {
	channel, launcher := newChannelPair(t, allMethods)
	launcher.send(map[string]any{"type": "notice", "level": "info"})
	select {
	case <-channel.Ended():
	case <-time.After(testWait):
		t.Fatal("the channel stayed open")
	}
	require.ErrorIs(t, channel.EndErr(), ErrProtocol)
	_, isOpen := <-launcher.received
	require.False(t, isOpen, "the launcher reads EOF")
}

// A launch line over 1 MiB comes back over the limit, so the checks reject it.
func TestReadLaunchLineCutsAnOversizeLine(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	tuiFile, launcherFile := os.NewFile(uintptr(fds[0]), "tui"), os.NewFile(uintptr(fds[1]), "launcher")
	tuiConnection, err := net.FileConn(tuiFile)
	require.NoError(t, err)
	require.NoError(t, tuiFile.Close())
	channel := NewLauncherChannel(tuiConnection, ContractRequestTimeouts())
	t.Cleanup(func() { require.NoError(t, channel.Close()) })
	go func() {
		line := append([]byte(`{"type":"launch"}`), make([]byte, launchcontract.MaxLineBytes)...)
		for index := 17; index < len(line); index++ {
			line[index] = ' '
		}
		if _, writeErr := launcherFile.Write(append(line, '\n')); writeErr != nil {
			t.Errorf("write the launch line: %v", writeErr)
		}
		if closeErr := launcherFile.Close(); closeErr != nil {
			t.Errorf("close the launcher end: %v", closeErr)
		}
	}()
	line, err := channel.ReadLaunchLine()
	require.NoError(t, err)
	_, rejection := launchcontract.CheckLaunchLine(line, launchcontract.SupportedModes{1: {launchcontract.ModeChat}}, os.LookupEnv)
	require.NotNil(t, rejection)
	require.Equal(t, launchcontract.RejectionInvalidLaunch, rejection.Code)
}
