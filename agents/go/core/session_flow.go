// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

package dexagents

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/superdurable/dex/sdk-go/dex"
)

// The RPC names of the session contract, shared with the Python and TypeScript cores.
const (
	// SendUserMessageRPCName is the protocol name of AgentSessionFlow.SendUserMessage.
	SendUserMessageRPCName = "send_user_message"
	// ResolveInterruptRPCName is the protocol name of AgentSessionFlow.ResolveInterrupt.
	ResolveInterruptRPCName = "resolve_interrupt"
	// GetAgentSnapshotRPCName is the protocol name of AgentSessionFlow.GetAgentSnapshot.
	GetAgentSnapshotRPCName = "get_agent_snapshot"
	// ListAgentMessagesRPCName is the protocol name of AgentSessionFlow.ListAgentMessages.
	ListAgentMessagesRPCName = "list_agent_messages"
	// UpdateSessionSettingsRPCName is the protocol name of AgentSessionFlow.UpdateSessionSettings.
	UpdateSessionSettingsRPCName = "update_session_settings"
)

// The persistence definitions that every agent session Flow type registers.
//
// The names are wire contracts shared with the Python and TypeScript cores, so
// a Client in any language reads any session with Client.GetAttributes:
//
//	var state dexagents.AgentState
//	_, err := client.GetAttributes(ctx, "session-42", dex.ReadAttribute(dexagents.AgentStateAttribute, &state))
//
// The AgentEvents Stream is not here: each Flow type owns its own Stream,
// available from AgentSessionFlow.AgentEvents. The definitions after
// AgentUsageAttribute belong to contract parts that this Go core does not run.
// It registers them anyway, so the schema matches the other cores and
// get_agent_snapshot returns what their Workers wrote.
var (
	// AgentStateAttribute is the current session position. It is unset until StartAgentSession
	// commits.
	AgentStateAttribute = dex.DefineAttribute[AgentState]("AgentState")
	// AgentMessagesAttributeMap holds the newest messages of the conversation, the window, keyed by
	// MessageSequenceKey. Older messages move into AgentMessageChunksAttributeMap.
	AgentMessagesAttributeMap = dex.DefineAttributeMap[AgentMessage]("AgentMessages")
	// AgentMessageChunksAttributeMap holds the archived messages, keyed by MessageSequenceKey of each
	// chunk's first message. Each instance is written once.
	AgentMessageChunksAttributeMap = dex.DefineAttributeMap[AgentMessageChunk]("AgentMessageChunks")
	// AgentMessageIndexAttribute says which messages are in the window and which chunks exist. It is
	// unset until the session first adds a chunk.
	AgentMessageIndexAttribute = dex.DefineAttribute[AgentMessageIndex]("AgentMessageIndex")
	// TurnReplySequenceAttribute records where the latest turn's replies start, tagged with the turn.
	TurnReplySequenceAttribute = dex.DefineAttribute[TurnReplySequence]("TurnReplySequence")
	// FrameworkRunStateAttribute is opaque framework run state JSON text, set only while a turn
	// waits for its tool calls.
	FrameworkRunStateAttribute = dex.DefineAttribute[string]("FrameworkRunState")
	// AgentToolCallsAttributeMap holds the deferred tool call records of the active turn, keyed by
	// tool call ID.
	AgentToolCallsAttributeMap = dex.DefineAttributeMap[AgentToolCall]("AgentToolCalls")
	// PendingInterruptsAttribute holds the interrupts that resolve_interrupt can still answer.
	PendingInterruptsAttribute = dex.DefineAttribute[[]AgentInterrupt]("PendingInterrupts")
	// AgentUsageAttribute is the model usage of the whole session.
	AgentUsageAttribute = dex.DefineAttribute[AgentUsage]("AgentUsage")
	// QueuedUserMessagesChannel holds user messages waiting to start their turns.
	QueuedUserMessagesChannel = dex.DefineChannel[AgentUserMessage]("QueuedUserMessages")
	// InterruptResolutionsChannelMap carries interrupt answers, keyed by interrupt ID.
	InterruptResolutionsChannelMap = dex.DefineChannelMap[AgentInterruptResolution]("InterruptResolutions")
	// ToolCallResultsChannel is internal: ResumeTurn waits for one message per tool call.
	ToolCallResultsChannel = dex.DefineChannel[ToolCallCompleted]("ToolCallResults")
	// SteeredUserMessagesChannel holds steered user messages that steering has not applied yet.
	SteeredUserMessagesChannel = dex.DefineChannel[AgentUserMessage]("SteeredUserMessages")
	// TurnControlRequestsChannel is internal: steering and cancellation wake-ups.
	TurnControlRequestsChannel = dex.DefineChannel[TurnControlRequest]("TurnControlRequests")
	// TurnCancellationRequestAttribute is the latest cancel_turn request.
	TurnCancellationRequestAttribute = dex.DefineAttribute[TurnCancellationRequest]("TurnCancellationRequest")
	// ToolCallCancellationsChannelMap is internal: wake-ups of Steps that wait on one tool call.
	ToolCallCancellationsChannelMap = dex.DefineChannelMap[ToolCallCancellation]("ToolCallCancellations")
	// AgentTraceContextAttribute is the W3C traceparent of the current activation's root span.
	AgentTraceContextAttribute = dex.DefineAttribute[string]("AgentTraceContext")
	// AgentPlanAttribute is the session's plan.
	AgentPlanAttribute = dex.DefineAttribute[AgentPlan]("AgentPlan")
	// PlanExecutionsChannelMap carries approved plan executions, keyed by revision.
	PlanExecutionsChannelMap = dex.DefineChannelMap[PlanExecutionRequest]("PlanExecutions")
	// PendingPlanExecutionAttribute is the plan execution accepted and not yet consumed.
	PendingPlanExecutionAttribute = dex.DefineAttribute[PlanExecutionRequest]("PendingPlanExecution")
	// ContextSummaryAttribute is the summary that compaction wrote.
	ContextSummaryAttribute = dex.DefineAttribute[ContextSummary]("ContextSummary")
	// SessionSettingsAttribute holds the settings the next turn starts with: the start input's
	// SessionSettings, then each update_session_settings. It is unset when the session has none.
	SessionSettingsAttribute = dex.DefineAttribute[map[string]any]("SessionSettings")
	// TurnSessionSettingsAttribute holds the settings a turn started with, tagged with its turn,
	// copied from SessionSettingsAttribute when the turn began. A copy from an earlier turn reads
	// as no settings.
	TurnSessionSettingsAttribute = dex.DefineAttribute[TurnSessionSettings]("TurnSessionSettings")
	// SessionTerminalLeaseAttribute holds the lease of the one terminal that has the session open;
	// unset when no terminal holds it. Since 0.6.3.
	SessionTerminalLeaseAttribute = dex.DefineAttribute[SessionTerminalLease]("SessionTerminalLease")
	// SessionLastTerminalEnvironmentAttribute holds the Environment of the latest
	// acquire_session_terminal_lease call that gave the lease to a new holder. It
	// stays after a release, so it says where the session was last open. It is
	// unset until such a call carries an Environment. Since 0.6.7.
	SessionLastTerminalEnvironmentAttribute = dex.DefineAttribute[map[string]string]("SessionLastTerminalEnvironment")
)

const (
	snapshotMessageLimit     = 200
	messagePageLimit         = 200
	defaultMessagePageLimit  = 50
	defaultMessageWindowSize = 64
	defaultMessageChunkSize  = 32
	// DefaultMessageChunkMaxBytes is the default of WithMessageChunkMaxBytes: 1 MiB.
	DefaultMessageChunkMaxBytes  = 1024 * 1024
	backgroundHeartbeatInterval  = 20 * time.Second
	modelCallMethodTimeout       = 15 * time.Minute
	modelCallHeartbeatTimeout    = 2 * time.Minute
	modelCallRetryTotalDuration  = 30 * time.Minute
	modelCallMaximumAttempts     = 3
	multiAttemptToolMaxAttempts  = 3
	recoveryRetryMaximumInterval = 5 * time.Second
)

// AgentSessionOption configures one AgentSessionFlow. Pass options to NewAgentSessionFlow.
type AgentSessionOption interface {
	applyAgentSession(options *agentSessionOptions)
}

type agentSessionOptions struct {
	toolPolicies             map[string]ToolPolicy
	defaultToolEffect        ToolEffect
	eventStreamCapacityBytes int64
	interruptDeadline        time.Duration
	logger                   *slog.Logger
	messageWindowSize        int64
	messageChunkSize         int64
	messageChunkMaxBytes     int64
}

type agentSessionOptionFunc func(options *agentSessionOptions)

func (option agentSessionOptionFunc) applyAgentSession(options *agentSessionOptions) {
	option(options)
}

// WithToolPolicies sets tool policies by tool name.
//
// They override framework metadata and the default tool effect. The default
// is none. The map is copied.
func WithToolPolicies(toolPolicies map[string]ToolPolicy) AgentSessionOption {
	copied := maps.Clone(toolPolicies)
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.toolPolicies = copied
	})
}

// WithDefaultToolEffect sets the effect of a tool without a policy or framework metadata.
//
// The default is ToolEffectUnclassified, so unknown tools run once and need approval.
func WithDefaultToolEffect(effect ToolEffect) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.defaultToolEffect = effect
	})
}

// WithEventStreamCapacityBytes sets the approximate byte budget of the AgentEvents Stream.
//
// The budget is shared by all sessions of the Flow type. The default is
// DefaultAgentEventsCapacityBytes, 10 MiB. It must be positive.
func WithEventStreamCapacityBytes(capacityBytes int64) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.eventStreamCapacityBytes = capacityBytes
	})
}

// WithInterruptDeadline sets how long an approval may stay pending.
//
// After the deadline the session rejects the call with "approval deadline
// passed". The default, no option, waits indefinitely. It must be positive.
// Dex Timers take whole seconds, so the session wakes at the deadline rounded
// up to the next whole second.
func WithInterruptDeadline(deadline time.Duration) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.interruptDeadline = deadline
	})
}

// WithMessageWindowSize sets the most messages the AgentMessages window holds.
//
// Every Step that stores messages loads the whole window. When it holds more
// than this many messages, its oldest messages move into one immutable
// AgentMessageChunks instance per chunk, in the same commit. The default is
// 64. It must be at least the message chunk size.
func WithMessageWindowSize(windowSize int64) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.messageWindowSize = windowSize
	})
}

// WithMessageChunkSize sets the most messages one archived AgentMessageChunks instance holds.
//
// AgentMessageIndex records each chunk's first and last sequence, so a change
// applies to the next chunk. The default is 32. It must be positive.
func WithMessageChunkSize(chunkSize int64) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.messageChunkSize = chunkSize
	})
}

