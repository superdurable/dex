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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/agents/go/core/agentstest"
	"github.com/superdurable/dex/agents/go/core/remotesession"
	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	// remoteSessionStateDirEnvironmentVariable names the state directory of a remote session test Worker.
	remoteSessionStateDirEnvironmentVariable = "REMOTE_SESSION_STATE_DIR"
	remoteSessionCallTimeout                 = 30 * time.Second
	// frozenWorkerCallTimeout bounds a call that waits for a Worker frozen with SIGSTOP.
	frozenWorkerCallTimeout = 3 * time.Second
	approvedPlanMessage     = "executed plan"
	raceAttempts            = 20
)

// remoteSessionWorkerLanguage starts the Worker of one core for the remote session test agent.
type remoteSessionWorkerLanguage struct {
	name     string
	flowType string
	start    func(stack *agentTestStack, flowType string) *agentstest.KillableProcess
}

func remoteSessionWorkerLanguages() []remoteSessionWorkerLanguage {
	return []remoteSessionWorkerLanguage{
		{name: "Python", flowType: "PythonRemoteSessionAgent", start: startPythonRemoteSessionWorker},
		{name: "TypeScript", flowType: "TypeScriptRemoteSessionAgent", start: startTypeScriptRemoteSessionWorker},
	}
}

// remoteSessionHarness drives one Worker's sessions with a Client whose Registry holds only a RemoteSessionFlow.
type remoteSessionHarness struct {
	stack    *agentTestStack
	worker   *agentstest.KillableProcess
	flowType string
	sessions *remotesession.RemoteSessionClient
	starter  *dexagents.AgentSessionClient
}

// A Go Client with only remotesession calls every session RPC of real Python and TypeScript Workers
// with the names, locks, state loads, and wire values of their Flows, and reads their AgentEvents.
func TestRemoteSessionClientDrivesPythonAndTypeScriptWorkers(t *testing.T) {
	t.Parallel()
	for _, language := range remoteSessionWorkerLanguages() {
		t.Run(language.name, func(t *testing.T) {
			t.Parallel()
			harness := newRemoteSessionHarness(t, language)
			t.Run("TurnsMessagesAndEvents", harness.testTurnsMessagesAndEvents)
			t.Run("Approvals", harness.testApprovals)
			t.Run("Questions", harness.testQuestions)
			t.Run("PlanMode", harness.testPlanMode)
			t.Run("Images", harness.testImages)
			t.Run("SessionSettings", harness.testSessionSettings)
			t.Run("QueueSteerAndDelete", harness.testQueueSteerAndDelete)
			t.Run("CancelTurn", harness.testCancelTurn)
			t.Run("LockConflicts", harness.testLockConflicts)
			t.Run("ConcurrentApprovals", harness.testConcurrentApprovals)
			t.Run("AnswerRacesCancel", harness.testAnswerRacesCancel)
			t.Run("SteerRacesDelete", harness.testSteerRacesDelete)
		})
	}
}

func newRemoteSessionHarness(t *testing.T, language remoteSessionWorkerLanguage) *remoteSessionHarness {
	t.Helper()
	stack := newAgentTestStack(t)
	worker := language.start(stack, language.flowType)
	// The typing-only Flow starts sessions: a client-only Flow has no starting Step.
	startingFlow, err := newTypingOnlyFlow(language.flowType)
	require.NoError(t, err)
	stack.addClientFlow(startingFlow)

	remoteFlow, err := remotesession.NewRemoteSessionFlow(language.flowType)
	require.NoError(t, err)
	registry, err := dex.NewRegistry([]dex.Flow{remoteFlow})
	require.NoError(t, err)
	cache, err := agentstest.OpenTestBlobCache(filepath.Join(stack.stateDir, "remote-client-blob-cache"))
	require.NoError(t, err)
	_, err = dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: stack.engine.Address()})
	var clientOnly *dex.ClientOnlyFlowError
	require.ErrorAs(t, err, &clientOnly, "a Worker refuses the client-only Registry")
	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: stack.engine.Address()})
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close the remote session Client: %v", err)
		}
	})
	return &remoteSessionHarness{
		stack:    stack,
		worker:   worker,
		flowType: language.flowType,
		sessions: remotesession.NewRemoteSessionClient(client, remoteFlow),
		starter:  stack.sessions(language.flowType),
	}
}

