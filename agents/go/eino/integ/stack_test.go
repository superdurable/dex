// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package integ runs the Eino adapter against a private Dex engine with scripted Eino models.
package integ

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Environment of a Worker child: the test binary runs again as a Worker when these are set.
const (
	workerRoleVariable        = "DEX_AGENTS_EINO_TEST_WORKER"
	serverAddressVariable     = "DEX_AGENTS_EINO_SERVER_ADDRESS"
	blobCacheDirVariable      = "DEX_AGENTS_EINO_BLOB_CACHE_DIR"
	coordinationDirVariable   = "DEX_AGENTS_EINO_COORDINATION_DIR"
	deployVariable            = "DEX_AGENTS_EINO_DEPLOY"
	openAIBaseURLVariable     = "DEX_AGENTS_EINO_OPENAI_BASE_URL"
	einoWorkerRole            = "eino"
	sessionConvergenceTimeout = 90 * time.Second
	sessionPollInterval       = 100 * time.Millisecond
	workerReadyTimeout        = time.Minute
	coordinationTimeout       = 60 * time.Second
	coordinationPollInterval  = 50 * time.Millisecond
)

func TestMain(m *testing.M) {
	if os.Getenv(workerRoleVariable) == einoWorkerRole {
		os.Exit(runEinoWorker())
	}
	os.Exit(m.Run())
}

func runEinoWorker() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	flows, err := newEinoAgentFlows(os.Getenv(coordinationDirVariable), agentDeploy(os.Getenv(deployVariable)),
		os.Getenv(openAIBaseURLVariable))
	if err == nil {
		err = agentstest.ServeAgentWorker(ctx, flows, os.Getenv(serverAddressVariable), os.Getenv(blobCacheDirVariable))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Eino test Worker failed:", err)
		return 1
	}
	return 0
}

// agentTestStack owns one private engine, its Worker children, and a test-side Client.
type agentTestStack struct {
	t             *testing.T
	stateDir      string
	coordination  coordinationFiles
	engine        *agentstest.PrivateDexEngine
	client        *dex.Client
	flows         map[string]*dexagents.AgentSessionFlow
	workers       []*agentstest.KillableProcess
	openAIBaseURL string
}

func newAgentTestStack(t *testing.T) *agentTestStack {
	t.Helper()
	return newAgentTestStackWithOpenAI(t, "")
}

// newAgentTestStackWithOpenAI starts a stack whose Workers send the OpenAI agent's requests to baseURL.
func newAgentTestStackWithOpenAI(t *testing.T, openAIBaseURL string) *agentTestStack {
	t.Helper()
	stateDir := t.TempDir()
	stack := &agentTestStack{t: t, stateDir: stateDir, coordination: coordinationFiles{dir: filepath.Join(stateDir, "coordination")},
		openAIBaseURL: openAIBaseURL}
	require.NoError(t, os.MkdirAll(stack.coordination.dir, 0o755))
	engine, err := agentstest.NewPrivateDexEngine(filepath.Join(stateDir, "engine"))
	require.NoError(t, err)
	stack.engine = engine
	require.NoError(t, engine.Start(context.Background()))
	t.Cleanup(stack.close)
	flowList, err := newEinoAgentFlows(stack.coordination.dir, deployOriginal, openAIBaseURL)
	require.NoError(t, err)
	stack.flows = map[string]*dexagents.AgentSessionFlow{}
	for _, flow := range flowList {
		stack.flows[flow.GetFlowType()] = flow.(*dexagents.AgentSessionFlow)
	}
	registry, err := dex.NewRegistry(flowList)
	require.NoError(t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stateDir, "client-blob-cache"))
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: engine.Address()})
	require.NoError(t, err)
	stack.client = client
	return stack
}

func (stack *agentTestStack) close() {
	for _, worker := range stack.workers {
		worker.Kill()
	}
	if stack.client != nil {
		if err := stack.client.Close(); err != nil {
			stack.t.Logf("close Client: %v", err)
		}
	}
	stack.engine.Kill()
	if stack.t.Failed() {
		stack.dumpLogs()
	}
}

func (stack *agentTestStack) dumpLogs() {
	paths, err := filepath.Glob(filepath.Join(stack.stateDir, "*.log"))
	if err != nil {
		stack.t.Logf("list logs: %v", err)
		return
	}
	for _, path := range append(paths, filepath.Join(stack.stateDir, "engine", "dexcli.log")) {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		if len(text) > 8000 {
			text = text[len(text)-8000:]
		}
		stack.t.Logf("==== %s ====\n%s", path, text)
	}
}

// startWorker starts a Worker child on the original deploy and waits until it serves every pull queue.
func (stack *agentTestStack) startWorker() *agentstest.KillableProcess {
	stack.t.Helper()
	return stack.startWorkerOnDeploy(deployOriginal)
}

