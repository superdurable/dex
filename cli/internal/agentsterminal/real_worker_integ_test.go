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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
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
	chatTerminalWidth  = 110
	chatTerminalHeight = 32
	realWorkerFlowType = "TerminalUIRemoteSessionAgent"
	realWorkerTimeout  = 2 * time.Minute
	screenWait         = 30 * time.Second
	approvedPlanReply  = "executed plan"
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
	t.Run("EscCancelsTheTurn", stack.testEscCancelsTheTurn)
	t.Run("IdleCtrlCExitsOnTheSecondPress", stack.testIdleCtrlCExitsOnTheSecondPress)
	t.Run("ResumeKeepsAWaitingApprovalClosed", stack.testResumeKeepsAWaitingApprovalClosed)
	t.Run("QuestionAnswer", stack.testQuestionAnswer)
	t.Run("QueueAndSteer", stack.testQueueAndSteer)
	t.Run("PlanAndRun", stack.testPlanAndRun)
	t.Run("ImageThroughPutImage", stack.testImageThroughPutImage)
	t.Run("PastedImagePathAttaches", stack.testPastedImagePathAttaches)
	t.Run("CtrlOShowsTheToolOutput", stack.testCtrlOShowsTheToolOutput)
	t.Run("HistoryReadsEarlierMessages", stack.testHistoryReadsEarlierMessages)
	t.Run("DebugLogAndStderrStayPrivate", stack.testDebugLogAndStderrStayPrivate)
}

// The debug log names errors by kind only, never by their text, and nothing
// written to fd 2 reaches the terminal. A session that does not exist makes
// the server answer every snapshot with an error that names the session.
func (stack *realWorkerStack) testDebugLogAndStderrStayPrivate(t *testing.T) {
	marker := fmt.Sprintf("private-marker-%d", time.Now().UnixNano())
	probeContext, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	_, probeErr := stack.sessions.GetAgentSnapshot(probeContext, marker)
	cancelProbe()
	require.Error(t, probeErr)
	require.Contains(t, probeErr.Error(), marker, "the server's error text holds the marker")
	chat := stack.openChatWithLaunch(t, marker, false, chatLaunch{methods: []string{}, isDebugLogOn: true})
	chat.waitForScreen("Session not loaded yet")
	dexcliPID := childProcessID(t, chat.host.Process.Pid)
	logPath := filepath.Join(os.TempDir(), fmt.Sprintf("dexcli-agents-tui-%d.log", dexcliPID))
	if _, err := os.Stat(logPath); err != nil {
		logPath = filepath.Join("/tmp", fmt.Sprintf("dexcli-agents-tui-%d.log", dexcliPID))
	}
	t.Cleanup(func() {
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			t.Logf("remove the debug log: %v", err)
		}
	})
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(logPath)
		return err == nil && strings.Contains(string(data), "read the snapshot: grpc")
	}, screenWait, 50*time.Millisecond, "the debug log names the snapshot error by kind")
	// SIGQUIT makes the Go runtime dump every goroutine to fd 2.
	require.NoError(t, syscall.Kill(dexcliPID, syscall.SIGQUIT))
	select {
	case <-chat.hostDone:
	case <-time.After(30 * time.Second):
		t.Fatal("dexcli did not exit after SIGQUIT")
	}
	debugLogText, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NotContains(t, string(debugLogText), marker, "no error text in the debug log")
	require.NotContains(t, string(debugLogText), "goroutine ", "fd 2 never goes to the debug log")
	screen := chat.screen()
	require.NotContains(t, screen, "goroutine ", "fd 2 never reaches the terminal")
	require.NotContains(t, screen, "SIGQUIT")
}

// /plan asks for a planning turn; /run approves the draft through execute_plan.
func (stack *realWorkerStack) testPlanAndRun(t *testing.T) {
	sessionID := "terminal-ui-plan"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("/plan\r")
	chat.waitForScreen("the next message asks for a plan")
	chat.typeText("plan write tests | ship it\r")
	drafted := stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return isIdleAfterTurns(1)(snapshot) && snapshot.Plan != nil && snapshot.Plan.Status == dexagents.PlanStatusDraft
	})
	require.Len(t, drafted.Plan.Tasks, 2)
	require.Equal(t, "drafted 2 tasks", lastAssistantText(drafted))
	chat.waitForScreen("drafted 2 tasks")
	// /run needs the plan state, which the next snapshot brings.
	time.Sleep(time.Second)
	chat.typeText("/run\r")
	executed := stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return isIdleAfterTurns(2)(snapshot) && lastAssistantText(snapshot) == approvedPlanReply
	})
	require.NotNil(t, executed.Plan)
	chat.waitForScreen(approvedPlanReply)
	chat.exit()
}

