// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"context"
	"errors"
	"time"

	"github.com/superdurable/dex/sdk-go/dex"
)

const (
	defaultLockConflictRetryTimeout = 5 * time.Second
	lockConflictRetryInterval       = 50 * time.Millisecond
)

// AgentSessionClientOption configures one AgentSessionClient. Pass options to NewAgentSessionClient.
type AgentSessionClientOption interface {
	applyAgentSessionClient(client *AgentSessionClient)
}

type agentSessionClientOptionFunc func(client *AgentSessionClient)

func (option agentSessionClientOptionFunc) applyAgentSessionClient(client *AgentSessionClient) {
	option(client)
}

// WithLockConflictRetryTimeout sets how long ResolveInterrupt retries a dex.RPCLockConflictError.
//
// A retry is safe because a conflicting call makes no change. The default is
// 5 seconds; zero disables retries.
func WithLockConflictRetryTimeout(timeout time.Duration) AgentSessionClientOption {
	return agentSessionClientOptionFunc(func(client *AgentSessionClient) {
		client.lockConflictRetryTimeout = timeout
	})
}

// AgentSessionClient calls the session RPCs of one agent Flow type with typed values.
//
// It wraps a dex.Client whose Registry holds the flow. The flow only types the
// calls: the session may run on Workers of any core, because the RPC names
// and wire values are shared.
//
//	sessions := dexagents.NewAgentSessionClient(client, flow)
//	initialMessage := "Where is order 7?"
//	if _, err := sessions.StartSession(ctx, "session-42", &initialMessage, dex.StartFlowOptions{}); err != nil {
//		return err
//	}
//	snapshot, err := sessions.GetAgentSnapshot(ctx, "session-42")
//	if err != nil {
//		return err
//	}
//	for _, interrupt := range snapshot.PendingInterrupts {
//		if _, err := sessions.ResolveInterrupt(ctx, "session-42", interrupt.InterruptID, true, nil); err != nil {
//			return err
//		}
//	}
type AgentSessionClient struct {
	client                   *dex.Client
	flow                     *AgentSessionFlow
	lockConflictRetryTimeout time.Duration
}

// NewAgentSessionClient creates a typed session client.
//
// client and flow must not be nil, and client's Registry must hold flow.
func NewAgentSessionClient(client *dex.Client, flow *AgentSessionFlow, options ...AgentSessionClientOption) *AgentSessionClient {
	if client == nil || flow == nil {
		panic("dexagents: NewAgentSessionClient needs a Client and a Flow")
	}
	sessions := &AgentSessionClient{client: client, flow: flow, lockConflictRetryTimeout: defaultLockConflictRetryTimeout}
	for _, option := range options {
		option.applyAgentSessionClient(sessions)
	}
	return sessions
}

// StartSession starts one agent session whose Flow ID is sessionID.
//
// initialMessage is the optional first user message; nil starts the session
// waiting for one. It returns the server-assigned run ID, or
// dex.FlowAlreadyStartedError when the Flow ID is in use.
func (sessions *AgentSessionClient) StartSession(
	ctx context.Context,
	sessionID string,
	initialMessage *string,
	options dex.StartFlowOptions,
) (string, error) {
	return sessions.client.StartFlow(ctx, sessions.flow, sessionID, AgentSessionStartInput{InitialMessage: initialMessage}, options)
}

// StartSessionWithSettings starts one agent session with session settings.
//
// It is StartSession with AgentSessionStartInput.SessionSettings set; see
// that field. It returns an error without starting anything when the
// settings are larger than MaxSessionSettingsBytes.
//
//	runID, err := sessions.StartSessionWithSettings(ctx, "session-42", nil,
//		map[string]any{"model": "gpt-5-mini"}, dex.StartFlowOptions{})
func (sessions *AgentSessionClient) StartSessionWithSettings(
	ctx context.Context,
	sessionID string,
	initialMessage *string,
	sessionSettings map[string]any,
	options dex.StartFlowOptions,
) (string, error) {
	if err := ValidateSessionSettings(sessionSettings); err != nil {
		return "", err
	}
	input := AgentSessionStartInput{InitialMessage: initialMessage, SessionSettings: sessionSettings}
	return sessions.client.StartFlow(ctx, sessions.flow, sessionID, input, options)
}

// UpdateSessionSettings replaces the session's settings from the next turn on through
// update_session_settings.
//
// A running turn keeps the settings it started with; an empty map clears
// them, and a nil map is refused, so the RPC returns false. It returns an
// error without calling the RPC when the settings are larger than
// MaxSessionSettingsBytes, and false when the session has not initialized
// yet.
func (sessions *AgentSessionClient) UpdateSessionSettings(
	ctx context.Context,
	sessionID string,
	sessionSettings map[string]any,
) (bool, error) {
	if err := ValidateSessionSettings(sessionSettings); err != nil {
		return false, err
	}
	var isStored bool
	request := UpdateSessionSettingsRequest{SessionSettings: sessionSettings}
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.UpdateSessionSettings, request, &isStored)
	return isStored, err
}

