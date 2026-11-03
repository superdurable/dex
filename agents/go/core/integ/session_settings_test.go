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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// Settings from the start input choose the model and the instructions, and an update applies at the next turn.
func TestSessionSettingsChooseTheModelAndUpdateAtTheNextTurn(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(settingsAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSessionWithSettings(ctx, "settings", ptr("which-model"),
		map[string]any{"model": "alpha", "persona": "pirate"}, dex.StartFlowOptions{})
	require.NoError(t, err)
	first := stack.waitForTurnCount(settingsAgentFlowType, "settings", 1)
	require.Equal(t, "model alpha: You are the settings agent. Persona: pirate.", *first.Messages[1].Text)
	require.Equal(t, map[string]any{"model": "alpha", "persona": "pirate"}, first.SessionSettings)

	isStored, err := sessions.UpdateSessionSettings(ctx, "settings", map[string]any{"model": "beta"})
	require.NoError(t, err)
	require.True(t, isStored)
	_, err = sessions.SendUserMessage(ctx, "settings", "which-model")
	require.NoError(t, err)
	second := stack.waitForTurnCount(settingsAgentFlowType, "settings", 2)
	require.Equal(t, "model beta: You are the settings agent.", *second.Messages[3].Text)

	// An empty map clears the settings.
	isStored, err = sessions.UpdateSessionSettings(ctx, "settings", map[string]any{})
	require.NoError(t, err)
	require.True(t, isStored)
	_, err = sessions.SendUserMessage(ctx, "settings", "which-model")
	require.NoError(t, err)
	third := stack.waitForTurnCount(settingsAgentFlowType, "settings", 3)
	require.Equal(t, "model default: You are the settings agent.", *third.Messages[5].Text)
	require.Nil(t, third.SessionSettings)
}

// A session started without settings, as every 0.1.0 session was, runs on the default model.
func TestASessionWithoutSettingsUsesTheDefaultModel(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(settingsAgentFlowType)

	_, err := sessions.StartSession(context.Background(), "no-settings", ptr("which-model"), dex.StartFlowOptions{})
	require.NoError(t, err)
	snapshot := stack.waitForTurnCount(settingsAgentFlowType, "no-settings", 1)
	require.Equal(t, "model default: You are the settings agent.", *snapshot.Messages[1].Text)
	require.Nil(t, snapshot.SessionSettings)
}

// An update during a turn waits for the next turn, and a Worker killed in between retries with the turn's settings.
func TestAWorkerKilledAfterAnUpdateRetriesWithTheTurnSettings(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	worker := stack.startWorker()
	sessions := stack.sessions(settingsAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSessionWithSettings(ctx, "settings-kill", ptr("think slowly"),
		map[string]any{"model": "alpha"}, dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForMarker("settings-model-attempt-1")
	isStored, err := sessions.UpdateSessionSettings(ctx, "settings-kill", map[string]any{"model": "beta"})
	require.NoError(t, err)
	require.True(t, isStored)
	worker.Kill()
	stack.startWorker()

	first := stack.waitForTurnCount(settingsAgentFlowType, "settings-kill", 1)
	require.Equal(t, "answered on attempt 2 by alpha", *first.Messages[1].Text)
	require.Equal(t, map[string]any{"model": "beta"}, first.SessionSettings)
	_, err = sessions.SendUserMessage(ctx, "settings-kill", "which-model")
	require.NoError(t, err)
	second := stack.waitForTurnCount(settingsAgentFlowType, "settings-kill", 2)
	require.Equal(t, "model beta: You are the settings agent.", *second.Messages[3].Text)
}

// Settings larger than the limit are refused by the Client and by the RPC.
func TestSessionSettingsOverTheLimitAreRefused(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(settingsAgentFlowType)
	ctx := context.Background()
	// {"note":"<padding>"} is 11 bytes plus the padding.
	largest := map[string]any{"note": strings.Repeat("x", dexagents.MaxSessionSettingsBytes-11)}
	tooLarge := map[string]any{"note": strings.Repeat("x", dexagents.MaxSessionSettingsBytes-10)}
	size, err := dexagents.SessionSettingsSize(largest)
	require.NoError(t, err)
	require.Equal(t, dexagents.MaxSessionSettingsBytes, size)
	require.NoError(t, dexagents.ValidateSessionSettings(largest))
	require.ErrorContains(t, dexagents.ValidateSessionSettings(tooLarge), "more than the 16384 allowed")
	// Non-ASCII and HTML characters count as their UTF-8 bytes, unescaped.
	size, err = dexagents.SessionSettingsSize(map[string]any{"note": "é<"})
	require.NoError(t, err)
	require.Equal(t, 14, size)

	_, err = sessions.StartSessionWithSettings(ctx, "settings-limit", nil, tooLarge, dex.StartFlowOptions{})
	require.Error(t, err)
	_, err = sessions.StartSessionWithSettings(ctx, "settings-limit", nil, largest, dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForTurnCount(settingsAgentFlowType, "settings-limit", 0)
	// The session accepts a tolerance over the Client's limit, never more.
	beyondTolerance := map[string]any{
		"note": strings.Repeat("x", dexagents.MaxSessionSettingsBytes+dexagents.SessionSettingsSizeToleranceBytes),
	}
	require.False(t, invokeUpdateSessionSettings(t, stack, "settings-limit", beyondTolerance))
	snapshot, err := sessions.GetAgentSnapshot(ctx, "settings-limit")
	require.NoError(t, err)
	require.Equal(t, largest, snapshot.SessionSettings)
	require.True(t, invokeUpdateSessionSettings(t, stack, "settings-limit", tooLarge))
}

// An update with nil settings, as a missing or null field decodes, returns false and changes nothing.
func TestAnUpdateWithoutSettingsReturnsFalse(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	sessions := stack.sessions(settingsAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSessionWithSettings(ctx, "settings-nil", ptr("which-model"),
		map[string]any{"model": "alpha"}, dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForTurnCount(settingsAgentFlowType, "settings-nil", 1)
	require.False(t, invokeUpdateSessionSettings(t, stack, "settings-nil", nil))
	isStored, err := sessions.UpdateSessionSettings(ctx, "settings-nil", nil)
	require.NoError(t, err)
	require.False(t, isStored)
	snapshot, err := sessions.GetAgentSnapshot(ctx, "settings-nil")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"model": "alpha"}, snapshot.SessionSettings)
}

// A start whose settings the session refuses fails the session at once, with no retries.
func TestAStartWithRefusedSettingsFailsTheSessionAtOnce(t *testing.T) {
	t.Parallel()
	stack := newAgentTestStack(t)
	stack.startWorker()
	ctx := context.Background()
	oversized := map[string]any{
		"note": strings.Repeat("x", dexagents.MaxSessionSettingsBytes+dexagents.SessionSettingsSizeToleranceBytes),
	}

	// The raw Client skips ValidateSessionSettings.
	_, err := stack.client.StartFlow(ctx, stack.flows[settingsAgentFlowType], "settings-refused",
		dexagents.AgentSessionStartInput{InitialMessage: ptr("which-model"), SessionSettings: oversized},
		dex.StartFlowOptions{})
	require.NoError(t, err)
	waitContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := stack.client.WaitForFlow(waitContext, "settings-refused", dex.WaitForFlowOptions{})
	require.NoError(t, err)
	require.Equal(t, dex.FlowFailed, result.Status)
	require.Contains(t, result.ErrorMessage, dexagents.InvalidStartSessionSettingsReason)
	for _, entry := range stack.stepHistory("settings-refused") {
		if entry.StepType == "StartAgentSession" {
			require.Equal(t, 1, entry.FinalAttempt)
		}
	}
}

func invokeUpdateSessionSettings(t *testing.T, stack *agentTestStack, sessionID string, sessionSettings map[string]any) bool {
	t.Helper()
	var isStored bool
	require.NoError(t, stack.client.InvokeRPC(context.Background(), sessionID,
		stack.flows[settingsAgentFlowType].UpdateSessionSettings,
		dexagents.UpdateSessionSettingsRequest{SessionSettings: sessionSettings}, &isStored))
	return isStored
}
