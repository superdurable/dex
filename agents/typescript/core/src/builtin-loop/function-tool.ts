// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentEventWriter, ToolInputRequested } from "../agent-framework-runner.js";
import type { MediaStore } from "../agent-media.js";
import type { AgentImageReference, QuestionAnswer, ToolEffect } from "../agent-session-values.js";

const toolNamePattern = /^[A-Za-z0-9_-]{1,64}$/;
const toolEffects: readonly ToolEffect[] = ["read", "idempotent_write", "non_idempotent_write", "unclassified"];
// Identity, not fields, marks a result, so a tool's plain JSON with the same keys stays JSON.
const createdFunctionToolResults = new WeakSet<object>();

/**
 * A value that {@link FunctionToolDefinition.prepareArguments} may return: any value except a Promise or another
 * object with a `then` method, because the conversion must be synchronous.
 *
 * Return an object for arguments that can pass the schema check. Any other value fails the call with
 * `expected a JSON object`. An argument named `then` is allowed when its value is JSON, such as a string;
 * only a function there is refused. To return the decoded arguments unchanged, narrow them first, such as
 * with `args as object`.
 * @since 0.5.0
 */
export type PreparedToolArguments =
  | string
  | number
  | boolean
  | null
  | readonly unknown[]
  | { readonly then?: PreparedToolArgumentThenValue; readonly [key: string]: unknown }
  | (object & { readonly then?: PreparedToolArgumentThenValue });

/** JSON values that an argument named `then` may hold; a function there would make the arguments a thenable. */
type PreparedToolArgumentThenValue =
  | string
  | number
  | boolean
  | null
  | readonly unknown[]
  | Readonly<Record<string, unknown>>;

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
  /**
   * Effect of this call after the tool policies are applied.
   *
   * It is the effect stored on the call, which chose `CallTool` or `CallSingleAttemptTool` and the default
   * approval. It can differ from {@link FunctionToolDefinition.effect} when a policy in
   * {@link AgentDefinition.toolPolicies} or a session policy overrides it.
   *
   * The runner always sets it. It is `undefined` only in a context built by hand, such as in a unit test.
   * Treat `undefined` as an unknown effect, and handle the call as a write whose outcome may be unknown.
   *
   * Use it in a wrapper that turns errors into {@link ToolExecutionError}. `non_idempotent_write` and
   * `unclassified` calls run once in `CallSingleAttemptTool`. For them, and for an unknown effect, let an error
   * whose outcome is unknown propagate, so the session records `outcome_unknown` and never repeats the call.
   *
   * @example
   * ```ts
   * execute: async (args, context) => {
   *   try {
   *     return await client.send(args);
   *   } catch (error) {
   *     if (context.effect !== "read" && context.effect !== "idempotent_write") throw error;
   *     throw new ToolExecutionError(String(error));
   *   }
   * },
   * ```
   * @since 0.5.0
   */
  readonly effect?: ToolEffect;
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
   * The store that holds the session's images, the one the runner was built with, or `undefined` when it has none.
   *
   * Upload image bytes with `putImage` and return the references with {@link functionToolResult}. See
   * {@link functionToolResult} for an example.
   * @since 0.5.0
   */
  readonly mediaStore?: MediaStore;
  /**
   * Records a heartbeat. The session already records one every 20 seconds while the tool runs.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/**
 * Text and images that a function tool returns. Create it with {@link functionToolResult}.
 *
 * Only an object that {@link functionToolResult} created is a result with images. Any other object with the same
 * fields, such as a copy or a literal, is serialized as JSON like every other tool result.
 * @since 0.5.0
 */
export interface FunctionToolResult {
  /** Tool output text the model sees. */
  readonly output: string;
  /**
   * Images the model sees after {@link output}, uploaded with `MediaStore.putImage`, at most
   * `maxImagesPerMessage`. Omit it for none.
   */
  readonly images?: readonly AgentImageReference[];
}

/**
 * Creates a function tool result with text and images.
 *
 * Return it from {@link FunctionToolDefinition.execute} to show the model images with the output. Upload each
 * image's bytes with {@link ToolCallContext.mediaStore} inside the call, then return the references. A retried
 * attempt uploads the same bytes to the same object, so uploading again is safe.
 *
 * The runner returns the result as a `ToolExecutionResult` with the same output and images, so the session checks
 * the images exactly as it checks a tool source's images. More than `maxImagesPerMessage` images, or a reference
 * that `validateImageReference` refuses, fails the attempt with `ImageValidationError`. A failed attempt of a
 * `read` or `idempotent_write` tool retries; a failed single-attempt call records `outcome_unknown`. Images from a
 * runner without a media store throw `ToolExecutionError` instead, because no retry could show them: the session
 * records `failed` once and the model sees the error text. A result without images is the same as returning
 * `output` as a string. Return the object it creates: a copy is serialized as JSON.
 *
 * @example
 * ```ts
 * const takeScreenshot = functionTool<Record<string, never>>({
 *   name: "take_screenshot",
 *   description: "Take a screenshot of the screen.",
 *   parameters: { type: "object", properties: {} },
 *   effect: "read",
 *   execute: async (_args, context) => {
 *     if (context.mediaStore === undefined) {
 *       return "Screenshots need a media store.";
 *     }
 *     const reference = await context.mediaStore.putImage(await screen.capturePng());
 *     return functionToolResult({ output: "Took a screenshot.", images: [reference] });
 *   },
 * });
 * ```
 * @param result - The output text and, optionally, the image references.
 * @returns The frozen result, to return from `execute`.
 * @throws {TypeError} When `output` is not a string or `images` is not an array.
 * @since 0.5.0
 */
