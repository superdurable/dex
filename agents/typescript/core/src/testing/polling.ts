// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

/** Options of {@link pollUntil}. */
export interface PollOptions {
  /** What is awaited, used in the timeout error. */
  readonly description: string;
  /** Total milliseconds to wait. Defaults to 90,000. */
  readonly timeoutMs?: number;
  /** Milliseconds between probes. Defaults to 200. */
  readonly intervalMs?: number;
  /** Signal that ends the wait early with an `AbortError`. */
  readonly signal?: AbortSignal;
}

/**
 * Calls a probe until it returns a value other than `undefined`, or the deadline passes.
 *
 * A probe that throws counts as not ready, so a probe can call an RPC while no Worker runs.
 * Use it only for state that has no direct Client wait.
 *
 * @example
 * ```ts
 * const toolCall = await pollUntil(async () => {
 *   const [record] = await client.getAttributes(sessionId, flow.persistence.agentToolCalls.instance(id));
 *   return record?.status === "outcome_unknown" ? record : undefined;
 * }, { description: "outcome_unknown tool call" });
 * ```
 * @typeParam Value - Value returned by the probe once ready.
 * @param probe - Returns the awaited value, or `undefined` while not ready.
 * @param options - Description, timeout, interval, and signal.
 * @returns The first value the probe returned.
 * @throws {Error} When the deadline passes; the message holds the description and the last probe error.
 */
export async function pollUntil<Value>(
  probe: () => Value | undefined | Promise<Value | undefined>,
  options: PollOptions,
): Promise<Value> {
  const deadline = Date.now() + (options.timeoutMs ?? 90_000);
  let lastError: unknown;
  for (;;) {
    options.signal?.throwIfAborted();
    try {
      const value = await probe();
      if (value !== undefined) {
        return value;
      }
    } catch (error: unknown) {
      lastError = error;
    }
    if (Date.now() >= deadline) {
      const lastErrorText = lastError === undefined ? "" : `; last probe error: ${String(lastError)}`;
      throw new Error(`timed out waiting for ${options.description}${lastErrorText}`, {
        cause: lastError,
      });
    }
    await delay(options.intervalMs ?? 200, undefined, options.signal === undefined ? {} : { signal: options.signal });
  }
}