func startPythonRemoteSessionWorker(stack *agentTestStack, flowType string) *agentstest.KillableProcess {
	stack.t.Helper()
	pythonCore, err := filepath.Abs(pythonCoreDir)
	require.NoError(stack.t, err)
	scriptPath, err := filepath.Abs(filepath.Join("testdata", "python", "remote_session_worker.py"))
	require.NoError(stack.t, err)
	return stack.startRemoteSessionWorker("Python remote session Worker",
		[]string{"uv", "run", "--project", pythonCore, "--frozen", "python", scriptPath, flowType},
		"PYTHONUNBUFFERED=1")
}

func startTypeScriptRemoteSessionWorker(stack *agentTestStack, flowType string) *agentstest.KillableProcess {
	stack.t.Helper()
	typescriptCore, err := filepath.Abs(typescriptCoreDir)
	require.NoError(stack.t, err)
	scriptPath, err := filepath.Abs(filepath.Join("testdata", "typescript", "remote_session_worker.mjs"))
	require.NoError(stack.t, err)
	return stack.startRemoteSessionWorker("TypeScript remote session Worker",
		[]string{"node", scriptPath, typescriptCore, flowType})
}

func (stack *agentTestStack) startRemoteSessionWorker(
	name string,
	arguments []string,
	extraEnvironment ...string,
) *agentstest.KillableProcess {
	stack.t.Helper()
	worker := agentstest.NewKillableProcess(name, filepath.Join(stack.stateDir, "remote-session-worker.log"))
	stack.workers = append(stack.workers, worker)
	environment := append(os.Environ(),
		serverAddressEnvironmentVariable+"="+stack.engine.Address(),
		blobCacheDirEnvironmentVariable+"="+filepath.Join(stack.stateDir, "remote-session-worker-blob-cache"),
		remoteSessionStateDirEnvironmentVariable+"="+stack.coordinationDir,
	)
	environment = append(environment, extraEnvironment...)
	require.NoError(stack.t, worker.Start(context.Background(), arguments, environment,
		agentstest.WorkerReadyLine, workerReadyTimeout))
	return worker
}

func (harness *remoteSessionHarness) testTurnsMessagesAndEvents(t *testing.T) {
	sessionID := harness.startSession(t, "turns")
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: "   "})
	require.NoError(t, err)
	require.False(t, isQueued, "blank text is refused")
	harness.sendAndFinish(t, sessionID, dexagents.AgentUserMessage{Text: "say hello"}, 1)
	harness.sendAndFinish(t, sessionID, dexagents.AgentUserMessage{Text: "say again"}, 2)
	snapshot := harness.snapshot(t, sessionID)
	require.Equal(t, "said: again", lastMessageText(t, snapshot))
	require.Equal(t, dexagents.InteractionModeChat, snapshot.State.InteractionMode)

	page, err := harness.sessions.ListAgentMessages(callContext(t), sessionID, nil, 2)
	require.NoError(t, err)
	require.Len(t, page.Messages, 2)
	require.Equal(t, "said: again", *page.Messages[1].Text)
	require.NotNil(t, page.NextBeforeSequence)
	older, err := harness.sessions.ListAgentMessages(callContext(t), sessionID, page.NextBeforeSequence, 200)
	require.NoError(t, err)
	require.NotEmpty(t, older.Messages)
	require.Less(t, older.Messages[len(older.Messages)-1].Sequence, page.Messages[0].Sequence)

	events, err := harness.sessions.ListAgentEvents(callContext(t), sessionID, 200, "")
	require.NoError(t, err)
	eventTypes := make([]string, 0, len(events.Messages))
	for _, event := range events.Messages {
		eventTypes = append(eventTypes, event.Value.Type)
	}
	require.Contains(t, eventTypes, "turn_started")
	require.Contains(t, eventTypes, "turn_completed")
	require.Equal(t, "turn_completed", eventTypes[0], "the page is newest first")
	first, firstMessage, err := harness.sessions.ReadAgentEvent(callContext(t), sessionID, "")
	require.NoError(t, err)
	require.Equal(t, "turn_started", first.Type)
	second, _, err := harness.sessions.ReadAgentEvent(callContext(t), sessionID, firstMessage.ResumeToken)
	require.NoError(t, err)
	require.NotEmpty(t, second.Type)
}

