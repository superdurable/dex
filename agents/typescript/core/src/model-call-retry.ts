// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { retryAfter } from "@superdurable/dex";

/**
 * Sets how `RunModel` and `CompactContext` retry a model call that failed with a transient model API error.
 *
 * A transient error is an HTTP 408, 409, 429, or 5xx answer from the model API, or a connection error or timeout on
 * the way to it. Dex retries such a call with exponential backoff and jitter. The first wait is
 * {@link initialIntervalMs}. Each next wait doubles, up to {@link maximumIntervalMs}. Each wait is drawn between half
 * of that value and all of it, so sessions that failed together do not all retry together. When the error carries
 * `Retry-After` or `retry-after-ms`, the wait is at least that long.
 *
 * Retries stop after the nominal waits add up to more than {@link totalDurationMs}. Then the turn fails, as before.
 * The defaults wait about two minutes in all. That rides out a typical provider outage or rate-limit window, which
 * usually clears within a minute, and still ends a turn whose provider is down instead of keeping it waiting.
 *
 * Any other HTTP 4xx answer, such as 400 or 401, fails the turn at once: a retry would get the same answer. Other
 * errors, method timeouts, and Worker crashes retry up to {@link RunModelLimit.maximumAttempts}, as before.
 * `maximumAttempts: 1` disables every retry, transient ones included.
 *
 * @example
 * ```ts
 * const flow = createVercelAiAgentSessionFlow(agent, "SupportAgent", {
 *   // Wait up to five minutes for the provider, at most one minute between attempts.
 *   runModelLimit: { retry: { maximumIntervalMs: 60_000, totalDurationMs: 5 * 60_000 } },
 * });
 * ```
 *
 * @since 0.6.16
 */
export interface ModelRetryPolicy {
  /** Wait before the first retry of a transient error, in milliseconds, a whole number of seconds. Defaults to 1 second. */
  readonly initialIntervalMs?: number;
  /** Longest wait between two attempts, in milliseconds, a whole number of seconds. Defaults to 30 seconds. */
  readonly maximumIntervalMs?: number;
  /**
   * The nominal waits of transient retries add up to at most this, in milliseconds, a whole number of seconds.
   * Defaults to 2 minutes. `0` gives transient errors only the retries of `maximumAttempts`.
   */
  readonly totalDurationMs?: number;
}

/** A {@link ModelRetryPolicy} with every default filled in. */
export interface ResolvedModelRetryPolicy {
  /** Wait before the first retry, in milliseconds. */
  readonly initialIntervalMs: number;
  /** Longest wait between two attempts, in milliseconds. */
  readonly maximumIntervalMs: number;
  /** Most nominal waiting for transient retries, in milliseconds. */
  readonly totalDurationMs: number;
  /** Retries the nominal waits fit in `totalDurationMs`, on top of `maximumAttempts`. */
  readonly transientRetries: number;
}

export const defaultModelRetryPolicy = Object.freeze({
  initialIntervalMs: 1_000,
  maximumIntervalMs: 30_000,
  totalDurationMs: 120_000,
});

/**
 * Fills in a {@link ModelRetryPolicy}.
 * @param policy - The application's policy.
 * @returns The policy with its defaults and the transient retries it allows.
 * @throws {RangeError} When a value is not a whole number of seconds, the initial interval is zero, or the maximum
 * interval is shorter than the initial interval.
 */
export function resolveModelRetryPolicy(policy: ModelRetryPolicy = {}): ResolvedModelRetryPolicy {
  const initialIntervalMs = policy.initialIntervalMs ?? defaultModelRetryPolicy.initialIntervalMs;
  const maximumIntervalMs = policy.maximumIntervalMs ?? defaultModelRetryPolicy.maximumIntervalMs;
  const totalDurationMs = policy.totalDurationMs ?? defaultModelRetryPolicy.totalDurationMs;
  for (const [name, value, minimum] of [
    ["initialIntervalMs", initialIntervalMs, 1_000],
    ["maximumIntervalMs", maximumIntervalMs, 1_000],
    ["totalDurationMs", totalDurationMs, 0],
  ] as const) {
    if (!Number.isSafeInteger(value) || value < minimum || value % 1_000 !== 0 || value > maxIntervalMs) {
      throw new RangeError(`runModelLimit.retry.${name} must be a whole number of seconds, at least ${minimum / 1_000}`);
    }
  }
  if (maximumIntervalMs < initialIntervalMs) {
    throw new RangeError("runModelLimit.retry.maximumIntervalMs must not be shorter than initialIntervalMs");
  }
  let transientRetries = 0;
  let waitedMs = 0;
  for (;;) {
    waitedMs += nominalRetryIntervalMs(initialIntervalMs, maximumIntervalMs, transientRetries + 1);
    if (waitedMs > totalDurationMs) {
      break;
    }
    transientRetries += 1;
  }
  return Object.freeze({ initialIntervalMs, maximumIntervalMs, totalDurationMs, transientRetries });
}

