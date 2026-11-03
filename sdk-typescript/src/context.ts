// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Attribute, AttributeMap } from "./persistence.js";
import type { Channel, ChannelMap, ChannelMessage } from "./wait.js";
import type { Codec } from "./codec.js";
import type { Stream } from "./stream.js";

/**
 * Describes how far the attempts of a failed Step method got.
 *
 * Dex reports the furthest attempt of the failed method in {@link RecoveryErrorInfo.attemptProgress}.
 * A recovery Step uses it to tell work that never ran from work that may have run.
 *
 * - `"unspecified"`: the Dex Server does not report progress. Treat the work as possibly run.
 * - `"notAssigned"`: no attempt was sent to a Worker, so the work never ran.
 * - `"assigned"`: an attempt was sent to a Worker, and no attempt marked itself started. The work
 *   did not start only for a Step that calls {@link AsyncContext.markAttemptStarted} before every
 *   external effect. For any other Step, treat it as possibly run.
 * - `"started"`: an attempt marked itself started, so its external effect may have run.
 */
export type StepAttemptProgress = "unspecified" | "notAssigned" | "assigned" | "started";

/** Describes the final failure that selected a recovery path. */
export interface RecoveryErrorInfo {
  /** Failure message supplied by the Worker or backend. */
  readonly detail: string;
  /**
   * Original Worker error type or backend failure type.
   *
   * A failed attempt that had marked itself started and lost its Worker result to a Dex Server
   * restart reports `StartedAttemptInterrupted`.
   */
  readonly errorType: string;
  /**
   * How far the failed method's attempts got.
   *
   * It is `"unspecified"` from a Dex Server that does not report it.
   */
  readonly attemptProgress: StepAttemptProgress;
}

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
  /** Final failure that selected this recovery path, or `undefined` when absent. */
  readonly recoveryError: RecoveryErrorInfo | undefined;
  /** UTC timestamp of the first handler attempt. */
  readonly firstAttemptAt: Date;
  /**
   * Absolute UTC deadline of the current Step method attempt, or `undefined` when absent.
   *
   * Available during `waitFor`, `execute`, and Flow timeout handlers. RPC handlers have no
   * deadline. A shorter remaining RetryPolicy total duration limits this value. Each retry
   * receives its own effective deadline. Dates have millisecond precision.
   *
   * @example
   * Compute the remaining attempt budget in milliseconds inside a handler:
   * ```ts
   * const deadline = context.deadline;
   * if (deadline !== undefined) {
   *   const remainingMs = deadline.getTime() - Date.now();
   * }
   * ```
   */
  readonly deadline: Date | undefined;
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
   * Value from a present null value, because both decode to `undefined`.
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
 * Annotate an async `waitFor`, `execute`, or Flow timeout handler with AsyncContext when it
 * records heartbeats. RPC handlers receive Context and cannot record heartbeats.
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
   * All failures reject this Promise; this method never throws synchronously.
   * @throws {Error} The Promise rejects if the task is inactive, finished, cancelled, or its stream is closed.
   * @throws {TypeError} The Promise rejects when the Context cannot record heartbeats.
   * @throws {ValueMappingError} The Promise rejects if the heartbeat value cannot be encoded.
   */
  recordHeartbeat<T>(value: T | undefined, codec?: Codec<T>): Promise<void>;

  /**
   * Records durably that this attempt is about to start its external effect.
   *
   * Call this method right before an external effect that must not run twice, such as a payment
   * or an email. The Promise resolves only after Dex has committed the mark. From then on, Dex
   * never delivers this attempt again at the same attempt number. When the Worker dies, or a Dex
   * Server node restarts or fails over, the attempt fails instead. Its retry policy then applies,
   * and after the last attempt the failure route receives {@link RecoveryErrorInfo.attemptProgress}
   * set to `"started"`. An attempt that fails before the mark reports `"notAssigned"` or
   * `"assigned"`, so a failure route can tell that the effect never started.
   *
   * Time travel is the exception. Time travel to a point before the mark runs the attempt again in
   * the new run attempt, like every Step after that point.
   *
   * The call costs one commit round trip. Call it once per attempt. A call made while another one
   * waits for its acknowledgement shares that acknowledgement. A later call commits again and has
   * no further effect. A heartbeat does not replace it, because Dex does not acknowledge
   * heartbeats.
   *
   * @example
   * ```ts
   * class ChargeCard implements Step<string> {
   *   public readonly inputCodec = stringCodec;
   *
   *   public async execute(context: AsyncContext, input: string): Promise<StepDecision> {
   *     await context.markAttemptStarted();
   *     await payments.charge(input, { idempotencyKey: context.stepExecutionId });
   *     return deadEnd();
   *   }
   * }
   * ```
   *
   * @returns A Promise resolved after Dex acknowledges the committed mark. It never throws
   * synchronously.
   * @throws {AttemptStartedNotAcknowledgedError} The Promise rejects when the Dex Server does not
   * support the mark, or when the task was cancelled or its stream closed before Dex acknowledged
   * the mark. Do not start the effect after this error.
   * @throws {TypeError} The Promise rejects when the Context cannot mark attempts, such as in an RPC.
   */
  markAttemptStarted(): Promise<void>;
}
