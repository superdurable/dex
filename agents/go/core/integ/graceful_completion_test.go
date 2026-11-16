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
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/builtinloop"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	gracefulCompletionAgentFlowType = "GracefulCompletionAgent"
	completeSessionText             = "complete the session"
	holdTurnText                    = "hold the turn"
)

// A tool that stops its own session with GracefulComplete lets the turn finish, and the session
// Flow then completes with the tool result and the final answer stored.
func TestToolThatStopsItsOwnSessionCompletesItAfterTheTurn(t *testing.T) {
	t.Parallel()
	stack, _ := newGracefulCompletionStack(t)
	sessions := stack.sessions(gracefulCompletionAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "tool-stop", ptr(completeSessionText), dex.StartFlowOptions{})
	require.NoError(t, err)

	require.Equal(t, dex.FlowCompleted, waitForClosedFlowStatus(t, stack, "tool-stop"))
	snapshot, err := sessions.GetAgentSnapshot(ctx, "tool-stop")
	require.NoError(t, err)
	require.NotNil(t, snapshot.State)
	require.Equal(t, int64(1), snapshot.State.TurnCount)
	require.Nil(t, snapshot.State.ActiveTurnID)
	require.Equal(t, []dexagents.MessageRole{
		dexagents.MessageRoleUser, dexagents.MessageRoleAssistant, dexagents.MessageRoleTool, dexagents.MessageRoleAssistant,
	}, messageRoles(snapshot.Messages))
	require.Equal(t, "completing tool-stop", *snapshot.Messages[2].ToolOutput)
	require.Equal(t, "Done: completing tool-stop", *snapshot.Messages[3].Text)
}

// A user message queued before the request still runs its turn before the session completes.
func TestMessageQueuedBeforeGracefulCompletionStillRunsItsTurn(t *testing.T) {
	t.Parallel()
	stack, heldTurn := newGracefulCompletionStack(t)
	sessions := stack.sessions(gracefulCompletionAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "queued", ptr(holdTurnText), dex.StartFlowOptions{})
	require.NoError(t, err)
	heldTurn.waitUntilReached(t)
	isQueued, err := sessions.SendUserMessage(ctx, "queued", "after the hold")
	require.NoError(t, err)
	require.True(t, isQueued)
	require.NoError(t, stack.client.StopFlow(ctx, "queued", dex.StopOptions{Type: dex.GracefulCompleteFlow}))
	heldTurn.releaseTurn()

	require.Equal(t, dex.FlowCompleted, waitForClosedFlowStatus(t, stack, "queued"))
	snapshot, err := sessions.GetAgentSnapshot(ctx, "queued")
	require.NoError(t, err)
	require.NotNil(t, snapshot.State)
	require.Equal(t, int64(2), snapshot.State.TurnCount)
	require.Zero(t, snapshot.QueuedMessageCount)
	require.Equal(t, []string{holdTurnText, "Held until released", "after the hold", "You said: after the hold"},
		messageTexts(snapshot.Messages))
}

// Without a request the session keeps waiting for the next user message after every turn.
func TestSessionWithoutGracefulCompletionKeepsWaiting(t *testing.T) {
	t.Parallel()
	stack, _ := newGracefulCompletionStack(t)
	sessions := stack.sessions(gracefulCompletionAgentFlowType)
	ctx := context.Background()

	_, err := sessions.StartSession(ctx, "no-request", ptr("first"), dex.StartFlowOptions{})
	require.NoError(t, err)
	stack.waitForTurnCount(gracefulCompletionAgentFlowType, "no-request", 1)
	requireFlowStatus(t, stack, "no-request", dex.FlowRunning)

	isQueued, err := sessions.SendUserMessage(ctx, "no-request", "second")
	require.NoError(t, err)
	require.True(t, isQueued)
	snapshot := stack.waitForTurnCount(gracefulCompletionAgentFlowType, "no-request", 2)
	require.Equal(t, "You said: second", *snapshot.Messages[len(snapshot.Messages)-1].Text)
	requireFlowStatus(t, stack, "no-request", dex.FlowRunning)
}

// newGracefulCompletionStack serves the graceful completion agent from an in-process Worker.
func newGracefulCompletionStack(t *testing.T) (*agentTestStack, *heldTurnModelClient) {
	t.Helper()
	stack := newAgentTestStack(t)
	heldTurn := &heldTurnModelClient{
		scripted: agentstest.NewScriptedModelClient(respondToGracefulCompletionScript),
		reached:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	toolClient := newSessionStoppingClient(t, stack)
	workerFlow, err := newGracefulCompletionAgentFlow(toolClient, heldTurn)
	require.NoError(t, err)
	stack.startInProcessWorker([]dex.Flow{workerFlow})
	// Registered after the Worker, so it runs before the Worker stops and frees a held model call.
	t.Cleanup(heldTurn.releaseTurn)
	clientFlow, err := newGracefulCompletionAgentFlow(toolClient, heldTurn)
	require.NoError(t, err)
	stack.addClientFlow(clientFlow)
	return stack, heldTurn
}

// newSessionStoppingClient returns the Client that the complete_session tool stops sessions with.
func newSessionStoppingClient(t *testing.T, stack *agentTestStack) *dex.Client {
	t.Helper()
	typingFlow, err := newTypingOnlyFlow(gracefulCompletionAgentFlowType)
	require.NoError(t, err)
	registry, err := dex.NewRegistry([]dex.Flow{typingFlow})
	require.NoError(t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stack.stateDir, "session-stopping-client-cache"))
	require.NoError(t, err)
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: stack.engine.Address()})
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close the session stopping Client: %v", err)
		}
	})
	return client
}