func (harness *remoteSessionHarness) testApprovals(t *testing.T) {
	sessionID := harness.startSession(t, "approvals")
	interrupt := harness.sendAndWaitForInterrupt(t, sessionID, "publish approved note")
	require.Equal(t, dexagents.InterruptKindToolApproval, interrupt.Kind)
	require.Equal(t, "publish_note", interrupt.ToolName)
	isResolved, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
	})
	require.NoError(t, err)
	require.True(t, isResolved)
	isResolvedAgain, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
	})
	require.NoError(t, err)
	require.False(t, isResolvedAgain, "only one call resolves an interrupt")
	snapshot := harness.waitForIdleTurns(t, sessionID, 1)
	require.Equal(t, "publish_note: published approved note", lastMessageText(t, snapshot))

	interrupt = harness.sendAndWaitForInterrupt(t, sessionID, "publish rejected note")
	isResolved, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, false, ptr("not today"))
	})
	require.NoError(t, err)
	require.True(t, isResolved)
	snapshot = harness.waitForIdleTurns(t, sessionID, 2)
	require.Contains(t, lastMessageText(t, snapshot), "not today")
}

func (harness *remoteSessionHarness) testQuestions(t *testing.T) {
	sessionID := harness.startSession(t, "questions")
	question := harness.sendAndWaitForInterrupt(t, sessionID, "ask Which color? | red | blue")
	require.Equal(t, dexagents.InterruptKindQuestion, question.Kind)
	require.Equal(t, "Which color?", *question.Question)
	require.Equal(t, []string{"red", "blue"}, question.Choices)
	require.False(t, question.AllowsFreeText)
	isAnswered, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.AnswerQuestion(ctx, sessionID, question.InterruptID, "green")
	})
	require.NoError(t, err)
	require.False(t, isAnswered, "an answer outside the choices is refused")
	isApproved, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, question.InterruptID, true, nil)
	})
	require.NoError(t, err)
	require.False(t, isApproved, "a question needs an answer, not an approval")
	isAnswered, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.AnswerQuestion(ctx, sessionID, question.InterruptID, "blue")
	})
	require.NoError(t, err)
	require.True(t, isAnswered)
	snapshot := harness.waitForIdleTurns(t, sessionID, 1)
	require.Contains(t, lastMessageText(t, snapshot), "blue")

	question = harness.sendAndWaitForInterrupt(t, sessionID, "ask Which size? | small | large")
	isDeclined, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, question.InterruptID, false, nil)
	})
	require.NoError(t, err)
	require.True(t, isDeclined, "resolve_interrupt declines a question")
	harness.waitForIdleTurns(t, sessionID, 2)
}

func (harness *remoteSessionHarness) testPlanMode(t *testing.T) {
	sessionID := harness.startSession(t, "plan")
	harness.sendAndFinish(t, sessionID, dexagents.AgentUserMessage{Text: "plan look up order | refund it", PlanMode: true}, 1)
	snapshot := harness.snapshot(t, sessionID)
	require.NotNil(t, snapshot.Plan)
	require.Equal(t, dexagents.PlanStatusDraft, snapshot.Plan.Status)
	require.Len(t, snapshot.Plan.Tasks, 2)
	revision := snapshot.Plan.Revision

	isAccepted, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ExecutePlan(ctx, sessionID, revision+1)
	})
	require.NoError(t, err)
	require.False(t, isAccepted, "an unknown revision is refused")
	isAccepted, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ExecutePlan(ctx, sessionID, revision)
	})
	require.NoError(t, err)
	require.True(t, isAccepted)
	snapshot = harness.waitForIdleTurns(t, sessionID, 2)
	require.Equal(t, approvedPlanMessage, lastMessageText(t, snapshot))
}

func (harness *remoteSessionHarness) testImages(t *testing.T) {
	sessionID := harness.startSession(t, "images")
	text, err := os.ReadFile(filepath.Join(harness.stack.coordinationDir, "image-reference.json"))
	require.NoError(t, err)
	var reference dexagents.AgentImageReference
	require.NoError(t, json.Unmarshal(text, &reference))
	harness.sendAndFinish(t, sessionID,
		dexagents.AgentUserMessage{Text: "describe", Images: []dexagents.AgentImageReference{reference}}, 1)
	snapshot := harness.snapshot(t, sessionID)
	require.Equal(t, "saw 1 images", lastMessageText(t, snapshot))
	userMessage := snapshot.Messages[len(snapshot.Messages)-2]
	require.Equal(t, []dexagents.AgentImageReference{reference}, userMessage.Images)

	invalid := reference
	invalid.SHA256 = "not-a-digest"
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID,
		dexagents.AgentUserMessage{Text: "describe", Images: []dexagents.AgentImageReference{invalid}})
	require.NoError(t, err)
	require.False(t, isQueued, "an invalid image reference is refused")
}

