// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ExecuteFailure, StepRef, type AttributeMapLoad, type RetryPolicy, type StepOptions } from "@superdurable/dex";

import type { AgentSessionPersistence } from "./agent-session-persistence.js";
import { agentSessionStepTypes } from "./agent-session-step-types.js";
import {
  modelStepMaximumAttempts,
  resolveModelRetryPolicy,
  type ModelRetryPolicy,
  type ResolvedModelRetryPolicy,
} from "./model-call-retry.js";

export const minuteMs = 60_000;
const maxStepDurationMs = 2_147_483_647_000;

/**
 * Sets how long one `RunModel` Step may run and how often it runs again.
 *
 * The defaults fit one model call. A harness runner, such as the Claude Agent SDK adapter, runs a whole turn segment
 * in one `RunModel`: several model calls and inline tool calls. Raise {@link methodTimeoutMs} when a segment needs
 * longer than 15 minutes.
 *
 * Background heartbeats continue through the Step, so a long silent call stays within the heartbeat timeout and
 * `methodTimeoutMs` is its only cap. Dex ends an attempt at `methodTimeoutMs` and aborts its `cancellationSignal`. The
 * next attempt reruns the whole segment and pays for it again, so consider `maximumAttempts: 1` for an expensive
 * segment. A hung call holds its Worker slot until `methodTimeoutMs`, so do not set it higher than needed.
 *
 * The retry budget moves with these values. It is `methodTimeoutMs` times one less than `maximumAttempts`, and at least
 * twice `methodTimeoutMs`, so the default limit gives 30 minutes. Dex checks it only after an attempt fails, and never
 * ends a running attempt, so it must exceed `methodTimeoutMs` for a timed-out attempt to retry at all.
 *
 * A transient model API error, such as HTTP 429 or 503, retries under {@link retry} with exponential backoff and jitter,
 * on top of `maximumAttempts`. Any other HTTP 4xx answer fails the turn without a retry. See {@link ModelRetryPolicy}.
 *
 * `CompactContext` keeps the defaults, because it makes one model call. A changed limit applies to each `RunModel`
 * scheduled after the Worker restarts. An attempt in flight, and its retries, keep their limit.
 *
 * Changed in 0.6.16: a transient model API error waits with exponential backoff and jitter, about two minutes in all
 * by default, instead of using the three attempts about one and two seconds apart. Any other HTTP 4xx answer fails
 * the turn at once instead of being retried.
 *
 * @example
 * ```ts
 * const flow = createClaudeAgentSessionFlow(agent, "SupportAgent", {
 *   runModelLimit: { methodTimeoutMs: 60 * 60_000, maximumAttempts: 2 },
 * });
 * ```
 *
 * @since 0.3.0
 */
export interface RunModelLimit {
  /** Longest one attempt may run, in milliseconds, a whole number of seconds. Defaults to 15 minutes. */
  readonly methodTimeoutMs?: number;
  /**
   * Attempts, including the first, for an error that is not a transient model API error, a method timeout, or a
   * Worker crash. A transient error may also use the retries of {@link retry}. `1` disables every retry. Defaults to 3.
   */
  readonly maximumAttempts?: number;
  /**
   * Backoff for a transient model API error. Defaults to waits from 1 to 30 seconds, about two minutes in all. See
   * {@link ModelRetryPolicy}.
   *
   * @since 0.6.16
   */
  readonly retry?: ModelRetryPolicy;
}

/** A {@link RunModelLimit} with every default filled in and the retry budget it implies. */
export interface ResolvedRunModelLimit {
  /** Longest one attempt may run, in milliseconds. */
  readonly methodTimeoutMs: number;
  /** Attempts, including the first. */
  readonly maximumAttempts: number;
  /** The retry budget in milliseconds. */
  readonly totalDurationMs: number;
  /**
   * The resolved backoff for transient model API errors. `resolveRunModelLimit` always sets it; omitted means the
   * default.
   *
   * @since 0.6.16
   */
  readonly retry?: ResolvedModelRetryPolicy;
}

/**
 * Fills in a {@link RunModelLimit}.
 * @param limit - The application's limit.
 * @returns The limit with its defaults and retry budget.
 * @throws {RangeError} When `methodTimeoutMs` is not a positive whole number of seconds, `maximumAttempts` is not a
 * positive integer, or the retry budget does not fit a signed 32-bit number of seconds, or `retry` is invalid; see
 * {@link resolveModelRetryPolicy}.
 */
