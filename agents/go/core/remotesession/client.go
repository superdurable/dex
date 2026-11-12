// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package remotesession

import (
	"context"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// RemoteSessionClient calls the session RPCs and reads the AgentEvents Stream of one agent session Flow type.
//
// It wraps a dex.Client whose Registry holds the RemoteSessionFlow. Every
// method takes the session ID, which is the session's Flow ID, and is safe
// for concurrent use. A method returns the RPC's own result, so false means
// the session refused the request, not that it failed. The methods never
// retry: a *dex.RPCLockConflictError means a Step or another RPC held a lock
// of the call, and a *dex.ChannelMessageNotFoundError means the session
// consumed a queued message while the call ran. Both calls changed nothing,
// so they are safe to retry. Other errors are the dex.Client errors, such as
// *dex.FlowNotActiveOrNotFoundError for a closed session.
//
// The snapshot is the source of truth; events are best effort. Read the
// snapshot after every event gap, and to learn whether a turn ended.
// Since 0.6.0.
//
//	sessions := remotesession.NewRemoteSessionClient(client, flow)
//	snapshot, err := sessions.GetAgentSnapshot(ctx, "session-42")
//	if err != nil {
//		return err
//	}
//	for _, interrupt := range snapshot.PendingInterrupts {
//		var isResolved bool
//		if interrupt.Kind == dexagents.InterruptKindQuestion {
//			isResolved, err = sessions.AnswerQuestion(ctx, "session-42", interrupt.InterruptID, "blue")
//		} else {
//			isResolved, err = sessions.ResolveInterrupt(ctx, "session-42", interrupt.InterruptID, true, nil)
//		}
//		var lockConflict *dex.RPCLockConflictError
//		if errors.As(err, &lockConflict) {
//			continue // A Step held PendingInterrupts; read the snapshot again and retry.
//		}
//		if err != nil {
//			return err
//		}
//		log.Printf("interrupt %s resolved: %t", interrupt.InterruptID, isResolved)
//	}
type RemoteSessionClient struct {
	client *dex.Client
	flow   *RemoteSessionFlow
}

// NewRemoteSessionClient creates a typed session client.
//
// client and flow must not be nil, and client's Registry must hold flow;
// otherwise every call returns a *dex.FlowDefinitionError. It panics when
// client or flow is nil. Since 0.6.0.
func NewRemoteSessionClient(client *dex.Client, flow *RemoteSessionFlow) *RemoteSessionClient {
	if client == nil || flow == nil {
		panic("remotesession: NewRemoteSessionClient needs a Client and a RemoteSessionFlow")
	}
	return &RemoteSessionClient{client: client, flow: flow}
}

// Flow returns the RemoteSessionFlow passed to NewRemoteSessionClient.
func (sessions *RemoteSessionClient) Flow() *RemoteSessionFlow {
	return sessions.flow
}

// SendUserMessage queues one user message through send_user_message.
//
// The session starts one turn per queued message, in order, after the
// current turn ends. Set message.PlanMode to ask for a planning turn, and
// message.Images to attach images that the serving application stored. It
// returns true when the message was queued, and false for blank text, for
// PlanMode when the Flow has no plan mode, or for images that the Flow's
// runner cannot take or that fail the reference checks. A Go Worker returns
// false for PlanMode and for images.
func (sessions *RemoteSessionClient) SendUserMessage(
	ctx context.Context,
	sessionID string,
	message dexagents.AgentUserMessage,
) (bool, error) {
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.SendUserMessage, message)
}

// SteerUserMessage steers the running turn through steer_user_message.
//
// Set exactly one of request.MessageID, to move a queued message, and
// request.Text, for new text. A steered message applies at the turn's next
// safe Step boundary: the session ends the turn and starts a new one with
// every steered message. While no turn runs, new text is queued like
// SendUserMessage and a queued MessageID stays queued. It returns true when
// the message is steered, or queued while no turn runs; false when both or
// neither field is set, the text is blank, the message is no longer queued,
// or the active turn does not accept turn control (see
// dexagents.AgentState.AcceptsTurnControl). When the session consumes the
// queued message during the call, it returns *dex.ChannelMessageNotFoundError.
func (sessions *RemoteSessionClient) SteerUserMessage(
	ctx context.Context,
	sessionID string,
	request dexagents.SteerUserMessageRequest,
) (bool, error) {
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.SteerUserMessage, request)
}