// /image stores the file through the launcher's put_image, and the next
// message carries the returned reference to the Worker unchanged.
func (stack *realWorkerStack) testImageThroughPutImage(t *testing.T) {
	sessionID := "terminal-ui-image"
	stack.startSession(t, sessionID)
	referenceText, err := os.ReadFile(filepath.Join(stack.coordination, "image-reference.json"))
	require.NoError(t, err)
	var reference map[string]any
	require.NoError(t, json.Unmarshal(referenceText, &reference))
	imagePath := filepath.Join(t.TempDir(), "dot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("\x89PNG stand-in; the launcher side stores images"), 0o600))
	chat := stack.openChatWithLaunch(t, sessionID, false, chatLaunch{
		methods:   []string{"put_image"},
		hasImages: true,
		launcherSteps: []any{
			map[string]any{"from": "tui", "message": map[string]any{
				"type": "request", "id": 1, "idempotency_key": "01a1caff-6ed8-71f4-b2a9-0c3d5e6f7081",
				"method": "put_image", "params": map[string]any{"path": "/home/user/dot.png"},
			}, "ignore": []string{"idempotency_key", "params.path"}},
			map[string]any{"from": "launcher", "message": map[string]any{
				"type": "response", "id": 1, "result": map[string]any{"image": reference, "name": "dot.png"},
			}},
		},
	})
	chat.typeText("/image " + imagePath + "\r")
	chat.waitForScreen("dot.png")
	time.Sleep(300 * time.Millisecond)
	chat.typeText("describe\r")
	finished := stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	require.Equal(t, "saw 1 images", lastAssistantText(finished))
	chat.waitForScreen("saw 1 images")
	chat.exit()
}

// putImageLaunch is a chat launch whose launcher stores one image through
// put_image, answering with the Worker's stored reference.
func (stack *realWorkerStack) putImageLaunch(t *testing.T) chatLaunch {
	referenceText, err := os.ReadFile(filepath.Join(stack.coordination, "image-reference.json"))
	require.NoError(t, err)
	var reference map[string]any
	require.NoError(t, json.Unmarshal(referenceText, &reference))
	return chatLaunch{
		methods:   []string{"put_image"},
		hasImages: true,
		launcherSteps: []any{
			map[string]any{"from": "tui", "message": map[string]any{
				"type": "request", "id": 1, "idempotency_key": "01a1caff-6ed8-71f4-b2a9-0c3d5e6f7081",
				"method": "put_image", "params": map[string]any{"path": "/home/user/dot.png"},
			}, "ignore": []string{"idempotency_key", "params.path"}},
			map[string]any{"from": "launcher", "message": map[string]any{
				"type": "response", "id": 1, "result": map[string]any{"image": reference, "name": "dot.png"},
			}},
		},
	}
}

// A terminal pastes a dropped file as its path; the chat offers to attach
// it, and Enter stores it through put_image.
func (stack *realWorkerStack) testPastedImagePathAttaches(t *testing.T) {
	sessionID := "terminal-ui-paste"
	stack.startSession(t, sessionID)
	imagePath := filepath.Join(t.TempDir(), "dot.png")
	require.NoError(t, os.WriteFile(imagePath, []byte("\x89PNG stand-in; the launcher side stores images"), 0o600))
	chat := stack.openChatWithLaunch(t, sessionID, false, stack.putImageLaunch(t))
	chat.waitForScreen("Type a message.")
	chat.typeText("\x1b[200~'" + imagePath + "'\x1b[201~")
	chat.waitForScreen("Attach dot.png?")
	chat.typeText("\r")
	chat.waitForScreen("image/png")
	time.Sleep(300 * time.Millisecond)
	chat.typeText("describe\r")
	finished := stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	require.Equal(t, "saw 1 images", lastAssistantText(finished))
	chat.waitForScreen("saw 1 images")
	chat.exit()
}

// Ctrl-O opens the last tool's stored output with its arguments; raw mode
// keeps ^O from the terminal's discard character.
func (stack *realWorkerStack) testCtrlOShowsTheToolOutput(t *testing.T) {
	sessionID := "terminal-ui-tool-output"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("publish pager note\r")
	chat.waitForScreen("Approve publish_note?")
	chat.typeText("y")
	stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	chat.waitForScreen("publish_note: published pager note")
	chat.typeText("\x0f")
	chat.waitForScreen("Tool output")
	chat.waitForScreen("Arguments")
	require.Contains(t, chat.screenSince("Tool output"), "published pager note", "the pager shows the stored output")
	chat.typeText("q")
	chat.waitForFrame("Ctrl-D exit")
	chat.exit()
}

// A resumed chat shows the newest page; /history reads the page before it.
func (stack *realWorkerStack) testHistoryReadsEarlierMessages(t *testing.T) {
	sessionID := "terminal-ui-history"
	stack.startSession(t, sessionID)
	// Two stored messages per turn; 26 turns leave one page above the newest 50.
	for turn := 1; turn <= 26; turn++ {
		text := fmt.Sprintf("say filler %d", turn)
		if turn == 1 {
			text = "say oldest-entry"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		isQueued, err := stack.sessions.SendUserMessage(ctx, sessionID, dexagents.AgentUserMessage{Text: text})
		cancel()
		require.NoError(t, err)
		require.True(t, isQueued)
		stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(int64(turn)))
	}
	chat := stack.openChat(t, sessionID, true)
	// The renderer redraws only changed cells, so wait for a unique text.
	chat.waitForScreen("turn 26")
	require.NotContains(t, chat.screen(), "oldest-entry", "the first turn is above the newest page")
	chat.typeText("/history\r")
	chat.waitForScreen("Earlier messages")
	chat.waitForScreen("said: oldest-entry")
	chat.waitForScreen("start of the session")
	chat.typeText("q")
	chat.exit()
}

func (stack *realWorkerStack) testQuestionAnswer(t *testing.T) {
	sessionID := "terminal-ui-question"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("ask Which color? | red | blue\r")
	stack.waitForSnapshot(t, sessionID, func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.PendingInterrupts) == 1 && snapshot.PendingInterrupts[0].Kind == dexagents.InterruptKindQuestion
	})
	// The tool row shows the question before the popup opens; wait for the
	// popup's hints.
	chat.waitForScreen("Enter answer")
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
	chat.waitForScreen("Esc interrupt")
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
	// The real command shows the Dex logo in the welcome area.
	chat.waitForScreen("Super Durable")
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
	chat.waitForFrame("said: again")
	// The raw log may hold a redrawn line twice; the frame shows what the user sees.
	require.Equal(t, 1, strings.Count(chat.frame(), "say again"), "the message this terminal sent shows once")
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
	chat.waitForScreen("Esc interrupt")
	chat.typeText("\x03")
	chat.waitForScreen("cancelling")
	require.NoError(t, os.WriteFile(filepath.Join(stack.coordination, "cancel-gate.released"), nil, 0o600))
	stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	chat.waitForScreen("turn cancelled")
	chat.exit()
}

