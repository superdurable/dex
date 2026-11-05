// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdurable/dex/sdk-go/dex"
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

// A tool Step that never reached a Worker records failed, so the model may call the tool again.
// Any other failure keeps the status of the call's effect.
func TestRecoveredToolCallRecordsACallThatNeverRanAsFailed(t *testing.T) {
	t.Parallel()
	approved := AgentToolCall{
		ToolCallID: "call-1",
		TurnID:     "turn-1",
		ToolName:   "publish",
		Arguments:  "{}",
		Effect:     ToolEffectNonIdempotentWrite,
		Status:     ToolCallStatusApproved,
	}
	answered := approved
	answered.InputAnswers = []QuestionAnswer{{InterruptID: "call-1-input-1", Question: "Which list?", Answer: "news"}}
	failure := func(progress dex.StepAttemptProgress) *dex.RecoveryErrorInfo {
		return &dex.RecoveryErrorInfo{
			Detail:          "worker lost",
			ErrorType:       "FLOW_ERROR_TYPE_WORKER_API_FAIL",
			AttemptProgress: progress,
		}
	}
	notRunError := ToolCallNotRunErrorPrefix + ": worker lost"
	for name, testCase := range map[string]struct {
		toolCall       AgentToolCall
		recoveryError  *dex.RecoveryErrorInfo
		status         ToolCallStatus
		expectedStatus ToolCallStatus
		expectedError  string
	}{
		"single-attempt call that never started": {approved, failure(dex.StepAttemptProgressNotStarted),
			ToolCallStatusOutcomeUnknown, ToolCallStatusFailed, notRunError},
		"read call that never started": {approved, failure(dex.StepAttemptProgressNotStarted),
			ToolCallStatusFailed, ToolCallStatusFailed, notRunError},
		"call that may have run": {approved, failure(dex.StepAttemptProgressNotCompleted),
			ToolCallStatusOutcomeUnknown, ToolCallStatusOutcomeUnknown, "worker lost"},
		"call on an older Server": {approved, failure(dex.StepAttemptProgressUnspecified),
			ToolCallStatusOutcomeUnknown, ToolCallStatusOutcomeUnknown, "worker lost"},
		"call without a recovery error": {approved, nil,
			ToolCallStatusOutcomeUnknown, ToolCallStatusOutcomeUnknown, "unknown failure"},
		"call that asked a question": {answered, failure(dex.StepAttemptProgressNotStarted),
			ToolCallStatusOutcomeUnknown, ToolCallStatusOutcomeUnknown, "worker lost"},
	} {
		recorded := recoveredToolCall(testCase.toolCall, testCase.recoveryError, testCase.status)
		require.Equal(t, testCase.expectedStatus, recorded.Status, name)
		require.NotNil(t, recorded.Error, name)
		require.Equal(t, testCase.expectedError, *recorded.Error, name)
	}
}

func TestToolCallNotRunErrorPrefixMatchesTheConformanceSuite(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile(filepath.Join("..", "..", "conformance", "texts.json"))
	require.NoError(t, err)
	var texts struct {
		Constants map[string]string `json:"constants"`
	}
	require.NoError(t, json.Unmarshal(text, &texts))
	require.Equal(t, texts.Constants["tool_call_not_run_error_prefix"], ToolCallNotRunErrorPrefix)
}
