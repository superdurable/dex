// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Attribute,
  AttributeMap,
  Channel,
  ChannelMap,
  IndexType,
  Stream,
  int64Codec,
  stringCodec,
  type Codec,
} from "@superdurable/dex";

import {
  agentEventCodec,
  agentInterruptListCodec,
  agentInterruptResolutionCodec,
  agentMessageChunkCodec,
  agentMessageCodec,
  agentMessageIndexCodec,
  agentPlanCodec,
  agentStateCodec,
  agentToolCallCodec,
  agentUsageCodec,
  agentUserMessageCodec,
  planExecutionRequestCodec,
  contextSummaryCodec,
  sessionSettingsCodec,
  turnSessionSettingsCodec,
  turnReplySequenceCodec,
  toolCallCancellationCodec,
  toolCallCompletedCodec,
  turnCancellationRequestCodec,
  turnControlRequestCodec,
} from "./agent-session-codecs.js";
import type {
  AgentEvent,
  AgentInterrupt,
  AgentInterruptResolution,
  AgentMessage,
  AgentMessageChunk,
  AgentMessageIndex,
  AgentPlan,
  AgentState,
  AgentToolCallRecord,
  AgentUsage,
  AgentUserMessage,
  PlanExecutionRequest,
  ContextSummary,
  SessionSettings,
  TurnReplySequence,
  TurnSessionSettings,
  ToolCallCancellation,
  ToolCallCompleted,
  TurnCancellationRequest,
  TurnControlRequest,
} from "./agent-session-values.js";

/** Index keys of the session summary Attributes, shared with the Python core. */
export const sessionSummaryIndexKeys = { title: "text1", lastActivityAt: "datetime1", turnCount: "int1" } as const;

// An int64 Value, as the Python core's Attribute[int] encodes it.
const turnCountCodec: Codec<number> = {
  typeName: "AgentSessionTurnCount",
  wireKind: "int64",
  encode: (value) => int64Codec.encode(BigInt(value)),
  decode: (value) => Number(int64Codec.decode(value)),
};

/** Default byte budget of the `AgentEvents` Stream: 10 MiB. */
export const defaultAgentEventsCapacityBytes = 10 * 1024 * 1024;

/**
 * Persistence definitions registered by one agent session Flow.
 *
 * Every Flow returned by {@link createAgentSessionFlow} owns its own definition objects, available as
 * properties of the Flow. Pass them to `Client.getAttributes`, `Client.readStream`, or
 * `Client.listStreamMessages` to read a session from outside the Flow. The persisted names are wire
 * contracts shared with the Python core.
 *
 * @example
 * ```ts
 * const [state, usage] = await client.getAttributes(sessionId, flow.agentState, flow.agentUsage);
 * const page = await client.listStreamMessages(sessionId, flow.agentEvents, 50);
 * ```
 */
