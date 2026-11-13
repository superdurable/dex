// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Package remotesession calls agent sessions that Workers of any Dex Agents core serve, from a
// Go Client that serves none.
//
// RemoteSessionFlow describes the complete session RPC set of the Python and
// TypeScript cores: send_user_message with plan_mode and images,
// steer_user_message, delete_queued_message, cancel_turn, resolve_interrupt,
// answer_question, execute_plan, update_session_settings, get_agent_snapshot,
// and list_agent_messages, with their names, locks, state loads, transaction
// modes, and wire types, plus the AgentEvents Stream. It embeds
// dex.ClientOnlyFlow and declares no Steps, so a dex.Worker refuses a
// Registry that holds it. The Go core's dexagents.AgentSessionFlow stays the
// Go Worker's schema; it serves only the RPCs that Go Workers run.
//
// RemoteSessionClient wraps a dex.Client with typed calls. It never retries:
// a *dex.RPCLockConflictError and a *dex.ChannelMessageNotFoundError reach
// the caller, which decides how long to retry; both are safe to retry,
// because the conflicting call changed nothing. The package starts no
// sessions: a Flow without Steps has no starting Step, so start sessions
// through the application that serves the Flow type.
//
//	flow, err := remotesession.NewRemoteSessionFlow("SupportAgent")
//	if err != nil {
//		return err
//	}
//	registry, err := dex.NewRegistry([]dex.Flow{flow})
//	if err != nil {
//		return err
//	}
//	client, err := dex.NewClient(registry, cache, dex.ClientOptions{FlowServiceAddress: "localhost:8801"})
//	if err != nil {
//		return err
//	}
//	sessions := remotesession.NewRemoteSessionClient(client, flow)
//	isQueued, err := sessions.SendUserMessage(ctx, "session-42", dexagents.AgentUserMessage{Text: "Where is order 7?"})
package remotesession

import (
	"errors"
	"fmt"
	"strings"

	dexagents "github.com/superdurable/dex/agents/go/core"
	"github.com/superdurable/dex/sdk-go/dex"
)

// The RPC names of the session contract that only Workers of other cores serve.
//
// dexagents declares the names that the Go core also serves, such as
// dexagents.SendUserMessageRPCName.
const (
	// SteerUserMessageRPCName is the protocol name of RemoteSessionFlow.SteerUserMessage.
	SteerUserMessageRPCName = "steer_user_message"
	// DeleteQueuedMessageRPCName is the protocol name of RemoteSessionFlow.DeleteQueuedMessage.
	DeleteQueuedMessageRPCName = "delete_queued_message"
	// CancelTurnRPCName is the protocol name of RemoteSessionFlow.CancelTurn.
	CancelTurnRPCName = "cancel_turn"
	// AnswerQuestionRPCName is the protocol name of RemoteSessionFlow.AnswerQuestion.
	AnswerQuestionRPCName = "answer_question"
	// ExecutePlanRPCName is the protocol name of RemoteSessionFlow.ExecutePlan.
	ExecutePlanRPCName = "execute_plan"
	// AcquireSessionTerminalLeaseRPCName is the protocol name of
	// RemoteSessionFlow.AcquireSessionTerminalLease. Since 0.6.3.
	AcquireSessionTerminalLeaseRPCName = "acquire_session_terminal_lease"
	// ReleaseSessionTerminalLeaseRPCName is the protocol name of
	// RemoteSessionFlow.ReleaseSessionTerminalLease. Since 0.6.3.
	ReleaseSessionTerminalLeaseRPCName = "release_session_terminal_lease"
)

