// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ToolsInput } from "@mastra/core/agent";
import { RequestContext } from "@mastra/core/request-context";
import { toStandardSchema } from "@mastra/core/schema";
import { MASTRA_TOOL_MARKER, isValidationError, validateToolInput } from "@mastra/core/tools";
import {
  ToolExecutionError,
  type DeferredToolExecution,
  type ToolExecutionResult,
} from "@superdurable/dex-agents";

import { formatMastraToolOutputText } from "./mastra-message-codec.js";

type ToolExecuteFunction = (input: unknown, context: Readonly<Record<string, unknown>>) => unknown;

/** Start of the message of Mastra's request context check, the only one that runs before the tool. */
const mastraRequestContextValidationFailurePrefix = "Request context validation failed";

/**
 * `RequestContext` key under which an agent tool finds the call's idempotency key.
 *
 * The session passes `<flowId>/<turnId>/<toolCallId>`, identical on every attempt of the call. Read it in an
 * `idempotent_write` tool and send it to a service that deduplicates requests, so a retry after a Worker
 * crash takes effect once. Tools that Mastra runs itself inside `RunModel`, such as an allowed sub-agent
 * tool, do not receive it.
 *
 * @example
 * ```ts
 * import { idempotencyKeyRequestContextKey } from "@superdurable/dex-agents-mastra";
 *
 * const refundOrder = createTool({
 *   id: "refund_order",
 *   description: "Refund one order",
 *   inputSchema: z.object({ orderId: z.string() }),
 *   execute: async ({ orderId }, context) => {
 *     const idempotencyKey = String(context.requestContext.get(idempotencyKeyRequestContextKey));
 *     return paymentsApi.refund(orderId, { idempotencyKey });
 *   },
 * });
 * ```
 */
export const idempotencyKeyRequestContextKey = "dex.idempotencyKey";

export async function executeMastraDeferredToolCall(
  agentTools: ToolsInput,
  agentId: string,
  call: DeferredToolExecution,
  requestContext: RequestContext,
): Promise<ToolExecutionResult> {
  const { toolName, toolCallId } = call.toolCall;
  const tool: unknown = Object.hasOwn(agentTools, toolName) ? agentTools[toolName] : undefined;
  const execute = readToolExecuteFunction(tool);
  if (execute === undefined) {
    throw new ToolExecutionError(`tool ${toolName} is not a tool with an execute function on Mastra agent ${agentId}`);
  }
  const rawInput: unknown = JSON.parse(call.toolCall.arguments);
  const inputValidation = validateToolInput(readToolInputSchema(tool), rawInput, toolName);
  if (inputValidation.error !== undefined) {
    throw new ToolExecutionError(inputValidation.error.message);
  }
  // A Mastra tool validates and transforms its input itself; transformed data would fail that second pass.
  const isMastraTool = typeof tool === "object" && tool !== null && MASTRA_TOOL_MARKER in tool;
  // A copy keeps the key out of a RequestContext that the application's factory may share.
  const toolRequestContextEntries: Array<readonly [string, unknown]> = [
    ...requestContext.entries(),
    [idempotencyKeyRequestContextKey, call.idempotencyKey],
  ];
  const toolRequestContext = new RequestContext<unknown>(toolRequestContextEntries);
  // The context satisfies both Mastra tools, which read agent fields, and AI SDK tools.
  const result = await execute.call(tool, isMastraTool ? rawInput : inputValidation.data, {
    toolCallId,
    messages: [],
    agentId,
    abortSignal: call.cancellationSignal,
    requestContext: toolRequestContext,
    suspend: async () => {
      throw new ToolExecutionError(`tool ${toolName} cannot suspend: the Dex session already ran its approval`);
    },
  });
  // Only a Mastra tool's wrapper returns ValidationError; an AI SDK tool's result is just its value.
  if (isMastraTool && isValidationError(result)) {
    // Mastra checks the request context before the tool runs, so that failure is safe to report.
    if (result.message.startsWith(mastraRequestContextValidationFailurePrefix)) {
      throw new ToolExecutionError(result.message);
    }
    throw new Error(`tool ${toolName} returned a validation error after it may have run: ${result.message}`);
  }
  return { output: formatMastraToolOutputText(result) };
}

// Mastra tools hold a converted inputSchema; AI SDK tools hold inputSchema, or parameters before v5.
function readToolInputSchema(tool: unknown): ReturnType<typeof toStandardSchema> | undefined {
  const rawSchema: unknown =
    (tool as { readonly inputSchema?: unknown }).inputSchema ?? (tool as { readonly parameters?: unknown }).parameters;
  return rawSchema === undefined || rawSchema === null
    ? undefined
    : toStandardSchema(rawSchema as Parameters<typeof toStandardSchema>[0]);
}

function readToolExecuteFunction(tool: unknown): ToolExecuteFunction | undefined {
  if (typeof tool !== "object" || tool === null) {
    return undefined;
  }
  const execute: unknown = (tool as { readonly execute?: unknown }).execute;
  return typeof execute === "function" ? (execute as ToolExecuteFunction) : undefined;
}