// WithMessageChunkMaxBytes sets the most encoded bytes one archived AgentMessageChunks instance holds.
//
// A chunk is one Dex value, which server-zd caps at values.maxValueBytes (16
// MiB by default). A chunk stops taking messages at the message chunk size or
// when the next message would carry it past this many bytes of its JSON
// encoding, whichever comes first. A message whose own chunk, 15 bytes larger
// than the message, would pass this cap skips the window: the Step that stores
// it writes it straight into a single-message chunk, so a message too large for
// values.maxValueBytes fails the commit that first stores it. The default is
// 1 MiB (1048576). It must be between 1 and 8388608. Lower it when
// values.maxValueBytes is smaller than the default.
func WithMessageChunkMaxBytes(chunkMaxBytes int64) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.messageChunkMaxBytes = chunkMaxBytes
	})
}

// WithLogger sets the logger for warnings the session logs instead of failing a Step.
//
// The default is slog.Default().
func WithLogger(logger *slog.Logger) AgentSessionOption {
	return agentSessionOptionFunc(func(options *agentSessionOptions) {
		options.logger = logger
	})
}

// AgentSessionFlow runs one durable agent session per Flow execution with a framework runner.
//
// One Flow execution is one agent session, and the Flow ID is the session ID.
// Register one AgentSessionFlow per agent, each with its own Flow type. Every
// model call runs in its own RunModel Step and every tool call in its own tool
// Step, so each has its own retry policy, timeout, failure route, and history
// entry. The session never completes on its own: it waits for the next user
// message after every turn. Stop it with Client.StopFlow.
//
// The Flow type, Step types, persistence names, RPC names, and wire values are
// the contract the Python and TypeScript cores share, so a session that a
// Client in any of those languages started runs on a Go Worker, and a Go
// Client reads a session that their Workers run.
//
//	flow, err := dexagents.NewAgentSessionFlow(runner, "TriageAgent",
//		dexagents.WithInterruptDeadline(24*time.Hour))
//	if err != nil {
//		return err
//	}
//	registry, err := dex.NewRegistry([]dex.Flow{flow})
//	if err != nil {
//		return err
//	}
//	worker, err := dex.NewWorker(registry, cache, dex.WorkerOptions{FlowServiceAddress: "localhost:8801"})
type AgentSessionFlow struct {
	runner      AgentFrameworkRunner
	flowType    string
	options     agentSessionOptions
	agentEvents dex.Stream[AgentEvent]
}

// NewAgentSessionFlow creates the session Flow for one agent.
//
// Construct it once per agent at application startup and pass the same value
// to the Registry, the Worker, and every Client call. runner must not be nil.
// flowType is the protocol Flow type, unique within the Registry, such as
// "TriageAgent"; keep it stable, because running sessions resume by it.
//
// It returns an error when flowType is blank, the default tool effect or a
// tool policy's effect is not a contract value, the Stream capacity or the
// interrupt deadline is not positive, the message chunk size is not positive,
// or the message window size is smaller than the message chunk size.
func NewAgentSessionFlow(
	runner AgentFrameworkRunner,
	flowType string,
	options ...AgentSessionOption,
) (*AgentSessionFlow, error) {
	if runner == nil {
		panic("dexagents: NewAgentSessionFlow needs a runner")
	}
	if isBlankText(flowType) {
		return nil, errors.New("flow type must not be blank")
	}
	configured := agentSessionOptions{
		toolPolicies:             map[string]ToolPolicy{},
		defaultToolEffect:        ToolEffectUnclassified,
		eventStreamCapacityBytes: DefaultAgentEventsCapacityBytes,
		logger:                   slog.Default(),
		messageWindowSize:        defaultMessageWindowSize,
		messageChunkSize:         defaultMessageChunkSize,
		messageChunkMaxBytes:     DefaultMessageChunkMaxBytes,
	}
	hasInterruptDeadline := false
	for _, option := range options {
		option.applyAgentSession(&configured)
		hasInterruptDeadline = hasInterruptDeadline || configured.interruptDeadline != 0
	}
	if err := validateAgentSessionOptions(configured, hasInterruptDeadline); err != nil {
		return nil, err
	}
	if configured.toolPolicies == nil {
		configured.toolPolicies = map[string]ToolPolicy{}
	}
	if configured.logger == nil {
		configured.logger = slog.Default()
	}
	return &AgentSessionFlow{
		runner:      runner,
		flowType:    flowType,
		options:     configured,
		agentEvents: dex.DefineStream[AgentEvent](AgentEventsStreamName, configured.eventStreamCapacityBytes),
	}, nil
}

func validateAgentSessionOptions(options agentSessionOptions, hasInterruptDeadline bool) error {
	if !options.defaultToolEffect.IsValid() {
		return fmt.Errorf("default tool effect %q is not a tool effect", options.defaultToolEffect)
	}
	for toolName, policy := range options.toolPolicies {
		if !policy.Effect.IsValid() {
			return fmt.Errorf("tool policy for %q has effect %q, which is not a tool effect", toolName, policy.Effect)
		}
	}
	if options.eventStreamCapacityBytes <= 0 {
		return errors.New("event stream capacity bytes must be positive")
	}
	if hasInterruptDeadline && options.interruptDeadline <= 0 {
		return errors.New("interrupt deadline must be positive")
	}
	if options.messageChunkSize <= 0 {
		return errors.New("message chunk size must be positive")
	}
	if options.messageWindowSize < options.messageChunkSize {
		return errors.New("message window size must be at least message chunk size")
	}
	if options.messageChunkMaxBytes <= 0 || options.messageChunkMaxBytes > MessageRolloverMaxBytesPerCommit {
		return fmt.Errorf("message chunk max bytes must be between 1 and %d", MessageRolloverMaxBytesPerCommit)
	}
	return nil
}

// Runner returns the framework runner this Flow calls.
func (flow *AgentSessionFlow) Runner() AgentFrameworkRunner {
	return flow.runner
}

// AgentEvents returns this Flow type's AgentEvents Stream.
//
// Pass it to Client.ReadStream or Client.ListStreamMessages, or use
// AgentSessionClient.ReadAgentEvent.
func (flow *AgentSessionFlow) AgentEvents() dex.Stream[AgentEvent] {
	return flow.agentEvents
}

// GetFlowType returns the Flow type passed to NewAgentSessionFlow.
func (flow *AgentSessionFlow) GetFlowType() string {
	return flow.flowType
}

// GetSteps returns StartAgentSession as the start Step and every other session Step.
func (flow *AgentSessionFlow) GetSteps() []dex.StepDef {
	return []dex.StepDef{
		dex.DefineStartStep(StartAgentSession{session: flow}),
		dex.DefineStep(AwaitUserMessage{session: flow}),
		dex.DefineStep(RunModel{session: flow}),
		dex.DefineStep(AwaitToolApproval{session: flow}),
		dex.DefineStep(CallTool{session: flow}),
		dex.DefineStep(CallSingleAttemptTool{session: flow}),
		dex.DefineStep(RecordToolFailure{session: flow}),
		dex.DefineStep(RecordToolOutcomeUnknown{session: flow}),
		dex.DefineStep(ResumeTurn{session: flow}),
		dex.DefineStep(RecordTurnFailure{session: flow}),
	}
}

// GetRPCs returns the session RPCs under their contract names.
func (flow *AgentSessionFlow) GetRPCs() []dex.RPCDef {
	return []dex.RPCDef{
		dex.DefineRPC(flow.SendUserMessage, &dex.RPCOptions{Name: SendUserMessageRPCName, IsTransactional: true}),
		dex.DefineRPC(flow.ResolveInterrupt, &dex.RPCOptions{
			Name:           ResolveInterruptRPCName,
			LockAttributes: []dex.AttributeLock{dex.LockAttribute(PendingInterruptsAttribute)},
		}),
		dex.DefineRPC(flow.GetAgentSnapshot, &dex.RPCOptions{
			Name: GetAgentSnapshotRPCName,
			LoadAttributeMaps: []dex.AttributeDef{
				AgentMessagesAttributeMap, AgentMessageChunksAttributeMap, AgentToolCallsAttributeMap,
			},
			LoadChannels: []dex.ChannelDef{QueuedUserMessagesChannel, SteeredUserMessagesChannel},
		}),
		dex.DefineRPC(flow.ListAgentMessages, &dex.RPCOptions{
			Name:              ListAgentMessagesRPCName,
			LoadAttributeMaps: []dex.AttributeDef{AgentMessagesAttributeMap, AgentMessageChunksAttributeMap},
		}),
		dex.DefineRPC(flow.UpdateSessionSettings, &dex.RPCOptions{Name: UpdateSessionSettingsRPCName, IsTransactional: true}),
	}
}

// GetPersistenceSchema returns the session Attributes, Channels, and AgentEvents Stream.
//
// The schema lists every name of the contract, including those of parts this
// Go core does not run, so it matches the schema of the other cores.
func (flow *AgentSessionFlow) GetPersistenceSchema() dex.PersistenceSchema {
	return dex.PersistenceSchema{
		Attributes: []dex.AttributeDef{
			AgentStateAttribute,
			AgentMessagesAttributeMap,
			AgentMessageChunksAttributeMap,
			AgentMessageIndexAttribute,
			TurnReplySequenceAttribute,
			FrameworkRunStateAttribute,
			AgentToolCallsAttributeMap,
			PendingInterruptsAttribute,
			AgentUsageAttribute,
			TurnCancellationRequestAttribute,
			AgentTraceContextAttribute,
			AgentPlanAttribute,
			PendingPlanExecutionAttribute,
			ContextSummaryAttribute,
			SessionSettingsAttribute,
			TurnSessionSettingsAttribute,
			SessionTerminalLeaseAttribute,
			SessionLastTerminalEnvironmentAttribute,
		},
		Channels: []dex.ChannelDef{
			QueuedUserMessagesChannel,
			InterruptResolutionsChannelMap,
			ToolCallResultsChannel,
			SteeredUserMessagesChannel,
			TurnControlRequestsChannel,
			ToolCallCancellationsChannelMap,
			PlanExecutionsChannelMap,
		},
		Streams: []dex.StreamDef{flow.agentEvents},
	}
}

