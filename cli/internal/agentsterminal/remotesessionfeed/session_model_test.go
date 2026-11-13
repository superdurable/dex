// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesessionfeed

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launchcontract"
	"github.com/superdurable/dex/cli/internal/agentsterminal/launcherchannel"
	"github.com/superdurable/dex/cli/internal/agentsterminal/sessionfeed"
	"github.com/superdurable/dex/sdk-go/dex"
)

// fakeSessions answers the session RPCs from functions a test sets.
type fakeSessions struct {
	mutex       sync.Mutex
	snapshot    dexagents.AgentSnapshot
	cancelTurn  func() (bool, error)
	steer       func() (bool, error)
	cancelCalls int
	// listMessages answers ListAgentMessages when set.
	listMessages func(sessionID string, beforeSequence *int64, limit int64) (dexagents.AgentMessagesPage, error)
	// listEvents answers ListAgentEvents when set.
	listEvents func(pageSize int32, beforePageToken string) (dex.StreamMessagesPage[dexagents.AgentEvent], error)
	// writtenSettings are the UpdateSessionSettings arguments, in order.
	writtenSettings []map[string]any
}

func (sessions *fakeSessions) SendUserMessage(context.Context, string, dexagents.AgentUserMessage) (bool, error) {
	return true, nil
}

func (sessions *fakeSessions) SteerUserMessage(context.Context, string, dexagents.SteerUserMessageRequest) (bool, error) {
	return sessions.steer()
}

func (sessions *fakeSessions) DeleteQueuedMessage(context.Context, string, string) (bool, error) {
	return true, nil
}

func (sessions *fakeSessions) CancelTurn(context.Context, string, *string) (bool, error) {
	sessions.mutex.Lock()
	sessions.cancelCalls++
	sessions.mutex.Unlock()
	return sessions.cancelTurn()
}

func (sessions *fakeSessions) ResolveInterrupt(context.Context, string, string, bool, *string) (bool, error) {
	return true, nil
}

func (sessions *fakeSessions) AnswerQuestion(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func (sessions *fakeSessions) ExecutePlan(context.Context, string, int64) (bool, error) {
	return true, nil
}

// UpdateSessionSettings records the settings and stores them in the
// snapshot, as the session would.
func (sessions *fakeSessions) UpdateSessionSettings(_ context.Context, _ string, sessionSettings map[string]any) (bool, error) {
	sessions.mutex.Lock()
	defer sessions.mutex.Unlock()
	sessions.writtenSettings = append(sessions.writtenSettings, maps.Clone(sessionSettings))
	sessions.snapshot.SessionSettings = maps.Clone(sessionSettings)
	return true, nil
}

func (sessions *fakeSessions) recordedSettings() []map[string]any {
	sessions.mutex.Lock()
	defer sessions.mutex.Unlock()
	return slices.Clone(sessions.writtenSettings)
}

func (sessions *fakeSessions) GetAgentSnapshot(context.Context, string) (dexagents.AgentSnapshot, error) {
	sessions.mutex.Lock()
	defer sessions.mutex.Unlock()
	return sessions.snapshot, nil
}

func (sessions *fakeSessions) ListAgentMessages(_ context.Context, sessionID string, beforeSequence *int64, limit int64) (dexagents.AgentMessagesPage, error) {
	if sessions.listMessages != nil {
		return sessions.listMessages(sessionID, beforeSequence, limit)
	}
	return dexagents.AgentMessagesPage{}, nil
}

func (sessions *fakeSessions) ListAgentEvents(_ context.Context, _ string, pageSize int32, beforePageToken string) (dex.StreamMessagesPage[dexagents.AgentEvent], error) {
	if sessions.listEvents != nil {
		return sessions.listEvents(pageSize, beforePageToken)
	}
	return dex.StreamMessagesPage[dexagents.AgentEvent]{}, nil
}

func (sessions *fakeSessions) ReadAgentEvent(ctx context.Context, _ string, _ string) (dexagents.AgentEvent, dex.StreamMessage, error) {
	<-ctx.Done()
	return dexagents.AgentEvent{}, dex.StreamMessage{}, ctx.Err()
}

// noLocalRequests serves no local request.
type noLocalRequests struct{}

func (noLocalRequests) HasMethod(string) bool                              { return false }
func (noLocalRequests) DisableMethod(string)                               {}
func (noLocalRequests) Notifications() <-chan launcherchannel.Notification { return nil }
func (noLocalRequests) Call(context.Context, string, string, any) launcherchannel.RequestOutcome {
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeChannelEnded}
}
func (noLocalRequests) CallWithRecoveryKey(context.Context, string, string, any, func()) launcherchannel.RequestOutcome {
	return launcherchannel.RequestOutcome{Kind: launcherchannel.OutcomeChannelEnded}
}

