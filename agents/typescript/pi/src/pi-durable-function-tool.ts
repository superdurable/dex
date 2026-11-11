// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ToolExecutionError, type ToolEffect } from "@superdurable/dex-agents";
import type { FunctionTool, FunctionToolResult, ToolCallContext } from "@superdurable/dex-agents/builtin-loop";

import {
  buildPiFunctionTool,
  describePiToolContent,
  throwPiToolArgumentsFailure,
  throwPiToolExecutionFailure,
  toPiFunctionToolResult,
  validatePiToolArguments,
  type PiToolContentPart,
} from "./pi-tool-content.js";

/**
 * The result of a `pi-durable` tool, as `piDurableFunctionTool` reads it. A `ToolExecutionResult` satisfies it.
 *
 * @since 0.5.0
 */
export interface PiDurableToolResultLike {
  /**
   * Content for the model. Text parts reach the model. With a media store on the session Flow, each image reaches
   * the model after the text; otherwise it becomes a note naming its type.
   */
  readonly output?: readonly PiToolContentPart[];
  /** Whether the result reports a failure. `true` records the call as failed and shows the output to the model. */
  readonly isError?: boolean;
  /** Loop controls. Dex does not support them: a result with `control` fails the call. */
  readonly control?: unknown;
}

/**
 * The fields of a `pi-durable` tool, made with its `defineTool`, that `piDurableFunctionTool` uses. This
 * structural type keeps `pi-durable` out of this package's dependencies.
 *
 * @since 0.5.0
 */
export interface PiDurableToolLike {
  /** Tool name as the model calls it: 1 to 64 letters, digits, `_`, or `-`. */
  readonly name: string;
  /** Description for the model. */
  readonly description: string;
  /** JSON Schema of the arguments, an object schema. */
  readonly parameters: object;
  /** Whether an interrupted call may run again: `safe` means yes; `unsafe`, the default, means no. */
  readonly replay?: "safe" | "unsafe";
  /** `pi-durable`'s execution mode. Dex always runs the calls of one model response in parallel. */
  readonly executionMode?: "sequential" | "parallel";
  /**
   * Repairs raw arguments, such as an argument shape an earlier version of the tool accepted.
   *
   * The built-in agent loop runner calls it with the decoded arguments before it checks them against
   * `parameters`. It must be synchronous and deterministic and have no side effects, because every attempt
   * converts the stored arguments again. A throw or a Promise fails the call: the model sees
   * `invalid arguments for <tool>: <message>`, and `execute` does not run.
   */
  prepareArguments?(args: unknown): unknown;
  /**
   * Runs the tool, as `pi-durable` does.
   * @param args - The validated arguments.
   * @param api - `pi-durable`'s Harness operations. On Dex every one throws.
   * @param context - A context that carries the attempt's cancellation signal as `abortSignal`.
   * @returns The result.
   */
  execute(args: any, api: any, context: any): Promise<PiDurableToolResultLike>;
}

/**
 * Options of {@link piDurableFunctionTool}.
 *
 * @since 0.5.0
 */
export interface PiDurableFunctionToolOptions {
  /**
   * Side-effect class of the tool, which picks its tool Step and default approval. Omitted, `replay: "safe"` gives
   * `idempotent_write`, and `replay: "unsafe"` or no `replay` gives `non_idempotent_write`. A passed effect wins.
   */
  readonly effect?: ToolEffect;
}