export interface AgentSessionPersistence {
  /** The `AgentState` Attribute. It is always set after `StartAgentSession`. */
  readonly agentState: Attribute<AgentState>;
  /**
   * The `AgentMessages` AttributeMap: the window of the newest messages, one instance per message, keyed by
   * {@link messageSequenceKey}. Older messages are in {@link agentMessageChunks}; see {@link agentMessageIndex}.
   */
  readonly agentMessages: AttributeMap<AgentMessage>;
  /**
   * The `AgentMessageChunks` AttributeMap: immutable archive chunks of older messages, keyed by
   * {@link messageSequenceKey} of each chunk's first sequence.
   */
  readonly agentMessageChunks: AttributeMap<AgentMessageChunk>;
  /**
   * The `AgentMessageIndex` Attribute: where the window and the retained chunks start. A session that a 0.1.0
   * Worker wrote has none until its first write on this version.
   */
  readonly agentMessageIndex: Attribute<AgentMessageIndex>;
  /** The `FrameworkRunState` Attribute: opaque framework JSON, set only while a turn is suspended. */
  readonly frameworkRunState: Attribute<string>;
  /** The `AgentToolCalls` AttributeMap, keyed by tool call ID; holds only the active turn's records. */
  readonly agentToolCalls: AttributeMap<AgentToolCallRecord>;
  /** The `PendingInterrupts` Attribute: interrupts that `resolve_interrupt` still accepts. */
  readonly pendingInterrupts: Attribute<readonly AgentInterrupt[]>;
  /** The `AgentUsage` Attribute: cumulative usage of every turn. */
  readonly agentUsage: Attribute<AgentUsage>;
  /** The `QueuedUserMessages` Channel; `send_user_message` publishes and `AwaitUserMessage` consumes. */
  readonly queuedUserMessages: Channel<AgentUserMessage>;
  /** The `InterruptResolutions` ChannelMap, keyed by interrupt ID. */
  readonly interruptResolutions: ChannelMap<AgentInterruptResolution>;
  /**
   * The internal `ToolCallResults` Channel. Every tool call of a batch publishes one message when its
   * record becomes final, and `ResumeTurn` consumes them. Applications never publish to it.
   */
  readonly toolCallResults: Channel<ToolCallCompleted>;
  /** The `SteeredUserMessages` Channel; `steer_user_message` publishes and `ApplyTurnControl` consumes. */
  readonly steeredUserMessages: Channel<AgentUserMessage>;
  /**
   * The internal `TurnControlRequests` Channel. `steer_user_message` and `cancel_turn` publish to it, and its
   * messages only wake `ResumeTurn` or `AwaitUserMessage`.
   */
  readonly turnControlRequests: Channel<TurnControlRequest>;
  /** The `TurnCancellationRequest` Attribute: the latest `cancel_turn` request. Only `cancel_turn` writes it. */
  readonly turnCancellationRequest: Attribute<TurnCancellationRequest>;
  /**
   * The internal `ToolCallCancellations` ChannelMap, keyed by tool call ID. `ResumeTurn` publishes to it to wake
   * the Step that waits on a call of a steered or cancelled turn.
   */
  readonly toolCallCancellations: ChannelMap<ToolCallCancellation>;
  /** The best-effort `AgentEvents` Stream. Rebuild durable state with `get_agent_snapshot`. */
  readonly agentEvents: Stream<AgentEvent>;
  /**
   * The `AgentTraceContext` Attribute: the W3C `traceparent` of the current activation's root span when
   * that span has a valid context, sampled or not; an activation whose root has none, such as one without
   * an OpenTelemetry SDK, deletes it. `RunModel` reads it to join the activation's trace.
   */
  readonly agentTraceContext: Attribute<string>;
  /** The `AgentPlan` Attribute: the session's plan, unset until `write_todos` first succeeds. */
  readonly agentPlan: Attribute<AgentPlan>;
  /**
   * The `PlanExecutions` ChannelMap, keyed by {@link planExecutionKey}: approved plan executions that
   * `execute_plan` publishes and `AwaitUserMessage` consumes.
   */
  readonly planExecutions: ChannelMap<PlanExecutionRequest>;
  /**
   * The `PendingPlanExecution` Attribute: the execution `execute_plan` accepted and `AwaitUserMessage` has
   * not consumed yet.
   */
  readonly pendingPlanExecution: Attribute<PlanExecutionRequest>;
  /**
   * The `ContextSummary` Attribute: the summary that replaces the earlier messages in the model input,
   * set once the session compacts. Only `CompactContext` writes it.
   */
  readonly contextSummary: Attribute<ContextSummary>;
  /**
   * The `SessionSettings` Attribute: the settings the next turn starts with, from the start input and then each
   * `update_session_settings`. Unset when the session has none.
   */
  readonly sessionSettings: Attribute<SessionSettings>;
  /**
   * The `TurnSessionSettings` Attribute: the settings a turn started with, tagged with its turn, copied from
   * `SessionSettings` when the turn began. A copy from an earlier turn reads as no settings.
   */
  readonly turnSessionSettings: Attribute<TurnSessionSettings>;
  /**
   * The `TurnReplySequence` Attribute: where the latest turn's replies start, tagged with its turn. The Step
   * that starts a turn writes it; turn events count the replies from it.
   */
  readonly turnReplySequence: Attribute<TurnReplySequence>;
  /**
   * The `AgentSessionTitle` Attribute: the session's first user message with each run of spaces, tabs, and line
   * breaks made one space, cut to 80 code points; see `buildSessionTitle`. Only the session's first turn sets
   * it, so a session that an earlier release started never has one. Indexed as `FULL_TEXT` under `text1`; see
   * `listAgentSessions`.
   *
   * @since 0.6.0
   */
  readonly agentSessionTitle: Attribute<string>;
  /**
   * The `AgentSessionLastActivityAt` Attribute: when the latest turn started or ended, as RFC 3339 UTC text with
   * milliseconds, such as `2026-11-02T09:30:00.000Z`. It is the time Dex scheduled the Step that started or ended the
   * turn, at one-second precision. Indexed as `DATETIME` under `datetime1`.
   *
   * @since 0.6.0
   */
  readonly agentSessionLastActivityAt: Attribute<string>;
  /**
   * The `AgentSessionTurnCount` Attribute: {@link AgentState.turnCount} as of the latest turn start or end, the turns
   * that completed, failed, or ended by steering or cancellation. Indexed as `INT` under `int1`.
   *
   * @since 0.6.0
   */
  readonly agentSessionTurnCount: Attribute<number>;
}