export function functionToolResult(result: {
  readonly output: string;
  readonly images?: readonly AgentImageReference[];
}): FunctionToolResult {
  if (typeof result?.output !== "string") {
    throw new TypeError("function tool result output must be a string");
  }
  if (result.images !== undefined && !Array.isArray(result.images)) {
    throw new TypeError("function tool result images must be an array");
  }
  const created: FunctionToolResult = Object.freeze({
    output: result.output,
    ...(result.images === undefined ? {} : { images: result.images }),
  });
  createdFunctionToolResults.add(created);
  return created;
}

export function isFunctionToolResult(value: unknown): value is FunctionToolResult {
  return typeof value === "object" && value !== null && createdFunctionToolResults.has(value);
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
   * Converts the model's arguments before the {@link parameters} check, such as an argument shape that an
   * earlier version of the tool accepted.
   *
   * The runner calls it with the arguments decoded from JSON, which may be any JSON value. It checks the
   * returned value against {@link parameters} and passes it to {@link execute}. A throw fails the call like
   * a schema violation: the model sees `invalid arguments for <tool>: <message>`, and {@link execute} does not
   * run. Absent, the decoded arguments are checked unchanged.
   *
   * It must be synchronous and deterministic, and it must have no side effects. Every attempt converts the
   * stored arguments again. A Promise or another object with a `then` method fails the call with
   * `invalid arguments for <tool>: argument conversion must be synchronous`. The stored tool call, the approval
   * interrupt, and the transcript keep the model's original arguments.
   *
   * @example
   * ```ts
   * // Version 1 took { id }; version 2 takes { order_id }.
   * prepareArguments: (args) => {
   *   const record = args as Record<string, unknown>;
   *   return "id" in record && !("order_id" in record) ? { order_id: record["id"] } : record;
   * },
   * ```
   * @param args - The model's arguments, decoded from JSON.
   * @returns The arguments to check against {@link parameters}.
   * @since 0.5.0
   */
  readonly prepareArguments?: (args: unknown) => PreparedToolArguments;
  /**
   * Runs the tool once, inside its own tool Step.
   *
   * A `string` result is the output the model sees. A {@link FunctionToolResult} from {@link functionToolResult}
   * shows the model its output and images. A {@link ToolInputRequested} result asks the user a
   * question; return it only before the tool took any effect, and the tool runs again with the answer in
   * {@link ToolCallContext.inputAnswers}. Any other result is serialized as JSON, with the separators
   * of Python's `json.dumps`. Throw `ToolExecutionError` for a failure the model should see; any other
   * error fails the attempt, as {@link ToolExecutionError} describes.
   *
   * Changed in 0.5.0: a {@link FunctionToolResult} result shows the model its images.
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
   * Converts the model's decoded arguments before the schema check, or `undefined` to check them unchanged.
   * See {@link FunctionToolDefinition.prepareArguments}.
   * @param args - The model's arguments, decoded from JSON.
   * @returns The arguments to check against {@link parameters}.
   * @since 0.5.0
   */
  readonly prepareArguments?: (args: unknown) => PreparedToolArguments;
  /**
   * Runs the tool with checked arguments.
   * @param args - Arguments that passed the schema check.
   * @param context - Context of this run.
   * @returns A Promise of the tool result.
   */
  execute(args: unknown, context: ToolCallContext): Promise<unknown>;
}

/**
 * Creates a function tool for a built-in loop agent.
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
 * @param definition - Name, description, schema, effect, approval, argument conversion, and implementation.
 * @returns The tool, to pass in {@link AgentDefinition.tools}.
 * @throws {TypeError} When the name, description, schema, effect, argument conversion, or implementation is
 * invalid.
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
  if (definition.prepareArguments !== undefined && typeof definition.prepareArguments !== "function") {
    throw new TypeError(`function tool ${definition.name} prepareArguments must be a function`);
  }
  const execute = definition.execute;
  return Object.freeze({
    kind: "function_tool",
    name: definition.name,
    description: definition.description,
    parameters,
    effect: definition.effect,
    requiresApproval: definition.requiresApproval,
    ...(definition.prepareArguments === undefined ? {} : { prepareArguments: definition.prepareArguments }),
    execute: async (args: unknown, context: ToolCallContext) => await execute(args as Arguments, context),
  });
}
