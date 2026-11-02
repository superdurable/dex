// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readdirSync } from "node:fs";

import type {
  DeferredToolExecution,
  DeferredToolOutcome,
  ToolTaskPoll,
  ToolTaskPollOutcome,
} from "../../src/agent-framework-runner.js";
import { createAgentSessionFlow, type AgentSessionFlow, type AgentSessionOptions } from "../../src/agent-session-flow.js";
import { StepwiseAgentRunner, functionTool, type AgentDefinition, type ModelRequest } from "../../src/stepwise/index.js";
import { ScriptedModelClient, type ScriptedModelReply } from "../../src/testing/index.js";
import { readConformanceJson } from "./conformance-suite.js";

// The same agent as the Python core's tests/conformance_agent.py; see that module for the reply rules.

export interface ScriptedReply {
  readonly text?: string;
  readonly reasoning?: string;
  readonly tool_calls?: readonly { readonly tool_name: string; readonly arguments: Readonly<Record<string, unknown>> }[];
}

export interface ConformanceScenario {
  readonly name: string;
  readonly agent: "chat" | "compacting";
  readonly replies: Readonly<Record<string, readonly ScriptedReply[]>>;
  readonly actions: readonly Readonly<Record<string, unknown>>[];
  readonly expected: unknown;
}

export const conformanceFlowTypes = { chat: "ConformanceAgent", compacting: "ConformanceCompactingAgent" } as const;

export function readConformanceScenarios(): ConformanceScenario[] {
  return readdirSync(new URL("../../../../../conformance/transcripts/", import.meta.url))
    .filter((name) => name.endsWith(".json"))
    .sort()
    .map((name) => readConformanceJson<ConformanceScenario>(`transcripts/${name}`));
}

class ConformanceRunner extends StepwiseAgentRunner {
  public override async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    if (call.toolCall.toolName === "start_report") {
      return { kind: "tool_task_started", taskId: `task-${call.toolCall.toolCallId}`, pollIntervalSeconds: 1 };
    }
    return super.executeDeferredToolCall(call);
  }

  public override async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    const { topic } = JSON.parse(poll.toolCall.arguments) as { topic: string };
    return { output: `report on ${topic}` };
  }
}

export interface ConformanceAgents {
  readonly flows: Readonly<Record<keyof typeof conformanceFlowTypes, AgentSessionFlow<StepwiseAgentRunner>>>;
  /** Model requests in the transcript's form, by session Flow ID. */
  readonly modelRequests: Map<string, unknown[]>;
}

export function createConformanceAgents(): ConformanceAgents {
  const replies = new Map<string, readonly ScriptedReply[]>();
  for (const scenario of readConformanceScenarios()) {
    for (const [userText, scripted] of Object.entries(scenario.replies)) {
      if (replies.has(userText)) {
        throw new Error(`two scenarios script the user text ${userText}`);
      }
      replies.set(userText, scripted);
    }
  }
  const modelRequests = new Map<string, unknown[]>();
  const model = new ScriptedModelClient((request) => {
    const { flowId, ...described } = describeModelRequest(request);
    modelRequests.set(flowId, [...(modelRequests.get(flowId) ?? []), described]);
    const scripted = replies.get(ScriptedModelClient.userTexts(request).at(-1) ?? "") ?? [];
    let resultCount = ScriptedModelClient.toolResults(request).length;
    let index = 0;
    while (resultCount > 0) {
      resultCount -= scripted[index]?.tool_calls?.length ?? 0;
      index += 1;
    }
    return toScriptedModelReply(scripted[index] ?? {});
  });
  const tools = [
    functionTool<{ query: string }>({
      name: "lookup_order",
      description: "Look up an order.",
      parameters: {
        type: "object",
        properties: { query: { type: "string" } },
        required: ["query"],
        additionalProperties: false,
      },
      effect: "read",
      execute: ({ query }) => `order ${query}: shipped`,
    }),
    functionTool<{ to: string; body: string }>({
      name: "send_email",
      description: "Send an email.",
      parameters: {
        type: "object",
        properties: { to: { type: "string" }, body: { type: "string" } },
        required: ["to", "body"],
        additionalProperties: false,
      },
      effect: "non_idempotent_write",
      execute: ({ to, body }) => ({ sent_to: to, body }),
    }),
    functionTool<{ topic: string }>({
      name: "start_report",
      description: "Start building a report.",
      parameters: {
        type: "object",
        properties: { topic: { type: "string" } },
        required: ["topic"],
        additionalProperties: false,
      },
      effect: "read",
      execute: () => {
        throw new Error("ConformanceRunner starts the report task");
      },
    }),
  ];
  const createFlow = (profile: keyof typeof conformanceFlowTypes): AgentSessionFlow<StepwiseAgentRunner> => {
    const definition: AgentDefinition = {
      name: `conformance-${profile}`,
      instructions: "You are the conformance agent.",
      model,
      tools,
      builtInTools: ["ask_user", "durable_wait", "write_todos"],
      planModeEnabled: true,
      ...(profile === "compacting"
        ? { compaction: { contextWindowTokens: 1000, triggerRatio: 0.3, retainedRatio: 0.15 } }
        : {}),
    };
    const options: AgentSessionOptions = {
      builtInTools: definition.builtInTools ?? [],
      planModeEnabled: true,
      ...(definition.compaction === undefined ? {} : { compaction: definition.compaction }),
    };
    return createAgentSessionFlow(new ConformanceRunner(definition), conformanceFlowTypes[profile], options);
  };
  return { flows: { chat: createFlow("chat"), compacting: createFlow("compacting") }, modelRequests };
}

function toScriptedModelReply(scripted: ScriptedReply): ScriptedModelReply {
  return {
    text: scripted.text ?? "",
    ...(scripted.reasoning === undefined ? {} : { reasoning: scripted.reasoning }),
    toolCalls: (scripted.tool_calls ?? []).map((call) => ({ toolName: call.tool_name, arguments: call.arguments })),
  };
}

function describeModelRequest(request: ModelRequest): { flowId: string } & Readonly<Record<string, unknown>> {
  return {
    flowId: request.metadata.flowId,
    step_execution_id: request.metadata.stepExecutionId,
    turn_id: request.metadata.turnId,
    attempt: request.metadata.attempt,
    instructions: request.instructions,
    tools: request.tools.map((tool) => ({
      name: tool.name,
      description: tool.description,
      parameters_json: JSON.stringify(tool.parameters),
    })),
    tool_choice: typeof request.toolChoice === "string" ? request.toolChoice : { tool_name: request.toolChoice.toolName },
    items: request.inputItems.map((item) => {
      switch (item.type) {
        case "user_text":
          return { type: "user_text", text: item.text };
        case "tool_result":
          return { type: "tool_result", tool_call_id: item.toolCallId, tool_name: item.toolName, output: item.output };
        case "model_output":
          return {
            type: "model_output",
            provider: item.provider,
            api: item.api,
            item: item.item,
            text: item.text,
            tool_call_ids: item.toolCalls.map((call) => call.toolCallId),
          };
      }
    }),
  };
}
