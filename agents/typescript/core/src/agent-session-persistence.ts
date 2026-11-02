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
  agentStateCodec,
  agentToolCallCodec,
  agentUsageCodec,
  agentUserMessageCodec,
  toolCallCompletedCodec,
} from "./agent-session-codecs.js";
import type {
  AgentEvent,
  AgentInterrupt,
  AgentInterruptResolution,
  AgentMessage,
  AgentState,
  AgentToolCall,
  AgentUsage,
  AgentUserMessage,
  ToolCallCompleted,
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
  readonly agentToolCalls: AttributeMap<AgentToolCall>;
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
  /** The best-effort `AgentEvents` Stream. Rebuild durable state with `get_agent_snapshot`. */
  readonly agentEvents: Stream<AgentEvent>;
  /**
   * The `AgentTraceContext` Attribute: the W3C `traceparent` of the current activation's root span when
   * that span has a valid context, sampled or not; an activation whose root has none, such as one without
   * an OpenTelemetry SDK, deletes it. `RunModel` reads it to join the activation's trace.
   */
  readonly agentTraceContext: Attribute<string>;
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
    agentEvents: new Stream("AgentEvents", agentEventCodec, agentEventsCapacityBytes),
    agentTraceContext: new Attribute("AgentTraceContext", stringCodec),
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