/**
 * Wraps a `pi-durable` tool made with `defineTool` as a built-in loop {@link FunctionTool}, for tools that do not use
 * the Harness `api`.
 *
 * Use it to move simple tools from `pi-durable` to Dex unchanged. The effect comes from `options.effect` or from
 * the tool's `replay`: `safe` runs as `idempotent_write`, which `CallTool` retries, and anything else runs as
 * `non_idempotent_write`, which runs at most once and waits for approval by default.
 *
 * Each call runs `tool.execute(args, api, context)`. `prepareArguments`, when the tool has one, repairs the raw
 * arguments before the built-in agent loop runner checks them against `parameters`; pi-ai's validation then
 * coerces them, as in `pi-durable`. `context.abortSignal` is the attempt's cancellation signal; the context has no
 * values and a no-op `telemetry`. The result's text parts, joined by newlines, are the output the model sees. With a
 * media store on the session Flow, each image part is uploaded to it during the call, and the model sees the images
 * after the text, in content order; the session stores only their references. An image the store refuses, such as
 * an SVG, and every image without a store, become `[image omitted: <mime type>]` in the text instead. Only the
 * first `maxImagesPerMessage` (20) images are uploaded; each later one becomes a note, and the text ends with
 * `(more than 20 images; the rest were omitted)`. A failed upload fails the attempt: a `read` or `idempotent_write` call retries, and a single-attempt call records
 * `outcome_unknown`. The text of an `isError` result always shows images as notes. A result with `isError: true`, a
 * thrown `ToolExecutionError`, or arguments that fail conversion or validation become a `ToolExecutionError`, so the model
 * sees the failure. Any other thrown error is handled by the call's effect, which is the effect after
 * `AgentDefinition.toolPolicies` when a policy overrides the wrapped one. For `read` and `idempotent_write` it
 * becomes a `ToolExecutionError` too. For `non_idempotent_write` and `unclassified` the tool may have changed
 * state, so the error propagates and the session records `outcome_unknown` without running the call again. A
 * context without an effect, such as one built by hand in a test, is handled like `unclassified`.
 *
 * Dex does not support the Harness parts of a `pi-durable` tool:
 * - Reading any property of `api`, such as `api.commit` or `api.executeTool`, throws an error that names it, which
 *   the rule above handles like any thrown error. A tool that uses `api` must be rewritten as a Pi `AgentTool` for `piAgentFunctionTool`, or as a Dex
 *   `functionTool`.
 * - A result with `control` fails the call with a `ToolExecutionError`. The tool's effect has already happened by
 *   then.
 * - `executionMode: "sequential"` throws here, because Dex runs the calls of one model response in parallel.
 *
 * @example
 * ```ts
 * import { defineTool } from "@earendil-works/pi-durable";
 * import { piDurableFunctionTool } from "@superdurable/dex-agents-pi";
 *
 * const getWeather = defineTool({
 *   name: "get_weather",
 *   description: "Get the weather for a city.",
 *   parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
 *   replay: "safe",
 *   execute: async ({ city }) => ({ output: [{ type: "text", text: await weather.forecast(city) }] }),
 * });
 * const tools = [piDurableFunctionTool(getWeather, { effect: "read" })];
 * ```
 * @param tool - A `pi-durable` tool.
 * @param options - The effect.
 * @returns The function tool, to pass in `AgentDefinition.tools`.
 * @throws {TypeError} When `executionMode` is `"sequential"`, or when the name, description, or schema is invalid.
 * @since 0.5.0
 */
export function piDurableFunctionTool(tool: PiDurableToolLike, options: PiDurableFunctionToolOptions = {}): FunctionTool {
  if (typeof tool?.execute !== "function") {
    throw new TypeError(`pi-durable tool ${String(tool?.name)} needs an execute function`);
  }
  if (tool.executionMode === "sequential") {
    throw new TypeError(
      `pi-durable tool ${tool.name} has executionMode "sequential", but Dex runs the tool calls of one model response in parallel`,
    );
  }
  const effect = options.effect ?? (tool.replay === "safe" ? "idempotent_write" : "non_idempotent_write");
  return buildPiFunctionTool({
    name: tool.name,
    description: tool.description,
    parameters: tool.parameters,
    effect,
    requiresApproval: undefined,
    prepareArguments: tool.prepareArguments?.bind(tool),
    execute: (args, context) => executePiDurableTool(tool, args, context),
  });
}

async function executePiDurableTool(
  tool: PiDurableToolLike,
  args: unknown,
  context: ToolCallContext,
): Promise<string | FunctionToolResult> {
  let validatedArguments: unknown;
  try {
    validatedArguments = validatePiToolArguments(tool, args, context.toolCallId);
  } catch (error) {
    throwPiToolArgumentsFailure(context, error);
  }
  let result: PiDurableToolResultLike;
  try {
    result = await tool.execute(validatedArguments, createUnsupportedHarnessApi(tool.name), createToolContext(context));
  } catch (error) {
    throwPiToolExecutionFailure(context, error);
  }
  if (result?.control !== undefined) {
    throw new ToolExecutionError(
      `pi-durable tool ${tool.name} returned control ${JSON.stringify(result.control)}, which is not supported on Dex`,
    );
  }
  if (result?.isError === true) {
    const output = describePiToolContent(result.output);
    throw new ToolExecutionError(output === "" ? `${tool.name} failed` : output);
  }
  return await toPiFunctionToolResult(context, result?.output);
}

/** An `api` whose every operation throws, naming it; awaiting or logging the object itself still works. */
function createUnsupportedHarnessApi(toolName: string): object {
  return new Proxy(
    {},
    {
      get(_target, property) {
        if (typeof property === "symbol" || property === "then" || property === "toJSON") {
          return undefined;
        }
        throw new Error(
          `pi-durable tool ${toolName} used api.${property}, which is not supported on Dex; ` +
            "rewrite the tool as a Pi AgentTool or a Dex functionTool",
        );
      },
    },
  );
}

/** A structural pi-durable context: the attempt's abort signal, no values, and no-op telemetry. */
function createToolContext(context: ToolCallContext): object {
  return Object.freeze({
    abortSignal: context.cancellationSignal,
    telemetry: noopTelemetrySpan,
    value: () => undefined,
    toString: () => `[Context dex tool call ${context.toolCallId}]`,
  });
}

const noopTelemetrySpan: object = Object.freeze({
  startSpan: async <T>(_options: unknown, callback: (span: unknown) => T | Promise<T>): Promise<T> =>
    await callback(noopTelemetrySpan),
  addLink: () => undefined,
  addEvent: () => undefined,
  setAttributes: () => undefined,
  setStatus: () => undefined,
});
