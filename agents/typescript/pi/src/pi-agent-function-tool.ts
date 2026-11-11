// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ToolExecutionError, type ToolEffect } from "@superdurable/dex-agents";
import type { FunctionTool, ToolCallContext } from "@superdurable/dex-agents/builtin-loop";

import {
  buildPiFunctionTool,
  describePiToolContent,
  throwPiToolArgumentsFailure,
  throwPiToolExecutionFailure,
  validatePiToolArguments,
  writePiToolProgress,
  type PiToolContentPart,
} from "./pi-tool-content.js";

/**
 * The result a Pi tool returns or reports as progress, as `piAgentFunctionTool` reads it. A Pi
 * `AgentToolResult` satisfies it.
 *
 * @since 0.5.0
 */
export interface PiAgentToolResultLike {
  /** Text and image content for the model. Text parts reach the model; each image becomes a note naming its type. */
  readonly content: readonly PiToolContentPart[];
  /** Structured data for a UI. Dex does not store it. */
  readonly details?: unknown;
  /** Whether the result reports a failure. `true` records the call as failed and shows the content to the model. */
  readonly isError?: boolean;
  /** Pi's hint to stop after the tool batch. Dex ignores it; see `piAgentFunctionTool`. */
  readonly terminate?: boolean;
}

/**
 * The fields of a Pi `AgentTool` that `piAgentFunctionTool` uses. Any `AgentTool` from
 * `@earendil-works/pi-agent-core` satisfies this structural type, so this package does not depend on it.
 *
 * @since 0.5.0
 */
export interface PiAgentToolLike {
  /** Tool name as the model calls it: 1 to 64 letters, digits, `_`, or `-`. */
  readonly name: string;
  /** Description for the model. */
  readonly description: string;
  /** JSON Schema of the arguments, an object schema, such as a TypeBox or pi-schema schema. */
  readonly parameters: object;
  /**
   * Pi's declaration of whether an interrupted call may run again: `safe` means yes and `never` means no.
   * `piAgentFunctionTool` infers the Dex effect from it when no effect is passed.
   */
  readonly replay?: "never" | "safe";
  /** Pi's per-tool execution mode. Dex always runs the calls of one model response in parallel. */
  readonly executionMode?: "sequential" | "parallel";
  /**
   * Pi's shim that repairs raw arguments, such as an argument shape an earlier version of the tool accepted.
   *
   * The built-in agent loop runner calls it with the decoded arguments before it checks them against
   * `parameters`, so a repaired old shape passes that check. It must be synchronous and deterministic and have
   * no side effects, because every attempt converts the stored arguments again. A throw or a Promise fails the
   * call: the model sees `invalid arguments for <tool>: <message>`, and `execute` does not run.
   */
  readonly prepareArguments?: (args: unknown) => unknown;
  /**
   * Runs the tool, as Pi's `AgentTool.execute`.
   * @param toolCallId - The model's tool call ID.
   * @param params - The validated arguments.
   * @param signal - Aborted when Dex cancels the attempt.
   * @param onUpdate - Receives partial results; their text becomes progress events.
   * @returns The result.
   */
  execute(
    toolCallId: string,
    params: any,
    signal?: AbortSignal,
    onUpdate?: (partialResult: PiAgentToolResultLike) => void,
  ): Promise<PiAgentToolResultLike>;
}

/**
 * Options of {@link piAgentFunctionTool}.
 *
 * @since 0.5.0
 */
export interface PiAgentFunctionToolOptions {
  /**
   * Side-effect class of the tool, which picks its tool Step and default approval. Required when the tool
   * declares no `replay`. Omitted, `replay: "safe"` gives `idempotent_write` and `replay: "never"` gives
   * `non_idempotent_write`. A passed effect wins over `replay`.
   */
  readonly effect?: ToolEffect;
  /**
   * Whether every call waits for `resolve_interrupt` before it runs. Defaults to the effect's default: `true` for
   * `non_idempotent_write` and `unclassified`, `false` otherwise. A tool policy in `AgentDefinition.toolPolicies`
   * overrides it.
   */
  readonly requiresApproval?: boolean;
}