// DeleteQueuedMessage deletes one queued user message through delete_queued_message.
//
// messageID is dexagents.PendingUserMessage.MessageID from the snapshot's
// QueuedMessages. It returns true when the message was deleted, and false
// when it is no longer queued. When the session consumes the message during
// the call, it returns *dex.ChannelMessageNotFoundError.
func (sessions *RemoteSessionClient) DeleteQueuedMessage(ctx context.Context, sessionID string, messageID string) (bool, error) {
	request := dexagents.DeleteQueuedMessageRequest{MessageID: messageID}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.DeleteQueuedMessage, request)
}

// CancelTurn stops the running turn through cancel_turn, with no new turn.
//
// turnID names the turn to stop, or nil for the active turn. A tool call
// that has started runs to its end; calls that have not started record
// rejected. It returns true when the request is recorded, and also for a
// second request for the same turn; a turn whose final answer already
// committed completes instead. It returns false when no turn is active,
// turnID names another turn, or the active turn does not accept turn
// control. A true result does not mean the turn has ended: read the
// snapshot. It locks PendingInterrupts and TurnCancellationRequest.
func (sessions *RemoteSessionClient) CancelTurn(ctx context.Context, sessionID string, turnID *string) (bool, error) {
	request := dexagents.CancelTurnRequest{TurnID: turnID}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.CancelTurn, request)
}

// ResolveInterrupt approves or rejects one pending tool approval, or declines one question, through resolve_interrupt.
//
// rejectionMessage is what the model sees after a rejection; nil makes it
// see "The user rejected this tool call." It is dropped when isApproved is
// true. A question needs an answer: approving one is refused, so answer it
// with AnswerQuestion. It returns true when the interrupt was pending and is
// now resolved, and false when it is unknown, already resolved, past its
// deadline, or a question with isApproved true. Only one call succeeds per
// interrupt. It locks PendingInterrupts.
func (sessions *RemoteSessionClient) ResolveInterrupt(
	ctx context.Context,
	sessionID string,
	interruptID string,
	isApproved bool,
	rejectionMessage *string,
) (bool, error) {
	request := dexagents.ResolveInterruptRequest{InterruptID: interruptID, Approved: isApproved, RejectionMessage: rejectionMessage}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.ResolveInterrupt, request)
}

// AnswerQuestion answers one pending question interrupt through answer_question.
//
// The answer becomes the result of the ask_user call, so the model continues
// the same turn. It returns true when the question was pending and is now
// answered, and false when the interrupt is not a pending question or is past
// its deadline, the answer is blank, or the answer is not one of the choices
// while free text is not allowed. Only one call succeeds per question. It
// locks PendingInterrupts.
func (sessions *RemoteSessionClient) AnswerQuestion(
	ctx context.Context,
	sessionID string,
	interruptID string,
	answer string,
) (bool, error) {
	request := dexagents.AnswerQuestionRequest{InterruptID: interruptID, Answer: answer}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.AnswerQuestion, request)
}

// ExecutePlan approves one plan revision through execute_plan, so the session starts a turn that runs it.
//
// revision is dexagents.AgentPlan.Revision of the plan the user reviewed.
// The session runs a revision once per approval. It returns true when the
// execution is accepted, and false unless the session waits for a user
// message, the plan has this revision and is draft or active, no execution of
// it is pending, and no queued or steered message or interrupt is pending. It
// locks PendingPlanExecution.
func (sessions *RemoteSessionClient) ExecutePlan(ctx context.Context, sessionID string, revision int64) (bool, error) {
	request := dexagents.ExecutePlanRequest{Revision: revision}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.ExecutePlan, request)
}

