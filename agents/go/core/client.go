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

// SendUserMessage queues one user message through send_user_message.
//
// It returns true when the message was queued and false for blank text.
func (sessions *AgentSessionClient) SendUserMessage(ctx context.Context, sessionID string, text string) (bool, error) {
	var isQueued bool
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.SendUserMessage, AgentUserMessage{Text: text}, &isQueued)
	return isQueued, err
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

// GetAgentSnapshot returns the session snapshot through get_agent_snapshot.
//
// It works on a running or retained closed session.
func (sessions *AgentSessionClient) GetAgentSnapshot(ctx context.Context, sessionID string) (AgentSnapshot, error) {
	var snapshot AgentSnapshot
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.GetAgentSnapshot, nil, &snapshot)
	return snapshot, err
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