/**
 * Wraps a Pi `AgentTool` as a built-in loop {@link FunctionTool}, so each of its calls runs in its own Dex tool Step.
 *
 * Every Dex tool declares its side-effect class. Pass `effect`, or declare `replay` on the tool:
 * `replay: "safe"` runs as `idempotent_write`, which `CallTool` retries, and `replay: "never"` runs as
 * `non_idempotent_write`, which runs at most once and waits for approval by default. A tool with neither throws
 * a `TypeError` here, so a Worker fails while it builds the agent definition, not when the model first calls the
 * tool.
 *
 * Each call runs `tool.execute(toolCallId, params, signal, onUpdate)`:
 * - `prepareArguments`, when the tool has one, repairs the raw arguments first. The built-in agent loop runner
 *   then checks them against `parameters`, and pi-ai's `validateToolArguments` validates and coerces them, as Pi
 *   does.
 * - `signal` is the attempt's cancellation signal.
 * - The text of each `onUpdate` partial result is written as a `pi.tool_progress` event with `tool_call_id`,
 *   `tool_name`, and `text`.
 * - The result's text parts, joined by newlines, are the output the model sees. Each image part becomes
 *   `[image omitted: <mime type>]`. `details` is not stored.
 * - A result with `isError: true`, a thrown `ToolExecutionError`, or arguments that fail conversion or validation
 *   become a `ToolExecutionError`: the call is recorded as failed, the model sees the failure, and the session
 *   continues.
 * - Any other thrown error is handled by the call's effect, which is the effect after `AgentDefinition.toolPolicies`
 *   when a policy overrides the wrapped one. For `read` and `idempotent_write` it becomes a `ToolExecutionError`
 *   too, as in Pi. For `non_idempotent_write` and `unclassified` the tool may have changed state, so the error
 *   propagates and the session records `outcome_unknown`, as for any `CallSingleAttemptTool`; the call never runs
 *   again. A context without an effect, such as one built by hand in a test, is handled like `unclassified`. To
 *   report a definite failure from such a tool, return `isError: true` or throw `ToolExecutionError`.
 * - An error thrown after Dex cancelled the attempt is rethrown unchanged.
 *
 * Dex runs the calls of one model response in parallel, so `executionMode: "sequential"` is not supported. Dex
 * ignores the result's `terminate` hint.
 *
 * Pass the idempotency key yourself when a tool writes: Pi tools do not receive Dex's `ToolCallContext`, so wrap
 * such a tool with `functionTool` instead when it needs `idempotencyKey`.
 *
 * @example
 * ```ts
 * import type { AgentTool } from "@earendil-works/pi-agent-core";
 * import { obj, str } from "@earendil-works/pi-schema";
 * import { piAgentFunctionTool } from "@superdurable/dex-agents-pi";
 *
 * const orderArguments = obj({ orderId: str() });
 * const lookupOrder: AgentTool<typeof orderArguments> = {
 *   name: "lookup_order",
 *   label: "Look up order",
 *   description: "Look up an order by its ID.",
 *   parameters: orderArguments,
 *   replay: "safe",
 *   execute: async (_toolCallId, { orderId }) => ({
 *     content: [{ type: "text", text: JSON.stringify(await orders.get(orderId)) }],
 *     details: undefined,
 *   }),
 * };
 * const tools = [
 *   piAgentFunctionTool(lookupOrder, { effect: "read" }),
 *   piAgentFunctionTool(sendEmail, { effect: "non_idempotent_write" }),
 * ];
 * ```
 * @param tool - A Pi `AgentTool`.
 * @param options - Effect and approval.
 * @returns The function tool, to pass in `AgentDefinition.tools`.
 * @throws {TypeError} When neither `options.effect` nor `tool.replay` gives an effect, when `executionMode` is
 * `"sequential"`, or when the name, description, or schema is invalid.
 * @since 0.5.0
 */
export function piAgentFunctionTool(tool: PiAgentToolLike, options: PiAgentFunctionToolOptions = {}): FunctionTool {
  if (typeof tool?.execute !== "function") {
    throw new TypeError(`Pi tool ${String(tool?.name)} needs an execute function`);
  }
  if (tool.executionMode === "sequential") {
    throw new TypeError(
      `Pi tool ${tool.name} has executionMode "sequential", but Dex runs the tool calls of one model response in parallel`,
    );
  }
  const effect = inferPiAgentToolEffect(tool, options.effect);
  return buildPiFunctionTool({
    name: tool.name,
    description: tool.description,
    parameters: tool.parameters,
    effect,
    requiresApproval: options.requiresApproval,
    prepareArguments: tool.prepareArguments?.bind(tool),
    execute: (args, context) => executePiAgentTool(tool, args, context),
  });
}

function inferPiAgentToolEffect(tool: PiAgentToolLike, effect: ToolEffect | undefined): ToolEffect {
  if (effect !== undefined) {
    return effect;
  }
  switch (tool.replay) {
    case "safe":
      return "idempotent_write";
    case "never":
      return "non_idempotent_write";
    default:
      throw new TypeError(
        `Pi tool ${tool.name} declares no replay, so pass its effect: piAgentFunctionTool(tool, { effect: "read" })`,
      );
  }
}

async function executePiAgentTool(
  tool: PiAgentToolLike,
  args: unknown,
  context: ToolCallContext,
): Promise<string> {
  let params: unknown;
  try {
    params = validatePiToolArguments(tool, args, context.toolCallId);
  } catch (error) {
    throwPiToolArgumentsFailure(context, error);
  }
  let result: PiAgentToolResultLike;
  try {
    result = await tool.execute(context.toolCallId, params, context.cancellationSignal, (partialResult) =>
      writePiToolProgress(context, tool.name, describePiToolContent(partialResult?.content)),
    );
  } catch (error) {
    throwPiToolExecutionFailure(context, error);
  }
  const output = describePiToolContent(result?.content);
  if (result?.isError === true) {
    throw new ToolExecutionError(output === "" ? `${tool.name} failed` : output);
  }
  return output;
}
