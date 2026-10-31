// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";

import type { AgentEventWriter } from "@superdurable/dex-agents";

/**
 * The Dex session, turn, and Step attempt in which an AI SDK tool's `execute` function, or a model
 * call, runs.
 *
 * Read it with {@link getAgentToolExecutionScope}. Every tool call runs in its own tool Step,
 * `CallTool` or `CallSingleAttemptTool`, so a tool always sees the stable idempotency key that Dex
 * assigned to the call. Inside `RunModel`, where only the model call and its middleware run, the key is
 * `null`.
 *
 * @example
 * ```ts
 * import { tool, jsonSchema } from "ai";
 * import { getAgentToolExecutionScope } from "@superdurable/dex-agents-vercel-ai";
 *
 * const refundOrder = tool({
 *   description: "Refund an order",
 *   inputSchema: jsonSchema<{ orderId: string }>({
 *     type: "object",
 *     properties: { orderId: { type: "string" } },
 *     required: ["orderId"],
 *   }),
 *   execute: async ({ orderId }) => {
 *     const scope = getAgentToolExecutionScope();
 *     return payments.refund(orderId, { idempotencyKey: scope?.idempotencyKey ?? undefined });
 *   },
 * });
 * ```
 */
export interface AgentToolExecutionScope {
  /** Flow ID, which is the agent session ID. */
  readonly flowId: string;
  /** Turn ID, `turn-<n>`, of the turn that made the model call or asked for the tool call. */
  readonly turnId: string;
  /** One-based attempt of the running Step: `RunModel` or the tool Step. */
  readonly attempt: number;
  /**
   * Stable key `<flowId>/<turnId>/<toolCallId>` of the tool call that this tool Step runs, the same on
   * every attempt of the call. The turn ID keeps keys distinct when a provider reuses tool call IDs
   * across turns. It is `null` inside `RunModel`.
   */
  readonly idempotencyKey: string | null;
  /**
   * Events writer of the running Step. Write adapter-specific progress with a prefixed type, such as
   * `"vercel_ai.tool_progress"`. Writes after the Step handler returns are dropped.
   */
  readonly events: AgentEventWriter;
}

const toolExecutionScopeStorage = new AsyncLocalStorage<AgentToolExecutionScope>();

/**
 * Returns the Dex scope of the running tool call or model call, for use inside a tool's `execute` or a
 * model middleware.
 *
 * The scope is carried through asynchronous calls, so it is available anywhere inside `execute`, including
 * in a tool that yields preliminary results. Inside `RunModel` it covers the model call, so middleware
 * added with AI SDK `wrapLanguageModel` can read the session ID and attempt, for example to tag logs.
 * It is `undefined` outside a Dex agent session Step, for example in a unit test that calls `execute`
 * directly. See {@link AgentToolExecutionScope} for an example.
 *
 * @returns The scope of the running Step, or `undefined` outside an agent session Step.
 */
export function getAgentToolExecutionScope(): AgentToolExecutionScope | undefined {
  return toolExecutionScopeStorage.getStore();
}

export function runInAgentToolExecutionScope<Result>(
  scope: AgentToolExecutionScope,
  operation: () => Promise<Result>,
): Promise<Result> {
  return toolExecutionScopeStorage.run(scope, operation);
}
