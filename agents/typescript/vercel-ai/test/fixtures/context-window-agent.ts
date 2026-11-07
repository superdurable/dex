// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { jsonSchema, tool } from "ai";
import { MockLanguageModelV4 } from "ai/test";

import {
  ContextWindowModelScript,
  contextWindowSessionOptions,
  contextWindowToolName,
  readContextWindowSettings,
  type ContextWindowReply,
  type ContextWindowSettings,
} from "@superdurable/dex-agents/testing";

import { createVercelAiAgentSessionFlow, type AgentSessionFlow } from "../../src/index.js";

type CallOptions = Parameters<MockLanguageModelV4["doStream"]>[0];
type StreamPart =
  Awaited<ReturnType<MockLanguageModelV4["doStream"]>>["stream"] extends ReadableStream<infer Part> ? Part : never;
type GenerateResult = Awaited<ReturnType<MockLanguageModelV4["doGenerate"]>>;

/** Flow type of the context window test agent. */
export const contextWindowAgentFlowType = "VercelAiContextWindowAgent";
/** Environment variable naming the state directory of the context window test. */
export const contextWindowStateDirectoryVariable = "VERCEL_AI_CONTEXT_WINDOW_STATE_DIRECTORY";

const usage = {
  inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
  outputTokens: { total: 5, text: 5, reasoning: 0 },
};

/** Creates the agent Flow that tests and the Worker child build identically. */
export function createContextWindowAgentFlow(
  stateDirectory: string,
  settings: ContextWindowSettings = readContextWindowSettings(),
): AgentSessionFlow {
  const script = new ContextWindowModelScript(stateDirectory, settings);
  const respond = async (options: CallOptions): Promise<ContextWindowReply> => {
    const system = options.prompt.flatMap((message) => (message.role === "system" ? [message.content] : []));
    const lastUserIndex = options.prompt.map((message) => message.role).lastIndexOf("user");
    return script.reply({
      instructions: system.join("\n"),
      inputText: JSON.stringify(options.prompt),
      hasToolResult: options.prompt.slice(lastUserIndex).some((message) => message.role === "tool"),
    });
  };
  const model = new MockLanguageModelV4({
    provider: "dex-test",
    modelId: "context-window-model",
    doGenerate: async (options): Promise<GenerateResult> => {
      const reply = await respond(options);
      return {
        content: reply.kind === "text" ? [{ type: "text", text: reply.text }] : [toolCallContent(reply.tag)],
        finishReason: { unified: reply.kind === "text" ? "stop" : "tool-calls", raw: undefined },
        usage,
        warnings: [],
      };
    },
    doStream: async (options) => {
      const reply = await respond(options);
      const parts: StreamPart[] = [{ type: "stream-start", warnings: [] }];
      if (reply.kind === "text") {
        parts.push(
          { type: "text-start", id: "answer" },
          { type: "text-delta", id: "answer", delta: reply.text },
          { type: "text-end", id: "answer" },
        );
      } else {
        parts.push(toolCallContent(reply.tag));
      }
      parts.push({
        type: "finish",
        usage,
        finishReason: { unified: reply.kind === "text" ? "stop" : "tool-calls", raw: undefined },
      });
      return {
        stream: new ReadableStream<StreamPart>({
          start(controller) {
            parts.forEach((part) => controller.enqueue(part));
            controller.close();
          },
        }),
      };
    },
  });
  return createVercelAiAgentSessionFlow(
    {
      model,
      instructions: "Answer with what the lookup tool returns.",
      tools: {
        [contextWindowToolName]: tool({
          description: "Look up a tag.",
          inputSchema: jsonSchema<{ tag: string }>({
            type: "object",
            properties: { tag: { type: "string" } },
            required: ["tag"],
          }),
          execute: async ({ tag }) => script.toolOutput(tag),
        }),
      },
      toolPolicies: { [contextWindowToolName]: { effect: "read" } },
    },
    contextWindowAgentFlowType,
    contextWindowSessionOptions(settings),
  );
}

function toolCallContent(tag: string): { type: "tool-call"; toolCallId: string; toolName: string; input: string } {
  const input = JSON.stringify({ tag });
  return { type: "tool-call", toolCallId: `call-${tag}`, toolName: contextWindowToolName, input };
}

/** Directory of the test's state when the Worker child runs, from {@link contextWindowStateDirectoryVariable}. */
export function readContextWindowStateDirectory(): string {
  const directory = process.env[contextWindowStateDirectoryVariable];
  if (directory === undefined || directory === "") {
    throw new Error(`${contextWindowStateDirectoryVariable} is not set`);
  }
  return join(directory);
}