// Dex takes Step durations as a signed 32-bit number of seconds.
const maxIntervalMs = 2_147_483_647_000;

/** The wait before retry `retry` (one-based) before jitter: the initial interval doubled, up to the maximum. */
function nominalRetryIntervalMs(initialIntervalMs: number, maximumIntervalMs: number, retry: number): number {
  return Math.min(initialIntervalMs * 2 ** Math.min(retry - 1, 52), maximumIntervalMs);
}

/** What a model call's error says about calling again. */
export type ModelErrorKind = "transient" | "permanent" | "unknown";

/** A model call's error classified, with the provider's requested wait when it sent one. */
export interface ModelErrorClassification {
  readonly kind: ModelErrorKind;
  /** The `Retry-After` or `retry-after-ms` wait in milliseconds, or `undefined`. */
  readonly retryAfterMs?: number;
}

const transientStatuses = new Set([408, 409, 429]);
const connectionErrorNames = new Set([
  "APIConnectionError",
  "APIConnectionTimeoutError",
  "ConnectTimeoutError",
  "FetchError",
  "HeadersTimeoutError",
  "BodyTimeoutError",
  "SocketError",
  "TimeoutError",
]);
const connectionErrorCodes = new Set([
  "ECONNREFUSED",
  "ECONNRESET",
  "ECONNABORTED",
  "EPIPE",
  "ETIMEDOUT",
  "EAI_AGAIN",
  "ENETUNREACH",
  "EHOSTUNREACH",
  "UND_ERR_SOCKET",
  "UND_ERR_CONNECT_TIMEOUT",
  "UND_ERR_HEADERS_TIMEOUT",
  "UND_ERR_BODY_TIMEOUT",
  "UND_ERR_CLOSED",
]);

/**
 * Classifies a model call's error from the fields that the common model clients set: `status`, `statusCode`, or
 * `response.status` for the HTTP status, `headers`, `responseHeaders`, or `response.headers` for `Retry-After`, and the
 * error name or code for a connection error. An error without a status and with `isRetryable: true` is transient. It follows `cause` and the AI SDK's `lastError`.
 * @param error - The thrown value.
 * @returns `transient` for a retryable model API error, `permanent` for any other HTTP 4xx, else `unknown`.
 */
export function classifyModelError(error: unknown): ModelErrorClassification {
  const seen = new Set<unknown>();
  let current: unknown = error;
  while (typeof current === "object" && current !== null && !seen.has(current)) {
    seen.add(current);
    const record = current as Record<string, unknown>;
    const status = readStatus(record);
    if (status !== undefined) {
      if (transientStatuses.has(status) || status >= 500) {
        const retryAfterMs = readRetryAfterMs(record);
        return retryAfterMs === undefined ? { kind: "transient" } : { kind: "transient", retryAfterMs };
      }
      if (status >= 400) {
        return { kind: "permanent" };
      }
    }
    if (isConnectionError(record)) {
      return { kind: "transient" };
    }
    // The AI SDK's RetryError keeps the last attempt's error; other clients chain `cause`.
    current = record.lastError ?? record.cause;
  }
  return { kind: "unknown" };
}

function readStatus(record: Record<string, unknown>): number | undefined {
  const response = record.response;
  for (const value of [
    record.status,
    record.statusCode,
    typeof response === "object" && response !== null ? (response as Record<string, unknown>).status : undefined,
  ]) {
    if (typeof value === "number" && Number.isInteger(value) && value >= 100 && value <= 599) {
      return value;
    }
  }
  return undefined;
}

function isConnectionError(record: Record<string, unknown>): boolean {
  if (typeof record.code === "string" && connectionErrorCodes.has(record.code)) {
    return true;
  }
  if (typeof record.name === "string" && connectionErrorNames.has(record.name)) {
    return true;
  }
  // The AI SDK sets it on a call error without a status, such as a request that got no answer.
  return record.isRetryable === true;
}

function readRetryAfterMs(record: Record<string, unknown>): number | undefined {
  const response = record.response;
  for (const headers of [
    record.headers,
    record.responseHeaders,
    typeof response === "object" && response !== null ? (response as Record<string, unknown>).headers : undefined,
  ]) {
    const milliseconds = Number(readHeader(headers, "retry-after-ms"));
    if (Number.isFinite(milliseconds) && milliseconds > 0) {
      return milliseconds;
    }
    const retryAfter = readHeader(headers, "retry-after");
    if (retryAfter === undefined) {
      continue;
    }
    const seconds = Number(retryAfter);
    if (Number.isFinite(seconds) && seconds > 0) {
      return seconds * 1_000;
    }
    const date = Date.parse(retryAfter);
    if (Number.isFinite(date) && date > Date.now()) {
      return date - Date.now();
    }
  }
  return undefined;
}