// Esc on the empty input cancels the running turn, as Ctrl-C does.
func (stack *realWorkerStack) testEscCancelsTheTurn(t *testing.T) {
	sessionID := "terminal-ui-esc-cancel"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("stall esc-gate\r")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(stack.coordination, "esc-gate.reached"))
		return err == nil
	}, realWorkerTimeout, 50*time.Millisecond, "the model Step waits at the gate")
	chat.waitForScreen("Esc interrupt")
	chat.typeText("\x1b")
	chat.waitForScreen("cancelling")
	require.NoError(t, os.WriteFile(filepath.Join(stack.coordination, "esc-gate.released"), nil, 0o600))
	stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	chat.waitForScreen("turn cancelled")
	chat.exit()
}

// An idle Ctrl-C asks for a second press, which exits.
func (stack *realWorkerStack) testIdleCtrlCExitsOnTheSecondPress(t *testing.T) {
	sessionID := "terminal-ui-idle-ctrl-c"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.waitForFrame("Ctrl-D exit")
	chat.typeText("\x03")
	chat.waitForFrame("Press Ctrl-C again to exit")
	chat.typeText("\x03")
	chat.waitForHostExit()
}

// A resumed chat leaves a waiting approval in the pending bar, so the user
// reads the history before Tab opens it.
func (stack *realWorkerStack) testResumeKeepsAWaitingApprovalClosed(t *testing.T) {
	sessionID := "terminal-ui-resume-approval"
	stack.startSession(t, sessionID)
	chat := stack.openChat(t, sessionID, false)
	chat.typeText("publish later\r")
	chat.waitForScreen("Approve publish_note?")
	chat.typeText("\x1b")
	chat.waitForFrame("1 approval waiting")
	chat.exit()
	resumed := stack.openChat(t, sessionID, true)
	resumed.waitForFrame("1 approval waiting")
	resumed.waitForFrame("Tab opens approvals")
	// The deferred popup would open after a second; this one never does.
	time.Sleep(1500 * time.Millisecond)
	require.NotContains(t, resumed.screen(), "Approve publish_note?")
	resumed.typeText("\t")
	time.Sleep(300 * time.Millisecond)
	resumed.typeText("\r")
	resumed.waitForScreen("Approve publish_note?")
	resumed.typeText("y")
	stack.waitForSnapshot(t, sessionID, isIdleAfterTurns(1))
	resumed.waitForScreen("publish_note: published later")
	resumed.exit()
}