// SendUserMessage queues one user message for the session; its RPC name is send_user_message.
//
// The session starts a turn for each queued message, in order, after the
// current turn ends. The RPC is transactional and requires a running session.
// Call it through AgentSessionClient.SendUserMessage or Client.InvokeRPC with
// flow.SendUserMessage.
//
// It returns true when the message was queued, and false for whitespace-only
// text or a message with PlanMode set, because this Go core has no plan mode.
// It also returns false for a message with images, because this Go core has no
// image input, and for a SenderName that IsValidSenderName refuses.
func (flow *AgentSessionFlow) SendUserMessage(ctx dex.Context, input AgentUserMessage) (*dex.RPCResult[bool], error) {
	if isBlankText(input.Text) || input.PlanMode || len(input.Images) > 0 {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if input.SenderName != nil && !IsValidSenderName(*input.SenderName) {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := QueuedUserMessagesChannel.Publish(ctx, input); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

// ResolveInterrupt approves or rejects one pending interrupt; its RPC name is resolve_interrupt.
//
// The RPC removes the interrupt from PendingInterrupts and publishes the
// answer in the same commit, so only one call succeeds for each interrupt. It
// locks PendingInterrupts: a concurrent session Step can make it fail with
// dex.RPCLockConflictError, which is safe to retry and which
// AgentSessionClient.ResolveInterrupt retries. An approval drops
// RejectionMessage.
//
// It returns true when the interrupt was pending and is now resolved, and
// false when it is unknown, already resolved, or past its deadline, or when it
// approves a question.
func (flow *AgentSessionFlow) ResolveInterrupt(ctx dex.Context, input ResolveInterruptRequest) (*dex.RPCResult[bool], error) {
	pending, err := readPendingInterrupts(ctx)
	if err != nil {
		return nil, err
	}
	index := indexOfInterrupt(pending, input.InterruptID)
	if index < 0 {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	resolved := pending[index]
	isPassed, err := isDeadlinePassed(resolved, time.Now())
	if err != nil {
		return nil, err
	}
	if isPassed || (resolved.Kind == InterruptKindQuestion && input.Approved) {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	remaining := append(append([]AgentInterrupt{}, pending[:index]...), pending[index+1:]...)
	if err := PendingInterruptsAttribute.Set(ctx, remaining); err != nil {
		return nil, err
	}
	resolution := AgentInterruptResolution{Approved: input.Approved}
	if !input.Approved {
		resolution.RejectionMessage = input.RejectionMessage
	}
	if err := InterruptResolutionsChannelMap.Publish(ctx, resolved.InterruptID, resolution); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

// GetAgentSnapshot returns the session state a UI renders; its RPC name is get_agent_snapshot.
//
// The RPC reads committed state without locks, so it also serves a retained
// closed session. It loads the AgentMessages window and every retained
// AgentMessageChunks instance.
//
// It returns the state, the last 200 messages, pending interrupts, the active
// turn's tool calls, usage, the queued message count, and the queued and
// steered messages with their Channel message IDs; the plan and context
// summary when a Worker with those additions wrote them.
func (flow *AgentSessionFlow) GetAgentSnapshot(ctx dex.Context, _ dex.None) (*dex.RPCResult[AgentSnapshot], error) {
	state, err := readOptionalAttribute(ctx, AgentStateAttribute)
	if err != nil {
		return nil, err
	}
	snapshot := AgentSnapshot{
		Messages: []AgentMessage{}, PendingInterrupts: []AgentInterrupt{}, ToolCalls: []AgentToolCall{},
		QueuedMessages: []PendingUserMessage{}, SteeredMessages: []PendingUserMessage{},
	}
	if state == nil {
		return &dex.RPCResult[AgentSnapshot]{Output: snapshot}, nil
	}
	snapshot.State = state
	end := state.NextMessageSequence
	if snapshot.Messages, err = readStoredMessages(ctx, end-snapshotMessageLimit, end); err != nil {
		return nil, err
	}
	if snapshot.PendingInterrupts, err = readPendingInterrupts(ctx); err != nil {
		return nil, err
	}
	if snapshot.ToolCalls, err = readTurnToolCalls(ctx, state.ActiveTurnID); err != nil {
		return nil, err
	}
	if snapshot.Usage, err = readAgentUsage(ctx); err != nil {
		return nil, err
	}
	snapshot.QueuedMessageCount = int64(QueuedUserMessagesChannel.Size(ctx))
	if snapshot.Plan, err = readOptionalAttribute(ctx, AgentPlanAttribute); err != nil {
		return nil, err
	}
	if snapshot.QueuedMessages, err = readPendingUserMessages(ctx, QueuedUserMessagesChannel); err != nil {
		return nil, err
	}
	if snapshot.SteeredMessages, err = readPendingUserMessages(ctx, SteeredUserMessagesChannel); err != nil {
		return nil, err
	}
	if snapshot.ContextSummary, err = readOptionalAttribute(ctx, ContextSummaryAttribute); err != nil {
		return nil, err
	}
	if snapshot.SessionSettings, err = readSessionSettings(ctx, SessionSettingsAttribute); err != nil {
		return nil, err
	}
	return &dex.RPCResult[AgentSnapshot]{Output: snapshot}, nil
}

// UpdateSessionSettings replaces the session's settings from the next turn on; its RPC name is
// update_session_settings.
//
// The new settings apply when the next turn starts. A running turn keeps the
// settings it started with for every model call and retry, so one turn never
// mixes two models. The settings replace the old ones; nothing is merged, and
// an empty map clears them, so get_agent_snapshot returns nil. Never put a
// secret in them. The RPC is transactional and locks nothing: when two updates
// race, the one that commits last wins.
//
//	isStored, err := sessions.UpdateSessionSettings(ctx, "session-42", map[string]any{"model": "gpt-5"})
//
// It returns true when the settings are stored, and false before
// StartAgentSession committed, or when the settings are nil (a missing or null
// field) or larger than MaxSessionSettingsBytes plus
// SessionSettingsSizeToleranceBytes. Call the RPC only after every Worker of
// the Flow type runs dex-agents 0.2.0.
func (flow *AgentSessionFlow) UpdateSessionSettings(ctx dex.Context, input UpdateSessionSettingsRequest) (*dex.RPCResult[bool], error) {
	state, err := readOptionalAttribute(ctx, AgentStateAttribute)
	if err != nil {
		return nil, err
	}
	if state == nil || input.SessionSettings == nil || checkStoredSessionSettings(input.SessionSettings) != nil {
		return &dex.RPCResult[bool]{Output: false}, nil
	}
	if err := writeSessionSettings(ctx, SessionSettingsAttribute, input.SessionSettings); err != nil {
		return nil, err
	}
	return &dex.RPCResult[bool]{Output: true}, nil
}

// ListAgentMessages returns one page of stored messages, oldest first; its RPC name is list_agent_messages.
//
// Start without BeforeSequence for the newest page, then pass each page's
// NextBeforeSequence to walk back. Limit is clamped to 1 through 200. The RPC
// reads committed state without locks, so it also serves a retained closed
// session. It loads the AgentMessages window and every retained
// AgentMessageChunks instance.
//
// It returns the page and the anchor of the previous page.
func (flow *AgentSessionFlow) ListAgentMessages(ctx dex.Context, input ListAgentMessagesRequest) (*dex.RPCResult[AgentMessagesPage], error) {
	state, err := readOptionalAttribute(ctx, AgentStateAttribute)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return &dex.RPCResult[AgentMessagesPage]{Output: AgentMessagesPage{Messages: []AgentMessage{}}}, nil
	}
	reader, err := newStoredMessageReader(ctx)
	if err != nil {
		return nil, err
	}
	first := reader.firstSequence()
	limit := max(1, min(input.Limit, messagePageLimit))
	end := state.NextMessageSequence
	if input.BeforeSequence != nil {
		end = max(first, min(*input.BeforeSequence, end))
	}
	start := max(first, end-limit)
	messages, err := reader.readMessages(start, end)
	if err != nil {
		return nil, err
	}
	page := AgentMessagesPage{Messages: messages}
	if start > first {
		page.NextBeforeSequence = &start
	}
	if first > 1 {
		page.DeletedBeforeSequence = &first
	}
	return &dex.RPCResult[AgentMessagesPage]{Output: page}, nil
}

// isBlankText reports whether text is empty or only whitespace as Python's str.strip removes it.
func isBlankText(text string) bool {
	return strings.TrimFunc(text, isPythonWhitespace) == ""
}

func isPythonWhitespace(character rune) bool {
	// Python also treats the information separators U+001C to U+001F as whitespace.
	return unicode.IsSpace(character) || (character >= 0x1C && character <= 0x1F)
}

// StartAgentSession initializes the session and starts its first turn when a message is given.
//
// It initializes AgentState, AgentUsage, and an empty PendingInterrupts. A
// non-blank initial message starts the first turn in RunModel; otherwise the
// session moves to AwaitUserMessage.
//
// dex:group group-id:turn group-label:"Turn"
// dex:explanation text:"Initialize the session state and start the first turn when the start input has a message."
type StartAgentSession struct {
	dex.StepDefaultsNoWaitFor[AgentSessionStartInput]
	session *AgentSessionFlow
}

// GetStepOptions makes Execute SYNC, so an ASYNC Flow default never replays the Step result.
func (StartAgentSession) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentMessagesAttributeMap},
	}
}

// Execute initializes the session and starts the first turn when InitialMessage is not blank.
func (step StartAgentSession) Execute(ctx dex.Context, input AgentSessionStartInput) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	state := AgentState{
		Status:              AgentSessionStatusWaitingForUserMessage,
		NextMessageSequence: 1,
		InteractionMode:     InteractionModeChat,
	}
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return nil, err
	}
	if err := AgentUsageAttribute.Set(ctx, AgentUsage{}); err != nil {
		return nil, err
	}
	if err := PendingInterruptsAttribute.Set(ctx, []AgentInterrupt{}); err != nil {
		return nil, err
	}
	if err := checkStoredSessionSettings(input.SessionSettings); err != nil {
		// Every retry would fail the same way, so the session fails now.
		return dex.ForceFail(fmt.Sprintf("%s: %v", InvalidStartSessionSettingsReason, err)), nil
	}
	if err := writeSessionSettings(ctx, SessionSettingsAttribute, input.SessionSettings); err != nil {
		return nil, err
	}
	if input.InitialMessage == nil || isBlankText(*input.InitialMessage) {
		return dex.GoTo(AwaitUserMessage{session: step.session}, nil), nil
	}
	if err := step.session.beginTurn(ctx, state, AgentUserMessage{Text: *input.InitialMessage}, input.SessionSettings); err != nil {
		return nil, err
	}
	return dex.GoTo(RunModel{session: step.session}, (*CompactContextInput)(nil)), nil
}

// AwaitUserMessage waits for the next queued user message and starts its turn.
//
// dex:group group-id:turn group-label:"Turn"
// dex:explanation text:"Wait for the next queued user message, store it, and start its turn."
type AwaitUserMessage struct {
	dex.StepDefaults
	session *AgentSessionFlow
}

// GetStepOptions makes WaitFor and Execute SYNC and loads the AgentMessages window it appends to.
func (AwaitUserMessage) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForDurability:        dex.StepDurabilitySync,
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentMessagesAttributeMap},
	}
}

