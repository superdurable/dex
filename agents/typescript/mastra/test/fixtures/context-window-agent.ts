// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  LanguageModelV3,
  LanguageModelV3CallOptions,
  LanguageModelV3GenerateResult,
  LanguageModelV3StreamPart,
  LanguageModelV3StreamResult,
  LanguageModelV3Usage,
} from "@ai-sdk/provider";
import { Agent } from "@mastra/core/agent";
import { createTool } from "@mastra/core/tools";
import { z } from "zod";

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import {
  ContextWindowModelScript,
  contextWindowSessionOptions,
  contextWindowToolName,
  readContextWindowSettings,
  type ContextWindowReply,
  type ContextWindowSettings,
} from "@superdurable/dex-agents/testing";

import { createMastraAgentSessionFlow } from "../../src/index.js";

/** Flow type of the context window test agent. */
export const contextWindowAgentFlowType = "MastraContextWindowAgent";
/** Environment variable naming the state directory of the context window test. */
export const contextWindowStateDirectoryVariable = "MASTRA_CONTEXT_WINDOW_STATE_DIRECTORY";

const usage: LanguageModelV3Usage = {
  inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
  outputTokens: { total: 5, text: 5, reasoning: 0 },
};

/** An AI SDK language model (specification v3) that answers from a {@link ContextWindowModelScript}. */
class ContextWindowLanguageModel implements LanguageModelV3 {
  public readonly specificationVersion = "v3";
  public readonly provider = "dex-test";
  public readonly modelId = "context-window-model";
  public readonly supportedUrls = {};

  public constructor(private readonly script: ContextWindowModelScript) {}

  public async doGenerate(options: LanguageModelV3CallOptions): Promise<LanguageModelV3GenerateResult> {
    const reply = await this.respond(options);
    return {
      content: reply.kind === "text" ? [{ type: "text", text: reply.text }] : [toolCallPart(reply.tag)],
      finishReason: finishReason(reply),
      usage,
      warnings: [],
    };
  }

  public async doStream(options: LanguageModelV3CallOptions): Promise<LanguageModelV3StreamResult> {
    const reply = await this.respond(options);
    const parts: LanguageModelV3StreamPart[] = [{ type: "stream-start", warnings: [] }];
    if (reply.kind === "text") {
      parts.push(
        { type: "text-start", id: "answer" },
        { type: "text-delta", id: "answer", delta: reply.text },
        { type: "text-end", id: "answer" },
      );
    } else {
      parts.push(toolCallPart(reply.tag));
    }
    parts.push({ type: "finish", usage, finishReason: finishReason(reply) });
    return {
      stream: new ReadableStream<LanguageModelV3StreamPart>({
        start(controller) {
          parts.forEach((part) => controller.enqueue(part));
          controller.close();
        },
      }),
    };
  }

  private respond(options: LanguageModelV3CallOptions): Promise<ContextWindowReply> {
    const system = options.prompt.flatMap((message) => (message.role === "system" ? [message.content] : []));
    const lastUserIndex = options.prompt.map((message) => message.role).lastIndexOf("user");
    return this.script.reply({
      instructions: system.join("\n"),
      inputText: JSON.stringify(options.prompt),
      hasToolResult: options.prompt.slice(lastUserIndex).some((message) => message.role === "tool"),
    });
  }
}

function toolCallPart(tag: string): { type: "tool-call"; toolCallId: string; toolName: string; input: string } {
  const input = JSON.stringify({ tag });
  return { type: "tool-call", toolCallId: `call-${tag}`, toolName: contextWindowToolName, input };
}

function finishReason(reply: ContextWindowReply): LanguageModelV3GenerateResult["finishReason"] {
  return reply.kind === "text" ? { unified: "stop", raw: "stop" } : { unified: "tool-calls", raw: "tool_calls" };
}

/** Creates the agent Flow that tests and the Worker child build identically. */
export function createContextWindowAgentFlow(
  stateDirectory: string,
  settings: ContextWindowSettings = readContextWindowSettings(),
): AgentSessionFlow {
  const script = new ContextWindowModelScript(stateDirectory, settings);
  const agent = new Agent({
    id: "context-window",
    name: "Context window",
    instructions: "Answer with what the lookup tool returns.",
    model: new ContextWindowLanguageModel(script),
    tools: {
      [contextWindowToolName]: createTool({
        id: contextWindowToolName,
        description: "Look up a tag.",
        inputSchema: z.object({ tag: z.string() }),
        execute: async ({ tag }) => script.toolOutput(tag),
      }),
    },
  });
  return createMastraAgentSessionFlow(agent, contextWindowAgentFlowType, {
    toolPolicies: { [contextWindowToolName]: { effect: "read" } },
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