// newGracefulCompletionAgentFlow builds an agent whose complete_session tool stops its own session.
func newGracefulCompletionAgentFlow(toolClient *dex.Client, modelClient builtinloop.ModelClient) (*dexagents.AgentSessionFlow, error) {
	completeSession, err := builtinloop.NewFunctionTool("complete_session", "End this session after the turn.",
		json.RawMessage(`{"type":"object"}`), dexagents.ToolEffectIdempotentWrite,
		func(ctx context.Context, _ json.RawMessage, call builtinloop.ToolCallContext) (string, error) {
			if err := toolClient.StopFlow(ctx, call.FlowID, dex.StopOptions{Type: dex.GracefulCompleteFlow}); err != nil {
				return "", err
			}
			return "completing " + call.FlowID, nil
		},
		builtinloop.WithRequiresApproval(false))
	if err != nil {
		return nil, err
	}
	definition, err := builtinloop.NewAgentDefinition("graceful", "You end sessions on request.", modelClient,
		builtinloop.WithFunctionTools(completeSession))
	if err != nil {
		return nil, err
	}
	return builtinloop.NewBuiltinAgentSessionFlow(definition, gracefulCompletionAgentFlowType)
}

func respondToGracefulCompletionScript(request builtinloop.ModelRequest) (agentstest.ScriptedModelReply, error) {
	userTexts := agentstest.UserTexts(request)
	lastText := userTexts[len(userTexts)-1]
	results := agentstest.ToolResults(request)
	switch lastText {
	case completeSessionText:
		if len(results) == 0 {
			return toolCalls(agentstest.ScriptedToolCall{ToolName: "complete_session"}), nil
		}
		return agentstest.ScriptedModelReply{Text: "Done: " + results[0].Output}, nil
	case holdTurnText:
		return agentstest.ScriptedModelReply{Text: "Held until released"}, nil
	default:
		return agentstest.ScriptedModelReply{Text: "You said: " + lastText}, nil
	}
}

// heldTurnModelClient blocks the "hold the turn" model call until the test releases it.
type heldTurnModelClient struct {
	scripted    *agentstest.ScriptedModelClient
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
}

func (client *heldTurnModelClient) CallModel(
	ctx context.Context,
	request builtinloop.ModelRequest,
	output builtinloop.ModelOutputWriter,
) (builtinloop.ModelResponse, error) {
	userTexts := agentstest.UserTexts(request)
	if userTexts[len(userTexts)-1] == holdTurnText {
		client.reachedOnce.Do(func() { close(client.reached) })
		select {
		case <-client.release:
		case <-ctx.Done():
			return builtinloop.ModelResponse{}, ctx.Err()
		}
	}
	return client.scripted.CallModel(ctx, request, output)
}

func (client *heldTurnModelClient) EstimateInputTokens(request builtinloop.ModelRequest) int64 {
	return client.scripted.EstimateInputTokens(request)
}

func (client *heldTurnModelClient) waitUntilReached(t *testing.T) {
	t.Helper()
	select {
	case <-client.reached:
	case <-t.Context().Done():
		t.Fatal("the held model call never started")
	}
}

func (client *heldTurnModelClient) releaseTurn() {
	client.releaseOnce.Do(func() { close(client.release) })
}

func waitForClosedFlowStatus(t *testing.T, stack *agentTestStack, sessionID string) dex.FlowStatus {
	t.Helper()
	waitContext, cancel := context.WithTimeout(context.Background(), sessionConvergenceTimeout)
	defer cancel()
	result, err := stack.client.WaitForFlow(waitContext, sessionID, dex.WaitForFlowOptions{})
	require.NoError(t, err, "session %s must close", sessionID)
	return result.Status
}

func requireFlowStatus(t *testing.T, stack *agentTestStack, sessionID string, status dex.FlowStatus) {
	t.Helper()
	info, err := stack.client.DescribeFlow(context.Background(), sessionID)
	require.NoError(t, err)
	require.Equal(t, status, info.Status)
}

func messageTexts(messages []dexagents.AgentMessage) []string {
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		if message.Text != nil {
			texts = append(texts, *message.Text)
		}
	}
	return texts
}
