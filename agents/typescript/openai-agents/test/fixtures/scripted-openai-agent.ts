// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { Agent, handoff, tool, toolNamespace, type RunContext } from "@openai/agents";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import {
  ToolExecutionError,
  createOpenAIAgentsSessionFlow,
  getDeferredToolExecution,
} from "../../src/index.js";
import { ScriptedOpenAIModel, billingAgentInstructions } from "./scripted-openai-model.js";

/** Flow type of the scripted agent in every test. */
export const scriptedOpenAIAgentFlowType = "ScriptedOpenAIAgent";
/** Environment variable naming the directory where scripted tools write marker files. */
export const scriptedAgentMarkerDirectoryVariable = "SCRIPTED_AGENT_MARKER_DIRECTORY";

/** Application context the adapter creates for every model call and tool call. */
interface ScriptedAgentContext {
  readonly flowId: string;
}

type ToolCallDetails = { readonly toolCall?: { readonly callId: string }; readonly signal?: AbortSignal };

/**
 * Creates the scripted OpenAI Agents SDK agent Flow; tests and the Worker child build it identically.
 *
 * Every tool records `<flowId> <callId> attempt <n>` in the `<tool>.log` InvocationLog on every real
 * run, then `<flowId> <tool>` in `tool-runs.log`. `charge_card` then waits at the `charge_card`
 * FileGate, and `lookup_a` and `lookup_b` wait at the `lookups` FileGate.
 */