// chatRun is one dexcli agents-tui on a PTY, with the contract host as a
// launcher that keeps the channel open until dexcli exits.
type chatRun struct {
	t          *testing.T
	racePrefix string
	keys       io.WriteCloser
	screenPath string
	hostDone   chan error
	hostStderr *bytes.Buffer
	host       *exec.Cmd
}

// chatLaunch adds launcher methods and the steps the launcher plays after
// ready to a chat run.
type chatLaunch struct {
	methods       []string
	hasImages     bool
	launcherSteps []any
	isDebugLogOn  bool
}

func (stack *realWorkerStack) openChat(t *testing.T, sessionID string, isResumed bool) *chatRun {
	return stack.openChatWithLaunch(t, sessionID, isResumed, chatLaunch{methods: []string{}})
}

func (stack *realWorkerStack) openChatWithLaunch(t *testing.T, sessionID string, isResumed bool, options chatLaunch) *chatRun {
	directory := t.TempDir()
	capabilities := map[string]any{"plan_mode": true, "turn_control": true}
	if options.hasImages {
		capabilities["images"] = map[string]any{"max_per_message": 20, "max_bytes": 3750000}
	}
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
		"methods":      options.methods,
		"capabilities": capabilities,
		"notices":      []any{},
		"log_path":     "~/.dex/agents/test.log",
	}
	transcript := map[string]any{
		"name": "real_worker_chat", "description": "Hold the channel open while the test types into dexcli.",
		"real_roles": []string{"tui"}, "terminal_ui_environment": map[string]any{},
		"steps": append(append([]any{
			map[string]any{"from": "launcher", "message": launch},
			map[string]any{"from": "tui", "message": map[string]any{"type": "ready"}},
		}, options.launcherSteps...), map[string]any{"close": "tui"}),
		"tui_exit": 0,
	}
	transcriptPath := filepath.Join(directory, "transcript.json")
	encoded, err := json.Marshal(transcript)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(transcriptPath, encoded, 0o600))
	recordPath := filepath.Join(directory, "record.jsonl")
	chat := &chatRun{t: t, racePrefix: filepath.Join(directory, "race"), screenPath: filepath.Join(directory, "screen.log"), hostDone: make(chan error, 1), hostStderr: &bytes.Buffer{}}
	host := exec.Command(stack.pythonPath, contractHostScript, "launcher",
		"--transcript", transcriptPath, "--pty", fmt.Sprintf("%dx%d", chatTerminalWidth, chatTerminalHeight), "--screen-log", chat.screenPath, "--record", recordPath,
		"--step-timeout", "180", "--pty-input-from-stdin", raceEnvironmentArgument(chat.racePrefix), "--", stack.dexcli, "agents-tui")
	debugLog := os.Getenv("DEX_AGENTS_TUI_DEBUG")
	if options.isDebugLogOn {
		debugLog = "1"
	}
	host.Env = []string{"HOME=" + stack.home, "TERM=xterm-256color", "LANG=en_US.UTF-8", "PATH=" + os.Getenv("PATH"),
		"DEX_AGENTS_TUI_DEBUG=" + debugLog}
	chat.host = host
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

// frame returns the screen as the terminal shows it now.
func (chat *chatRun) frame() string {
	data, err := os.ReadFile(chat.screenPath)
	if err != nil {
		return ""
	}
	return strings.Join(terminalFrame(string(data), chatTerminalWidth, chatTerminalHeight), "\n")
}

// waitForFrame waits until the current frame shows text. Unlike the log,
// it holds a line redrawn from its middle.
func (chat *chatRun) waitForFrame(text string) {
	chat.t.Helper()
	require.Eventually(chat.t, func() bool { return strings.Contains(chat.frame(), text) },
		screenWait, 50*time.Millisecond, "the frame shows %q", text)
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
	chat.waitForHostExit()
}

// waitForHostExit waits for the host, which checks every step and exit 0.
func (chat *chatRun) waitForHostExit() {
	chat.t.Helper()
	select {
	case err := <-chat.hostDone:
		require.NoError(chat.t, err, "the host saw the expected exit")
		requireNoRaceReports(chat.t, chat.racePrefix)
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