// RemoteSessionFlow is the client-only description of one agent session Flow type.
//
// Register it in the Registry of a dex.Client and pass the same value to
// NewRemoteSessionClient. It describes a Flow type that Workers of the Python
// or TypeScript core serve, or Go Workers for the RPCs they run. A Registry
// holds one Flow per Flow type, so keep the Flow type's served definition,
// such as a dexagents.AgentSessionFlow, out of that Registry.
//
// The RPC methods exist only to type Client calls: each returns an error,
// and a dex.Worker never serves them, because NewWorker returns
// *dex.ClientOnlyFlowError for a Registry that holds this Flow. Do not pass
// it to Client.StartFlow: it has no starting Step, so the run would never
// start the session.
//
// A RemoteSessionFlow is immutable and safe for concurrent use. Since 0.6.0.
type RemoteSessionFlow struct {
	// ClientOnlyFlow marks the Flow client-only, so dex.NewWorker refuses a Registry that holds it.
	dex.ClientOnlyFlow
	flowType    string
	agentEvents dex.Stream[dexagents.AgentEvent]
}

var _ dex.Flow = (*RemoteSessionFlow)(nil)

// NewRemoteSessionFlow describes the agent session Flow type that Workers serve as flowType.
//
// flowType is the protocol Flow type, such as "SupportAgent", that the
// serving application passed to its session Flow. It returns an error when
// flowType is blank. Since 0.6.0.
//
//	flow, err := remotesession.NewRemoteSessionFlow("SupportAgent")
//	if err != nil {
//		return err
//	}
func NewRemoteSessionFlow(flowType string) (*RemoteSessionFlow, error) {
	if strings.TrimSpace(flowType) == "" {
		return nil, errors.New("remotesession: flow type must not be blank")
	}
	return &RemoteSessionFlow{
		flowType:    flowType,
		agentEvents: dex.DefineStream[dexagents.AgentEvent](dexagents.AgentEventsStreamName, dexagents.DefaultAgentEventsCapacityBytes),
	}, nil
}

// GetFlowType returns the Flow type passed to NewRemoteSessionFlow.
func (flow *RemoteSessionFlow) GetFlowType() string {
	return flow.flowType
}

// GetSteps returns no Steps: the serving Workers run them.
func (flow *RemoteSessionFlow) GetSteps() []dex.StepDef {
	return nil
}

// GetRPCs returns every session RPC under its contract name, with the contract's options.
func (flow *RemoteSessionFlow) GetRPCs() []dex.RPCDef {
	definitions := make([]dex.RPCDef, 0, len(sessionRPCContracts))
	for _, contract := range sessionRPCContracts {
		definitions = append(definitions, contract.define(flow, contract.rpcOptions()))
	}
	return definitions
}

// GetPersistenceSchema returns every session Attribute and Channel, and this Flow type's AgentEvents Stream.
//
// It lists the same definitions as dexagents.AgentSessionFlow, so the RPC
// locks and state loads resolve and Client.GetAttributes reads any of them.
func (flow *RemoteSessionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			dexagents.AgentStateAttribute,
			dexagents.AgentMessagesAttributeMap,
			dexagents.AgentMessageChunksAttributeMap,
			dexagents.AgentMessageIndexAttribute,
			dexagents.TurnReplySequenceAttribute,
			dexagents.FrameworkRunStateAttribute,
			dexagents.AgentToolCallsAttributeMap,
			dexagents.PendingInterruptsAttribute,
			dexagents.AgentUsageAttribute,
			dexagents.TurnCancellationRequestAttribute,
			dexagents.AgentTraceContextAttribute,
			dexagents.AgentPlanAttribute,
			dexagents.PendingPlanExecutionAttribute,
			dexagents.ContextSummaryAttribute,
			dexagents.SessionSettingsAttribute,
			dexagents.TurnSessionSettingsAttribute,
			dexagents.SessionTerminalLeaseAttribute,
		},
		Channels: []dex.ChannelDef{
			dexagents.QueuedUserMessagesChannel,
			dexagents.InterruptResolutionsChannelMap,
			dexagents.ToolCallResultsChannel,
			dexagents.SteeredUserMessagesChannel,
			dexagents.TurnControlRequestsChannel,
			dexagents.ToolCallCancellationsChannelMap,
			dexagents.PlanExecutionsChannelMap,
		},
		Streams: []dex.StreamDef{flow.agentEvents},
	}
}

