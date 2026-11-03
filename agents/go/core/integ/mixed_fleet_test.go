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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/sdk-go/dex"
)

// awaitToolApprovalOnlyFlow serves only StartAgentSession and AwaitToolApproval, and no RPC, of a session Flow type.
type awaitToolApprovalOnlyFlow struct {
	*dexagents.AgentSessionFlow
}

func (flow awaitToolApprovalOnlyFlow) GetSteps() []dex.StepDef {
	steps := flow.AgentSessionFlow.GetSteps()
	return []dex.StepDef{steps[0], steps[3]}
}

// GetRPCs serves no RPCs; the Python Worker serves them.
func (awaitToolApprovalOnlyFlow) GetRPCs() []dex.RPCDef {
	return nil
}

// A Go Step that lands in a turn a Python Worker with steering began fails the session with a
// reason, instead of waiting where cancel_turn could never wake it.
func TestGoStepInATurnAWorkerWithSteeringBeganFailsClearly(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	pythonCore, err := filepath.Abs(pythonCoreDir)
	require.NoError(t, err)
	scriptPath, err := filepath.Abs(filepath.Join("testdata", "python", "worker_without_await_tool_approval.py"))
	require.NoError(t, err)
	pythonWorker := agentstest.NewKillableProcess("Python Worker", filepath.Join(stack.stateDir, "python-worker.log"))
	stack.workers = append(stack.workers, pythonWorker)
	require.NoError(t, pythonWorker.Start(context.Background(),
		[]string{"uv", "run", "--project", pythonCore, "--frozen", "python", scriptPath, pythonCore},
		append(os.Environ(),
			serverAddressEnvironmentVariable+"="+stack.engine.Address(),
			blobCacheDirEnvironmentVariable+"="+filepath.Join(stack.stateDir, "python-worker-blob-cache"),
			"STEPWISE_AGENT_STATE_DIR="+stack.coordinationDir,
			"PYTHONUNBUFFERED=1",
		), agentstest.WorkerReadyLine, workerReadyTimeout))
	goFlow, err := newTypingOnlyFlow(pythonStepwiseFlowType)
	require.NoError(t, err)
	stack.startInProcessWorker([]dex.Flow{awaitToolApprovalOnlyFlow{goFlow}})
	clientFlow, err := newTypingOnlyFlow(pythonStepwiseFlowType)
	require.NoError(t, err)
	stack.addClientFlow(clientFlow)
	sessions := stack.sessions(pythonStepwiseFlowType)
	ctx := context.Background()

	_, err = sessions.StartSession(ctx, "mixed-fleet", ptr("publish hello"), dex.StartFlowOptions{})
	require.NoError(t, err)
	waitContext, cancel := context.WithTimeout(ctx, sessionConvergenceTimeout)
	defer cancel()
	result, err := stack.client.WaitForFlow(waitContext, "mixed-fleet", dex.WaitForFlowOptions{})
	require.NoError(t, err, "the session must end instead of stalling")
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Contains(t, result.ErrorMessage, "this Go Worker cannot serve session mixed-fleet")
	require.Contains(t, result.ErrorMessage, "a Worker with steering began the active turn")
	require.Contains(t, stack.executedStepTypes("mixed-fleet"), "AwaitToolApproval")
	require.Zero(t, stack.callCount("publish_note"), "the write never runs")
}
