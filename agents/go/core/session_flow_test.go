// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Every state that needs a contract addition this core lacks has a reason; a Go-only session has none.
func TestUnsupportedSessionReasonNamesEachAddition(t *testing.T) {
	t.Parallel()
	activeTurn := "turn-1"
	chatTurn := &AgentState{InteractionMode: InteractionModeChat, ActiveTurnID: &activeTurn}
	for name, testCase := range map[string]struct {
		state                *AgentState
		plan                 *AgentPlan
		pendingPlanExecution *PlanExecutionRequest
		summary              *ContextSummary
		cancellation         *TurnCancellationRequest
		expectedReason       string
	}{
		"Go-only session": {state: chatTurn},
		"steering turn": {state: &AgentState{InteractionMode: InteractionModeChat, AcceptsTurnControl: true},
			expectedReason: "a Worker with steering began the active turn"},
		"planning turn": {state: &AgentState{InteractionMode: InteractionModePlanning},
			expectedReason: "planning turn"},
		"cancelled turn": {state: chatTurn, cancellation: &TurnCancellationRequest{TurnID: activeTurn},
			expectedReason: "cancel_turn asked to stop the active turn"},
		"cancellation of an earlier turn": {state: chatTurn, cancellation: &TurnCancellationRequest{TurnID: "turn-0"}},
		"draft plan": {state: chatTurn, plan: &AgentPlan{Status: PlanStatusDraft},
			expectedReason: "draft plan"},
		"completed plan": {state: chatTurn, plan: &AgentPlan{Status: PlanStatusCompleted}},
		"pending plan execution": {state: chatTurn, pendingPlanExecution: &PlanExecutionRequest{Revision: 1},
			expectedReason: "plan execution is pending"},
		"context summary": {state: chatTurn, summary: &ContextSummary{Generation: 1},
			expectedReason: "context summary"},
	} {
		reason := unsupportedSessionReason(testCase.state, testCase.plan,
			testCase.pendingPlanExecution, testCase.summary, testCase.cancellation)
		if testCase.expectedReason == "" {
			require.Empty(t, reason, name)
			continue
		}
		require.Contains(t, reason, testCase.expectedReason, name)
	}
}