export function resolveRunModelLimit(limit: RunModelLimit = {}): ResolvedRunModelLimit {
  const methodTimeoutMs = limit.methodTimeoutMs ?? modelCallStepOptions.executeMethodTimeoutMs;
  if (!Number.isSafeInteger(methodTimeoutMs) || methodTimeoutMs < 1_000 || methodTimeoutMs % 1_000 !== 0) {
    throw new RangeError("runModelLimit.methodTimeoutMs must be a whole number of seconds, at least one");
  }
  const maximumAttempts = limit.maximumAttempts ?? modelCallStepOptions.executeRetry.maximumAttempts;
  if (!Number.isSafeInteger(maximumAttempts) || maximumAttempts < 1) {
    throw new RangeError("runModelLimit.maximumAttempts must be positive");
  }
  const totalDurationMs = methodTimeoutMs * Math.max(maximumAttempts - 1, 2);
  if (totalDurationMs > maxStepDurationMs) {
    throw new RangeError("runModelLimit.methodTimeoutMs times maximumAttempts is too long for Dex");
  }
  const retry = resolveModelRetryPolicy(limit.retry);
  return Object.freeze({ methodTimeoutMs, maximumAttempts, totalDurationMs, retry });
}

/** Returns a limit's retry policy, filling in the default for a limit built without one. */
export function runModelRetryPolicy(limit: ResolvedRunModelLimit): ResolvedModelRetryPolicy {
  return limit.retry ?? defaultResolvedRunModelLimit.retry!;
}

/** Returns the Dex retry policy of a model Step under a limit. */
export function modelStepRetryPolicy(limit: ResolvedRunModelLimit): RetryPolicy {
  const retry = runModelRetryPolicy(limit);
  return {
    initialIntervalMs: retry.initialIntervalMs,
    maximumIntervalMs: retry.maximumIntervalMs,
    maximumAttempts: modelStepMaximumAttempts(limit.maximumAttempts, retry),
    totalDurationMs: limit.totalDurationMs,
  };
}
// CompactContext makes one model call, so it shares RunModel's timeouts and retries.
export const modelCallStepOptions = {
  executeMethodTimeoutMs: 15 * minuteMs,
  heartbeatTimeoutMs: 2 * minuteMs,
  executeRetry: { maximumAttempts: 3, totalDurationMs: 30 * minuteMs },
  executeFailure: ExecuteFailure.proceedTo(new StepRef(agentSessionStepTypes.recordTurnFailure)),
} as const satisfies StepOptions;
// CompactContext keeps the default limit.
export const defaultResolvedRunModelLimit = resolveRunModelLimit();
export const compactContextStepOptions: StepOptions = {
  ...modelCallStepOptions,
  executeRetry: modelStepRetryPolicy(defaultResolvedRunModelLimit),
};
// Recovery Steps must eventually run; cap their backoff so a returning Worker picks them up quickly.
export const recoveryRetry: RetryPolicy = { maximumIntervalMs: 5_000 };
// An ASYNC FlowConfig could replay a committed result after a Server failover, re-running a write.
export const syncDurability = { waitForDurability: "sync", executeDurability: "sync" } as const;

/**
 * The archive chunks a Step reads on top of the whole `AgentMessages` window: exact instances, or every chunk
 * when the model input covers them all.
 */
export interface MessageChunkLoads {
  readonly isWholeMap: boolean;
  readonly instances: readonly AttributeMapLoad[];
}

/** Returns the options of a Step that reads model input and stores messages, with its chunk loads. */
export function messageStepOptions(
  persistence: AgentSessionPersistence,
  stepType: string,
  chunkLoads: MessageChunkLoads,
  runModelLimit: ResolvedRunModelLimit,
): StepOptions {
  const loads = {
    executeLoadAttributeMaps: chunkLoads.isWholeMap
      ? [persistence.agentMessages, persistence.agentToolCalls, persistence.agentMessageChunks]
      : [persistence.agentMessages, persistence.agentToolCalls],
    ...(chunkLoads.instances.length === 0 ? {} : { executeLoadAttributeMapInstances: chunkLoads.instances }),
  };
  switch (stepType) {
    case agentSessionStepTypes.runModel:
      return {
        ...syncDurability,
        ...modelCallStepOptions,
        executeMethodTimeoutMs: runModelLimit.methodTimeoutMs,
        executeRetry: modelStepRetryPolicy(runModelLimit),
        ...loads,
      };
    case agentSessionStepTypes.applyTurnControl:
      return { ...syncDurability, executeRetry: recoveryRetry, ...loads };
    default:
      throw new TypeError(`Step type ${stepType} does not read the model input`);
  }
}
