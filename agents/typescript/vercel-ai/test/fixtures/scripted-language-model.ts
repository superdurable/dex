// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { MockLanguageModelV4 } from "ai/test";

import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

import { getAgentToolExecutionScope } from "../../src/index.js";

type LanguageModelCallOptions = Parameters<MockLanguageModelV4["doStream"]>[0];
type LanguageModelPrompt = LanguageModelCallOptions["prompt"];
type LanguageModelPromptMessage = LanguageModelPrompt[number];
type LanguageModelStream = Awaited<ReturnType<MockLanguageModelV4["doStream"]>>["stream"];
type LanguageModelStreamPart = LanguageModelStream extends ReadableStream<infer Part> ? Part : never;
type UnifiedFinishReason = Extract<LanguageModelStreamPart, { type: "finish" }>["finishReason"]["unified"];
type PromptToolResultOutput = Extract<
  Extract<LanguageModelPromptMessage, { role: "tool" }>["content"][number],
  { type: "tool-result" }
>["output"];

/** Usage the scripted model reports for every call. */
export const scriptedModelCallUsage = {
  inputTokens: { total: 10, noCache: 8, cacheRead: 2, cacheWrite: 0 },
  outputTokens: { total: 5, text: 4, reasoning: 1 },
} as const;

/** Scripted AI SDK model that decides only from its prompt; the README's Development section lists its commands. */
export function createScriptedLanguageModel(markerDirectory: string): MockLanguageModelV4 {
  return new MockLanguageModelV4({
    provider: "dex-test",
    modelId: "scripted-model",
    doStream: async (options) => ({
      stream: readableStreamFromAsyncIterable(streamScriptedResponse(markerDirectory, options)),
    }),
  });
}

async function* streamScriptedResponse(
  markerDirectory: string,
  options: LanguageModelCallOptions,
): AsyncGenerator<LanguageModelStreamPart> {
  const prompt = options.prompt;
  const lastUserIndex = findLastUserMessageIndex(prompt);
  const lastUserMessage = prompt[lastUserIndex];
  if (lastUserMessage === undefined) {
    throw new Error("the scripted model received no user message");
  }
  const userText = readPromptText(lastUserMessage);
  const scope = getAgentToolExecutionScope();
  new InvocationLog(join(markerDirectory, "model-calls.log")).record(
    `${scope?.flowId} ${scope?.turnId} attempt ${scope?.attempt}`,
  );
  const turnNumber = prompt.filter((message) => message.role === "user").length;
  yield { type: "stream-start", warnings: [] };
  if (userText.startsWith("truncate ")) {
    const toolName = userText.slice("truncate ".length);
    yield toolCallPart(turnNumber, toolName, { requested_by: userText });
    yield finishPart("length");
    return;
  }
  if (userText.startsWith("call ")) {
    const finalAction = /(?: then (fail|pause))$/.exec(userText)?.[1];
    const isInvalidCall = userText.startsWith("call invalid ");
    const toolNameGroups = userText
      .slice(isInvalidCall ? "call invalid ".length : "call ".length)
      .replace(/ then (fail|pause)$/, "")
      .split(" then ")
      .map((group) => group.split(" and ").map((toolName) => toolName.trim()));
    const toolNames = toolNameGroups.flat();
    const toolResults = readToolResultsAfter(prompt, lastUserIndex);
    const missingToolNames =
      toolNameGroups
        .map((group) => group.filter((toolName) => !toolResults.has(toolName)))
        .find((group) => group.length > 0) ?? [];
    if (missingToolNames.length > 0) {
      for (const toolName of missingToolNames) {
        yield toolCallPart(turnNumber, toolName, { requested_by: isInvalidCall ? 42 : userText });
      }
      yield finishPart("tool-calls");
      return;
    }
    if (finalAction === "fail") {
      throw new Error(`scripted model failure after the tool calls of ${scope?.turnId}`);
    }
    if (finalAction === "pause") {
      await pauseAtTurnGate(markerDirectory, options.abortSignal);
    }
    yield* streamAnswer(toolNames.map((toolName) => `${toolName} -> ${toolResults.get(toolName)}`).join(" | "));
    return;
  }
  if (userText === "history") {
    const earlierTexts = prompt.slice(0, lastUserIndex).flatMap((message) => {
      if (message.role !== "user" && message.role !== "assistant") {
        return [];
      }
      const text = readPromptText(message);
      return text === "" ? [] : [`${message.role}: ${text}`];
    });
    yield* streamAnswer(["history", ...earlierTexts].join(" | "));
    return;
  }
  if (userText === "think") {
    yield { type: "reasoning-start", id: "reasoning-1" };
    yield { type: "reasoning-delta", id: "reasoning-1", delta: "weighing the request" };
    yield { type: "reasoning-end", id: "reasoning-1" };
    yield* streamAnswer("thought done");
    return;
  }
  if (userText === "slow stream") {
    yield { type: "text-start", id: "text-1" };
    yield { type: "text-delta", id: "text-1", delta: `partial text of attempt ${scope?.attempt}. ` };
    await pauseAtTurnGate(markerDirectory, options.abortSignal);
    yield { type: "text-delta", id: "text-1", delta: "slow turn done" };
    yield { type: "text-end", id: "text-1" };
    yield finishPart("stop");
    return;
  }
  yield* streamAnswer(`echo: ${userText}`);
}