export function createScriptedOpenAIAgentFlow(
  markerDirectory: string,
  flowOptions: { readonly flowType?: string } = {},
): AgentSessionFlow {
  const invocationLog = (name: string): InvocationLog =>
    new InvocationLog(join(markerDirectory, `${name}.log`));
  // Logging comes first, so a tool that ran outside a Dex tool Step still leaves a trace.
  const recordToolRun = (
    toolName: string,
    runContext: RunContext<ScriptedAgentContext> | undefined,
    details: ToolCallDetails | undefined,
  ): string => {
    const execution = getDeferredToolExecution();
    const flowId = runContext?.context.flowId ?? "no-context";
    invocationLog(toolName).record(`${flowId} ${details?.toolCall?.callId} attempt ${execution?.attempt ?? "inline"}`);
    invocationLog("tool-runs").record(`${flowId} ${toolName}`);
    if (execution === undefined) {
      throw new Error(`${toolName} ran outside a Dex tool Step`);
    }
    return execution.idempotencyKey;
  };
  const waitAtLookupGate = async (details: ToolCallDetails | undefined): Promise<void> => {
    const gate = new FileGate(markerDirectory, "lookups");
    gate.markReached();
    await gate.waitForRelease(details?.signal);
  };

  const getWeather = tool({
    name: "get_weather",
    description: "Read the weather of a city.",
    parameters: z.object({ city: z.string() }),
    errorFunction: null,
    execute: ({ city }, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("get_weather", runContext, details);
      return `sunny in ${city}`;
    },
  });
  const lookupA = tool({
    name: "lookup_a",
    description: "Look up record a.",
    parameters: z.object({ key: z.string() }),
    errorFunction: null,
    execute: async (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("lookup_a", runContext, details);
      await waitAtLookupGate(details);
      return "lookup_a found";
    },
  });
  const lookupB = tool({
    name: "lookup_b",
    description: "Look up record b.",
    parameters: z.object({ key: z.string() }),
    errorFunction: null,
    execute: async (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("lookup_b", runContext, details);
      await waitAtLookupGate(details);
      return "lookup_b found";
    },
  });
  const sendEmail = tool({
    name: "send_email",
    description: "Send an email.",
    parameters: z.object({ to: z.string() }),
    errorFunction: null,
    execute: (_input, runContext?: RunContext<ScriptedAgentContext>, details?) =>
      `email sent for ${recordToolRun("send_email", runContext, details)}`,
  });
  const chargeCard = tool({
    name: "charge_card",
    description: "Charge the card on file.",
    parameters: z.object({ amountCents: z.number() }),
    errorFunction: null,
    execute: async (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("charge_card", runContext, details);
      const gate = new FileGate(markerDirectory, "charge_card");
      gate.markReached();
      await gate.waitForRelease(details?.signal);
      return "card charged";
    },
  });
  const refundOrder = tool({
    name: "refund_order",
    description: "Refund an order.",
    parameters: z.object({ orderId: z.string() }),
    errorFunction: null,
    execute: (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("refund_order", runContext, details);
      return "refund issued";
    },
  });
  const deleteAccount = tool({
    name: "delete_account",
    description: "Delete an account.",
    parameters: z.object({ accountId: z.string() }),
    errorFunction: null,
    execute: (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("delete_account", runContext, details);
      throw new ToolExecutionError("account deletion is disabled");
    },
  });
  const lookupInvoice = tool({
    name: "lookup_invoice",
    description: "Look up an invoice.",
    parameters: z.object({ invoiceId: z.string() }),
    needsApproval: true,
    errorFunction: null,
    execute: ({ invoiceId }, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("lookup_invoice", runContext, details);
      return `invoice ${invoiceId} is paid`;
    },
  });
  // These two keep tool()'s default errorFunction, which hides their errors behind fixed text.
  const postUpdate = tool({
    name: "post_update",
    description: "Post a status update.",
    parameters: z.object({ text: z.string() }),
    execute: (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("post_update", runContext, details);
      throw new Error("the feed service returned 503 after posting");
    },
  });
  const readInventory = tool({
    name: "read_inventory",
    description: "Read the inventory of a product.",
    parameters: z.object({ sku: z.string() }),
    execute: (_input, runContext?: RunContext<ScriptedAgentContext>, details?) => {
      recordToolRun("read_inventory", runContext, details);
      throw new Error("the inventory service is down");
    },
  });
  const crmTools = toolNamespace({
    name: "crm",
    description: "Customer records.",
    tools: [
      tool({
        name: "update_contact",
        description: "Update a contact's email address.",
        parameters: z.object({ contactId: z.string(), email: z.string() }),
        errorFunction: null,
        execute: ({ contactId }, runContext?: RunContext<ScriptedAgentContext>, details?) => {
          recordToolRun("crm.update_contact", runContext, details);
          return `contact ${contactId} updated`;
        },
      }),
    ],
  });

  const model = new ScriptedOpenAIModel(markerDirectory);
  const billingAgent = new Agent<ScriptedAgentContext>({
    name: "Billing agent",
    instructions: billingAgentInstructions,
    model,
    tools: [refundOrder],
  });
  const startingAgent = new Agent<ScriptedAgentContext>({
    name: "Assistant",
    instructions: "You are the scripted assistant.",
    model,
    tools: [
      getWeather,
      lookupA,
      lookupB,
      sendEmail,
      chargeCard,
      refundOrder,
      deleteAccount,
      lookupInvoice,
      postUpdate,
      readInventory,
      ...crmTools,
    ],
    handoffs: [handoff(billingAgent)],
  });
  // A raw Agent handoff back to the starting agent exercises a cyclic graph.
  billingAgent.handoffs = [startingAgent];

  return createOpenAIAgentsSessionFlow(startingAgent, flowOptions.flowType ?? scriptedOpenAIAgentFlowType, {
    toolPolicies: {
      get_weather: { effect: "read" },
      lookup_a: { effect: "read" },
      lookup_b: { effect: "read" },
      lookup_invoice: { effect: "read" },
      read_inventory: { effect: "read" },
      charge_card: { effect: "non_idempotent_write" },
      post_update: { effect: "non_idempotent_write", requiresApproval: false },
      refund_order: { effect: "idempotent_write" },
      "crm.update_contact": { effect: "idempotent_write" },
    },
    runConfig: { tracingDisabled: true },
    createContext: (flowId) => ({ flowId }),
  });
}
