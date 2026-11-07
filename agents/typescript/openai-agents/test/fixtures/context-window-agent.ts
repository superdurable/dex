// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Agent,
  Usage,
  tool,
  type AgentInputItem,
  type Model,
  type ModelRequest,
  type ModelResponse,
  type StreamEvent,
  type protocol,
} from "@openai/agents";
import { z } from "zod";

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import {
  ContextWindowModelScript,
  contextWindowSessionOptions,
  contextWindowToolName,
  readContextWindowSettings,
  type ContextWindowSettings,
} from "@superdurable/dex-agents/testing";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";

/** Flow type of the context window test agent. */
export const contextWindowAgentFlowType = "OpenAIAgentsContextWindowAgent";
/** Environment variable naming the state directory of the context window test. */
export const contextWindowStateDirectoryVariable = "OPENAI_AGENTS_CONTEXT_WINDOW_STATE_DIRECTORY";

const usage = {
  requests: 1,
  inputTokens: 10,
  outputTokens: 5,
  totalTokens: 15,
  inputTokensDetails: { cached_tokens: 0 },
  outputTokensDetails: { reasoning_tokens: 0 },
};

/** An OpenAI Agents SDK `Model` that answers from a {@link ContextWindowModelScript}. */
class ContextWindowModel implements Model {
  public constructor(private readonly script: ContextWindowModelScript) {}

  public async getResponse(request: ModelRequest): Promise<ModelResponse> {
    return { usage: new Usage(usage), output: await this.respond(request), responseId: "context-window" };
  }

  public async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    const output = await this.respond(request);
    yield { type: "response_started" };
    yield { type: "response_done", response: { id: "context-window", usage, output } };
  }

  private async respond(request: ModelRequest): Promise<protocol.OutputModelItem[]> {
    const input: AgentInputItem[] =
      typeof request.input === "string" ? [{ type: "message", role: "user", content: request.input }] : request.input;
    const lastUserIndex = input.map((item) => ("role" in item ? item.role : undefined)).lastIndexOf("user");
    const reply = await this.script.reply({
      instructions: request.systemInstructions,
      inputText: JSON.stringify(input),
      hasToolResult: input.slice(lastUserIndex).some((item) => item.type === "function_call_result"),
    });
    if (reply.kind === "tool_call") {
      return [
        {
          type: "function_call",
          id: `fc_${reply.tag}`,
          callId: `call_${reply.tag}`,
          name: contextWindowToolName,
          arguments: JSON.stringify({ tag: reply.tag }),
          status: "completed",
        },
      ];
    }
    return [
      {
        type: "message",
        id: "msg_context_window",
        role: "assistant",
        status: "completed",
        content: [{ type: "output_text", text: reply.text }],
      },
    ];
  }
}

/** Creates the agent Flow that tests and the Worker child build identically. */
export function createContextWindowAgentFlow(
  stateDirectory: string,
  settings: ContextWindowSettings = readContextWindowSettings(),
): AgentSessionFlow {
  const script = new ContextWindowModelScript(stateDirectory, settings);
  const agent = new Agent({
    name: "Assistant",
    instructions: "Answer with what the lookup tool returns.",
    model: new ContextWindowModel(script),
    tools: [
      tool({
        name: contextWindowToolName,
        description: "Look up a tag.",
        parameters: z.object({ tag: z.string() }),
        errorFunction: null,
        execute: ({ tag }) => script.toolOutput(tag),
      }),
    ],
  });
  return createOpenAIAgentsSessionFlow(agent, contextWindowAgentFlowType, {
    toolPolicies: { [contextWindowToolName]: { effect: "read" } },
    runConfig: { tracingDisabled: true },
    ...contextWindowSessionOptions(settings),
  });
}

/** Directory of the test's state when the Worker child runs, from {@link contextWindowStateDirectoryVariable}. */
export function readContextWindowStateDirectory(): string {
  const directory = process.env[contextWindowStateDirectoryVariable];
  if (directory === undefined || directory === "") {
    throw new Error(`${contextWindowStateDirectoryVariable} is not set`);
  }
  return directory;
}