async function pauseAtTurnGate(markerDirectory: string, abortSignal: AbortSignal | undefined): Promise<void> {
  const scope = getAgentToolExecutionScope();
  new InvocationLog(join(markerDirectory, "turn.log")).record(
    `${scope?.flowId} ${scope?.turnId} attempt ${scope?.attempt}`,
  );
  const gate = new FileGate(markerDirectory, "turn");
  gate.markReached();
  await gate.waitForRelease(abortSignal);
}

function toolCallPart(turnNumber: number, toolName: string, input: Record<string, unknown>): LanguageModelStreamPart {
  return {
    type: "tool-call",
    toolCallId: `call-${turnNumber}-${toolName}`,
    toolName,
    input: JSON.stringify(input),
  };
}

function* streamAnswer(text: string): Generator<LanguageModelStreamPart> {
  yield { type: "text-start", id: "answer" };
  yield { type: "text-delta", id: "answer", delta: text };
  yield { type: "text-end", id: "answer" };
  yield finishPart("stop");
}

function finishPart(unifiedFinishReason: UnifiedFinishReason): LanguageModelStreamPart {
  return {
    type: "finish",
    usage: {
      inputTokens: { ...scriptedModelCallUsage.inputTokens },
      outputTokens: { ...scriptedModelCallUsage.outputTokens },
    },
    finishReason: { unified: unifiedFinishReason, raw: unifiedFinishReason },
  };
}

function findLastUserMessageIndex(prompt: LanguageModelPrompt): number {
  for (let index = prompt.length - 1; index >= 0; index -= 1) {
    if (prompt[index]?.role === "user") {
      return index;
    }
  }
  return -1;
}

function readToolResultsAfter(prompt: LanguageModelPrompt, userIndex: number): Map<string, string> {
  const toolResults = new Map<string, string>();
  for (const message of prompt.slice(userIndex + 1)) {
    if (message.role !== "tool") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-result") {
        toolResults.set(part.toolName, describePromptToolResultOutput(part.output));
      }
    }
  }
  return toolResults;
}

function describePromptToolResultOutput(output: PromptToolResultOutput): string {
  switch (output.type) {
    case "text":
    case "error-text":
      return output.value;
    case "json":
    case "error-json":
      return JSON.stringify(output.value);
    case "execution-denied":
      return `denied: ${output.reason ?? "no reason"}`;
    case "content":
      return output.value.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("\n");
  }
}

function readPromptText(message: LanguageModelPromptMessage): string {
  if (message.role === "system") {
    return message.content;
  }
  return message.content
    .flatMap((part) => (part.type === "text" ? [part.text] : []))
    .join("");
}

function readableStreamFromAsyncIterable<Part>(parts: AsyncIterable<Part>): ReadableStream<Part> {
  const iterator = parts[Symbol.asyncIterator]();
  return new ReadableStream<Part>({
    async pull(controller) {
      const next = await iterator.next();
      if (next.done === true) {
        controller.close();
        return;
      }
      controller.enqueue(next.value);
    },
    async cancel() {
      await iterator.return?.();
    },
  });
}