type modelHarness struct {
	t        *testing.T
	feed     *RemoteSessionFeed
	model    *sessionModel
	sessions *fakeSessions
	now      time.Time
}

func newModelHarness(t *testing.T) *modelHarness {
	harness := &modelHarness{t: t, sessions: &fakeSessions{}, now: time.Unix(1_800_000_000, 0)}
	harness.feed = NewRemoteSessionFeed(RemoteSessionFeedConfig{
		Sessions: harness.sessions,
		Requests: noLocalRequests{},
		Launch: launchcontract.LaunchDocument{
			Command:      launchcontract.LaunchCommand{Name: "dex-agents-test", Harness: "test"},
			Session:      launchcontract.LaunchSession{FlowType: "Test", SessionID: "session-1"},
			Capabilities: launchcontract.LaunchCapabilities{HasTurnControl: true},
		},
		Timings: FeedTimings{LockConflictBudget: 200 * time.Millisecond, TurnEndEventWait: 2 * time.Second},
		Now:     func() time.Time { return harness.now },
	})
	harness.model = newSessionModel(harness.feed, "session-1", 0, false)
	harness.applySnapshot(snapshotOf(idleState(0)))
	harness.drain()
	return harness
}

func (harness *modelHarness) applySnapshot(snapshot dexagents.AgentSnapshot) {
	harness.model.applySnapshot(snapshot)
}

// drain returns every update queued so far.
func (harness *modelHarness) drain() []sessionfeed.SessionUpdate {
	var updates []sessionfeed.SessionUpdate
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		update, err := harness.feed.NextSessionUpdate(ctx)
		cancel()
		if err != nil {
			return updates
		}
		updates = append(updates, update)
	}
}

func idleState(turnCount int64) *dexagents.AgentState {
	return &dexagents.AgentState{Status: dexagents.AgentSessionStatusWaitingForUserMessage, TurnCount: turnCount, AcceptsTurnControl: true}
}

func runningState(turnID string, turnCount int64) *dexagents.AgentState {
	return &dexagents.AgentState{Status: dexagents.AgentSessionStatusRunningModel, TurnCount: turnCount, ActiveTurnID: &turnID, AcceptsTurnControl: true}
}

func snapshotOf(state *dexagents.AgentState, messages ...dexagents.AgentMessage) dexagents.AgentSnapshot {
	return dexagents.AgentSnapshot{State: state, Messages: messages}
}

func message(sequence int64, turnID string, role dexagents.MessageRole, text string) dexagents.AgentMessage {
	return dexagents.AgentMessage{Sequence: sequence, TurnID: turnID, Role: role, Text: &text}
}

func textEvent(turnID string, step string, attempt int64, text string) dexagents.AgentEvent {
	return dexagents.AgentEvent{Type: "text_delta", TurnID: &turnID, StepExecutionID: step, Attempt: attempt, Payload: `{"text":"` + text + `"}`}
}

func endEvent(turnID string, eventType string, payload string) dexagents.AgentEvent {
	return dexagents.AgentEvent{Type: eventType, TurnID: &turnID, Attempt: 1, Payload: payload}
}

func updatesOfType[T sessionfeed.SessionUpdate](updates []sessionfeed.SessionUpdate) []T {
	var matching []T
	for _, update := range updates {
		if typed, isType := update.(T); isType {
			matching = append(matching, typed)
		}
	}
	return matching
}

