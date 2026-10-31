// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { Agent, type ToolsInput } from "@mastra/core/agent";
import { MASTRA_THREAD_ID_KEY, RequestContext } from "@mastra/core/request-context";
import { createTool } from "@mastra/core/tools";
import {
  ToolExecutionError,
  createAgentSessionFlow,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ToolExecutionResult,
  type ToolPolicy,
} from "@superdurable/dex-agents";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import {
  MastraAgentRunner,
  createMastraAgentSessionFlow,
  idempotencyKeyRequestContextKey,
  type MastraAgentRunnerOptions,
} from "../../src/index.js";
import { ScriptedLanguageModel } from "./scripted-language-model.js";

/** Flow type of the scripted Mastra agent in every test. */
export const scriptedMastraAgentFlowType = "ScriptedMastraAgent";
/** Environment variable naming the directory where scripted tools write marker files. */
export const scriptedMastraMarkerDirectoryVariable = "SCRIPTED_MASTRA_MARKER_DIRECTORY";
/** RequestContext key carrying the session ID, set by `createRequestContext`. */
const sessionIdRequestContextKey = "dexSessionId";

const scriptedToolInputSchema = z.object({ requested_by: z.string() });

interface ScriptedToolContext {
  readonly requestContext?: RequestContext;
  readonly agent?: { readonly toolCallId: string };
  readonly toolCallId?: string;
  readonly abortSignal?: AbortSignal;
}

/** Variants of the scripted agent, each registered under its own Flow type. */
export interface ScriptedMastraAgentFlowOptions {
  readonly flowType?: string;
  readonly interruptDeadlineMs?: number;
  /** Gives `agent-researcher`, the sub-agent tool Mastra adds, an inline `read` policy. */
  readonly isResearcherAllowed?: boolean;
  /** Adds an agent tool named `agent-researcher`, which the sub-agent tool replaces. */
  readonly isResearcherShadowed?: boolean;
  /** Makes `createRequestContext` select a Mastra Memory thread. */
  readonly isMemoryThreadSelected?: boolean;
  /** Moves the `createdAt` of every completed turn's messages this far ahead, like a fast Worker clock. */
  readonly clockSkewMs?: number;
}

/**
 * Creates the scripted Mastra agent's session Flow; tests and the Worker child build it identically.
 *
 * The agent has a `researcher` sub-agent, which Mastra offers as the tool `agent-researcher`. Every real
 * tool run records `<sessionId> <toolCallId>` in `<tool>.log`, and `<sessionId> <tool>` in `tools.log` in
 * run order. `charge_card` then waits at the `charge_card` FileGate, and `lookup_a` and `lookup_b` at
 * the `lookups` FileGate.
 */
export function createScriptedMastraAgentFlow(
  markerDirectory: string,
  flowOptions: ScriptedMastraAgentFlowOptions = {},
): AgentSessionFlow {
  const toolPolicies: Record<string, ToolPolicy> = {
    get_weather: { effect: "read" },
    lookup_a: { effect: "read" },
    lookup_b: { effect: "read" },
    charge_card: { effect: "non_idempotent_write" },
    refund_order: { effect: "idempotent_write" },
    archive_order: { effect: "idempotent_write" },
    sync_inventory: { effect: "idempotent_write" },
    issue_invoice: { effect: "non_idempotent_write", requiresApproval: false },
    convert_credit: { effect: "idempotent_write" },
    set_quota: { effect: "idempotent_write" },
    report_status: { effect: "read" },
    schedule_pickup: { effect: "idempotent_write" },
    audit_export: { effect: "non_idempotent_write", requiresApproval: false },
    ...(flowOptions.isResearcherAllowed === true ? { "agent-researcher": { effect: "read" } } : {}),
  };
  const runnerOptions: MastraAgentRunnerOptions = {
    toolPolicies,
    maxSteps: 8,
    createRequestContext: ({ flowId }) =>
      new RequestContext([
        [sessionIdRequestContextKey, flowId],
        ...(flowOptions.isMemoryThreadSelected === true ? [[MASTRA_THREAD_ID_KEY, flowId] as const] : []),
      ]),
  };
  const agent = createScriptedMastraAgent(markerDirectory, flowOptions);
  const flowType = flowOptions.flowType ?? scriptedMastraAgentFlowType;
  const sessionOptions =
    flowOptions.interruptDeadlineMs === undefined ? {} : { interruptDeadlineMs: flowOptions.interruptDeadlineMs };
  if (flowOptions.clockSkewMs === undefined) {
    return createMastraAgentSessionFlow(agent, flowType, { ...runnerOptions, ...sessionOptions });
  }
  const skewedRunner = new ClockSkewedRunner(new MastraAgentRunner(agent, runnerOptions), flowOptions.clockSkewMs);
  return createAgentSessionFlow(skewedRunner, flowType, { toolPolicies, ...sessionOptions });
}