// SendUserMessage queues one user message through send_user_message.
//
// It returns true when the message was queued and false for blank text.
//
// It returns *AgentSessionFailedError, with the Flow's reason, when the
// session failed, such as at the state limit; errors.As also finds the
// *dex.FlowNotActiveOrNotFoundError in it. Messages queued earlier never run.
// It returns *dex.FlowNotActiveOrNotFoundError alone for a session that does
// not exist or closed in another way.
//
// After the closed-Flow error it calls Client.DescribeFlow, and
// Client.WaitForFlow when the Flow failed. An error from them is joined to
// the closed-Flow error.
//
// Changed in 0.6.16: a message to a failed session returns
// *AgentSessionFailedError.
func (sessions *AgentSessionClient) SendUserMessage(ctx context.Context, sessionID string, text string) (bool, error) {
	var isQueued bool
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.SendUserMessage, AgentUserMessage{Text: text}, &isQueued)
	var closed *dex.FlowNotActiveOrNotFoundError
	if !errors.As(err, &closed) {
		return isQueued, err
	}
	reason, isFailed, readErr := readSessionFailure(ctx, sessions.client, sessionID)
	if readErr != nil {
		return isQueued, errors.Join(err, readErr)
	}
	if !isFailed {
		return isQueued, err
	}
	return isQueued, &AgentSessionFailedError{SessionID: sessionID, Reason: reason, Err: err}
}

// ResolveInterrupt approves or rejects one pending interrupt through resolve_interrupt.
//
// It retries a dex.RPCLockConflictError until the lock conflict retry
// timeout passes. rejectionMessage is what the model sees after a rejection;
// nil makes it see "The user rejected this tool call." It returns true when
// resolved, and false when the interrupt is unknown, already resolved, or past
// its deadline.
func (sessions *AgentSessionClient) ResolveInterrupt(
	ctx context.Context,
	sessionID string,
	interruptID string,
	isApproved bool,
	rejectionMessage *string,
) (bool, error) {
	request := ResolveInterruptRequest{InterruptID: interruptID, Approved: isApproved, RejectionMessage: rejectionMessage}
	deadline := time.Now().Add(sessions.lockConflictRetryTimeout)
	for {
		var isResolved bool
		err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.ResolveInterrupt, request, &isResolved)
		var lockConflict *dex.RPCLockConflictError
		if !errors.As(err, &lockConflict) || !time.Now().Before(deadline) {
			return isResolved, err
		}
		timer := time.NewTimer(lockConflictRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

// GetAgentSnapshot returns the session snapshot through get_agent_snapshot, with a failed Flow's status and reason.
//
// It works on a running or retained closed session. When the session's Flow
// failed, the snapshot has AgentState.Status failed and LastTurnError set to
// the Flow's reason, no active turn, and empty PendingInterrupts and ToolCalls,
// even when the session had no room left to store them. That happens at the
// state limit when the failing Step has no failure route, such as
// AwaitUserMessage storing the next user message, or when the failure's own
// commit does not fit. TurnCount then counts the turn that was running. The
// get_agent_snapshot RPC alone returns only what the session stored. A
// snapshot with no state, from a session that failed before it stored one,
// is returned as stored; read the reason with Client.WaitForFlow.
//
// Unless the stored status is failed, it also calls Client.DescribeFlow, and
// Client.WaitForFlow when the Flow failed. It returns their errors.
//
// Changed in 0.6.16: it reads how the Flow closed, as the Python core's
// AgentSessionClient.get_agent_snapshot and the TypeScript core's
// readAgentSnapshot do.
func (sessions *AgentSessionClient) GetAgentSnapshot(ctx context.Context, sessionID string) (AgentSnapshot, error) {
	var snapshot AgentSnapshot
	if err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.GetAgentSnapshot, nil, &snapshot); err != nil {
		return snapshot, err
	}
	if !mayHideSessionFailure(snapshot) {
		return snapshot, nil
	}
	reason, isFailed, err := readSessionFailure(ctx, sessions.client, sessionID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	if isFailed {
		markSessionFailed(&snapshot, reason)
	}
	return snapshot, nil
}

// ListAgentMessages returns one page of stored messages, oldest first, through list_agent_messages.
//
// beforeSequence is the anchor from the previous page, or nil for the newest
// page. limit is clamped to 1 through 200.
func (sessions *AgentSessionClient) ListAgentMessages(
	ctx context.Context,
	sessionID string,
	beforeSequence *int64,
	limit int64,
) (AgentMessagesPage, error) {
	var page AgentMessagesPage
	request := ListAgentMessagesRequest{BeforeSequence: beforeSequence, Limit: limit}
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.ListAgentMessages, request, &page)
	return page, err
}

// ReadAgentEvent waits for the next AgentEvents message after a resume token.
//
// Pass "" for the oldest retained event, then each returned message's
// ResumeToken. Events are best effort: after a gap, rebuild from
// GetAgentSnapshot. It returns dex.LongPollTimeoutError when no event arrives
// before the server's long poll ends.
func (sessions *AgentSessionClient) ReadAgentEvent(
	ctx context.Context,
	sessionID string,
	resumeToken string,
) (AgentEvent, dex.StreamMessage, error) {
	var event AgentEvent
	message, err := sessions.client.ReadStream(ctx, sessionID, sessions.flow.agentEvents, resumeToken, &event)
	return event, message, err
}