func (harness *remoteSessionHarness) testSessionSettings(t *testing.T) {
	sessionID := harness.startSession(t, "settings")
	harness.waitForSnapshot(t, sessionID, "an initialized session", func(snapshot dexagents.AgentSnapshot) bool {
		return snapshot.State != nil
	})
	isStored, err := harness.sessions.UpdateSessionSettings(callContext(t), sessionID, map[string]any{"model": "remote"})
	require.NoError(t, err)
	require.True(t, isStored)
	require.Equal(t, map[string]any{"model": "remote"}, harness.snapshot(t, sessionID).SessionSettings)
	isStored, err = harness.sessions.UpdateSessionSettings(callContext(t), sessionID, nil)
	require.NoError(t, err)
	require.False(t, isStored, "nil settings are refused")
	isStored, err = harness.sessions.UpdateSessionSettings(callContext(t), sessionID, map[string]any{})
	require.NoError(t, err)
	require.True(t, isStored)
	require.Nil(t, harness.snapshot(t, sessionID).SessionSettings, "an empty map clears the settings")
}

func (harness *remoteSessionHarness) testQueueSteerAndDelete(t *testing.T) {
	sessionID := harness.startSession(t, "steer")
	snapshot := harness.sendAndWaitForGate(t, sessionID, "steer-gate")
	require.True(t, snapshot.State.AcceptsTurnControl, "the running turn accepts steering")
	for _, text := range []string{"say deleted", "say steered"} {
		isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: text})
		require.NoError(t, err)
		require.True(t, isQueued)
	}
	snapshot = harness.waitForSnapshot(t, sessionID, "two queued messages", func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.QueuedMessages) == 2
	})
	deleted, steered := snapshot.QueuedMessages[0], snapshot.QueuedMessages[1]
	require.Equal(t, "say deleted", deleted.Message.Text)

	isDeleted, err := harness.sessions.DeleteQueuedMessage(callContext(t), sessionID, deleted.MessageID)
	require.NoError(t, err)
	require.True(t, isDeleted)
	isDeleted, err = harness.sessions.DeleteQueuedMessage(callContext(t), sessionID, deleted.MessageID)
	require.NoError(t, err)
	require.False(t, isDeleted, "the message is no longer queued")
	isSteered, err := harness.sessions.SteerUserMessage(callContext(t), sessionID, dexagents.SteerUserMessageRequest{})
	require.NoError(t, err)
	require.False(t, isSteered, "neither field set")
	isSteered, err = harness.sessions.SteerUserMessage(callContext(t), sessionID,
		dexagents.SteerUserMessageRequest{MessageID: &steered.MessageID, Text: ptr("both")})
	require.NoError(t, err)
	require.False(t, isSteered, "both fields set")
	isSteered, err = harness.sessions.SteerUserMessage(callContext(t), sessionID,
		dexagents.SteerUserMessageRequest{MessageID: &steered.MessageID})
	require.NoError(t, err)
	require.True(t, isSteered)
	snapshot = harness.snapshot(t, sessionID)
	require.Empty(t, snapshot.QueuedMessages)
	require.Len(t, snapshot.SteeredMessages, 1)

	harness.releaseGate(t, "steer-gate")
	snapshot = harness.waitForSnapshot(t, sessionID, "the steered turn's reply", func(snapshot dexagents.AgentSnapshot) bool {
		return isIdle(snapshot) && messageText(snapshot.Messages[len(snapshot.Messages)-1]) == "said: steered"
	})
	require.Empty(t, snapshot.SteeredMessages)
	require.False(t, slices.ContainsFunc(snapshot.Messages, func(message dexagents.AgentMessage) bool {
		return messageText(message) == "said: deleted"
	}), "the deleted message never ran")

	isSteered, err = harness.sessions.SteerUserMessage(callContext(t), sessionID,
		dexagents.SteerUserMessageRequest{Text: ptr("say idle steer")})
	require.NoError(t, err)
	require.True(t, isSteered, "with no active turn, new text is queued")
	harness.waitForSnapshot(t, sessionID, "the queued steer text's reply", func(snapshot dexagents.AgentSnapshot) bool {
		return isIdle(snapshot) && messageText(snapshot.Messages[len(snapshot.Messages)-1]) == "said: idle steer"
	})
}