function createScriptedMastraAgent(markerDirectory: string, flowOptions: ScriptedMastraAgentFlowOptions): Agent {
  const recordRun = (toolName: string, context: ScriptedToolContext): string => {
    const sessionId = String(context.requestContext?.get(sessionIdRequestContextKey));
    const toolCallId = context.agent?.toolCallId ?? context.toolCallId ?? "no-tool-call-id";
    new InvocationLog(join(markerDirectory, `${toolName}.log`)).record(`${sessionId} ${toolCallId}`);
    new InvocationLog(join(markerDirectory, "tools.log")).record(`${sessionId} ${toolName}`);
    return toolCallId;
  };
  const waitAtGate = async (gateName: string, context: ScriptedToolContext): Promise<void> => {
    const gate = new FileGate(markerDirectory, gateName);
    gate.markReached();
    await gate.waitForRelease(context.abortSignal);
  };
  const tools: ToolsInput = {
    get_weather: createTool({
      id: "get_weather",
      description: "Returns the weather forecast",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("get_weather", context);
        return { forecast: "sunny in Paris" };
      },
    }),
    lookup_a: createTool({
      id: "lookup_a",
      description: "Looks up A",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("lookup_a", context);
        await waitAtGate("lookups", context);
        return "A found";
      },
    }),
    lookup_b: createTool({
      id: "lookup_b",
      description: "Looks up B",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("lookup_b", context);
        await waitAtGate("lookups", context);
        return "B found";
      },
    }),
    lookup_order: createTool({
      id: "lookup_order",
      description: "Looks up an order",
      inputSchema: scriptedToolInputSchema,
      mcp: { annotations: { readOnlyHint: true } },
      execute: async (_input, context) => {
        recordRun("lookup_order", context);
        return "order 42 shipped";
      },
    }),
    send_email: createTool({
      id: "send_email",
      description: "Sends an email",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("send_email", context);
        return `email sent for ${String(context.requestContext.get(idempotencyKeyRequestContextKey))}`;
      },
    }),
    charge_card: createTool({
      id: "charge_card",
      description: "Charges a card",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("charge_card", context);
        await waitAtGate("charge_card", context);
        return "card charged";
      },
    }),
    refund_order: createTool({
      id: "refund_order",
      description: "Refunds an order",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("refund_order", context);
        return { refunded: true };
      },
    }),
    archive_order: createTool({
      id: "archive_order",
      description: "Archives an order",
      inputSchema: scriptedToolInputSchema,
      requireApproval: true,
      execute: async (_input, context) => {
        recordRun("archive_order", context);
        return "order archived";
      },
    }),
    sync_inventory: createTool({
      id: "sync_inventory",
      description: "Synchronizes the inventory",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("sync_inventory", context);
        throw new Error("inventory service unavailable");
      },
    }),
    issue_invoice: createTool({
      id: "issue_invoice",
      description: "Issues an invoice",
      inputSchema: scriptedToolInputSchema,
      outputSchema: z.object({ invoiceId: z.string() }),
      // The write happens, then the result fails the output schema.
      execute: async (_input, context) => {
        recordRun("issue_invoice", context);
        return { invoiceId: 42 } as unknown as { invoiceId: string };
      },
    }),
    schedule_pickup: createTool({
      id: "schedule_pickup",
      description: "Schedules a pickup",
      // The transform changes the type, so validating its output again would fail.
      inputSchema: z.object({ requested_by: z.string().transform((text) => text.length) }),
      execute: async (input, context) => {
        recordRun("schedule_pickup", context);
        return `pickup scheduled for a request of ${input.requested_by} characters`;
      },
    }),
    audit_export: createTool({
      id: "audit_export",
      description: "Exports the audit log",
      inputSchema: scriptedToolInputSchema,
      requestContextSchema: z.object({ tenantId: z.string() }),
      execute: async (_input, context) => {
        recordRun("audit_export", context as unknown as ScriptedToolContext);
        return "audit exported";
      },
    }),
    delete_account: createTool({
      id: "delete_account",
      description: "Deletes an account",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input, context) => {
        recordRun("delete_account", context);
        throw new ToolExecutionError("account deletion is disabled");
      },
    }),
    // AI SDK tools, which Mastra never validates itself on this path.
    convert_credit: {
      description: "Converts store credit",
      inputSchema: z.object({ requested_by: z.string(), currency: z.string().default("USD") }),
      execute: async (input: { readonly currency: string }, context: ScriptedToolContext) => {
        recordRun("convert_credit", context);
        return `credit converted to ${input.currency}`;
      },
    },
    report_status: {
      description: "Reports a status shaped like a Mastra validation error",
      inputSchema: scriptedToolInputSchema,
      execute: async (_input: unknown, context: ScriptedToolContext) => {
        recordRun("report_status", context);
        return { error: true, message: "card declined", validationErrors: {} };
      },
    },
    set_quota: {
      description: "Sets a quota",
      inputSchema: z.object({ amount: z.number() }),
      execute: async (_input: unknown, context: ScriptedToolContext) => {
        recordRun("set_quota", context);
        return "quota set";
      },
    },
    ...(flowOptions.isResearcherShadowed === true
      ? {
          "agent-researcher": createTool({
            id: "agent-researcher",
            description: "Escalates to a human researcher",
            inputSchema: scriptedToolInputSchema,
            execute: async (_input, context) => {
              recordRun("agent-researcher", context);
              return "escalated";
            },
          }),
        }
      : {}),
  } as ToolsInput;
  const researcher = new Agent({
    id: "researcher",
    name: "researcher",
    description: "Researches a question",
    instructions: "Answer with the scripted model.",
    model: new ScriptedLanguageModel(markerDirectory, "researcher-model"),
  });
  return new Agent({
    id: "scripted-support",
    name: "Scripted support",
    instructions: "Answer with the scripted model.",
    model: new ScriptedLanguageModel(markerDirectory),
    tools,
    agents: { researcher },
  });
}

/** Moves completed messages' `createdAt` ahead, as if a Worker with a fast clock had run the turn. */
class ClockSkewedRunner implements AgentFrameworkRunner {
  public constructor(
    private readonly runner: MastraAgentRunner,
    private readonly clockSkewMs: number,
  ) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return this.runner.buildUserMessage(text);
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const outcome = await this.runner.runModelStep(turn);
    if (outcome.kind !== "completed") {
      return outcome;
    }
    return {
      ...outcome,
      newMessages: outcome.newMessages.map((message) => {
        const item = JSON.parse(message.frameworkItem) as { createdAt?: string };
        if (item.createdAt === undefined) {
          return message;
        }
        const skewedCreatedAt = new Date(Date.parse(item.createdAt) + this.clockSkewMs).toISOString();
        return { ...message, frameworkItem: JSON.stringify({ ...item, createdAt: skewedCreatedAt }) };
      }),
    };
  }

  public executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    return this.runner.executeDeferredToolCall(call);
  }
}
