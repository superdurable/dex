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
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// A Worker killed while an idempotent_write runs, and restarted on a deploy that renamed or removed
// the tool, records outcome_unknown: the first attempt may have taken effect. The turn still completes.
func TestDeployThatRenamedOrRemovedARunningToolRecordsOutcomeUnknown(t *testing.T) {
	t.Parallel()
	for name, deploy := range map[string]agentDeploy{"renamed": deployRenamed, "removed": deployRemoved} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stack := newAgentTestStack(t)
			worker := stack.startWorker()
			sessions := stack.sessions(einoAgentFlowType)
			ctx := context.Background()

			_, err := sessions.StartSession(ctx, "refund", ptr("idempotent refund model-gate order-5"), dex.StartFlowOptions{})
			require.NoError(t, err)
			stack.waitForMarker("issue_refund.started")
			worker.Kill()
			stack.startWorkerOnDeploy(deploy)
			// The next model call waits at the gate, so the test reads the final record before the turn ends.
			stack.waitForMarker("model-gate.reached")
			waiting, err := sessions.GetAgentSnapshot(ctx, "refund")
			require.NoError(t, err)
			stack.releaseMarker("model-gate")

			require.Len(t, waiting.ToolCalls, 1)
			require.Equal(t, dexagents.ToolCallStatusOutcomeUnknown, waiting.ToolCalls[0].Status)
			require.Equal(t, "unknown tool issue_refund", *waiting.ToolCalls[0].Error)
			snapshot := stack.waitForTurnCount(einoAgentFlowType, "refund", 1)
			require.Nil(t, snapshot.State.LastTurnError, "a deploy that renamed a tool never fails the turn")
			require.Equal(t, "Refund result: "+dexagents.OutcomeUnknownToolResult, *snapshot.Messages[3].Text)
			require.Equal(t, 1, stack.callCount("issue_refund"))
			require.Equal(t, []string{"StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"},
				stack.executedStepTypes("refund"))
		})
	}
}

// A deploy that changed the instruction while a turn waited on a tool takes effect at the next model
// call of the same turn, and the read that the crash interrupted runs again.
func TestInstructionChangedMidTurnTakesEffectAtTheNextModelCall(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(einoAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "instruction", ptr("instruction check"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("blocking_read.started")
	worker.Kill()
	stack.startWorkerOnDeploy(deployInstructionV2)

	snapshot := stack.waitForTurnCount(einoAgentFlowType, "instruction", 1)
	require.Equal(t, "Instruction: "+instructionV2+"; read after the restart", *snapshot.Messages[3].Text)
	require.Equal(t, 2, stack.callCount("blocking_read"), "a read runs again after a crash")
}