func (harness *remoteSessionHarness) testCancelTurn(t *testing.T) {
	sessionID := harness.startSession(t, "cancel")
	isCancelled, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.CancelTurn(ctx, sessionID, nil)
	})
	require.NoError(t, err)
	require.False(t, isCancelled, "no turn is active")
	snapshot := harness.sendAndWaitForGate(t, sessionID, "cancel-gate")
	activeTurnID := *snapshot.State.ActiveTurnID
	isCancelled, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.CancelTurn(ctx, sessionID, ptr("turn-999"))
	})
	require.NoError(t, err)
	require.False(t, isCancelled, "another turn's ID is refused")
	isCancelled, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.CancelTurn(ctx, sessionID, &activeTurnID)
	})
	require.NoError(t, err)
	require.True(t, isCancelled)
	isCancelled, err = harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.CancelTurn(ctx, sessionID, nil)
	})
	require.NoError(t, err)
	if !isCancelled {
		snapshot = harness.snapshot(t, sessionID)
		require.False(t, snapshot.State.ActiveTurnID != nil && *snapshot.State.ActiveTurnID == activeTurnID,
			"a second request returns true while the cancelled turn still runs")
	}
	// The gate stays closed: cancel_turn aborts the stalled model call.
	snapshot = harness.waitForIdle(t, sessionID)
	require.NotEqual(t, "stalled at cancel-gate", lastMessageText(t, snapshot))
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: "say after cancel"})
	require.NoError(t, err)
	require.True(t, isQueued)
	harness.waitForSnapshot(t, sessionID, "the next turn's reply", func(snapshot dexagents.AgentSnapshot) bool {
		return isIdle(snapshot) && messageText(snapshot.Messages[len(snapshot.Messages)-1]) == "said: after cancel"
	})
}

// A Worker frozen with SIGSTOP holds each RPC's locks, so a second RPC with a shared lock must
// fail with RPCLockConflictError: resolve_interrupt, answer_question, and cancel_turn share
// PendingInterrupts, and execute_plan locks PendingPlanExecution.
func (harness *remoteSessionHarness) testLockConflicts(t *testing.T) {
	sessionID := harness.startSession(t, "locks")
	interrupt := harness.sendAndWaitForInterrupt(t, sessionID, "publish locked note")
	require.NoError(t, harness.worker.SignalProcessGroup(syscall.SIGSTOP))
	isFrozen := true
	resume := func() {
		if isFrozen {
			require.NoError(t, harness.worker.SignalProcessGroup(syscall.SIGCONT))
			isFrozen = false
		}
	}
	t.Cleanup(resume)

	holder := harness.goInvoke(func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
	})
	// Until the holder takes PendingInterrupts, this call may take it instead and wait for the Worker.
	_, err := frozenWorkerCall(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
	})
	var lockConflict *dex.RPCLockConflictError
	if !errors.As(err, &lockConflict) {
		requireDeadlineExceeded(t, err, "the second approval waits with the lock or conflicts")
	}
	for name, call := range map[string]func(context.Context) (bool, error){
		"answer_question": func(ctx context.Context) (bool, error) {
			return harness.sessions.AnswerQuestion(ctx, sessionID, interrupt.InterruptID, "yes")
		},
		"cancel_turn": func(ctx context.Context) (bool, error) {
			return harness.sessions.CancelTurn(ctx, sessionID, nil)
		},
	} {
		_, err := frozenWorkerCall(t, call)
		require.ErrorAs(t, err, &lockConflict, name)
	}
	planHolder := harness.goInvoke(func(ctx context.Context) (bool, error) {
		return harness.sessions.ExecutePlan(ctx, sessionID, 7)
	})
	harness.requireLockConflictEventually(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ExecutePlan(ctx, sessionID, 7)
	})

	resume()
	planResult := <-planHolder
	if planResult.err == nil {
		require.False(t, planResult.isAccepted, "the session has no plan")
	} else {
		require.ErrorAs(t, planResult.err, &lockConflict)
	}
	holderResult := <-holder
	if holderResult.err != nil {
		require.ErrorAs(t, holderResult.err, &lockConflict, "the other approval held the lock")
	}
	snapshot := harness.waitForIdleTurns(t, sessionID, 1)
	require.Equal(t, "publish_note: published locked note", lastMessageText(t, snapshot))
	isResolved, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
		return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
	})
	require.NoError(t, err)
	require.False(t, isResolved)
}