// A message with a declared sender name shows the name instead of an origin
// label, live and in history.
func TestSenderNamedMessageShowsItsName(t *testing.T) {
	harness := newModelHarness(t)
	named := message(1, "turn-1", dexagents.MessageRoleUser, "hello")
	senderName := "Alice"
	named.SenderName = &senderName
	harness.applySnapshot(snapshotOf(idleState(1), named,
		message(2, "turn-1", dexagents.MessageRoleAssistant, "hi Alice")))
	recorded := updatesOfType[sessionfeed.UserMessageRecorded](harness.drain())
	require.Len(t, recorded, 1)
	require.Equal(t, "Alice", recorded[0].SenderName)
	require.Empty(t, recorded[0].Origin, "the name says who sent it")

	update, isShown := earlierMessageUpdate(named)
	require.True(t, isShown)
	require.Equal(t, "Alice", update.(sessionfeed.UserMessageRecorded).SenderName)
}

// A turn that started and ended between two snapshots still shows its user
// message, start, reply, and end, after the wait for its end event.
func TestTurnBetweenTwoSnapshots(t *testing.T) {
	harness := newModelHarness(t)
	finished := snapshotOf(idleState(1),
		message(1, "turn-1", dexagents.MessageRoleUser, "hello"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "hi there"))
	harness.applySnapshot(finished)
	updates := harness.drain()
	recorded := updatesOfType[sessionfeed.UserMessageRecorded](updates)
	require.Len(t, recorded, 1)
	require.Equal(t, otherClientOrigin, recorded[0].Origin, "this terminal did not send it")
	require.Len(t, updatesOfType[sessionfeed.TurnStarted](updates), 1)
	require.Equal(t, "hi there", updatesOfType[sessionfeed.AssistantMessageRecorded](updates)[0].Text)
	require.Empty(t, updatesOfType[sessionfeed.TurnEnded](updates), "it waits for the end event")
	harness.now = harness.now.Add(3 * time.Second)
	harness.applySnapshot(finished)
	ended := updatesOfType[sessionfeed.TurnEnded](harness.drain())
	require.Len(t, ended, 1)
	require.Equal(t, sessionfeed.TurnOutcomeCompleted, ended[0].Outcome)
}

// A message this terminal sent while idle is printed by the screen, so the
// feed does not record it again.
func TestLocalMessagePrintsOnce(t *testing.T) {
	harness := newModelHarness(t)
	harness.model.recordLocalSend("hello")
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "hello")))
	updates := harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.UserMessageRecorded](updates))
	require.Len(t, updatesOfType[sessionfeed.TurnStarted](updates), 1)
}

// Only deltas of the highest attempt of a Step are kept; deltas for a turn
// the snapshot has not started wait for it; deltas of an ended turn drop.
func TestDeltasFollowTheHighestAttempt(t *testing.T) {
	harness := newModelHarness(t)
	harness.model.applyEvent(textEvent("turn-1", "step-a", 1, "early"))
	require.Empty(t, harness.drain(), "held until the turn starts")
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "go")))
	deltas := updatesOfType[sessionfeed.AssistantTextDelta](harness.drain())
	require.Len(t, deltas, 1)
	require.Equal(t, "early", deltas[0].Text)
	harness.model.applyEvent(textEvent("turn-1", "step-a", 2, "retry"))
	harness.model.applyEvent(textEvent("turn-1", "step-a", 1, "stale"))
	deltas = updatesOfType[sessionfeed.AssistantTextDelta](harness.drain())
	require.Len(t, deltas, 1)
	require.Equal(t, 2, deltas[0].Attempt)
	harness.model.applyEvent(endEvent("turn-1", "turn_completed", `{"message_count":1}`))
	harness.applySnapshot(snapshotOf(idleState(1),
		message(1, "turn-1", dexagents.MessageRoleUser, "go"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "retry done")))
	updates := harness.drain()
	recorded := updatesOfType[sessionfeed.AssistantMessageRecorded](updates)
	require.Len(t, recorded, 1)
	require.Equal(t, "step-a", recorded[0].StepExecutionID, "the final message pairs with its streamed Step")
	require.Len(t, updatesOfType[sessionfeed.TurnEnded](updates), 1)
	harness.model.applyEvent(textEvent("turn-1", "step-a", 3, "late"))
	require.Empty(t, harness.drain())
}