// WaitFor waits for one QueuedUserMessages message.
func (step AwaitUserMessage) WaitFor(ctx dex.Context, _ dex.None) (*dex.Wait, error) {
	// Execute fails the session at once; waiting would stall it.
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(QueuedUserMessagesChannel.ForOne()), nil
}

// Execute stores the consumed message as the turn's user message and moves to RunModel.
func (step AwaitUserMessage) Execute(ctx dex.Context, _ dex.None) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	messages, err := QueuedUserMessagesChannel.GetConditionResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, errors.New("the user message wait completed without a message")
	}
	state, err := requireAgentState(ctx)
	if err != nil {
		return nil, err
	}
	sessionSettings, err := readSessionSettings(ctx, SessionSettingsAttribute)
	if err != nil {
		return nil, err
	}
	if err := step.session.beginTurn(ctx, state, messages[0], sessionSettings); err != nil {
		return nil, err
	}
	return dex.GoTo(RunModel{session: step.session}, (*CompactContextInput)(nil)), nil
}

// RunModel makes one model call of the active turn through the runner.
//
// Its input is always nil; the pointer type matches RecordTurnFailure, its
// failure route, as the contract requires. A final answer ends the turn. Tool
// calls start one tool Step per call, or AwaitToolApproval when a call needs
// approval, plus ResumeTurn, which waits for every result.
//
// dex:group group-id:model group-label:"Model"
// dex:explanation text:"Make one model call of the turn and either finish the turn or start one Step per requested tool call."
type RunModel struct {
	dex.StepDefaultsNoWaitFor[*CompactContextInput]
	session *AgentSessionFlow
}

// GetStepOptions sets the model call timeouts, three attempts in 30 minutes, and RecordTurnFailure.
func (step RunModel) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteMethodTimeout: modelCallMethodTimeout,
		HeartbeatTimeout:     modelCallHeartbeatTimeout,
		ExecuteRetry: &dex.RetryPolicy{
			MaximumAttempts: modelCallMaximumAttempts,
			TotalDuration:   modelCallRetryTotalDuration,
		},
		ExecuteDurability: dex.StepDurabilitySync,
		// Without compaction, the model input is every stored message, so RunModel loads every chunk.
		ExecuteLoadAttributeMaps: []dex.AttributeDef{
			AgentMessagesAttributeMap, AgentMessageChunksAttributeMap, AgentToolCallsAttributeMap,
		},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(
			RecordTurnFailure{session: step.session},
			RecordTurnFailure{session: step.session}.GetStepOptions(),
		),
	}
}

// Execute makes the model call, records its outcome, and starts one Step per requested tool call.
func (step RunModel) Execute(ctx dex.Context, _ *CompactContextInput) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	result, err := step.session.runModelCall(ctx)
	if err != nil {
		return nil, err
	}
	if result.isTurnOver {
		return dex.GoTo(AwaitUserMessage{session: step.session}, nil), nil
	}
	movements := make([]dex.StepMovement, 0, len(result.requestedToolCalls)+1)
	for _, toolCall := range result.requestedToolCalls {
		switch {
		case toolCall.RequiresApproval:
			movements = append(movements, dex.MovementOf(AwaitToolApproval{session: step.session}, toolCall.ToolCallID,
				dex.WithStepOptions(toolApprovalMovementOptions(toolCall.ToolCallID))))
		case toolCall.Effect.isMultiAttempt():
			movements = append(movements, dex.MovementOf(CallTool{session: step.session}, toolCall.ToolCallID,
				dex.WithStepOptions(step.session.callToolMovementOptions(toolCall.ToolCallID))))
		default:
			movements = append(movements, dex.MovementOf(CallSingleAttemptTool{session: step.session}, toolCall.ToolCallID,
				dex.WithStepOptions(step.session.callSingleAttemptToolMovementOptions(toolCall.ToolCallID))))
		}
	}
	movements = append(movements, dex.MovementOf(ResumeTurn{session: step.session}, len(result.requestedToolCalls)))
	return dex.GoToMany(movements...), nil
}

// AwaitToolApproval waits for one tool call's approval or deadline, then runs or rejects it.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Wait for a person to approve or reject one tool call, or for its approval deadline."
type AwaitToolApproval struct {
	dex.StepDefaults
	session *AgentSessionFlow
}

// GetStepOptions makes both methods SYNC and locks PendingInterrupts, which resolve_interrupt writes.
func (AwaitToolApproval) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForDurability:        dex.StepDurabilitySync,
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentToolCallsAttributeMap},
		ExecuteLockAttributes:    []dex.AttributeLock{dex.LockAttribute(PendingInterruptsAttribute)},
	}
}

// WaitFor waits for the call's resolution and, while its interrupt has a deadline, a Timer.
func (step AwaitToolApproval) WaitFor(ctx dex.Context, toolCallID string) (*dex.Wait, error) {
	// Execute fails the session at once; waiting would stall it.
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.SkipWaitImmediately(), nil
	}
	conditions := []dex.Condition{InterruptResolutionsChannelMap.ForOne(toolCallID)}
	interrupt, err := readPendingInterrupt(ctx, toolCallID)
	if err != nil {
		return nil, err
	}
	// After resolve_interrupt removed the interrupt, its deadline no longer applies.
	if interrupt != nil && interrupt.Deadline != nil {
		timer, err := timerUntil(*interrupt.Deadline, time.Now())
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, timer)
	}
	return dex.AnyOf(conditions...), nil
}

// Execute moves an approved call to its tool Step and records a rejected or expired one.
func (step AwaitToolApproval) Execute(ctx dex.Context, toolCallID string) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	outcome, toolCall, err := step.session.applyToolApproval(ctx, toolCallID)
	if err != nil {
		return nil, err
	}
	switch {
	case outcome == approvalWaitsAgain:
		return dex.GoToMany(dex.MovementOf(AwaitToolApproval{session: step.session}, toolCallID,
			dex.WithStepOptions(toolApprovalMovementOptions(toolCallID)))), nil
	case outcome == approvalFinished:
		return dex.DeadEnd(), nil
	case toolCall.Effect.isMultiAttempt():
		return dex.GoToMany(dex.MovementOf(CallTool{session: step.session}, toolCallID,
			dex.WithStepOptions(step.session.callToolMovementOptions(toolCallID)))), nil
	default:
		return dex.GoToMany(dex.MovementOf(CallSingleAttemptTool{session: step.session}, toolCallID,
			dex.WithStepOptions(step.session.callSingleAttemptToolMovementOptions(toolCallID)))), nil
	}
}

// CallTool runs one read or idempotent_write tool call with up to three attempts.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Run one read or idempotent write tool call, retrying it up to three attempts with a stable idempotency key."
type CallTool struct {
	dex.StepDefaultsNoWaitFor[string]
	session *AgentSessionFlow
}

// GetStepOptions sets three attempts and RecordToolFailure as the failure route.
func (step CallTool) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:             &dex.RetryPolicy{MaximumAttempts: multiAttemptToolMaxAttempts},
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentToolCallsAttributeMap},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(
			RecordToolFailure{session: step.session}, RecordToolFailure{}.GetStepOptions(),
		),
	}
}

// Execute runs the tool and records its final status.
func (step CallTool) Execute(ctx dex.Context, toolCallID string) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	if err := step.session.runToolCall(ctx, toolCallID); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// CallSingleAttemptTool runs one non_idempotent_write or unclassified tool call exactly once.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Run one non-idempotent or unclassified tool call in a single attempt."
type CallSingleAttemptTool struct {
	dex.StepDefaultsNoWaitFor[string]
	session *AgentSessionFlow
}

// GetStepOptions sets one attempt and RecordToolOutcomeUnknown as the failure route.
func (step CallSingleAttemptTool) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:             &dex.RetryPolicy{MaximumAttempts: 1},
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentToolCallsAttributeMap},
		ExecuteFailure: dex.ProceedToOnExecuteFailure(
			RecordToolOutcomeUnknown{session: step.session}, RecordToolOutcomeUnknown{}.GetStepOptions(),
		),
	}
}

// Execute runs the tool and records its final status.
func (step CallSingleAttemptTool) Execute(ctx dex.Context, toolCallID string) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	if err := step.session.runToolCall(ctx, toolCallID); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// RecordToolFailure records a CallTool call whose attempts ran out.
//
// A read call becomes failed and an idempotent_write call outcome_unknown,
// with the recovery detail as the error. A call whose tool never ran becomes
// failed with ToolCallNotRunErrorPrefix.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Record a tool call whose attempts ran out as failed for a read or a never-run call and outcome unknown for a write."
type RecordToolFailure struct {
	dex.StepDefaultsNoWaitFor[string]
	session *AgentSessionFlow
}

// GetStepOptions retries quickly until the record is written.
func (RecordToolFailure) GetStepOptions() *dex.StepOptions {
	return recoveryStepOptions([]dex.AttributeDef{AgentToolCallsAttributeMap})
}

// Execute records the call's final status by its effect.
func (step RecordToolFailure) Execute(ctx dex.Context, toolCallID string) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	toolCall, err := AgentToolCallsAttributeMap.Get(ctx, toolCallID)
	if err != nil {
		return nil, err
	}
	status := ToolCallStatusOutcomeUnknown
	if toolCall.Effect == ToolEffectRead {
		status = ToolCallStatusFailed
	}
	if err := step.session.recordToolRecovery(ctx, toolCall, status); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// RecordToolOutcomeUnknown records that a single-attempt tool call stopped without a result.
//
// A call whose tool never ran becomes failed with ToolCallNotRunErrorPrefix instead.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Record a single-attempt tool call that stopped without a result as outcome unknown, or as failed when it never ran."
type RecordToolOutcomeUnknown struct {
	dex.StepDefaultsNoWaitFor[string]
	session *AgentSessionFlow
}

// GetStepOptions retries quickly until the record is written.
func (RecordToolOutcomeUnknown) GetStepOptions() *dex.StepOptions {
	return recoveryStepOptions([]dex.AttributeDef{AgentToolCallsAttributeMap})
}

// Execute records outcome_unknown with the recovery detail, or failed when the tool never ran.
func (step RecordToolOutcomeUnknown) Execute(ctx dex.Context, toolCallID string) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	toolCall, err := AgentToolCallsAttributeMap.Get(ctx, toolCallID)
	if err != nil {
		return nil, err
	}
	if err := step.session.recordToolRecovery(ctx, toolCall, ToolCallStatusOutcomeUnknown); err != nil {
		return nil, err
	}
	return dex.DeadEnd(), nil
}

// ResumeTurn waits for every tool call result of the model response, then calls the model again.
//
// Its input is the number of results to wait for.
//
// dex:group group-id:tools group-label:"Tools"
// dex:explanation text:"Wait until every tool call of the model response has a final result, then call the model again."
type ResumeTurn struct {
	dex.StepDefaults
	session *AgentSessionFlow
}