// Concurrent approvals of one interrupt: exactly one wins; the others see false or a lock conflict.
func (harness *remoteSessionHarness) testConcurrentApprovals(t *testing.T) {
	sessionID := harness.startSession(t, "concurrent-approvals")
	interrupt := harness.sendAndWaitForInterrupt(t, sessionID, "publish raced note")
	results := runConcurrently(6, func(int) (bool, error) {
		return harness.sessions.ResolveInterrupt(callContext(t), sessionID, interrupt.InterruptID, true, nil)
	})
	resolvedCount := 0
	for _, result := range results {
		requireAcceptedRefusedOrLockConflict(t, result)
		if result.err == nil && result.isAccepted {
			resolvedCount++
		}
	}
	if resolvedCount == 0 {
		// Every call met a lock; the interrupt is still pending.
		isResolved, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
			return harness.sessions.ResolveInterrupt(ctx, sessionID, interrupt.InterruptID, true, nil)
		})
		require.NoError(t, err)
		require.True(t, isResolved)
		resolvedCount = 1
	}
	require.Equal(t, 1, resolvedCount)
	snapshot := harness.waitForIdleTurns(t, sessionID, 1)
	require.Equal(t, "publish_note: published raced note", lastMessageText(t, snapshot))
}

// answer_question and cancel_turn share PendingInterrupts. Whatever order they commit in, the turn ends.
func (harness *remoteSessionHarness) testAnswerRacesCancel(t *testing.T) {
	sessionID := harness.startSession(t, "answer-cancel")
	for range 3 {
		question := harness.sendAndWaitForInterrupt(t, sessionID, "ask Proceed? | yes | no")
		results := runConcurrently(2, func(index int) (bool, error) {
			if index == 0 {
				return harness.sessions.AnswerQuestion(callContext(t), sessionID, question.InterruptID, "yes")
			}
			return harness.sessions.CancelTurn(callContext(t), sessionID, nil)
		})
		hasWinner := false
		for _, result := range results {
			requireAcceptedRefusedOrLockConflict(t, result)
			hasWinner = hasWinner || (result.err == nil && result.isAccepted)
		}
		if !hasWinner {
			isCancelled, err := harness.retryOnLockConflict(t, func(ctx context.Context) (bool, error) {
				return harness.sessions.CancelTurn(ctx, sessionID, nil)
			})
			require.NoError(t, err)
			require.True(t, isCancelled)
		}
		snapshot := harness.waitForIdle(t, sessionID)
		require.Empty(t, snapshot.PendingInterrupts)
	}
}

// steer_user_message and delete_queued_message race for one queued message: exactly one takes it.
func (harness *remoteSessionHarness) testSteerRacesDelete(t *testing.T) {
	sessionID := harness.startSession(t, "steer-delete")
	for attempt := 1; attempt <= 3; attempt++ {
		gate := fmt.Sprintf("steer-delete-gate-%d", attempt)
		text := fmt.Sprintf("say raced %d", attempt)
		harness.sendAndWaitForGate(t, sessionID, gate)
		isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: text})
		require.NoError(t, err)
		require.True(t, isQueued)
		queued := harness.waitForSnapshot(t, sessionID, "one queued message", func(snapshot dexagents.AgentSnapshot) bool {
			return len(snapshot.QueuedMessages) == 1
		}).QueuedMessages[0]
		results := runConcurrently(2, func(index int) (bool, error) {
			if index == 0 {
				return harness.sessions.SteerUserMessage(callContext(t), sessionID,
					dexagents.SteerUserMessageRequest{MessageID: &queued.MessageID})
			}
			return harness.sessions.DeleteQueuedMessage(callContext(t), sessionID, queued.MessageID)
		})
		var messageNotFound *dex.ChannelMessageNotFoundError
		takenCount := 0
		for _, result := range results {
			if result.err != nil {
				require.ErrorAs(t, result.err, &messageNotFound)
				continue
			}
			if result.isAccepted {
				takenCount++
			}
		}
		require.Equal(t, 1, takenCount, "exactly one of steer and delete takes the message")
		isSteered := results[0].err == nil && results[0].isAccepted
		harness.releaseGate(t, gate)
		snapshot := harness.waitForIdle(t, sessionID)
		hasRun := slices.ContainsFunc(snapshot.Messages, func(message dexagents.AgentMessage) bool {
			return messageText(message) == "said: "+strings.TrimPrefix(text, "say ")
		})
		require.Equal(t, isSteered, hasRun, "a steered message runs, and a deleted one never does")
	}
}