// AgentEvents returns this Flow type's AgentEvents Stream.
//
// Pass it to Client.ReadStream or Client.ListStreamMessages, or use
// RemoteSessionClient.ReadAgentEvent and RemoteSessionClient.ListAgentEvents.
func (flow *RemoteSessionFlow) AgentEvents() dex.Stream[dexagents.AgentEvent] {
	return flow.agentEvents
}

// SendUserMessage types the send_user_message RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) SendUserMessage(dex.Context, dexagents.AgentUserMessage) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(dexagents.SendUserMessageRPCName)
}

// SteerUserMessage types the steer_user_message RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) SteerUserMessage(dex.Context, dexagents.SteerUserMessageRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(SteerUserMessageRPCName)
}

// DeleteQueuedMessage types the delete_queued_message RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) DeleteQueuedMessage(dex.Context, dexagents.DeleteQueuedMessageRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(DeleteQueuedMessageRPCName)
}

// CancelTurn types the cancel_turn RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) CancelTurn(dex.Context, dexagents.CancelTurnRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(CancelTurnRPCName)
}

// ResolveInterrupt types the resolve_interrupt RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) ResolveInterrupt(dex.Context, dexagents.ResolveInterruptRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(dexagents.ResolveInterruptRPCName)
}

// AnswerQuestion types the answer_question RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) AnswerQuestion(dex.Context, dexagents.AnswerQuestionRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(AnswerQuestionRPCName)
}

// ExecutePlan types the execute_plan RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) ExecutePlan(dex.Context, dexagents.ExecutePlanRequest) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(ExecutePlanRPCName)
}

// AcquireSessionTerminalLease types the acquire_session_terminal_lease RPC. It never runs; it
// returns an error. Since 0.6.3.
func (flow *RemoteSessionFlow) AcquireSessionTerminalLease(
	dex.Context,
	dexagents.AcquireSessionTerminalLeaseRequest,
) (*dex.RPCResult[dexagents.SessionTerminalLeaseResult], error) {
	return nil, flow.clientOnlyError(AcquireSessionTerminalLeaseRPCName)
}

// ReleaseSessionTerminalLease types the release_session_terminal_lease RPC. It never runs; it
// returns an error. Since 0.6.3.
func (flow *RemoteSessionFlow) ReleaseSessionTerminalLease(
	dex.Context,
	dexagents.ReleaseSessionTerminalLeaseRequest,
) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(ReleaseSessionTerminalLeaseRPCName)
}

// UpdateSessionSettings types the update_session_settings RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) UpdateSessionSettings(
	dex.Context,
	dexagents.UpdateSessionSettingsRequest,
) (*dex.RPCResult[bool], error) {
	return nil, flow.clientOnlyError(dexagents.UpdateSessionSettingsRPCName)
}

// GetAgentSnapshot types the get_agent_snapshot RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) GetAgentSnapshot(dex.Context, dex.None) (*dex.RPCResult[dexagents.AgentSnapshot], error) {
	return nil, flow.clientOnlyError(dexagents.GetAgentSnapshotRPCName)
}

// ListAgentMessages types the list_agent_messages RPC. It never runs; it returns an error.
func (flow *RemoteSessionFlow) ListAgentMessages(
	dex.Context,
	dexagents.ListAgentMessagesRequest,
) (*dex.RPCResult[dexagents.AgentMessagesPage], error) {
	return nil, flow.clientOnlyError(dexagents.ListAgentMessagesRPCName)
}

func (flow *RemoteSessionFlow) clientOnlyError(rpcName string) error {
	return fmt.Errorf("remotesession: Flow type %s is client-only; its Workers serve RPC %s", flow.flowType, rpcName)
}