// GetStepOptions makes both methods SYNC and locks PendingInterrupts as the contract states.
func (ResumeTurn) GetStepOptions() *dex.StepOptions {
	return &dex.StepOptions{
		WaitForDurability:        dex.StepDurabilitySync,
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: []dex.AttributeDef{AgentToolCallsAttributeMap},
		ExecuteLockAttributes:    []dex.AttributeLock{dex.LockAttribute(PendingInterruptsAttribute)},
	}
}

// WaitFor waits for expectedResultCount ToolCallResults messages.
func (step ResumeTurn) WaitFor(ctx dex.Context, expectedResultCount int) (*dex.Wait, error) {
	// Execute fails the session at once; waiting would stall it.
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.SkipWaitImmediately(), nil
	}
	return dex.Until(ToolCallResultsChannel.ForN(expectedResultCount)), nil
}

// Execute moves to RunModel once every record of the turn is final.
func (step ResumeTurn) Execute(ctx dex.Context, _ int) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	state, err := requireAgentState(ctx)
	if err != nil {
		return nil, err
	}
	turnID, err := requireActiveTurnID(state)
	if err != nil {
		return nil, err
	}
	toolCalls, err := readTurnToolCalls(ctx, &turnID)
	if err != nil {
		return nil, err
	}
	unfinishedCount := 0
	for _, toolCall := range toolCalls {
		if !toolCall.Status.IsFinal() {
			unfinishedCount++
		}
	}
	if unfinishedCount > 0 {
		// A consumed result from an earlier turn must not resume this one early.
		return dex.GoTo(ResumeTurn{session: step.session}, unfinishedCount), nil
	}
	state.Status = AgentSessionStatusRunningModel
	state.AcceptsTurnControl = false
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return nil, err
	}
	return dex.GoTo(RunModel{session: step.session}, (*CompactContextInput)(nil)), nil
}

// RecordTurnFailure records a failed turn and waits for the next user message.
//
// RunModel reaches it when its attempts run out. It stores the runner's
// unfinished-turn messages and the turn-failure note, deletes the turn's
// records, and empties PendingInterrupts.
//
// dex:group group-id:model group-label:"Model"
// dex:explanation text:"Record a turn whose model call failed after its retries, note which tool calls already ran, and wait for the user."
type RecordTurnFailure struct {
	dex.StepDefaultsNoWaitFor[*CompactContextInput]
	session *AgentSessionFlow
}

// GetStepOptions retries quickly and loads the records, the window, and, for an incremental runner, the chunks.
func (step RecordTurnFailure) GetStepOptions() *dex.StepOptions {
	loads := []dex.AttributeDef{AgentToolCallsAttributeMap, AgentMessagesAttributeMap}
	if step.session != nil {
		if _, isIncremental := step.session.runner.(UnfinishedTurnMessageBuilder); isIncremental {
			loads = append(loads, AgentMessageChunksAttributeMap)
		}
	}
	return recoveryStepOptions(loads)
}

// Execute records the failure with the recovery detail.
func (step RecordTurnFailure) Execute(ctx dex.Context, _ *CompactContextInput) (*dex.StepDecision, error) {
	unsupported, err := step.session.unsupportedSessionError(ctx)
	if err != nil {
		return nil, err
	}
	if unsupported != "" {
		return dex.ForceFail(unsupported), nil
	}
	state, err := requireAgentState(ctx)
	if err != nil {
		return nil, err
	}
	if err := step.session.failTurn(ctx, state, recoveryDetail(ctx)); err != nil {
		return nil, err
	}
	return dex.GoTo(AwaitUserMessage{session: step.session}, nil), nil
}

// unsupportedSessionError returns the ForceFail reason when this core cannot serve the session's state, or "".
//
// A Worker of another core can leave state from contract parts this core does
// not run. Acting on it would stall the session or run tools those parts
// forbid, so the Step fails the session with a reason instead.
func (flow *AgentSessionFlow) unsupportedSessionError(ctx dex.Context) (string, error) {
	state, err := readOptionalAttribute(ctx, AgentStateAttribute)
	if err != nil {
		return "", err
	}
	plan, err := readOptionalAttribute(ctx, AgentPlanAttribute)
	if err != nil {
		return "", err
	}
	pendingPlanExecution, err := readOptionalAttribute(ctx, PendingPlanExecutionAttribute)
	if err != nil {
		return "", err
	}
	summary, err := readOptionalAttribute(ctx, ContextSummaryAttribute)
	if err != nil {
		return "", err
	}
	cancellation, err := readOptionalAttribute(ctx, TurnCancellationRequestAttribute)
	if err != nil {
		return "", err
	}
	reason := unsupportedSessionReason(state, plan, pendingPlanExecution, summary, cancellation)
	if reason == "" {
		return "", nil
	}
	return fmt.Sprintf("dexagents: this Go Worker cannot serve session %s: %s. "+
		"Serve each agent Flow type with Workers of one language.",
		ctx.FlowID(), reason), nil
}

// unsupportedSessionReason names the first state that needs a contract addition this core lacks, or "".
//
// Steered messages and cancellation requests exist only in a turn that
// accepts turn control, so the AcceptsTurnControl check covers them.
func unsupportedSessionReason(
	state *AgentState,
	plan *AgentPlan,
	pendingPlanExecution *PlanExecutionRequest,
	summary *ContextSummary,
	cancellation *TurnCancellationRequest,
) string {
	switch {
	case state != nil && state.AcceptsTurnControl:
		return "a Worker with steering began the active turn, and this core cannot apply steering or cancel_turn"
	case state != nil && state.InteractionMode != InteractionModeChat:
		return fmt.Sprintf("the session is in a %s turn, which needs plan mode", state.InteractionMode)
	case state != nil && state.ActiveTurnID != nil && cancellation != nil && cancellation.TurnID == *state.ActiveTurnID:
		return "cancel_turn asked to stop the active turn, which needs turn cancellation"
	case plan != nil && (plan.Status == PlanStatusDraft || plan.Status == PlanStatusActive):
		return fmt.Sprintf("the session has a %s plan, which needs plan mode", plan.Status)
	case pendingPlanExecution != nil:
		return "an approved plan execution is pending, which needs plan mode"
	case summary != nil:
		return "the session has a context summary, which needs compaction"
	default:
		return ""
	}
}

func recoveryStepOptions(loads []dex.AttributeDef) *dex.StepOptions {
	return &dex.StepOptions{
		ExecuteRetry:             &dex.RetryPolicy{MaximumInterval: recoveryRetryMaximumInterval},
		ExecuteDurability:        dex.StepDurabilitySync,
		ExecuteLoadAttributeMaps: loads,
	}
}

// beginTurn stores the turn's user message and makes it the active turn with these settings.
func (flow *AgentSessionFlow) beginTurn(ctx dex.Context, state AgentState, message AgentUserMessage, sessionSettings map[string]any) error {
	turnID := fmt.Sprintf("turn-%d", state.TurnCount+1)
	// The turn keeps these until it ends; later updates wait for the next turn.
	if err := writeTurnSessionSettings(ctx, turnID, sessionSettings); err != nil {
		return err
	}
	sequence := state.NextMessageSequence
	userMessage, err := flow.runner.BuildUserMessage(BuildSenderNamedText(message.SenderName, message.Text))
	if err != nil {
		return err
	}
	if userMessage.Role != MessageRoleUser {
		return errors.New("BuildUserMessage must return a user message")
	}
	if message.SenderName != nil {
		// The framework item carries the name for the model; the display text stays as sent.
		userMessage.Text = &message.Text
	}
	state, err = flow.appendMessages(ctx, state, turnID, []FrameworkMessage{userMessage}, true, nil,
		[]*string{message.SenderName})
	if err != nil {
		return err
	}
	state.Status = AgentSessionStatusRunningModel
	state.ActiveTurnID = &turnID
	state.InteractionMode = InteractionModeChat
	state.AcceptsTurnControl = false
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return err
	}
	if err := flow.eventWriter(ctx, &turnID).WriteEvent(AgentEventTypeTurnStarted, map[string]any{"sequence": sequence}); err != nil {
		return err
	}
	return nil
}

// modelCallResult is what RunModel decides from: a turn that ended, or the tool calls it requested.
type modelCallResult struct {
	isTurnOver         bool
	requestedToolCalls []AgentToolCall
}

// runModelCall makes the model call and commits its outcome.
func (flow *AgentSessionFlow) runModelCall(ctx dex.Context) (modelCallResult, error) {
	state, err := requireAgentState(ctx)
	if err != nil {
		return modelCallResult{}, err
	}
	turnID, err := requireActiveTurnID(state)
	if err != nil {
		return modelCallResult{}, err
	}
	toolCalls, err := readTurnToolCalls(ctx, &turnID)
	if err != nil {
		return modelCallResult{}, err
	}
	for _, toolCall := range toolCalls {
		if !toolCall.Status.IsFinal() {
			return modelCallResult{}, fmt.Errorf("turn %s resumed before every tool call finished", turnID)
		}
	}
	events := flow.eventWriter(ctx, &turnID)
	turn, err := flow.turnContext(ctx, state, turnID, toolCalls, events)
	if err != nil {
		return modelCallResult{}, err
	}
	var outcome AgentTurnOutcome
	err = flow.withBackgroundHeartbeats(ctx, func() error { return ctx.RecordHeartbeat(nil) }, func() error {
		var runErr error
		outcome, runErr = flow.runner.RunModelStep(ctx, turn)
		return runErr
	})
	if err != nil {
		return modelCallResult{}, err
	}
	switch typed := outcome.(type) {
	case *TurnCompleted:
		return modelCallResult{isTurnOver: true}, flow.completeTurn(ctx, state, turnID, toolCalls, typed, events)
	case *ToolCallsRequested:
		if _, isIncremental := flow.runner.(UnfinishedTurnMessageBuilder); len(typed.NewMessages) > 0 && !isIncremental {
			// A runner defect: a retry would only repeat the paid model call.
			if err := flow.recordUsage(ctx, events, typed.Usage); err != nil {
				return modelCallResult{}, err
			}
			return modelCallResult{isTurnOver: true},
				flow.failTurnWithRecords(ctx, state, turnID, toolCalls, events, MissingUnfinishedTurnMessageBuilder)
		}
		requested, err := flow.startToolCalls(ctx, state, turnID, toolCalls, typed, events)
		return modelCallResult{requestedToolCalls: requested}, err
	default:
		return modelCallResult{}, fmt.Errorf("RunModelStep must return *TurnCompleted or *ToolCallsRequested, got %T", outcome)
	}
}