// The end event decides the outcome: cancelled, or failed with its error.
func TestTurnOutcomeFromTheEndEvent(t *testing.T) {
	for _, testCase := range []struct {
		eventType string
		payload   string
		outcome   sessionfeed.TurnOutcome
		isFailure bool
	}{
		{"turn_cancelled", `{}`, sessionfeed.TurnOutcomeCancelled, false},
		{"turn_failed", `{"error":"model unavailable"}`, sessionfeed.TurnOutcomeFailed, true},
		{"turn_steered", `{}`, sessionfeed.TurnOutcomeCompleted, false},
	} {
		t.Run(testCase.eventType, func(t *testing.T) {
			harness := newModelHarness(t)
			harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "go")))
			harness.model.applyEvent(endEvent("turn-1", testCase.eventType, testCase.payload))
			harness.applySnapshot(snapshotOf(idleState(1), message(1, "turn-1", dexagents.MessageRoleUser, "go")))
			ended := updatesOfType[sessionfeed.TurnEnded](harness.drain())
			require.Len(t, ended, 1)
			require.Equal(t, testCase.outcome, ended[0].Outcome)
			if testCase.isFailure {
				require.Contains(t, ended[0].FailureText, "model unavailable")
			}
			require.Equal(t, map[bool]int{true: 1, false: 0}[testCase.isFailure], harness.feed.ExitCode())
		})
	}
}

// Without live events, an accepted cancel makes the ended turn cancelled.
func TestAcceptedCancelWithoutEvents(t *testing.T) {
	harness := newModelHarness(t)
	harness.model.isEventsAvailable = false
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "go")))
	harness.model.cancelAcceptedTurns["turn-1"] = true
	harness.applySnapshot(snapshotOf(idleState(1), message(1, "turn-1", dexagents.MessageRoleUser, "go")))
	ended := updatesOfType[sessionfeed.TurnEnded](harness.drain())
	require.Len(t, ended, 1)
	require.Equal(t, sessionfeed.TurnOutcomeCancelled, ended[0].Outcome)
}

// A steered turn ends; the session is idle for a moment with the steered
// message pending; the next turn starts with the message this terminal
// queued, which the screen did not print.
func TestSteeredTurnThenTheNextTurn(t *testing.T) {
	harness := newModelHarness(t)
	harness.model.recordLocalSend("first")
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0), message(1, "turn-1", dexagents.MessageRoleUser, "first")))
	harness.model.recordLocalSend("steer me")
	harness.drain()
	harness.model.applyEvent(endEvent("turn-1", "turn_steered", `{}`))
	between := snapshotOf(idleState(1), message(1, "turn-1", dexagents.MessageRoleUser, "first"))
	between.SteeredMessages = []dexagents.PendingUserMessage{{MessageID: "m-2", Message: dexagents.AgentUserMessage{Text: "steer me"}}}
	harness.applySnapshot(between)
	updates := harness.drain()
	require.Len(t, updatesOfType[sessionfeed.TurnEnded](updates), 1)
	require.Empty(t, updatesOfType[sessionfeed.TurnStarted](updates))
	harness.applySnapshot(snapshotOf(runningState("turn-2", 1),
		message(1, "turn-1", dexagents.MessageRoleUser, "first"),
		message(2, "turn-2", dexagents.MessageRoleUser, "steer me")))
	updates = harness.drain()
	recorded := updatesOfType[sessionfeed.UserMessageRecorded](updates)
	require.Len(t, recorded, 1)
	require.Equal(t, "steer me", recorded[0].Text)
	require.Empty(t, recorded[0].Origin, "this terminal queued it")
	started := updatesOfType[sessionfeed.TurnStarted](updates)
	require.Len(t, started, 1)
	require.Equal(t, 2, started[0].TurnNumber)
}

