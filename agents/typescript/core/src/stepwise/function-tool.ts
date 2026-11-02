// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentEventWriter, ToolInputRequested } from "../agent-framework-runner.js";
import type { QuestionAnswer, ToolEffect } from "../agent-session-values.js";

const toolNamePattern = /^[A-Za-z0-9_-]{1,64}$/;
const toolEffects: readonly ToolEffect[] = ["read", "idempotent_write", "non_idempotent_write", "unclassified"];

/**
 * What a function tool receives besides its arguments, for one run of one tool call.
 *
 * Pass {@link idempotencyKey} to services that deduplicate requests: it is the same on every attempt
 * of the call, so an `idempotent_write` retried by `CallTool` is applied once.
 */
export interface ToolCallContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn that requested the call. */
  readonly turnId: string;
  /** Model tool call ID. */
  readonly toolCallId: string;
  /** Stable key `<flowId>/<turnId>/<toolCallId>`, identical on every attempt of the call. */
  readonly idempotencyKey: string;
  /** One-based attempt of the tool Step; `read` and `idempotent_write` calls get up to three. */
  readonly attempt: number;
  /** Events writer bound to the turn and attempt, for progress text. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt; pass it to network calls. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined`. */
  readonly deadline: Date | undefined;
  /**
   * Answers to the questions this call asked by returning {@link ToolInputRequested}, oldest first;
   * empty on the first run.
   */
  readonly inputAnswers: readonly QuestionAnswer[];
  /** `continuation` of the call's last {@link ToolInputRequested}, or `null`. */
  readonly inputContinuation: string | null;
  /**
   * Records a heartbeat. The session already records one every 20 seconds while the tool runs.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/** Definition passed to {@link functionTool}. */
export interface FunctionToolDefinition<Arguments> {
  /** Tool name as the model calls it: 1 to 64 letters, digits, `_`, or `-`, unique within the agent. */
  readonly name: string;
  /** Description for the model; say what the tool does and when to use it. */
  readonly description: string;
  /**
   * JSON Schema of the arguments, with `type` `"object"`. The runner checks the model's arguments
   * against it before {@link execute} runs, as the Python core does: `type`, `enum`, `anyOf`,
   * `properties`, `required`, `additionalProperties`, `items`, `minimum`, and `maximum` are checked, and
   * other keywords are only shown to the model.
   */
  readonly parameters: Readonly<Record<string, unknown>>;
  /** Side-effect class; it picks `CallTool` or `CallSingleAttemptTool` and the default approval. */
  readonly effect: ToolEffect;
  /**
   * Whether every call waits for `resolve_interrupt` before it runs. Defaults to `true` for
   * `non_idempotent_write` and `unclassified` and to `false` otherwise. A tool policy in
   * {@link AgentDefinition.toolPolicies} overrides it.
   */
  readonly requiresApproval?: boolean;
  /**
   * Runs the tool once, inside its own tool Step.
   *
   * A `string` result is the output the model sees. A {@link ToolInputRequested} result asks the user a
   * question; return it only before the tool took any effect, and the tool runs again with the answer in
   * {@link ToolCallContext.inputAnswers}. Any other result is serialized as JSON, with the separators
   * of Python's `json.dumps`. Throw `ToolExecutionError` for a failure the model should see; any other
   * error fails the attempt, as {@link ToolExecutionError} describes.
   * @param args - Arguments that passed the {@link parameters} check.
   * @param context - Idempotency key, attempt, events, and heartbeat of this run.
   * @returns The tool result.
   */
  readonly execute: (args: Arguments, context: ToolCallContext) => unknown;
}

/** A tool implemented by a plain function, created by {@link functionTool}. */
export interface FunctionTool {
  /** Discriminator. */
  readonly kind: "function_tool";
  /** Tool name. */
  readonly name: string;
  /** Description for the model. */
  readonly description: string;
  /** JSON Schema of the arguments. */
  readonly parameters: Readonly<Record<string, unknown>>;
  /** Side-effect class. */
  readonly effect: ToolEffect;
  /** Approval override, or `undefined` for the effect's default. */
  readonly requiresApproval: boolean | undefined;
  /**
   * Runs the tool with checked arguments.
   * @param args - Arguments that passed the schema check.
   * @param context - Context of this run.
   * @returns A Promise of the tool result.
   */
  execute(args: unknown, context: ToolCallContext): Promise<unknown>;
}

/**
 * Creates a function tool for a Stepwise agent.
 *
 * Every function tool declares its effect, which picks the tool Step and the default approval exactly
 * as for adapter tools. Each call runs in its own Step, so a crash repeats at most a `read` or
 * `idempotent_write` call and never repeats a `non_idempotent_write` call.
 *
 * @example
 * ```ts
 * const lookupOrder = functionTool<{ order_id: string }>({
 *   name: "lookup_order",
 *   description: "Look up an order by its ID.",
 *   parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
 *   effect: "read",
 *   execute: async ({ order_id }) => orders.get(order_id),
 * });
 * const refundOrder = functionTool<{ order_id: string; amount_cents: number }>({
 *   name: "refund_order",
 *   description: "Refund part of an order.",
 *   parameters: {
 *     type: "object",
 *     properties: { order_id: { type: "string" }, amount_cents: { type: "integer", minimum: 1 } },
 *     required: ["order_id", "amount_cents"],
 *   },
 *   effect: "non_idempotent_write",
 *   execute: async ({ order_id, amount_cents }, context) =>
 *     payments.refund(order_id, amount_cents, { idempotencyKey: context.idempotencyKey }),
 * });
 * ```
 * @typeParam Arguments - Type of the checked arguments; it is not checked against the schema.
 * @param definition - Name, description, schema, effect, approval, and implementation.
 * @returns The tool, to pass in {@link AgentDefinition.tools}.
 * @throws {TypeError} When the name, description, schema, effect, or implementation is invalid.
 */
export function functionTool<Arguments = Record<string, unknown>>(
  definition: FunctionToolDefinition<Arguments>,
): FunctionTool {
  if (typeof definition?.name !== "string" || !toolNamePattern.test(definition.name)) {
    throw new TypeError(`function tool name ${JSON.stringify(definition?.name)} must be 1 to 64 letters, digits, _ or -`);
  }
  if (typeof definition.description !== "string" || definition.description.trim() === "") {
    throw new TypeError(`function tool ${definition.name} needs a non-blank description`);
  }
  const parameters = definition.parameters as Readonly<Record<string, unknown>> | undefined;
  if (typeof parameters !== "object" || parameters === null || parameters["type"] !== "object") {
    throw new TypeError(`function tool ${definition.name} needs a JSON Schema object with type "object"`);
  }
  if (!toolEffects.includes(definition.effect)) {
    throw new TypeError(`function tool ${definition.name} has unknown effect ${String(definition.effect)}`);
  }
  if (definition.requiresApproval !== undefined && typeof definition.requiresApproval !== "boolean") {
    throw new TypeError(`function tool ${definition.name} requiresApproval must be a boolean`);
  }
  if (typeof definition.execute !== "function") {
    throw new TypeError(`function tool ${definition.name} needs an execute function`);
  }
  const execute = definition.execute;
  return Object.freeze({
    kind: "function_tool",
    name: definition.name,
    description: definition.description,
    parameters,
    effect: definition.effect,
    requiresApproval: definition.requiresApproval,
    execute: async (args: unknown, context: ToolCallContext) => await execute(args as Arguments, context),
  });
}