func (flow *AgentSessionFlow) turnContext(
	ctx dex.Context,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	events *AgentEventWriter,
) (AgentTurnContext, error) {
	reader, err := newStoredMessageReader(ctx)
	if err != nil {
		return AgentTurnContext{}, err
	}
	if reader.firstSequence() > 1 {
		// This core has no compaction, so its runner never receives the summary that covers them.
		flow.options.logger.Warn("dex-agents: the session stores no messages before the oldest kept one; "+
			"retention deleted them, and this runner cannot use the context summary that covers them",
			"session", ctx.FlowID(), "first_sequence", reader.firstSequence())
	}
	messages, err := reader.readMessages(1, state.NextMessageSequence)
	if err != nil {
		return AgentTurnContext{}, err
	}
	frameworkRunState, err := readOptionalAttribute(ctx, FrameworkRunStateAttribute)
	if err != nil {
		return AgentTurnContext{}, err
	}
	recordsByID := make(map[string]AgentToolCall, len(toolCalls))
	for _, toolCall := range toolCalls {
		recordsByID[toolCall.ToolCallID] = toolCall
	}
	storedTurnSettings, err := readOptionalAttribute(ctx, TurnSessionSettingsAttribute)
	if err != nil {
		return AgentTurnContext{}, err
	}
	sessionSettings := selectTurnSessionSettings(storedTurnSettings, state.ActiveTurnID)
	return AgentTurnContext{
		FlowID:            ctx.FlowID(),
		TurnID:            turnID,
		Attempt:           int64(ctx.Attempt()),
		Messages:          messages,
		FrameworkRunState: frameworkRunState,
		ToolCalls:         recordsByID,
		ToolPolicies:      flow.options.toolPolicies,
		DefaultToolEffect: flow.options.defaultToolEffect,
		Events:            events,
		Heartbeat:         func() error { return ctx.RecordHeartbeat(nil) },
		InteractionMode:   state.InteractionMode,
		SessionSettings:   sessionSettings,
	}, nil
}

func (flow *AgentSessionFlow) completeTurn(
	ctx dex.Context,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	outcome *TurnCompleted,
	events *AgentEventWriter,
) error {
	// The turn's user messages are stored before RunModel; count only what came after them.
	storedTurnMessageCount, err := countMessagesAfterUserMessages(ctx, state, turnID)
	if err != nil {
		return err
	}
	state, err = flow.appendMessages(ctx, state, turnID, outcome.NewMessages, false, nil, nil)
	if err != nil {
		return err
	}
	if err := deleteTurnRecords(ctx, toolCalls); err != nil {
		return err
	}
	if err := flow.recordUsage(ctx, events, outcome.Usage); err != nil {
		return err
	}
	state.Status = AgentSessionStatusWaitingForUserMessage
	state.TurnCount++
	state.ActiveTurnID = nil
	state.AcceptsTurnControl = false
	state.LastTurnError = nil
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return err
	}
	messageCount := storedTurnMessageCount + int64(len(outcome.NewMessages))
	if err := events.WriteEvent(AgentEventTypeTurnCompleted, map[string]any{"message_count": messageCount}); err != nil {
		return err
	}
	return nil
}

func (flow *AgentSessionFlow) startToolCalls(
	ctx dex.Context,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	outcome *ToolCallsRequested,
	events *AgentEventWriter,
) ([]AgentToolCall, error) {
	if err := validateToolCallsRequested(outcome, toolCalls); err != nil {
		return nil, err
	}
	state, err := flow.appendMessages(ctx, state, turnID, outcome.NewMessages, false, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := FrameworkRunStateAttribute.Set(ctx, outcome.FrameworkRunState); err != nil {
		return nil, err
	}
	now := time.Now()
	interrupts, err := readPendingInterrupts(ctx)
	if err != nil {
		return nil, err
	}
	recorded := make([]AgentToolCall, 0, len(outcome.ToolCalls))
	for index, requested := range outcome.ToolCalls {
		isApprovalRequired := requested.Policy.IsApprovalRequired()
		toolCall := AgentToolCall{
			ToolCallID:          requested.ToolCallID,
			TurnID:              turnID,
			ToolName:            requested.ToolName,
			Arguments:           requested.Arguments,
			Effect:              requested.Policy.Effect,
			RequiresApproval:    isApprovalRequired,
			Status:              ToolCallStatusApproved,
			DeferralOrder:       int64(len(toolCalls) + index),
			ToolSourceReference: requested.ToolSourceReference,
			// Without tracing, no activation root exists for the call's spans to join.
			TraceContext:        nil,
			InputAnswers:        []QuestionAnswer{},
			InputChoices:        []string{},
			InputAllowsFreeText: true,
		}
		if isApprovalRequired {
			toolCall.Status = ToolCallStatusAwaitingApproval
		}
		if err := events.WriteEvent(AgentEventTypeToolCallDeferred, map[string]any{
			"tool_call_id":      toolCall.ToolCallID,
			"tool_name":         toolCall.ToolName,
			"effect":            string(toolCall.Effect),
			"requires_approval": isApprovalRequired,
		}); err != nil {
			return nil, err
		}
		if err := AgentToolCallsAttributeMap.Set(ctx, toolCall.ToolCallID, toolCall); err != nil {
			return nil, err
		}
		recorded = append(recorded, toolCall)
		if !isApprovalRequired {
			continue
		}
		interrupt := AgentInterrupt{
			InterruptID:    toolCall.ToolCallID,
			Kind:           InterruptKindToolApproval,
			ToolCallID:     toolCall.ToolCallID,
			ToolName:       toolCall.ToolName,
			Arguments:      toolCall.Arguments,
			CreatedAt:      formatRFC3339(now),
			Deadline:       flow.interruptDeadline(now),
			Choices:        []string{},
			AllowsFreeText: true,
		}
		interrupts = append(interrupts, interrupt)
		if err := writeInterruptRaised(events, interrupt); err != nil {
			return nil, err
		}
	}
	if err := PendingInterruptsAttribute.Set(ctx, interrupts); err != nil {
		return nil, err
	}
	if err := flow.recordUsage(ctx, events, outcome.Usage); err != nil {
		return nil, err
	}
	state.Status = AgentSessionStatusRunningTools
	state.AcceptsTurnControl = false
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return nil, err
	}
	return recorded, nil
}

func (flow *AgentSessionFlow) interruptDeadline(now time.Time) *string {
	if flow.options.interruptDeadline <= 0 {
		return nil
	}
	deadline := formatRFC3339(now.Add(flow.options.interruptDeadline))
	return &deadline
}

// approvalOutcome is what AwaitToolApproval decides from.
type approvalOutcome int

const (
	// approvalApproved moves the call to its tool Step.
	approvalApproved approvalOutcome = iota
	// approvalWaitsAgain waits again, after an early Timer or an answer that committed first.
	approvalWaitsAgain
	// approvalFinished means the record is final: rejected now, or already before.
	approvalFinished
)

func (flow *AgentSessionFlow) applyToolApproval(ctx dex.Context, toolCallID string) (approvalOutcome, AgentToolCall, error) {
	toolCall, err := AgentToolCallsAttributeMap.Get(ctx, toolCallID)
	if err != nil {
		return approvalFinished, AgentToolCall{}, err
	}
	if toolCall.Status.IsFinal() {
		return approvalFinished, toolCall, nil
	}
	events := flow.eventWriter(ctx, &toolCall.TurnID)
	resolutions, err := InterruptResolutionsChannelMap.GetConditionResults(ctx, toolCallID)
	if err != nil {
		return approvalFinished, AgentToolCall{}, err
	}
	if len(resolutions) > 0 {
		return flow.resolveToolApproval(ctx, toolCall, resolutions[0], events)
	}
	if !ctx.HasTimerFired() {
		return approvalFinished, AgentToolCall{}, fmt.Errorf("the approval wait for %s completed without an answer", toolCallID)
	}
	interrupt, err := readPendingInterrupt(ctx, toolCallID)
	if err != nil {
		return approvalFinished, AgentToolCall{}, err
	}
	if interrupt == nil || interrupt.Deadline == nil {
		// resolve_interrupt answered first; the next wait consumes that answer.
		return approvalWaitsAgain, toolCall, nil
	}
	isPassed, err := isDeadlinePassed(*interrupt, time.Now())
	if err != nil {
		return approvalFinished, AgentToolCall{}, err
	}
	if !isPassed {
		return approvalWaitsAgain, toolCall, nil
	}
	if err := removePendingInterrupt(ctx, toolCallID); err != nil {
		return approvalFinished, AgentToolCall{}, err
	}
	rejectionMessage := ApprovalDeadlinePassed
	return flow.resolveToolApproval(ctx, toolCall, AgentInterruptResolution{RejectionMessage: &rejectionMessage}, events)
}

func (flow *AgentSessionFlow) resolveToolApproval(
	ctx dex.Context,
	toolCall AgentToolCall,
	resolution AgentInterruptResolution,
	events *AgentEventWriter,
) (approvalOutcome, AgentToolCall, error) {
	if err := events.WriteEvent(AgentEventTypeInterruptResolved, map[string]any{
		"interrupt_id":        toolCall.ToolCallID,
		"approved":            resolution.Approved,
		"rejection_message":   resolution.RejectionMessage,
		"kind":                string(InterruptKindToolApproval),
		"answered":            false,
		"cancellation_reason": nil,
	}); err != nil {
		return approvalFinished, toolCall, err
	}
	if resolution.Approved {
		toolCall.Status = ToolCallStatusApproved
		// Without tracing, an approval starts no activation, so the call joins no span.
		toolCall.TraceContext = nil
		if err := AgentToolCallsAttributeMap.Set(ctx, toolCall.ToolCallID, toolCall); err != nil {
			return approvalFinished, toolCall, err
		}
		return approvalApproved, toolCall, nil
	}
	toolCall.Status = ToolCallStatusRejected
	toolCall.RejectionMessage = resolution.RejectionMessage
	return approvalFinished, toolCall, finishToolCall(ctx, toolCall, events)
}

