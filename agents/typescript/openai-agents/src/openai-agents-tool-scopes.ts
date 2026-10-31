// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";

import type { AgentTurnContext, DeferredToolExecution } from "@superdurable/dex-agents";

/** The `RunModel` Step a gated tool runs in, and the resumed calls whose results it may return. */
export interface RunningModelStep {
  readonly turn: AgentTurnContext;
  /** Resumed call IDs not yet returned; each result is returned once. */
  readonly unreturnedResumedToolCallIds: Set<string>;
}

// Tool wrappers are built once per Flow, so they find the running Step through async context.
export const runningModelStepScope = new AsyncLocalStorage<RunningModelStep>();
export const runningDeferredToolExecutionScope = new AsyncLocalStorage<DeferredToolExecution>();

/**
 * Returns the deferred tool call that the current tool code runs for, when it runs in a Dex tool Step.
 *
 * Call it inside a function tool's `execute` to read the stable idempotency key, the attempt, the
 * Flow ID, or the cancellation signal of the Dex tool Step that runs the call. Pass
 * `idempotencyKey`, `<flowId>/<turnId>/<toolCallId>`, to services that deduplicate requests: an
 * `idempotent_write` call keeps the same key across its up to three attempts. In Step mode every
 * function tool runs in a Dex tool Step, so the value is set inside every tool call the session
 * makes; code outside a tool Step gets `undefined`.
 *
 * The value is scoped to one call through Node async context. Do not keep it after `execute`
 * returns.
 *
 * @example
 * ```ts
 * import { tool } from "@openai/agents";
 * import { z } from "zod";
 * import { getDeferredToolExecution } from "@superdurable/dex-agents-openai-agents";
 *
 * const refundOrder = tool({
 *   name: "refund_order",
 *   description: "Refund an order.",
 *   parameters: z.object({ orderId: z.string() }),
 *   execute: async ({ orderId }) => {
 *     const execution = getDeferredToolExecution();
 *     await payments.refund(orderId, { idempotencyKey: execution?.idempotencyKey });
 *     return `refunded ${orderId}`;
 *   },
 * });
 * ```
 * @returns The running deferred call, or `undefined` outside a Dex tool Step.
 */
export function getDeferredToolExecution(): DeferredToolExecution | undefined {
  return runningDeferredToolExecutionScope.getStore();
}
