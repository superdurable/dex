// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package integ

import (
	"context"
	"errors"
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
	workerRoleEnvironmentVariable       = "DEX_AGENTS_GO_TEST_WORKER"
	serverAddressEnvironmentVariable    = "DEX_AGENTS_SERVER_ADDRESS"
	blobCacheDirEnvironmentVariable     = "DEX_AGENTS_BLOB_CACHE_DIR"
	coordinationDirEnvironmentVariable  = "DEX_AGENTS_COORDINATION_DIR"
	sessionConvergenceTimeout           = 90 * time.Second
	sessionConvergencePollInterval      = 100 * time.Millisecond
	workerReadyTimeout                  = time.Minute
	scriptedWorkerRole                  = "scripted"
	coordinationMarkerTimeout           = 60 * time.Second
	coordinationMarkerPollInterval      = 50 * time.Millisecond
	coordinationReleaseWaitPollInterval = 50 * time.Millisecond
)

func TestMain(m *testing.M) {
	if os.Getenv(workerRoleEnvironmentVariable) == scriptedWorkerRole {
		os.Exit(runScriptedWorker())
	}
	os.Exit(m.Run())
}

func runScriptedWorker() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	flows, err := newScriptedAgentFlows(os.Getenv(coordinationDirEnvironmentVariable))
	if err == nil {
		err = agentstest.ServeAgentWorker(ctx, flows,
			os.Getenv(serverAddressEnvironmentVariable), os.Getenv(blobCacheDirEnvironmentVariable))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scripted Worker failed:", err)
		return 1
	}
	return 0
}

// agentTestStack owns one private engine, its Worker children, and a test-side Client.
type agentTestStack struct {
	t               *testing.T
	stateDir        string
	coordinationDir string
	engine          *agentstest.PrivateDexEngine
	client          *dex.Client
	flows           scriptedAgentFlows
	workers         []*agentstest.KillableProcess
}

func newAgentTestStack(t *testing.T) *agentTestStack {
	t.Helper()
	return newAgentTestStackWithServerZDConfig(t, "")
}

// newAgentTestStackWithServerZDConfig starts the engine with a dex-server-zd YAML configuration,
// or with the defaults when serverZDConfig is empty.
func newAgentTestStackWithServerZDConfig(t *testing.T, serverZDConfig string) *agentTestStack {
	t.Helper()
	stateDir := t.TempDir()
	stack := &agentTestStack{t: t, stateDir: stateDir, coordinationDir: filepath.Join(stateDir, "coordination")}
	require.NoError(t, os.MkdirAll(stack.coordinationDir, 0o755))
	configPath := ""
	if serverZDConfig != "" {
		configPath = filepath.Join(stateDir, "server-zd.yaml")
		// A config of its own replaces the test engine's process-crash-safe durability.
		configText := serverZDConfig + "dev:\n  durabilityMode: processCrashSafe\n"
		require.NoError(t, os.WriteFile(configPath, []byte(configText), 0o600))
	}
	engine, err := agentstest.NewPrivateDexEngineWithServerZDConfig(filepath.Join(stateDir, "engine"), configPath)
	require.NoError(t, err)
	stack.engine = engine
	require.NoError(t, engine.Start(context.Background()))
	t.Cleanup(stack.close)
	flowList, err := newScriptedAgentFlows(stack.coordinationDir)
	require.NoError(t, err)
	stack.flows = scriptedAgentFlowsByType(flowList)
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

// startWorker starts a new Worker child and waits until it serves every pull queue.
func (stack *agentTestStack) startWorker() *agentstest.KillableProcess {
	stack.t.Helper()
	index := len(stack.workers) + 1
	worker := agentstest.NewKillableProcess(fmt.Sprintf("Worker %d", index),
		filepath.Join(stack.stateDir, fmt.Sprintf("worker-%d.log", index)))
	stack.workers = append(stack.workers, worker)
	environment := append(os.Environ(),
		workerRoleEnvironmentVariable+"="+scriptedWorkerRole,
		serverAddressEnvironmentVariable+"="+stack.engine.Address(),
		blobCacheDirEnvironmentVariable+"="+filepath.Join(stack.stateDir, fmt.Sprintf("worker-blob-cache-%d", index)),
		coordinationDirEnvironmentVariable+"="+stack.coordinationDir,
	)
	require.NoError(stack.t, worker.Start(context.Background(), []string{os.Args[0], "-test.run=^$"},
		environment, agentstest.WorkerReadyLine, workerReadyTimeout))
	return worker
}

// startPythonWorker starts the Python core's Stepwise test Worker as a child process.
func (stack *agentTestStack) startPythonWorker() *agentstest.KillableProcess {
	stack.t.Helper()
	index := len(stack.workers) + 1
	worker := agentstest.NewKillableProcess(fmt.Sprintf("Python Worker %d", index),
		filepath.Join(stack.stateDir, fmt.Sprintf("python-worker-%d.log", index)))
	stack.workers = append(stack.workers, worker)
	pythonCore, err := filepath.Abs(pythonCoreDir)
	require.NoError(stack.t, err)
	environment := append(os.Environ(),
		serverAddressEnvironmentVariable+"="+stack.engine.Address(),
		blobCacheDirEnvironmentVariable+"="+filepath.Join(stack.stateDir, fmt.Sprintf("python-worker-blob-cache-%d", index)),
		"STEPWISE_AGENT_STATE_DIR="+stack.coordinationDir,
		"PYTHONUNBUFFERED=1",
	)
	require.NoError(stack.t, worker.Start(context.Background(),
		[]string{"uv", "run", "--project", pythonCore, "--directory", pythonCore, "--frozen", "python", "-m", "tests.stepwise_worker"},
		environment, agentstest.WorkerReadyLine, workerReadyTimeout))
	return worker
}

// startInProcessWorker serves flows from this process until the test ends.
func (stack *agentTestStack) startInProcessWorker(flows []dex.Flow) {
	stack.t.Helper()
	registry, err := dex.NewRegistry(flows)
	require.NoError(stack.t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stack.stateDir, fmt.Sprintf("in-process-worker-cache-%d", len(stack.workers))))
	require.NoError(stack.t, err)
	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: stack.engine.Address()})
	require.NoError(stack.t, err)
	served := make(chan error, 1)
	go func() { served <- worker.Start() }()
	registrationContext, cancel := context.WithTimeout(context.Background(), workerReadyTimeout)
	defer cancel()
	require.NoError(stack.t, worker.WaitPullRegistered(registrationContext))
	stack.t.Cleanup(func() {
		stopContext, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStop()
		if err := errors.Join(worker.Stop(stopContext), <-served); err != nil {
			stack.t.Logf("stop the in-process Worker: %v", err)
		}
	})
}