// UpdateSessionSettings replaces the session's settings from the next turn on through update_session_settings.
//
// A running turn keeps the settings it started with. An empty map clears
// them, and a nil map is refused, so the RPC returns false. Never put a
// secret in them. It returns an error without calling the RPC when the
// settings are larger than dexagents.MaxSessionSettingsBytes, true when the
// settings are stored, and false when the session has not initialized yet.
// Concurrent updates race: the last commit wins.
func (sessions *RemoteSessionClient) UpdateSessionSettings(
	ctx context.Context,
	sessionID string,
	sessionSettings map[string]any,
) (bool, error) {
	if err := dexagents.ValidateSessionSettings(sessionSettings); err != nil {
		return false, err
	}
	request := dexagents.UpdateSessionSettingsRequest{SessionSettings: sessionSettings}
	return sessions.invokeBoolRPC(ctx, sessionID, sessions.flow.UpdateSessionSettings, request)
}

// GetAgentSnapshot returns the session snapshot through get_agent_snapshot.
//
// It reads committed state without locks, so it also works on a retained
// closed session. ToolCalls holds only the active turn's records; earlier
// tool calls are in the messages.
func (sessions *RemoteSessionClient) GetAgentSnapshot(ctx context.Context, sessionID string) (dexagents.AgentSnapshot, error) {
	var snapshot dexagents.AgentSnapshot
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.GetAgentSnapshot, nil, &snapshot)
	return snapshot, err
}

// ListAgentMessages returns one page of stored messages, oldest first, through list_agent_messages.
//
// beforeSequence is the page's NextBeforeSequence from the previous call, or
// nil for the newest page. limit is clamped to 1 through 200. When retention
// deleted older messages, the page's DeletedBeforeSequence says where the
// readable history starts.
func (sessions *RemoteSessionClient) ListAgentMessages(
	ctx context.Context,
	sessionID string,
	beforeSequence *int64,
	limit int64,
) (dexagents.AgentMessagesPage, error) {
	var page dexagents.AgentMessagesPage
	request := dexagents.ListAgentMessagesRequest{BeforeSequence: beforeSequence, Limit: limit}
	err := sessions.client.InvokeRPC(ctx, sessionID, sessions.flow.ListAgentMessages, request, &page)
	return page, err
}

// ListAgentEvents returns one newest-first page of the retained AgentEvents messages, without waiting.
//
// pageSize must be positive. Pass "" as beforePageToken for the newest
// events, then the page's NextPageToken for older ones. To follow a session
// live from now, list one event and pass its ResumeToken to ReadAgentEvent;
// an empty page means no event is retained yet, so start ReadAgentEvent with
// "".
func (sessions *RemoteSessionClient) ListAgentEvents(
	ctx context.Context,
	sessionID string,
	pageSize int32,
	beforePageToken string,
) (dex.StreamMessagesPage[dexagents.AgentEvent], error) {
	var page dex.StreamMessagesPage[dexagents.AgentEvent]
	err := sessions.client.ListStreamMessages(ctx, sessionID, sessions.flow.agentEvents, pageSize, beforePageToken, &page)
	return page, err
}

// ReadAgentEvent waits for the next AgentEvents message after a resume token.
//
// Pass "" for the oldest retained event, then each returned message's
// ResumeToken. Events are best effort: after a gap, rebuild from
// GetAgentSnapshot. It returns *dex.LongPollTimeoutError when no event
// arrives before the server's long poll ends; call it again with the same
// token.
func (sessions *RemoteSessionClient) ReadAgentEvent(
	ctx context.Context,
	sessionID string,
	resumeToken string,
) (dexagents.AgentEvent, dex.StreamMessage, error) {
	var event dexagents.AgentEvent
	message, err := sessions.client.ReadStream(ctx, sessionID, sessions.flow.agentEvents, resumeToken, &event)
	return event, message, err
}

func (sessions *RemoteSessionClient) invokeBoolRPC(ctx context.Context, sessionID string, rpc any, request any) (bool, error) {
	var isAccepted bool
	err := sessions.client.InvokeRPC(ctx, sessionID, rpc, request, &isAccepted)
	return isAccepted, err
}