func (flow *AgentSessionFlow) runToolCall(ctx dex.Context, toolCallID string) error {
	toolCall, err := AgentToolCallsAttributeMap.Get(ctx, toolCallID)
	if err != nil {
		return err
	}
	if toolCall.Status.IsFinal() {
		return nil
	}
	if toolCall.Status != ToolCallStatusApproved {
		return fmt.Errorf("tool call %s is not approved", toolCallID)
	}
	events := flow.eventWriter(ctx, &toolCall.TurnID)
	if err := events.WriteToolCallStarted(toolCall.ToolCallID, toolCall.ToolName); err != nil {
		return err
	}
	heartbeats, err := newToolStepHeartbeats(ctx)
	if err != nil {
		return err
	}
	// Output first: Dex returns an attempt as not run when its stream half-closes before any output.
	if err := heartbeats.record(); err != nil {
		return err
	}
	execution := DeferredToolExecution{
		FlowID:                  ctx.FlowID(),
		ToolCall:                toolCall,
		IdempotencyKey:          fmt.Sprintf("%s/%s/%s", ctx.FlowID(), toolCall.TurnID, toolCall.ToolCallID),
		Attempt:                 int64(ctx.Attempt()),
		Events:                  events,
		Heartbeat:               heartbeats.record,
		HeartbeatCheckpoint:     heartbeats.previousCheckpoint,
		SaveHeartbeatCheckpoint: heartbeats.saveCheckpoint,
	}
	var result DeferredToolOutcome
	err = flow.withBackgroundHeartbeats(ctx, heartbeats.record, func() error {
		var runErr error
		result, runErr = flow.runner.ExecuteDeferredToolCall(ctx, execution)
		return runErr
	})
	var toolError *ToolExecutionError
	if errors.As(err, &toolError) {
		toolCall.Status = ToolCallStatusFailed
		toolCall.Error = &toolError.Message
		return finishToolCall(ctx, toolCall, events)
	}
	if err != nil {
		return err
	}
	executionResult, isResult := result.(*ToolExecutionResult)
	if !isResult || executionResult == nil {
		return fmt.Errorf("ExecuteDeferredToolCall must return *ToolExecutionResult, got %T", result)
	}
	toolCall.Status = ToolCallStatusSucceeded
	toolCall.Output = &executionResult.Output
	toolCall.RunnerToolState = executionResult.RunnerToolState
	return finishToolCall(ctx, toolCall, events)
}

func (flow *AgentSessionFlow) recordToolRecovery(
	ctx dex.Context,
	toolCall AgentToolCall,
	status ToolCallStatus,
) error {
	if toolCall.Status.IsFinal() {
		return nil
	}
	finished := recoveredToolCall(toolCall, ctx.RecoveryError(), status)
	return finishToolCall(ctx, finished, flow.eventWriter(ctx, &toolCall.TurnID))
}

// recoveredToolCall returns the final record that a tool Step's failure route writes.
func recoveredToolCall(
	toolCall AgentToolCall,
	recoveryError *dex.RecoveryErrorInfo,
	status ToolCallStatus,
) AgentToolCall {
	detail := recoveryErrorDetail(recoveryError)
	if isToolCallNotRun(toolCall, recoveryError) {
		status = ToolCallStatusFailed
		detail = ToolCallNotRunErrorPrefix + ": " + detail
	}
	toolCall.Status = status
	toolCall.Error = &detail
	return toolCall
}

// isToolCallNotRun reports whether the failed Step certainly never ran the call's tool.
func isToolCallNotRun(toolCall AgentToolCall, recoveryError *dex.RecoveryErrorInfo) bool {
	// An answered question's call already ran its tool in an earlier Step.
	return recoveryError != nil &&
		recoveryError.AttemptProgress == dex.StepAttemptProgressNotStarted &&
		toolCall.Status == ToolCallStatusApproved &&
		len(toolCall.InputAnswers) == 0
}

func (flow *AgentSessionFlow) failTurn(ctx dex.Context, state AgentState, errorText string) error {
	turnID, err := requireActiveTurnID(state)
	if err != nil {
		return err
	}
	toolCalls, err := readTurnToolCalls(ctx, &turnID)
	if err != nil {
		return err
	}
	return flow.failTurnWithRecords(ctx, state, turnID, toolCalls, flow.eventWriter(ctx, &turnID), errorText)
}

func (flow *AgentSessionFlow) failTurnWithRecords(
	ctx dex.Context,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	events *AgentEventWriter,
	errorText string,
) error {
	messages := flow.buildUnfinishedTurnMessages(ctx, state, turnID, toolCalls, events)
	finished := make([]AgentToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall.Status.IsFinal() {
			finished = append(finished, toolCall)
		}
	}
	origins := make([]*AgentMessageOrigin, len(messages))
	if len(finished) > 0 {
		// The records are deleted below; the note keeps the next model from repeating writes.
		noteText, err := BuildTurnFailureNote(finished)
		if err != nil {
			return err
		}
		note, err := flow.runner.BuildUserMessage(noteText)
		if err != nil {
			return err
		}
		messages = append(messages, note)
		noteOrigin := AgentMessageOriginTurnFailureNote
		origins = append(origins, &noteOrigin)
	}
	state, err := flow.appendMessages(ctx, state, turnID, messages, false, origins, nil)
	if err != nil {
		return err
	}
	if err := deleteTurnRecords(ctx, toolCalls); err != nil {
		return err
	}
	if err := PendingInterruptsAttribute.Set(ctx, []AgentInterrupt{}); err != nil {
		return err
	}
	state.Status = AgentSessionStatusWaitingForUserMessage
	state.TurnCount++
	state.ActiveTurnID = nil
	state.AcceptsTurnControl = false
	state.LastTurnError = &errorText
	if err := AgentStateAttribute.Set(ctx, state); err != nil {
		return err
	}
	if err := events.WriteEvent(AgentEventTypeTurnFailed, map[string]any{"error": errorText}); err != nil {
		return err
	}
	return nil
}

// buildUnfinishedTurnMessages returns the runner's unfinished-turn messages, or none when it failed.
func (flow *AgentSessionFlow) buildUnfinishedTurnMessages(
	ctx dex.Context,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	events *AgentEventWriter,
) []FrameworkMessage {
	builder, isIncremental := flow.runner.(UnfinishedTurnMessageBuilder)
	if !isIncremental {
		return nil
	}
	messages, err := flow.doBuildUnfinishedTurnMessages(ctx, builder, state, turnID, toolCalls, events)
	if err != nil {
		// A runner defect must not block the failure route; the note still stores.
		flow.options.logger.Warn("build unfinished turn messages failed; storing only the note",
			"turn_id", turnID, "error", err)
		return nil
	}
	return messages
}

func (flow *AgentSessionFlow) doBuildUnfinishedTurnMessages(
	ctx dex.Context,
	builder UnfinishedTurnMessageBuilder,
	state AgentState,
	turnID string,
	toolCalls []AgentToolCall,
	events *AgentEventWriter,
) ([]FrameworkMessage, error) {
	turn, err := flow.turnContext(ctx, state, turnID, toolCalls, events)
	if err != nil {
		return nil, err
	}
	messages, err := builder.BuildUnfinishedTurnMessages(ctx, turn)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		switch message.Role {
		case MessageRoleUser, MessageRoleAssistant, MessageRoleTool:
		default:
			return nil, fmt.Errorf("unfinished turn message has role %q", message.Role)
		}
	}
	return messages, nil
}

// callToolMovementOptions loads only the call's record in CallTool and RecordToolFailure.
func (flow *AgentSessionFlow) callToolMovementOptions(toolCallID string) *dex.StepOptions {
	recordLoad := []dex.AttributeMapLoad{AgentToolCallsAttributeMap.Load(toolCallID)}
	options := CallTool{}.GetStepOptions()
	recoveryOptions := RecordToolFailure{}.GetStepOptions()
	options.ExecuteLoadAttributeMaps, recoveryOptions.ExecuteLoadAttributeMaps = nil, nil
	options.ExecuteLoadAttributeMapInstances, recoveryOptions.ExecuteLoadAttributeMapInstances = recordLoad, recordLoad
	options.ExecuteFailure = dex.ProceedToOnExecuteFailure(RecordToolFailure{session: flow}, recoveryOptions)
	return options
}

// callSingleAttemptToolMovementOptions loads only the call's record in both of its Steps.
func (flow *AgentSessionFlow) callSingleAttemptToolMovementOptions(toolCallID string) *dex.StepOptions {
	recordLoad := []dex.AttributeMapLoad{AgentToolCallsAttributeMap.Load(toolCallID)}
	options := CallSingleAttemptTool{}.GetStepOptions()
	recoveryOptions := RecordToolOutcomeUnknown{}.GetStepOptions()
	options.ExecuteLoadAttributeMaps, recoveryOptions.ExecuteLoadAttributeMaps = nil, nil
	options.ExecuteLoadAttributeMapInstances, recoveryOptions.ExecuteLoadAttributeMapInstances = recordLoad, recordLoad
	options.ExecuteFailure = dex.ProceedToOnExecuteFailure(RecordToolOutcomeUnknown{session: flow}, recoveryOptions)
	return options
}

// toolApprovalMovementOptions loads only the call's record, not the whole map.
func toolApprovalMovementOptions(toolCallID string) *dex.StepOptions {
	options := AwaitToolApproval{}.GetStepOptions()
	options.ExecuteLoadAttributeMaps = nil
	options.ExecuteLoadAttributeMapInstances = []dex.AttributeMapLoad{AgentToolCallsAttributeMap.Load(toolCallID)}
	return options
}

func (flow *AgentSessionFlow) eventWriter(ctx dex.Context, turnID *string) *AgentEventWriter {
	return NewAgentEventWriter(
		func(event AgentEvent) error { return flow.agentEvents.Write(ctx, event) },
		turnID,
		int64(ctx.Attempt()),
		ctx.StepExecutionID(),
	)
}

func (flow *AgentSessionFlow) recordUsage(ctx dex.Context, events *AgentEventWriter, usage AgentUsage) error {
	sessionUsage, err := readAgentUsage(ctx)
	if err != nil {
		return err
	}
	sessionUsage = sessionUsage.CombinedWith(usage)
	if err := AgentUsageAttribute.Set(ctx, sessionUsage); err != nil {
		return err
	}
	return events.WriteEvent(AgentEventTypeUsageRecorded, map[string]any{
		"recorded": usagePayload(usage),
		"session":  usagePayload(sessionUsage),
	})
}

// withBackgroundHeartbeats runs call while a goroutine records a heartbeat every 20 seconds.
func (flow *AgentSessionFlow) withBackgroundHeartbeats(ctx dex.Context, recordHeartbeat func() error, call func() error) error {
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(backgroundHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := recordHeartbeat(); err != nil {
					// The heartbeat timeout reports a lost Worker; the call itself goes on.
					flow.options.logger.Warn("record background heartbeat failed", "error", err)
				}
			}
		}
	}()
	err := call()
	close(stop)
	<-stopped
	return err
}

// toolStepHeartbeats records one tool Step attempt's heartbeats; each repeats the latest checkpoint.
//
// A bare heartbeat clears a saved checkpoint, so the earlier attempt's checkpoint is carried forward.
type toolStepHeartbeats struct {
	ctx                dex.Context
	previousCheckpoint *string
	mutex              sync.Mutex
	checkpoint         *string
}

func newToolStepHeartbeats(ctx dex.Context) (*toolStepHeartbeats, error) {
	var checkpoint string
	isFound, err := ctx.GetLastHeartbeatValue(&checkpoint)
	if err != nil {
		return nil, fmt.Errorf("read the earlier attempt's heartbeat checkpoint: %w", err)
	}
	heartbeats := &toolStepHeartbeats{ctx: ctx}
	if isFound {
		heartbeats.previousCheckpoint = &checkpoint
		heartbeats.checkpoint = &checkpoint
	}
	return heartbeats, nil
}