// addClientFlow registers another Flow in a new test-side Client, which only types the calls.
func (stack *agentTestStack) addClientFlow(flow *dexagents.AgentSessionFlow) {
	stack.t.Helper()
	stack.flows[flow.GetFlowType()] = flow
	flows := make([]dex.Flow, 0, len(stack.flows))
	for _, registered := range stack.flows {
		flows = append(flows, registered)
	}
	registry, err := dex.NewRegistry(flows)
	require.NoError(stack.t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stack.stateDir, fmt.Sprintf("client-blob-cache-%d", len(flows))))
	require.NoError(stack.t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: stack.engine.Address()})
	require.NoError(stack.t, err)
	require.NoError(stack.t, stack.client.Close())
	stack.client = client
}

func (stack *agentTestStack) sessions(flowType string) *dexagents.AgentSessionClient {
	flow, isKnown := stack.flows[flowType]
	require.True(stack.t, isKnown, flowType)
	return dexagents.NewAgentSessionClient(stack.client, flow)
}

// waitForSnapshot polls get_agent_snapshot until isDone accepts it.
func (stack *agentTestStack) waitForSnapshot(
	flowType string,
	sessionID string,
	description string,
	isDone func(dexagents.AgentSnapshot) bool,
) dexagents.AgentSnapshot {
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
		time.Sleep(sessionConvergencePollInterval)
	}
	stack.t.Fatalf("session %s never reached %s; last snapshot %+v, error %v", sessionID, description, last, lastErr)
	return last
}

func (stack *agentTestStack) waitForTurnCount(flowType string, sessionID string, turnCount int64) dexagents.AgentSnapshot {
	stack.t.Helper()
	return stack.waitForSnapshot(flowType, sessionID, fmt.Sprintf("turn count %d", turnCount),
		func(snapshot dexagents.AgentSnapshot) bool {
			return snapshot.State != nil && snapshot.State.TurnCount >= turnCount && snapshot.State.ActiveTurnID == nil
		})
}

func (stack *agentTestStack) waitForPendingInterrupts(flowType string, sessionID string, count int) dexagents.AgentSnapshot {
	stack.t.Helper()
	return stack.waitForSnapshot(flowType, sessionID, fmt.Sprintf("%d pending interrupts", count),
		func(snapshot dexagents.AgentSnapshot) bool { return len(snapshot.PendingInterrupts) == count })
}

func (stack *agentTestStack) executedStepTypes(sessionID string) []string {
	stack.t.Helper()
	history, err := agentstest.ReadStepHistory(context.Background(), stack.engine.Dexcli(), stack.engine.Address(), sessionID)
	require.NoError(stack.t, err)
	return agentstest.ExecutedStepTypes(history)
}

func (stack *agentTestStack) stepHistory(sessionID string) []agentstest.StepHistoryEntry {
	stack.t.Helper()
	history, err := agentstest.ReadStepHistory(context.Background(), stack.engine.Dexcli(), stack.engine.Address(), sessionID)
	require.NoError(stack.t, err)
	return history
}

// waitForMarker waits until a Worker child writes a coordination file.
func (stack *agentTestStack) waitForMarker(name string) {
	stack.t.Helper()
	deadline := time.Now().Add(coordinationMarkerTimeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(stack.coordinationDir, name)); err == nil {
			return
		}
		time.Sleep(coordinationMarkerPollInterval)
	}
	stack.t.Fatalf("the Worker never wrote %s", name)
}

func (stack *agentTestStack) callCount(toolName string) int {
	stack.t.Helper()
	content, err := os.ReadFile(filepath.Join(stack.coordinationDir, toolName+".calls"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(stack.t, err)
	return strings.Count(string(content), "\n")
}

func ptr[Value any](value Value) *Value {
	return &value
}