// cancel_turn false means the turn already ended or the session refused;
// a lock that stays held ends in NotConfirmed after the retry budget.
func TestCancelAnswers(t *testing.T) {
	lockConflict := &dex.RPCLockConflictError{ServiceError: &dex.ServiceError{Op: "InvokeRPC", Code: codes.Aborted, Detail: "locked by a Step"}}
	for _, testCase := range []struct {
		name     string
		answer   func() (bool, error)
		snapshot dexagents.AgentSnapshot
		outcome  sessionfeed.CancelOutcome
	}{
		{"accepted after a lock conflict", sequence([]bool{false, true}, []error{lockConflict, nil}), snapshotOf(runningState("turn-1", 0)), sessionfeed.CancelOutcomeAccepted},
		{"turn already ended", sequence([]bool{false}, []error{nil}), snapshotOf(idleState(1)), sessionfeed.CancelOutcomeTurnAlreadyEnded},
		{"refused", sequence([]bool{false}, []error{nil}), snapshotOf(runningState("turn-1", 0)), sessionfeed.CancelOutcomeRefused},
		{"locked past the budget", func() (bool, error) { return false, lockConflict }, snapshotOf(runningState("turn-1", 0)), sessionfeed.CancelOutcomeNotConfirmed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newModelHarness(t)
			harness.sessions.cancelTurn = testCase.answer
			harness.sessions.snapshot = testCase.snapshot
			go func() {
				// cancelAccepted reaches the loop; nothing runs it in this test.
				for range harness.feed.inputs {
				}
			}()
			require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.CancelTurn{TurnID: "turn-1"}))
			answers := updatesOfType[sessionfeed.CancelRequestAnswered](harness.drain())
			require.Len(t, answers, 1)
			require.Equal(t, testCase.outcome, answers[0].Outcome)
		})
	}
}

// A steer that meets a consumed message says the message already started a turn.
func TestSteerOfAConsumedMessage(t *testing.T) {
	harness := newModelHarness(t)
	harness.sessions.steer = func() (bool, error) {
		return false, &dex.ChannelMessageNotFoundError{ServiceError: &dex.ServiceError{Op: "InvokeRPC", Code: codes.NotFound}}
	}
	go func() {
		for range harness.feed.inputs {
		}
	}()
	require.NoError(t, harness.feed.SubmitUserAction(context.Background(), sessionfeed.SteerQueuedMessage{MessageID: "m-1"}))
	notices := updatesOfType[sessionfeed.NoticeRaised](harness.drain())
	require.Len(t, notices, 1)
	require.Equal(t, "The message already started a turn.", notices[0].Text)
}