func (heartbeats *toolStepHeartbeats) record() error {
	heartbeats.mutex.Lock()
	checkpoint := heartbeats.checkpoint
	heartbeats.mutex.Unlock()
	if checkpoint == nil {
		return heartbeats.ctx.RecordHeartbeat(nil)
	}
	return heartbeats.ctx.RecordHeartbeat(*checkpoint)
}

func (heartbeats *toolStepHeartbeats) saveCheckpoint(checkpoint string) error {
	heartbeats.mutex.Lock()
	heartbeats.checkpoint = &checkpoint
	heartbeats.mutex.Unlock()
	return heartbeats.record()
}

func finishToolCall(ctx dex.Context, finished AgentToolCall, events *AgentEventWriter) error {
	if err := AgentToolCallsAttributeMap.Set(ctx, finished.ToolCallID, finished); err != nil {
		return err
	}
	if err := ToolCallResultsChannel.Publish(ctx, ToolCallCompleted{ToolCallID: finished.ToolCallID}); err != nil {
		return err
	}
	return events.WriteToolCallFinished(finished.ToolCallID, finished.ToolName, finished.Status)
}

func writeInterruptRaised(events *AgentEventWriter, interrupt AgentInterrupt) error {
	return events.WriteEvent(AgentEventTypeInterruptRaised, map[string]any{
		"interrupt_id":     interrupt.InterruptID,
		"tool_call_id":     interrupt.ToolCallID,
		"tool_name":        interrupt.ToolName,
		"deadline":         interrupt.Deadline,
		"kind":             string(interrupt.Kind),
		"question":         interrupt.Question,
		"choices":          nonNilSlice(interrupt.Choices),
		"allows_free_text": interrupt.AllowsFreeText,
		"display":          interruptDisplayPayload(interrupt.Display),
	})
}

func interruptDisplayPayload(display *AgentInterruptDisplay) any {
	if display == nil {
		return nil
	}
	return map[string]any{"target": display.Target, "summary": display.Summary, "preview": display.Preview}
}

func deleteTurnRecords(ctx dex.Context, toolCalls []AgentToolCall) error {
	for _, toolCall := range toolCalls {
		if err := AgentToolCallsAttributeMap.Delete(ctx, toolCall.ToolCallID); err != nil {
			return err
		}
	}
	if err := FrameworkRunStateAttribute.Delete(ctx); err != nil {
		return err
	}
	return nil
}

func removePendingInterrupt(ctx dex.Context, interruptID string) error {
	pending, err := readPendingInterrupts(ctx)
	if err != nil {
		return err
	}
	remaining := make([]AgentInterrupt, 0, len(pending))
	for _, interrupt := range pending {
		if interrupt.InterruptID != interruptID {
			remaining = append(remaining, interrupt)
		}
	}
	return PendingInterruptsAttribute.Set(ctx, remaining)
}

func validateToolCallsRequested(outcome *ToolCallsRequested, toolCalls []AgentToolCall) error {
	if outcome.FrameworkRunState == "" {
		return errors.New("ToolCallsRequested needs a non-empty framework run state")
	}
	if len(outcome.ToolCalls) == 0 {
		return errors.New("ToolCallsRequested needs at least one tool call")
	}
	seenIDs := make(map[string]bool, len(toolCalls)+len(outcome.ToolCalls))
	for _, toolCall := range toolCalls {
		seenIDs[toolCall.ToolCallID] = true
	}
	for _, requested := range outcome.ToolCalls {
		if isBlankText(requested.ToolCallID) || containsSlash(requested.ToolCallID) {
			return fmt.Errorf("tool call ID %q is empty or contains '/'", requested.ToolCallID)
		}
		if seenIDs[requested.ToolCallID] {
			return fmt.Errorf("tool call %s was already requested in this turn", requested.ToolCallID)
		}
		if !requested.Policy.Effect.IsValid() {
			return fmt.Errorf("tool call %s has effect %q, which is not a tool effect", requested.ToolCallID, requested.Policy.Effect)
		}
		seenIDs[requested.ToolCallID] = true
	}
	return nil
}

func containsSlash(text string) bool {
	for _, character := range text {
		if character == '/' {
			return true
		}
	}
	return false
}

func recoveryDetail(ctx dex.Context) string {
	return recoveryErrorDetail(ctx.RecoveryError())
}

func recoveryErrorDetail(recoveryError *dex.RecoveryErrorInfo) string {
	if recoveryError == nil {
		return "unknown failure"
	}
	if recoveryError.Detail != "" {
		return recoveryError.Detail
	}
	return recoveryError.ErrorType
}

func timerUntil(deadline string, now time.Time) (dex.Condition, error) {
	deadlineTime, err := parseRFC3339(deadline)
	if err != nil {
		return nil, err
	}
	// Dex Timers take whole seconds; an early fire waits again in Execute.
	wholeSeconds := math.Ceil(max(deadlineTime.Sub(now).Seconds(), 0))
	return dex.Timer(time.Duration(wholeSeconds) * time.Second), nil
}

func isDeadlinePassed(interrupt AgentInterrupt, now time.Time) (bool, error) {
	if interrupt.Deadline == nil {
		return false, nil
	}
	deadline, err := parseRFC3339(*interrupt.Deadline)
	if err != nil {
		return false, err
	}
	return !deadline.After(now), nil
}

// formatRFC3339 writes UTC with milliseconds and "Z", byte for byte as the other cores write times.
func formatRFC3339(moment time.Time) string {
	return moment.UTC().Format("2006-01-02T15:04:05.000Z")
}

func parseRFC3339(text string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse RFC 3339 time %q: %w", text, err)
	}
	return parsed, nil
}

func usagePayload(usage AgentUsage) map[string]any {
	return map[string]any{
		"model_calls":         usage.ModelCalls,
		"input_tokens":        usage.InputTokens,
		"output_tokens":       usage.OutputTokens,
		"cached_input_tokens": usage.CachedInputTokens,
		"reasoning_tokens":    usage.ReasoningTokens,
	}
}

func readOptionalAttribute[Value any](ctx dex.Context, attribute dex.Attribute[Value]) (*Value, error) {
	value, err := attribute.Get(ctx)
	var notFound *dex.AttributeNotFoundError
	if errors.As(err, &notFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// writeTurnSessionSettings tags non-empty settings with the turn, and deletes the copy for empty ones.
func writeTurnSessionSettings(ctx dex.Context, turnID string, sessionSettings map[string]any) error {
	if len(sessionSettings) > 0 {
		return TurnSessionSettingsAttribute.Set(ctx, TurnSessionSettings{TurnID: turnID, SessionSettings: sessionSettings})
	}
	stored, err := readOptionalAttribute(ctx, TurnSessionSettingsAttribute)
	if err != nil || stored == nil {
		return err
	}
	return TurnSessionSettingsAttribute.Delete(ctx)
}

// readSessionSettings returns the settings an Attribute holds, or nil when it is unset.
func readSessionSettings(ctx dex.Context, attribute dex.Attribute[map[string]any]) (map[string]any, error) {
	sessionSettings, err := readOptionalAttribute(ctx, attribute)
	if err != nil || sessionSettings == nil {
		return nil, err
	}
	return *sessionSettings, nil
}

// writeSessionSettings stores non-empty settings and deletes the Attribute for empty ones.
func writeSessionSettings(ctx dex.Context, attribute dex.Attribute[map[string]any], sessionSettings map[string]any) error {
	if len(sessionSettings) > 0 {
		return attribute.Set(ctx, sessionSettings)
	}
	stored, err := readOptionalAttribute(ctx, attribute)
	if err != nil || stored == nil {
		return err
	}
	return attribute.Delete(ctx)
}

func requireAgentState(ctx dex.Context) (AgentState, error) {
	state, err := readOptionalAttribute(ctx, AgentStateAttribute)
	if err != nil {
		return AgentState{}, err
	}
	if state == nil {
		return AgentState{}, errors.New("the agent session is not initialized")
	}
	return *state, nil
}

func requireActiveTurnID(state AgentState) (string, error) {
	if state.ActiveTurnID == nil {
		return "", errors.New("the agent session has no active turn")
	}
	return *state.ActiveTurnID, nil
}

func readPendingInterrupts(ctx dex.Context) ([]AgentInterrupt, error) {
	pending, err := readOptionalAttribute(ctx, PendingInterruptsAttribute)
	if err != nil || pending == nil {
		return []AgentInterrupt{}, err
	}
	return nonNilSlice(*pending), nil
}

func readPendingInterrupt(ctx dex.Context, interruptID string) (*AgentInterrupt, error) {
	pending, err := readPendingInterrupts(ctx)
	if err != nil {
		return nil, err
	}
	index := indexOfInterrupt(pending, interruptID)
	if index < 0 {
		return nil, nil
	}
	return &pending[index], nil
}

func indexOfInterrupt(interrupts []AgentInterrupt, interruptID string) int {
	for index, interrupt := range interrupts {
		if interrupt.InterruptID == interruptID {
			return index
		}
	}
	return -1
}

func readAgentUsage(ctx dex.Context) (AgentUsage, error) {
	usage, err := readOptionalAttribute(ctx, AgentUsageAttribute)
	if err != nil || usage == nil {
		return AgentUsage{}, err
	}
	return *usage, nil
}

func readTurnToolCalls(ctx dex.Context, turnID *string) ([]AgentToolCall, error) {
	toolCalls := []AgentToolCall{}
	if turnID == nil {
		return toolCalls, nil
	}
	for _, toolCallID := range AgentToolCallsAttributeMap.AllInstanceKeys(ctx) {
		toolCall, err := AgentToolCallsAttributeMap.Get(ctx, toolCallID)
		if err != nil {
			return nil, err
		}
		if toolCall.TurnID == *turnID {
			toolCalls = append(toolCalls, toolCall)
		}
	}
	sortByDeferralOrder(toolCalls)
	return toolCalls, nil
}

func sortByDeferralOrder(toolCalls []AgentToolCall) {
	slices.SortStableFunc(toolCalls, func(left, right AgentToolCall) int {
		return int(left.DeferralOrder - right.DeferralOrder)
	})
}

func readPendingUserMessages(ctx dex.Context, channel dex.Channel[AgentUserMessage]) ([]PendingUserMessage, error) {
	pending, err := channel.PendingMessages(ctx)
	if err != nil {
		return nil, err
	}
	messages := make([]PendingUserMessage, 0, len(pending))
	for _, message := range pending {
		messages = append(messages, PendingUserMessage{MessageID: message.MessageID, Message: message.Value})
	}
	return messages, nil
}
