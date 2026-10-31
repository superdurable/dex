// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { jsonSchema, tool, type StopCondition, type ToolSet } from "ai";
import { z } from "zod";

import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

import {
  ToolExecutionError,
  createVercelAiAgentSessionFlow,
  getAgentToolExecutionScope,
  type AgentSessionFlow,
} from "../../src/index.js";
import { createScriptedLanguageModel } from "./scripted-language-model.js";

/** Flow type of the scripted AI SDK agent. */
export const scriptedVercelAiAgentFlowType = "ScriptedVercelAiAgent";
/** Environment variable naming the directory where the scripted tools and model write marker files. */
export const scriptedAgentMarkerDirectoryVariable = "SCRIPTED_AGENT_MARKER_DIRECTORY";

/** Options of {@link createScriptedVercelAiAgentFlow}. */
export interface ScriptedVercelAiAgentFlowOptions {
  readonly flowType?: string;
  readonly stopWhen?: StopCondition<ToolSet>;
}

interface ScriptedToolInput {
  readonly requested_by: string;
}

const scriptedToolInputSchema = jsonSchema<ScriptedToolInput>({
  type: "object",
  properties: { requested_by: { type: "string" } },
  required: ["requested_by"],
});

/**
 * Creates the scripted AI SDK agent Flow; tests and the Worker child build it identically.
 *
 * Every real tool run records `<flowId> <toolCallId> attempt <n>` in the `<tool>.log` InvocationLog
 * and `<flowId> <tool>` in `tool-runs.log`.
 */
export function createScriptedVercelAiAgentFlow(
  markerDirectory: string,
  flowOptions: ScriptedVercelAiAgentFlowOptions = {},
): AgentSessionFlow {
  const recordToolRun = (toolName: string, toolCallId: string): void => {
    const scope = getAgentToolExecutionScope();
    if (scope === undefined || scope.idempotencyKey === null) {
      throw new Error(`${toolName} ran outside a tool Step`);
    }
    new InvocationLog(join(markerDirectory, `${toolName}.log`)).record(
      `${scope.flowId} ${toolCallId} attempt ${scope.attempt}`,
    );
    new InvocationLog(join(markerDirectory, "tool-runs.log")).record(`${scope.flowId} ${toolName}`);
  };
  const recordInputCallback = (): void => {
    new InvocationLog(join(markerDirectory, "input-callbacks.log")).record("tool input callback ran");
  };
  const waitAtLookupGate = async (abortSignal: AbortSignal | undefined): Promise<void> => {
    const gate = new FileGate(markerDirectory, "lookups");
    gate.markReached();
    await gate.waitForRelease(abortSignal);
  };
  const circularReport: Record<string, unknown> = { name: "graph" };
  circularReport["self"] = circularReport;
  return createVercelAiAgentSessionFlow(
    {
      model: createScriptedLanguageModel(markerDirectory),
      instructions: "You are a scripted test agent.",
      tools: {
        get_weather: tool({
          description: "Read the weather",
          inputSchema: z.object({ requested_by: z.string() }),
          onInputStart: recordInputCallback,
          onInputAvailable: recordInputCallback,
          execute: async (_input, options) => {
            recordToolRun("get_weather", options.toolCallId);
            return { forecast: "sunny", city: "Paris" };
          },
          toModelOutput: ({ output }) => ({ type: "text", value: `${output.forecast} in ${output.city}` }),
        }),
        lookup_a: tool({
          description: "Look up record A",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("lookup_a", options.toolCallId);
            await waitAtLookupGate(options.abortSignal);
            return "lookup_a found";
          },
        }),
        lookup_b: tool({
          description: "Look up record B",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("lookup_b", options.toolCallId);
            await waitAtLookupGate(options.abortSignal);
            return "lookup_b found";
          },
        }),
        lookup_inventory: tool({
          description: "Read stock levels from a trusted MCP server",
          inputSchema: scriptedToolInputSchema,
          metadata: { annotations: { readOnlyHint: true } },
          execute: async (_input, options) => {
            recordToolRun("lookup_inventory", options.toolCallId);
            return { in_stock: 3 };
          },
        }),
        count_records: tool({
          description: "Count records exactly",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("count_records", options.toolCallId);
            return { total: 12345678901234567890n } as unknown as { total: string };
          },
        }),
        describe_graph: tool({
          description: "Describe a self-referencing graph",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("describe_graph", options.toolCallId);
            return circularReport;
          },
        }),
        send_email: tool({
          description: "Send an email",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("send_email", options.toolCallId);
            return { delivered: true, idempotency_key: getAgentToolExecutionScope()?.idempotencyKey ?? null };
          },
        }),
        charge_card: tool({
          description: "Charge a card",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("charge_card", options.toolCallId);
            const gate = new FileGate(markerDirectory, "charge_card");
            gate.markReached();
            await gate.waitForRelease(options.abortSignal);
            return "card charged";
          },
        }),
        refund_order: tool({
          description: "Refund an order",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options) => {
            recordToolRun("refund_order", options.toolCallId);
            return "refund issued";
          },
        }),
        sync_inventory: tool({
          description: "Push stock levels to an inventory service",
          inputSchema: scriptedToolInputSchema,
          execute: async (_input, options): Promise<string> => {
            recordToolRun("sync_inventory", options.toolCallId);
            throw new Error("inventory service unavailable");
          },
        }),
        delete_account: tool({
          description: "Delete an account",
          inputSchema: scriptedToolInputSchema,
          needsApproval: true,
          execute: async (_input, options): Promise<string> => {
            recordToolRun("delete_account", options.toolCallId);
            throw new ToolExecutionError("account deletion is disabled");
          },
        }),
      },
      toolPolicies: {
        get_weather: { effect: "read" },
        lookup_a: { effect: "read" },
        lookup_b: { effect: "read" },
        count_records: { effect: "read" },
        describe_graph: { effect: "read" },
        charge_card: { effect: "non_idempotent_write" },
        refund_order: { effect: "idempotent_write" },
        sync_inventory: { effect: "idempotent_write" },
        delete_account: { effect: "idempotent_write" },
      },
      shouldTrustMcpToolAnnotations: true,
      ...(flowOptions.stopWhen === undefined ? {} : { stopWhen: flowOptions.stopWhen }),
    },
    flowOptions.flowType ?? scriptedVercelAiAgentFlowType,
  );
}
