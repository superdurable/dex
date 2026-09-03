// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Attribute, AttributeMap } from "./persistence.js";
import type { Channel, ChannelMap, ChannelMessage } from "./wait.js";
import type { Codec } from "./codec.js";
import type { Stream } from "./stream.js";

/**
 * Exposes execution metadata and decision-local persistence operations.
 * Dex supplies a Context to each Step or RPC handler; do not retain it afterward.
 */
export interface Context {
  /** Stable application Flow ID shared across runs. */
  readonly flowId: string;
  /** Current server-assigned run ID. */
  readonly runId: string;
  /** UTC timestamp at which the current Flow run started. */
  readonly flowStartedAt: Date;
  /** Current Step type and execution number encoded by Dex. */
  readonly stepExecutionId: string;
  /** Predecessor Step execution ID, or an empty string at Flow start. */
  readonly fromStepExecutionId: string;
  /** UTC timestamp of the first handler attempt. */
  readonly firstAttemptAt: Date;
  /** One-based handler retry attempt number. */
  readonly attempt: number;
  /**
   * Signal aborted when Dex cancels the active Worker call.
   *
   * Pass it to abort-aware APIs or inspect it at natural CPU-work boundaries. JavaScript
   * cannot forcibly stop synchronous code, and cancellation cannot make external effects atomic.
   */
  readonly cancellationSignal: AbortSignal;
  /**
   * Reports whether a previous regular-activity attempt recorded heartbeat details.
   * @returns Whether a last heartbeat Value is present.
   */
  hasLastHeartbeatValue(): boolean;
  /**
   * Decodes heartbeat details recorded by the previous regular-activity attempt.
   *
   * The codec must match the one passed to {@link AsyncContext.recordHeartbeat}. When omitted,
   * values use JSON encoding. Call {@link Context.hasLastHeartbeatValue} to distinguish an absent
   * Value from a present JSON null value, because both decode to `undefined`.
   *
   * @typeParam T - Expected heartbeat value type.
   * @param codec - Codec used to decode the Value; omitted for JSON.
   * @returns The decoded value, or `undefined` when no Value is present.
   */
  getLastHeartbeatValue<T = unknown>(codec?: Codec<T>): T | undefined;
  /**
   * Reports whether a Timer made the current Wait ready.
   * @param index - Optional zero-based Timer index; checks any Timer when omitted.
   * @returns Whether the selected Timer fired.
   */
  hasTimerFired(index?: number): boolean;
  /**
   * Reports whether `waitFor` failed before the current `execute` call.
   * @returns Whether failure policy proceeded to execution.
   */
  waitForMethodFailed(): boolean;
  /**
   * Stores process-local data for this Step execution; it is not durable.
   * @typeParam T - Local value type.
   * @param key - Non-empty execution-scoped key.
   * @param value - Value retained in worker memory.
   * @param codec - Codec used to serialize the local value when required.
   */
  setStepExecutionLocal<T>(key: string, value: T, codec: Codec<T>): void;
  /**
   * Reads process-local data for this Step execution.
   * @typeParam T - Expected local value type.
   * @param key - Key used when storing the value.
   * @param codec - Codec used to decode the value.
   * @returns The value, or `undefined` after absence or worker restart.
   */
  getStepExecutionLocal<T>(key: string, codec: Codec<T>): T | undefined;
  /**
   * Stages an application event in the current handler result.
   * @typeParam T - Event payload type.
   * @param name - Non-empty diagnostic event name.
   * @param value - Event payload.
   * @param codec - Payload codec.
   */
  recordEvent<T>(name: string, value: T, codec: Codec<T>): void;
  /**
   * Reads a typed Attribute from decision state.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered singleton or map definition.
   * @param instance - Required AttributeMap instance; omitted for a singleton.
   * @returns The decoded current value.
   */
  getAttribute<T>(attribute: Attribute<T> | AttributeMap<T>, instance?: string): T;
  /**
   * Stages an Attribute write in the current decision.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered singleton or map definition.
   * @param value - Typed value to persist.
   * @param instance - Required AttributeMap instance; omitted for a singleton.
   */
  setAttribute<T>(attribute: Attribute<T> | AttributeMap<T>, value: T, instance?: string): void;
  /**
   * Stages deletion of an Attribute value.
   * @param attribute - Registered singleton or map definition.
   * @param instance - Required AttributeMap instance; omitted for a singleton.
  */
  deleteAttribute(attribute: Attribute<unknown> | AttributeMap<unknown>, instance?: string): void;
  /**
   * Returns effective decoded AttributeMap keys in ascending order.
   * @param attribute - Registered AttributeMap definition.
   * @returns Keys visible after decision-local writes and deletions.
   */
  attributeMapKeys(attribute: AttributeMap<unknown>): readonly string[];
  /**
   * Stages one typed Channel publication.
   * @typeParam T - Channel element type.
   * @param channel - Registered singleton or map definition.
   * @param value - Value to append.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   */
  publish<T>(channel: Channel<T> | ChannelMap<T>, value: T, instance?: string): void;
  /**
   * Stages deletion of one pending Channel message from an RPC handler.
   * @param channel - Registered singleton or map definition.
   * @param messageId - Non-empty server-assigned message ID.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   */
  deleteChannelMessage(
    channel: Channel<unknown> | ChannelMap<unknown>,
    messageId: string,
    instance?: string,
  ): void;
  /**
   * Returns a Channel's current queued value count.
   * @param channel - Registered singleton or map definition.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   * @returns Non-negative queued value count.
   */
  channelSize(channel: Channel<unknown> | ChannelMap<unknown>, instance?: string): number;
  /**
   * Returns loaded pending Channel messages in FIFO order.
   * @typeParam T - Channel element type.
   * @param channel - Registered singleton or map definition selected by the RPC.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   * @returns Immutable pending message IDs and decoded values.
   */
  pendingChannelMessages<T>(
    channel: Channel<T> | ChannelMap<T>,
    instance?: string,
  ): readonly ChannelMessage<T>[];
  /**
   * Returns values selected by the satisfied Channel condition.
   * @typeParam T - Channel element type.
   * @param channel - Registered singleton or map definition.
   * @param instance - Required ChannelMap instance; omitted for a singleton.
   * @returns Ordered values for this Step execution.
   */
  channelResults<T>(channel: Channel<T> | ChannelMap<T>, instance?: string): readonly T[];
  /**
   * Returns decoded non-empty ChannelMap keys in ascending order during an RPC.
   * @param channel - Registered ChannelMap definition.
   * @returns Keys including publications buffered by the current RPC.
   */
  channelMapKeys(channel: ChannelMap<unknown>): readonly string[];
  /**
   * Appends one immediate best-effort Stream message.
   * @typeParam T - Stream message type.
   * @param stream - Stream registered by the current Flow type.
   * @param value - Typed message to append.
   */
  writeStream<T>(stream: Stream<T>, value: T): void;
}

/**
 * Adds asynchronous progress reporting to a Step invocation Context.
 *
 * Annotate an async `waitFor` or `execute` handler with AsyncContext when it records heartbeats.
 * RPC and Flow timeout handlers receive Context and cannot record Step heartbeats.
 */
export interface AsyncContext extends Context {
  /**
   * Records heartbeat progress for the current Step method attempt.
   *
   * Passing `undefined` explicitly clears persisted heartbeat details. Every other value produces
   * a present Value; omitted codecs use JSON. The Promise resolves when grpc-js accepts the frame,
   * not when Temporal persists it. Local activities ignore heartbeat frames.
   *
   * @typeParam T - Heartbeat value type.
   * @param value - Value to persist, or `undefined` to clear existing details.
   * @param codec - Codec used to encode the Value; omitted for JSON.
   * @returns A Promise resolved after the Worker accepts the output frame locally.
   */
  recordHeartbeat<T>(value: T | undefined, codec?: Codec<T>): Promise<void>;
}