// startWorkerOnDeploy starts a Worker child whose code is as deploy left it.
func (stack *agentTestStack) startWorkerOnDeploy(deploy agentDeploy) *agentstest.KillableProcess {
	stack.t.Helper()
	index := len(stack.workers) + 1
	worker := agentstest.NewKillableProcess(fmt.Sprintf("Eino Worker %d", index),
		filepath.Join(stack.stateDir, fmt.Sprintf("worker-%d.log", index)))
	stack.workers = append(stack.workers, worker)
	environment := append(os.Environ(),
		workerRoleVariable+"="+einoWorkerRole,
		serverAddressVariable+"="+stack.engine.Address(),
		blobCacheDirVariable+"="+filepath.Join(stack.stateDir, fmt.Sprintf("worker-blob-cache-%d", index)),
		coordinationDirVariable+"="+stack.coordination.dir,
		deployVariable+"="+string(deploy),
		openAIBaseURLVariable+"="+stack.openAIBaseURL,
	)
	require.NoError(stack.t, worker.Start(context.Background(), []string{os.Args[0], "-test.run=^$"},
		environment, agentstest.WorkerReadyLine, workerReadyTimeout))
	return worker
}

func (stack *agentTestStack) sessions(flowType string) *dexagents.AgentSessionClient {
	flow, isKnown := stack.flows[flowType]
	require.True(stack.t, isKnown, flowType)
	return dexagents.NewAgentSessionClient(stack.client, flow)
}

// waitForSnapshot polls get_agent_snapshot until isDone accepts it.
func (stack *agentTestStack) waitForSnapshot(flowType, sessionID, description string, isDone func(dexagents.AgentSnapshot) bool) dexagents.AgentSnapshot {
	stack.t.Helper()
	sessions := stack.sessions(flowType)
	deadline := time.Now().Add(sessionConvergenceTimeout)
	var last dexagents.AgentSnapshot
	var lastErr error
	for time.Now().Before(deadline) {
		snapshot, err := sessions.GetAgentSnapshot(context.Background(), sessionID)
		if err == nil && isDone(snapshot) {
			return snapshot
		}
		last, lastErr = snapshot, err
		time.Sleep(sessionPollInterval)
	}
	stack.t.Fatalf("session %s never reached %s; last snapshot %+v, error %v", sessionID, description, last, lastErr)
	return last
}

func (stack *agentTestStack) waitForTurnCount(flowType, sessionID string, turnCount int64) dexagents.AgentSnapshot {
	stack.t.Helper()
	return stack.waitForSnapshot(flowType, sessionID, fmt.Sprintf("turn count %d", turnCount),
		func(snapshot dexagents.AgentSnapshot) bool {
			return snapshot.State != nil && snapshot.State.TurnCount >= turnCount && snapshot.State.ActiveTurnID == nil
		})
}

func (stack *agentTestStack) waitForPendingInterrupts(flowType, sessionID string, count int) dexagents.AgentSnapshot {
	stack.t.Helper()
	return stack.waitForSnapshot(flowType, sessionID, fmt.Sprintf("%d pending interrupts", count),
		func(snapshot dexagents.AgentSnapshot) bool { return len(snapshot.PendingInterrupts) == count })
}

func (stack *agentTestStack) stepHistory(sessionID string) []agentstest.StepHistoryEntry {
	stack.t.Helper()
	history, err := agentstest.ReadStepHistory(context.Background(), stack.engine.Dexcli(), stack.engine.Address(), sessionID)
	require.NoError(stack.t, err)
	return history
}

func (stack *agentTestStack) executedStepTypes(sessionID string) []string {
	stack.t.Helper()
	return agentstest.ExecutedStepTypes(stack.stepHistory(sessionID))
}

// finalAttempt returns the attempt that completed the first Step of stepType.
func (stack *agentTestStack) finalAttempt(sessionID, stepType string) int {
	stack.t.Helper()
	for _, entry := range stack.stepHistory(sessionID) {
		if entry.EventType == agentstest.ExecuteCompletedEvent && entry.StepType == stepType {
			return entry.FinalAttempt
		}
	}
	stack.t.Fatalf("session %s completed no %s Step", sessionID, stepType)
	return 0
}

func (stack *agentTestStack) waitForMarker(name string) {
	stack.t.Helper()
	deadline := time.Now().Add(coordinationTimeout)
	for time.Now().Before(deadline) {
		if stack.coordination.hasMarker(name) {
			return
		}
		time.Sleep(coordinationPollInterval)
	}
	stack.t.Fatalf("the Worker never wrote %s", name)
}

func (stack *agentTestStack) releaseMarker(name string) {
	stack.coordination.writeMarker(name + ".release")
}

func (stack *agentTestStack) callCount(name string) int {
	stack.t.Helper()
	content, err := os.ReadFile(filepath.Join(stack.coordination.dir, name+".calls"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(stack.t, err)
	return strings.Count(string(content), "\n")
}

func messageRoles(messages []dexagents.AgentMessage) []dexagents.MessageRole {
	roles := make([]dexagents.MessageRole, 0, len(messages))
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	return roles
}