// A plan execution message shows as a notice line, never as a user message
// from another client, and a note for the model shows nothing.
func TestSessionWrittenMessages(t *testing.T) {
	harness := newModelHarness(t)
	planText := "[Dex] The user approved plan revision 3. Execute it and keep the task statuses current with write_todos."
	harness.applySnapshot(snapshotOf(runningState("turn-1", 0),
		originMessage(message(1, "turn-1", dexagents.MessageRoleUser, planText), dexagents.AgentMessageOriginPlanExecution)))
	updates := harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.UserMessageRecorded](updates))
	notices := updatesOfType[sessionfeed.NoticeRaised](updates)
	require.Len(t, notices, 1)
	require.Equal(t, sessionfeed.NoticeRaised{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 3", Sequence: 1}, notices[0])
	require.Len(t, updatesOfType[sessionfeed.TurnStarted](updates), 1)

	harness.model.applyEvent(endEvent("turn-1", "turn_failed", `{"error":"model unavailable"}`))
	harness.applySnapshot(snapshotOf(idleState(1),
		originMessage(message(1, "turn-1", dexagents.MessageRoleUser, planText), dexagents.AgentMessageOriginPlanExecution),
		originMessage(message(2, "turn-1", dexagents.MessageRoleUser, "[Dex] The previous turn failed after these tool calls ran:"), dexagents.AgentMessageOriginTurnFailureNote),
		originMessage(message(3, "turn-1", dexagents.MessageRoleUser, "[Dex] a later note"), "later_origin")))
	updates = harness.drain()
	require.Empty(t, updatesOfType[sessionfeed.UserMessageRecorded](updates))
	require.Empty(t, updatesOfType[sessionfeed.NoticeRaised](updates))
	require.Len(t, updatesOfType[sessionfeed.TurnEnded](updates), 1)

	// Running the same revision again is a new execution with its own notice.
	harness.applySnapshot(snapshotOf(runningState("turn-2", 1),
		originMessage(message(1, "turn-1", dexagents.MessageRoleUser, planText), dexagents.AgentMessageOriginPlanExecution),
		originMessage(message(4, "turn-2", dexagents.MessageRoleUser, planText), dexagents.AgentMessageOriginPlanExecution)))
	notices = updatesOfType[sessionfeed.NoticeRaised](harness.drain())
	require.Equal(t, []sessionfeed.NoticeRaised{
		{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 3", Sequence: 4},
	}, notices)
}

// History shows a plan execution as the same notice, each execution of one
// revision with its own sequence, and one whose text does not name a
// revision without the number.
func TestSessionWrittenMessagesInHistory(t *testing.T) {
	harness := newModelHarness(t)
	harness.model.emitHistory(dexagents.AgentMessagesPage{Messages: []dexagents.AgentMessage{
		message(1, "turn-1", dexagents.MessageRoleUser, "make a plan"),
		message(2, "turn-1", dexagents.MessageRoleAssistant, "here is the plan"),
		originMessage(message(3, "turn-2", dexagents.MessageRoleUser, "[Dex] The user approved plan revision 12. Execute it."), dexagents.AgentMessageOriginPlanExecution),
		originMessage(message(4, "turn-3", dexagents.MessageRoleUser, "[Dex] Run it."), dexagents.AgentMessageOriginPlanExecution),
		originMessage(message(5, "turn-4", dexagents.MessageRoleUser, "[Dex] The user approved plan revision 12. Execute it."), dexagents.AgentMessageOriginPlanExecution),
	}}, nil, nil)
	updates := harness.drain()
	recorded := updatesOfType[sessionfeed.UserMessageRecorded](updates)
	require.Len(t, recorded, 1)
	require.Equal(t, "make a plan", recorded[0].Text)
	require.Empty(t, recorded[0].Origin)
	notices := updatesOfType[sessionfeed.NoticeRaised](updates)
	require.Equal(t, []sessionfeed.NoticeRaised{
		{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 12", Sequence: 3},
		{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running the approved plan", Sequence: 4},
		{Severity: sessionfeed.NoticeSeverityInfo, Text: "Running plan revision 12", Sequence: 5},
	}, notices)
}

// The revision pattern reads every plan execution text the agents conformance suite fixes.
func TestPlanExecutionRevisionPatternMatchesTheConformanceTexts(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "agents", "conformance", "texts.json"))
	require.NoError(t, err)
	var texts struct {
		Templates struct {
			PlanExecutionMessage []struct {
				Input struct {
					Revision int64 `json:"revision"`
				} `json:"input"`
				Text string `json:"text"`
			} `json:"plan_execution_message"`
		} `json:"templates"`
	}
	require.NoError(t, json.Unmarshal(text, &texts))
	require.NotEmpty(t, texts.Templates.PlanExecutionMessage)
	for _, testCase := range texts.Templates.PlanExecutionMessage {
		match := planExecutionRevisionPattern.FindStringSubmatch(testCase.Text)
		require.NotNil(t, match, testCase.Text)
		require.Equal(t, strconv.FormatInt(testCase.Input.Revision, 10), match[1])
	}
}

func originMessage(stored dexagents.AgentMessage, origin dexagents.AgentMessageOrigin) dexagents.AgentMessage {
	stored.Origin = &origin
	return stored
}

func sequence(answers []bool, errs []error) func() (bool, error) {
	var mutex sync.Mutex
	call := 0
	return func() (bool, error) {
		mutex.Lock()
		defer mutex.Unlock()
		index := min(call, len(answers)-1)
		call++
		return answers[index], errs[index]
	}
}
