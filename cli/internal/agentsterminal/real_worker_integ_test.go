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
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/agents/go/core/remotesession"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	realWorkerFlowType = "TerminalUIRemoteSessionAgent"
	realWorkerTimeout  = 2 * time.Minute
	screenWait         = 30 * time.Second
)

var (
	pythonCoreDirectory       = filepath.Join(repositoryRoot, "agents", "python", "core")
	remoteSessionWorkerScript = filepath.Join(repositoryRoot, "agents", "go", "core", "integ", "testdata", "python", "remote_session_worker.py")
	terminalEscapes           = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]`)
)

// realWorkerStack is a private Dex engine with a real Python session Worker
// of the remote session test agent; see remote_session_worker.py.
type realWorkerStack struct {
	t              *testing.T
	pythonPath     string
	dexcli         string
	stateDirectory string
	home           string
	coordination   string
	address        string
	worker         *agentstest.KillableProcess
	starter        *dexagents.AgentSessionClient
	sessions       *remotesession.RemoteSessionClient
}

// dexcli drives real sessions of a Python Worker: a tool approval
// round-trip, a resumed chat that shows the history, and Ctrl-C cancel.
func TestChatWithARealPythonWorker(t *testing.T) {
	stack := newRealWorkerStack(t)
	t.Run("ApprovalRoundTrip", stack.testApprovalRoundTrip)
	t.Run("ResumeShowsHistory", stack.testResumeShowsHistory)
	t.Run("CtrlCCancelsTheTurn", stack.testCtrlCCancelsTheTurn)
	t.Run("QuestionAnswer", stack.testQuestionAnswer)
	t.Run("QueueAndSteer", stack.testQueueAndSteer)
}

func (stack *realWorkerStack) testQuestionAnswer(t *testing.T) {
	sessionID := "terminal-ui-question"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("ask Which color? | red | blue\r")
	stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.PendingInterrupts) == 1 && snapshot.PendingInterrupts[0].Kind == dexagents.InterruptKindQuestion
	})
	chat.waitForScreen("Which color?")
	chat.typeText("\r")
	finished := stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	require.Equal(t, `ask_user: {"status":"answered","answer":"red"}`, lastAssistantText(finished))
	chat.waitForScreen("answered: red")
	chat.exit()
}

// A message typed during a turn is queued; steering it ends the turn at its
// next safe boundary and starts a turn with it. The session is briefly idle
// between the two turns, which is not the steer being done.
func (stack *realWorkerStack) testQueueAndSteer(t *testing.T) {
	sessionID := "terminal-ui-steer"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("stall steer-gate\r")
	chat.waitForScreen("Ctrl-C cancel turn")
	chat.typeText("say steered\r")
	stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.QueuedMessages) == 1
	})
	chat.waitForScreen("Tab acts on it")
	chat.typeText("\t")
	time.Sleep(300 * time.Millisecond)
	chat.typeText("s")
	stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.SteeredMessages) == 1 && len(snapshot.QueuedMessages) == 0
	})
	require.NoError(t, os.WriteFile(filepath.Join(stack.coordination, "steer-gate.released"), nil, 0o600))
	finished := stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return isIdleAfterTurns(2)(snapshot) && lastAssistantText(snapshot) == "said: steered"
	})
	require.Empty(t, finished.SteeredMessages)
	chat.waitForScreen("said: steered")
	chat.exit()
}

func newRealWorkerStack(t *testing.T) *realWorkerStack {
	uvPath, uvErr := exec.LookPath("uv")
	pythonPath, pythonErr := exec.LookPath("python3")
	serverZD := os.Getenv(agentstest.DexServerZDEnvironmentVariable)
	if serverZD == "" {
		serverZD, _ = exec.LookPath("dex-server-zd")
	}
	if uvErr != nil || pythonErr != nil || serverZD == "" {
		t.Skip("needs uv, python3, and dex-server-zd (DEX_SERVER_ZD_BINARY)")
	}
	dexcli := buildDexcli(t)
	t.Setenv(agentstest.DexcliEnvironmentVariable, dexcli)
	t.Setenv(agentstest.DexServerZDEnvironmentVariable, serverZD)
	stateDirectory := t.TempDir()
	engine, err := agentstest.NewPrivateDexEngine(filepath.Join(stateDirectory, "engine"))
	require.NoError(t, err)
	require.NoError(t, engine.Start(context.Background()))
	t.Cleanup(engine.Kill)
	stack := &realWorkerStack{
		t: t, pythonPath: pythonPath, dexcli: dexcli, stateDirectory: stateDirectory, address: engine.Address(),
		home: filepath.Join(stateDirectory, "home"), coordination: filepath.Join(stateDirectory, "coordination"),
	}
	require.NoError(t, os.MkdirAll(stack.home, 0o700))
	require.NoError(t, os.MkdirAll(stack.coordination, 0o700))
	stack.worker = agentstest.NewKillableProcess("Python session Worker", filepath.Join(stateDirectory, "worker.log"))
	t.Cleanup(stack.worker.Kill)
	pythonCore, err := filepath.Abs(pythonCoreDirectory)
	require.NoError(t, err)
	script, err := filepath.Abs(remoteSessionWorkerScript)
	require.NoError(t, err)
	workerEnvironment := append(os.Environ(),
		"HOME="+stack.home,
		"PYTHONUNBUFFERED=1",
		"DEX_AGENTS_SERVER_ADDRESS="+stack.address,
		"DEX_AGENTS_BLOB_CACHE_DIR="+filepath.Join(stateDirectory, "worker-blob-cache"),
		"REMOTE_SESSION_STATE_DIR="+stack.coordination,
		"DEX_CONNECTORS_MCP_BINARY="+filepath.Join(stateDirectory, "dex-connectors-mcp-not-installed"),
		"DEX_AGENTS_OPEN_BROWSER=0",
	)
	require.NoError(t, stack.worker.Start(context.Background(),
		[]string{uvPath, "run", "--project", pythonCore, "--frozen", "python", script, realWorkerFlowType},
		workerEnvironment, agentstest.WorkerReadyLine, realWorkerTimeout))
	stack.connectClients()
	t.Cleanup(func() {
		if t.Failed() {
			workerLog, _ := stack.worker.ReadLog()
			t.Logf("worker log: %s", tail(workerLog, 4000))
		}
	})
	return stack
}

// connectClients makes a starter, through a typing-only Flow with the
// starting Step a client-only Flow lacks, and a remote observer.
func (stack *realWorkerStack) connectClients() {
	t := stack.t
	definition, err := builtinloop.NewAgentDefinition("typing-only", "unused",
		agentstest.NewScriptedModelClient(func(builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
			return agentstest.ScriptedModelReply{}, nil
		}))
	require.NoError(t, err)
	startingFlow, err := builtinloop.NewBuiltinAgentSessionFlow(definition, realWorkerFlowType)
	require.NoError(t, err)
	stack.starter = dexagents.NewAgentSessionClient(stack.newClient(startingFlow, "starter-blob-cache"), startingFlow)
	remoteFlow, err := remotesession.NewRemoteSessionFlow(realWorkerFlowType)
	require.NoError(t, err)
	stack.sessions = remotesession.NewRemoteSessionClient(stack.newClient(remoteFlow, "observer-blob-cache"), remoteFlow)
}

func (stack *realWorkerStack) newClient(flow dex.Flow, cacheName string) *dex.Client {
	t := stack.t
	registry, err := dex.NewRegistry([]dex.Flow{flow})
	require.NoError(t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stack.stateDirectory, cacheName))
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: stack.address})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return client
}

func (stack *realWorkerStack) startSession(t *testing.T, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := stack.starter.StartSession(ctx, sessionID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
}

func (stack *realWorkerStack) testApprovalRoundTrip(t *testing.T) {
	sessionID := "terminal-ui-approval"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("publish hello\r")
	interrupt := stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.PendingInterrupts) == 1
	}).PendingInterrupts[0]
	require.Equal(t, "publish_note", interrupt.ToolName)
	chat.waitForScreen("Approve publish_note?")
	chat.typeText("y")
	finished := stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	require.Equal(t, "publish_note: published hello", lastAssistantText(finished))
	chat.waitForScreen("publish_note: published hello")
	chat.exit()
	require.Contains(t, chat.screen(), "Session "+sessionID+" stays in Dex")
}

func (stack *realWorkerStack) testResumeShowsHistory(t *testing.T) {
	sessionID := "terminal-ui-approval"
	chat := stack.openChat(t, sessionID, true)
	chat.waitForScreen("publish hello")
	chat.waitForScreen("publish_note: published hello")
	chat.typeText("say again\r")
	finished := stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(2))
	require.Equal(t, "said: again", lastAssistantText(finished))
	chat.waitForScreen("said: again")
	require.Equal(t, 1, strings.Count(chat.screenSince("publish_note: published hello"), "say again"),
		"the message this terminal sent shows once")
	chat.exit()
}

func (stack *realWorkerStack) testCtrlCCancelsTheTurn(t *testing.T) {
	sessionID := "terminal-ui-cancel"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("stall cancel-gate\r")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(stack.coordination, "cancel-gate.reached"))
		return err == nil
	}, realWorkerTimeout, 50*time.Millisecond, "the model Step waits at the gate")
	running := stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return snapshot.State.ActiveTurnID != nil
	})
	require.True(t, running.State.AcceptsTurnControl)
	// Ctrl-C cancels only once the chat shows the running turn.
	chat.waitForScreen("Ctrl-C cancel turn")
	chat.typeText("\x03")
	chat.waitForScreen("cancelling")
	require.NoError(t, os.WriteFile(filepath.Join(stack.coordination, "cancel-gate.released"), nil, 0o600))
	stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	chat.waitForScreen("turn cancelled")
	chat.exit()
}

// chatRun is one dexcli agents-tui on a PTY, with the contract host as a
// launcher that keeps the channel open until dexcli exits.
type chatRun struct {
	t          *testing.T
	keys       io.WriteCloser
	screenPath string
	hostDone   chan error
	hostStderr *bytes.Buffer
}

func (stack *realWorkerStack) openChat(t *testing.T, sessionID string, isResumed bool) *chatRun {
	directory := t.TempDir()
	launch := map[string]any{
		"type": "launch", "contract": 1, "mode": "chat",
		"command": map[string]any{"name": "dex-agents-test", "harness": "test", "language": "python", "version": "0.0.0"},
		"dex": map[string]any{
			"server_address": stack.address,
			"tls":            map[string]any{"enabled": false, "ca_file": nil, "server_name": nil},
			"auth":           map[string]any{"mode": "none"},
			"web_url":        nil,
		},
		"session":      map[string]any{"flow_type": realWorkerFlowType, "session_id": sessionID, "is_resumed": isResumed},
		"display":      map[string]any{"workspace": "~/workspace"},
		"methods":      []string{},
		"capabilities": map[string]any{"plan_mode": true, "turn_control": true},
		"notices":      []any{},
		"log_path":     "~/.dex/agents/test.log",
	}
	transcript := map[string]any{
		"name": "real_worker_chat", "description": "Hold the channel open while the test types into dexcli.",
		"real_roles": []string{"tui"}, "terminal_ui_environment": map[string]any{},
		"steps": []any{
			map[string]any{"from": "launcher", "message": launch},
			map[string]any{"from": "tui", "message": map[string]any{"type": "ready"}},
			map[string]any{"close": "tui"},
		},
		"tui_exit": 0,
	}
	transcriptPath := filepath.Join(directory, "transcript.json")
	encoded, err := json.Marshal(transcript)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(transcriptPath, encoded, 0o600))
	recordPath := filepath.Join(directory, "record.jsonl")
	chat := &chatRun{t: t, screenPath: filepath.Join(directory, "screen.log"), hostDone: make(chan error, 1), hostStderr: &bytes.Buffer{}}
	host := exec.Command(stack.pythonPath, contractHostScript, "launcher",
		"--transcript", transcriptPath, "--pty", "110x32", "--screen-log", chat.screenPath, "--record", recordPath,
		"--step-timeout", "180", "--pty-input-from-stdin", "--", stack.dexcli, "agents-tui")
	host.Env = []string{"HOME=" + stack.home, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH"),
		"DEX_AGENTS_TUI_DEBUG=" + os.Getenv("DEX_AGENTS_TUI_DEBUG")}
	chat.keys, err = host.StdinPipe()
	require.NoError(t, err)
	host.Stderr = chat.hostStderr
	require.NoError(t, host.Start())
	go func() { chat.hostDone <- host.Wait() }()
	t.Cleanup(func() {
		if host.ProcessState == nil {
			if err := host.Process.Kill(); err != nil {
				t.Logf("kill the contract host: %v", err)
			}
			<-chat.hostDone
		}
		if t.Failed() {
			t.Logf("host stderr: %s\nscreen: %q", chat.hostStderr.String(), tail(chat.screen(), 4000))
		}
	})
	require.Eventually(t, func() bool { return countLines(recordPath) >= 3 }, 10*time.Second, 20*time.Millisecond, "dexcli sent ready")
	return chat
}

func (chat *chatRun) typeText(text string) {
	chat.t.Helper()
	_, err := io.WriteString(chat.keys, text)
	require.NoError(chat.t, err)
}

func (chat *chatRun) screen() string {
	data, err := os.ReadFile(chat.screenPath)
	if err != nil {
		return ""
	}
	return terminalEscapes.ReplaceAllString(string(data), "")
}

// screenSince returns the screen output after the first occurrence of mark.
func (chat *chatRun) screenSince(mark string) string {
	text := chat.screen()
	if index := strings.Index(text, mark); index >= 0 {
		return text[index+len(mark):]
	}
	return text
}

func (chat *chatRun) waitForScreen(text string) {
	chat.t.Helper()
	require.Eventually(chat.t, func() bool { return strings.Contains(chat.screen(), text) },
		screenWait, 50*time.Millisecond, "the screen shows %q", text)
}

// exit presses Ctrl-D on the empty input; the host checks exit 0.
func (chat *chatRun) exit() {
	chat.t.Helper()
	chat.typeText("\x04")
	select {
	case err := <-chat.hostDone:
		require.NoError(chat.t, err, "the host saw the expected exit")
	case <-time.After(30 * time.Second):
		chat.t.Fatal("dexcli did not exit")
	}
}

func (stack *realWorkerStack) waitForSnapshot(t *testing.T, sessionID string, isDone func(dexagents.AgentSnapshot) bool) dexagents.AgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(realWorkerTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		snapshot, err := stack.sessions.GetAgentSnapshot(ctx, sessionID)
		cancel()
		if err == nil && snapshot.State != nil && isDone(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session did not reach the state; last error: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isIdleAfterTurns(turnCount int64) func(dexagents.AgentSnapshot) bool {
	return func(snapshot dexagents.AgentSnapshot) bool {
		return snapshot.State.TurnCount == turnCount && snapshot.State.ActiveTurnID == nil &&
			len(snapshot.PendingInterrupts) == 0 && len(snapshot.QueuedMessages) == 0
	}
}

func lastAssistantText(snapshot dexagents.AgentSnapshot) string {
	for index := len(snapshot.Messages) - 1; index >= 0; index-- {
		message := snapshot.Messages[index]
		if message.Role == dexagents.MessageRoleAssistant && message.Text != nil {
			return *message.Text
		}
	}
	return ""
}