function readHeader(headers: unknown, name: string): string | undefined {
  if (typeof headers !== "object" || headers === null) {
    return undefined;
  }
  const get = (headers as { get?: unknown }).get;
  if (typeof get === "function") {
    const value: unknown = get.call(headers, name);
    return typeof value === "string" ? value : undefined;
  }
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === name && typeof value === "string") {
      return value;
    }
  }
  return undefined;
}

/** What a model Step does after its model call threw. */
export type ModelCallRetryDecision =
  | { readonly kind: "retry_after"; readonly afterSeconds: number }
  | { readonly kind: "rethrow" }
  | { readonly kind: "fail_turn" };

/**
 * Decides what a model Step attempt does with the error its model call threw.
 * @param error - The thrown value.
 * @param attempt - The one-based attempt number.
 * @param maximumAttempts - `RunModelLimit.maximumAttempts`.
 * @param retry - The resolved retry policy.
 * @param random - A number in [0, 1) for the jitter.
 * @returns `retry_after` with the jittered wait for a transient error, `fail_turn` for a permanent error, else
 * `rethrow`, which leaves the error to the model Step's attempt limit.
 */
export function decideModelCallRetry(
  error: unknown,
  attempt: number,
  maximumAttempts: number,
  retry: ResolvedModelRetryPolicy,
  random: number = Math.random(),
): ModelCallRetryDecision {
  if (maximumAttempts <= 1) {
    return { kind: "rethrow" };
  }
  const classification = classifyModelError(error);
  switch (classification.kind) {
    case "permanent":
      return { kind: "fail_turn" };
    case "transient": {
      const nominalMs = nominalRetryIntervalMs(retry.initialIntervalMs, retry.maximumIntervalMs, attempt);
      const jitteredMs = nominalMs / 2 + (nominalMs / 2) * random;
      const waitMs = Math.max(jitteredMs, classification.retryAfterMs ?? 0);
      return { kind: "retry_after", afterSeconds: Math.min(Math.max(1, Math.ceil(waitMs / 1_000)), 2_147_483_647) };
    }
    case "unknown":
      // The model Step's wrapper ends the turn on the last attempt, for every error.
      return { kind: "rethrow" };
  }
}

/** Returns the attempts the Step's Dex retry policy allows: `maximumAttempts` plus the transient retries. */
export function modelStepMaximumAttempts(maximumAttempts: number, retry: ResolvedModelRetryPolicy): number {
  return maximumAttempts <= 1 ? 1 : maximumAttempts + retry.transientRetries;
}

/** Returns the error text a failed turn records for a thrown value, as the Dex Worker reports it. */
export function modelCallErrorText(error: unknown): string {
  const cause = error instanceof Error ? error : new Error(String(error));
  return cause.message || cause.name;
}

/** Thrown inside a model Step when its model call failed in a way that ends the turn without another attempt. */
export class ModelCallTurnFailure extends Error {
  public constructor(public readonly errorText: string, options: { readonly cause: unknown }) {
    super(errorText, options);
    this.name = "ModelCallTurnFailure";
  }
}

/**
 * Runs one model call of a model Step under the retry policy.
 * @param call - The model call.
 * @param attempt - The one-based attempt number.
 * @param maximumAttempts - `RunModelLimit.maximumAttempts`.
 * @param retry - The resolved retry policy.
 * @param cancellationSignal - The attempt's signal; an error after it aborts is rethrown as is.
 * @returns The call's result.
 * @throws {RetryAfterError} For a transient error, with the jittered wait.
 * @throws {ModelCallTurnFailure} For an error that ends the turn now.
 * @throws The call's own error, for Dex to retry under the Step's retry policy.
 */
export async function callModelUnderRetryPolicy<Result>(
  call: () => Promise<Result>,
  attempt: number,
  maximumAttempts: number,
  retry: ResolvedModelRetryPolicy,
  cancellationSignal: AbortSignal,
): Promise<Result> {
  try {
    return await call();
  } catch (error: unknown) {
    if (cancellationSignal.aborted) {
      throw error;
    }
    const decision = decideModelCallRetry(error, attempt, maximumAttempts, retry);
    switch (decision.kind) {
      case "retry_after":
        throw retryAfter(decision.afterSeconds, error instanceof Error ? error : new Error(String(error)));
      case "fail_turn":
        throw new ModelCallTurnFailure(modelCallErrorText(error), { cause: error });
      case "rethrow":
        throw error;
    }
  }
}