func (harness *remoteSessionHarness) startSession(t *testing.T, name string) string {
	t.Helper()
	sessionID := strings.ToLower(harness.flowType) + "-" + name
	_, err := harness.starter.StartSession(callContext(t), sessionID, nil, dex.StartFlowOptions{})
	require.NoError(t, err)
	return sessionID
}

func (harness *remoteSessionHarness) sendAndFinish(
	t *testing.T,
	sessionID string,
	message dexagents.AgentUserMessage,
	turnCount int64,
) {
	t.Helper()
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, message)
	require.NoError(t, err)
	require.True(t, isQueued, message.Text)
	harness.waitForIdleTurns(t, sessionID, turnCount)
}

func (harness *remoteSessionHarness) sendAndWaitForInterrupt(t *testing.T, sessionID string, text string) dexagents.AgentInterrupt {
	t.Helper()
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: text})
	require.NoError(t, err)
	require.True(t, isQueued, text)
	return harness.waitForSnapshot(t, sessionID, "one pending interrupt", func(snapshot dexagents.AgentSnapshot) bool {
		return len(snapshot.PendingInterrupts) == 1
	}).PendingInterrupts[0]
}

// sendAndWaitForGate sends "stall <gate>" and returns a snapshot taken after the model call reached the gate.
func (harness *remoteSessionHarness) sendAndWaitForGate(t *testing.T, sessionID string, gate string) dexagents.AgentSnapshot {
	t.Helper()
	isQueued, err := harness.sessions.SendUserMessage(callContext(t), sessionID, dexagents.AgentUserMessage{Text: "stall " + gate})
	require.NoError(t, err)
	require.True(t, isQueued)
	harness.stack.waitForMarker(gate + ".reached")
	return harness.waitForSnapshot(t, sessionID, "an active turn", func(snapshot dexagents.AgentSnapshot) bool {
		return snapshot.State != nil && snapshot.State.ActiveTurnID != nil
	})
}

func (harness *remoteSessionHarness) releaseGate(t *testing.T, gate string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(harness.stack.coordinationDir, gate+".released"), nil, 0o644))
}

func (harness *remoteSessionHarness) snapshot(t *testing.T, sessionID string) dexagents.AgentSnapshot {
	t.Helper()
	snapshot, err := harness.sessions.GetAgentSnapshot(callContext(t), sessionID)
	require.NoError(t, err)
	return snapshot
}

func (harness *remoteSessionHarness) waitForIdleTurns(t *testing.T, sessionID string, turnCount int64) dexagents.AgentSnapshot {
	t.Helper()
	return harness.waitForSnapshot(t, sessionID, fmt.Sprintf("turn count %d with no active turn", turnCount),
		func(snapshot dexagents.AgentSnapshot) bool {
			return snapshot.State != nil && snapshot.State.TurnCount >= turnCount && snapshot.State.ActiveTurnID == nil &&
				len(snapshot.QueuedMessages) == 0
		})
}

// waitForIdle waits until no turn runs and no queued or steered message waits.
func (harness *remoteSessionHarness) waitForIdle(t *testing.T, sessionID string) dexagents.AgentSnapshot {
	t.Helper()
	return harness.waitForSnapshot(t, sessionID, "an idle session", isIdle)
}

func isIdle(snapshot dexagents.AgentSnapshot) bool {
	return snapshot.State != nil && snapshot.State.ActiveTurnID == nil && len(snapshot.Messages) > 0 &&
		len(snapshot.QueuedMessages) == 0 && len(snapshot.SteeredMessages) == 0
}

