/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import type {
  LanguageModelV4CallOptions,
  LanguageModelV4Prompt,
  LanguageModelV4StreamPart,
  LanguageModelV4ToolResultOutput,
} from "@ai-sdk/provider";
import { MockLanguageModelV4, simulateReadableStream } from "ai/test";

import { decideOrderSupportReply } from "./order-support-script.js";

/**
 * An AI SDK language model that follows the order-support script from its prompt alone, so it serves the Vercel AI
 * SDK and Mastra examples without a model key. Each call reads the last user message and the tool results after it.
 */
export function createScriptedOrderSupportLanguageModel(): MockLanguageModelV4 {
  return new MockLanguageModelV4({
    provider: "scripted",
    modelId: "scripted-order-support",
    doStream: async (options: LanguageModelV4CallOptions) => ({
      stream: simulateReadableStream({ chunks: replyToPrompt(options.prompt), chunkDelayInMs: null, initialDelayInMs: null }),
    }),
  });
}

function replyToPrompt(prompt: LanguageModelV4Prompt): LanguageModelV4StreamPart[] {
  const lastUserIndex = prompt.map((message) => message.role).lastIndexOf("user");
  const lastUser = prompt[lastUserIndex];
  const userText =
    lastUser?.role === "user" ? lastUser.content.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("") : "";
  const toolResults = new Map<string, string>();
  for (const message of prompt.slice(lastUserIndex + 1)) {
    if (message.role === "tool") {
      for (const part of message.content) {
        if (part.type === "tool-result") {
          toolResults.set(part.toolName, describeToolResultOutput(part.output));
        }
      }
    }
  }
  const reply = decideOrderSupportReply(userText, toolResults);
  const usage = {
    inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
    outputTokens: { total: 5, text: 5, reasoning: 0 },
  };
  if (reply.kind === "call_tool") {
    const toolCallId = `call-${prompt.filter((message) => message.role === "user").length}-${reply.toolName}`;
    return [
      { type: "stream-start", warnings: [] },
      { type: "tool-call", toolCallId, toolName: reply.toolName, input: JSON.stringify({ orderId: reply.orderId }) },
      { type: "finish", usage, finishReason: { unified: "tool-calls", raw: "tool-calls" } },
    ];
  }
  return [
    { type: "stream-start", warnings: [] },
    { type: "text-start", id: "answer" },
    { type: "text-delta", id: "answer", delta: reply.text },
    { type: "text-end", id: "answer" },
    { type: "finish", usage, finishReason: { unified: "stop", raw: "stop" } },
  ];
}

function describeToolResultOutput(output: LanguageModelV4ToolResultOutput): string {
  switch (output.type) {
    case "text":
    case "error-text":
      return output.value;
    case "json":
    case "error-json":
      return JSON.stringify(output.value);
    case "execution-denied":
      return `denied: ${output.reason ?? "no reason"}`;
    default:
      return JSON.stringify(output);
  }
}
