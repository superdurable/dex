// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Attribute, AttributeMap, Channel, ChannelMap, Stream, stringCodec } from "@superdurable/dex";

import {
  agentEventCodec,
  agentInterruptListCodec,
  agentInterruptResolutionCodec,
  agentMessageCodec,
  agentPlanCodec,
  agentStateCodec,
  agentToolCallCodec,
  agentUsageCodec,
  agentUserMessageCodec,
  planExecutionRequestCodec,
  contextSummaryCodec,
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
  AgentPlan,
  AgentState,
  AgentToolCallRecord,
  AgentUsage,
  AgentUserMessage,
  PlanExecutionRequest,
  ContextSummary,
  ToolCallCancellation,
  ToolCallCompleted,
  TurnCancellationRequest,
  TurnControlRequest,
} from "./agent-session-values.js";

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
  /** The `AgentMessages` AttributeMap, keyed by sequence as 12-digit zero-padded decimal. */
  readonly agentMessages: AttributeMap<AgentMessage>;
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
}

export function createAgentSessionPersistence(
  agentEventsCapacityBytes: number,
): AgentSessionPersistence {
  return {
    agentState: new Attribute("AgentState", agentStateCodec),
    agentMessages: new AttributeMap("AgentMessages", agentMessageCodec),
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
  };
}

/**
 * Returns the `AgentMessages` map instance of a message sequence.
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