func (harness *remoteSessionHarness) waitForSnapshot(
	t *testing.T,
	sessionID string,
	description string,
	isDone func(dexagents.AgentSnapshot) bool,
) dexagents.AgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(sessionConvergenceTimeout)
	var last dexagents.AgentSnapshot
	var lastErr error
	for time.Now().Before(deadline) {
		snapshot, err := harness.sessions.GetAgentSnapshot(callContext(t), sessionID)
		if err == nil && isDone(snapshot) {
			return snapshot
		}
		last, lastErr = snapshot, err
		time.Sleep(sessionConvergencePollInterval)
	}
	t.Fatalf("session %s never reached %s; last snapshot %+v, error %v", sessionID, description, last, lastErr)
	return last
}

// requireLockConflictEventually calls a locking RPC until it conflicts with a holder that waits for the frozen Worker.
func (harness *remoteSessionHarness) requireLockConflictEventually(t *testing.T, call func(context.Context) (bool, error)) {
	t.Helper()
	_, err := frozenWorkerCall(t, call)
	var lockConflict *dex.RPCLockConflictError
	if errors.As(err, &lockConflict) {
		return
	}
	// This call took the lock first; the holder conflicts instead, and so does the next call.
	requireDeadlineExceeded(t, err, "the call waits with the lock")
	_, err = frozenWorkerCall(t, call)
	require.ErrorAs(t, err, &lockConflict)
}

func (harness *remoteSessionHarness) retryOnLockConflict(t *testing.T, call func(context.Context) (bool, error)) (bool, error) {
	t.Helper()
	var lockConflict *dex.RPCLockConflictError
	for attempt := 1; ; attempt++ {
		isAccepted, err := call(callContext(t))
		if !errors.As(err, &lockConflict) || attempt == raceAttempts {
			return isAccepted, err
		}
		time.Sleep(sessionConvergencePollInterval)
	}
}

type rpcCallResult struct {
	isAccepted bool
	err        error
}

// goInvoke starts call with a context that outlasts a frozen Worker and returns its result channel.
func (harness *remoteSessionHarness) goInvoke(call func(context.Context) (bool, error)) <-chan rpcCallResult {
	results := make(chan rpcCallResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteSessionCallTimeout)
		defer cancel()
		isAccepted, err := call(ctx)
		results <- rpcCallResult{isAccepted: isAccepted, err: err}
	}()
	return results
}

// frozenWorkerCall runs call with a short deadline, because a call that takes its locks waits for the frozen Worker.
func frozenWorkerCall(t *testing.T, call func(context.Context) (bool, error)) (bool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), frozenWorkerCallTimeout)
	defer cancel()
	return call(ctx)
}

func requireDeadlineExceeded(t *testing.T, err error, message string) {
	t.Helper()
	var serviceError *dex.ServiceError
	require.ErrorAs(t, err, &serviceError, message)
	require.Equal(t, codes.DeadlineExceeded, serviceError.Code, message)
}

func runConcurrently(count int, call func(index int) (bool, error)) []rpcCallResult {
	results := make([]rpcCallResult, count)
	var started sync.WaitGroup
	var finished sync.WaitGroup
	start := make(chan struct{})
	for index := range count {
		started.Add(1)
		finished.Add(1)
		go func() {
			defer finished.Done()
			started.Done()
			<-start
			isAccepted, err := call(index)
			results[index] = rpcCallResult{isAccepted: isAccepted, err: err}
		}()
	}
	started.Wait()
	close(start)
	finished.Wait()
	return results
}

func requireAcceptedRefusedOrLockConflict(t *testing.T, result rpcCallResult) {
	t.Helper()
	if result.err != nil {
		var lockConflict *dex.RPCLockConflictError
		require.ErrorAs(t, result.err, &lockConflict)
	}
}

func callContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), remoteSessionCallTimeout)
	t.Cleanup(cancel)
	return ctx
}

func lastMessageText(t *testing.T, snapshot dexagents.AgentSnapshot) string {
	t.Helper()
	require.NotEmpty(t, snapshot.Messages)
	return messageText(snapshot.Messages[len(snapshot.Messages)-1])
}

func messageText(message dexagents.AgentMessage) string {
	if message.Text == nil {
		return ""
	}
	return *message.Text
}