export function createAgentSessionPersistence(
  agentEventsCapacityBytes: number,
): AgentSessionPersistence {
  return {
    agentState: new Attribute("AgentState", agentStateCodec),
    agentMessages: new AttributeMap("AgentMessages", agentMessageCodec),
    agentMessageChunks: new AttributeMap("AgentMessageChunks", agentMessageChunkCodec),
    agentMessageIndex: new Attribute("AgentMessageIndex", agentMessageIndexCodec),
    frameworkRunState: new Attribute("FrameworkRunState", stringCodec),
    agentToolCalls: new AttributeMap("AgentToolCalls", agentToolCallCodec),
    pendingInterrupts: new Attribute("PendingInterrupts", agentInterruptListCodec),
    agentUsage: new Attribute("AgentUsage", agentUsageCodec),
    queuedUserMessages: new Channel("QueuedUserMessages", agentUserMessageCodec),
    interruptResolutions: new ChannelMap("InterruptResolutions", agentInterruptResolutionCodec),
    toolCallResults: new Channel("ToolCallResults", toolCallCompletedCodec),
    steeredUserMessages: new Channel("SteeredUserMessages", agentUserMessageCodec),
    turnControlRequests: new Channel("TurnControlRequests", turnControlRequestCodec),
    turnCancellationRequest: new Attribute("TurnCancellationRequest", turnCancellationRequestCodec),
    toolCallCancellations: new ChannelMap("ToolCallCancellations", toolCallCancellationCodec),
    agentEvents: new Stream("AgentEvents", agentEventCodec, agentEventsCapacityBytes),
    agentTraceContext: new Attribute("AgentTraceContext", stringCodec),
    agentPlan: new Attribute("AgentPlan", agentPlanCodec),
    planExecutions: new ChannelMap("PlanExecutions", planExecutionRequestCodec),
    pendingPlanExecution: new Attribute("PendingPlanExecution", planExecutionRequestCodec),
    contextSummary: new Attribute("ContextSummary", contextSummaryCodec),
    sessionSettings: new Attribute("SessionSettings", sessionSettingsCodec),
    turnSessionSettings: new Attribute("TurnSessionSettings", turnSessionSettingsCodec),
    turnReplySequence: new Attribute("TurnReplySequence", turnReplySequenceCodec),
    agentSessionTitle: new Attribute("AgentSessionTitle", stringCodec, {
      type: IndexType.FULL_TEXT,
      indexKey: sessionSummaryIndexKeys.title,
    }),
    agentSessionLastActivityAt: new Attribute("AgentSessionLastActivityAt", stringCodec, {
      type: IndexType.DATETIME,
      indexKey: sessionSummaryIndexKeys.lastActivityAt,
    }),
    agentSessionTurnCount: new Attribute("AgentSessionTurnCount", turnCountCodec, {
      type: IndexType.INT,
      indexKey: sessionSummaryIndexKeys.turnCount,
    }),
  };
}

/**
 * Returns the `AgentMessages` map instance of a message sequence, which is also the `AgentMessageChunks`
 * instance of a chunk that starts at that sequence.
 *
 * A message stays in `AgentMessages` only while it is in the window; read older ones from their chunk, or
 * page through all of them with `list_agent_messages`.
 *
 * @example
 * ```ts
 * const [message] = await client.getAttributes(sessionId, flow.agentMessages.instance(messageSequenceKey(7)));
 * ```
 * @param sequence - Positive message sequence.
 * @returns The sequence as a 12-digit zero-padded decimal, such as `"000000000007"`.
 * @throws {RangeError} When `sequence` is not a positive safe integer.
 */
export function messageSequenceKey(sequence: number): string {
  if (!Number.isSafeInteger(sequence) || sequence <= 0) {
    throw new RangeError(`message sequence must be a positive integer: ${sequence}`);
  }
  return String(sequence).padStart(12, "0");
}

/**
 * Returns the `PlanExecutions` map instance of a plan revision.
 *
 * @param revision - Positive {@link AgentPlan.revision}.
 * @returns The revision as a decimal, such as `"3"`.
 * @throws {RangeError} When `revision` is not a positive safe integer.
 */
export function planExecutionKey(revision: number): string {
  if (!Number.isSafeInteger(revision) || revision <= 0) {
    throw new RangeError(`plan revision must be a positive integer: ${revision}`);
  }
  return String(revision);
}
