// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { appendFileSync, existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { MockLanguageModelV4 } from "ai/test";

import { FileGate } from "../../src/testing/index.js";

type LanguageModelCallOptions = Parameters<MockLanguageModelV4["doStream"]>[0];
type LanguageModelPrompt = LanguageModelCallOptions["prompt"];
type LanguageModelPromptMessage = LanguageModelPrompt[number];
type LanguageModelStream = Awaited<ReturnType<MockLanguageModelV4["doStream"]>>["stream"];
type LanguageModelStreamPart = LanguageModelStream extends ReadableStream<infer Part> ? Part : never;
type PromptToolResultOutput = Extract<
  Extract<LanguageModelPromptMessage, { role: "tool" }>["content"][number],
  { type: "tool-result" }
>["output"];

/** One logged `doStream` call, read back by tests with {@link readLoggedModelCalls}. */
export interface LoggedModelCall {
  readonly firstUserText: string;
  readonly prompt: LanguageModelPrompt;
  readonly toolNames: readonly string[];
  readonly toolChoice: unknown;
}

/** Usage the scripted model reports for every call. */
export const scriptedStepwiseModelUsage = {
  inputTokens: { total: 12, noCache: 10, cacheRead: 2, cacheWrite: 0 },
  outputTokens: { total: 6, text: 5, reasoning: 1 },
} as const;

/**
 * An AI SDK mock language model whose answers depend only on its prompt, so reruns are deterministic.
 *
 * The last user text is a command; tool calls are answered once their results follow it:
 * - `lookup <id>`, `refund <id>`, `charge <id>`: one `lookup_order`, `refund_order`, or `charge_card`
 *   call, then the answer `<tool>: <result>`. `lookup <id> then pause` streams partial text and waits at
 *   the `model` FileGate before that answer, and `lookup <id> then fail` throws instead of answering.
 * - `ask <id> [choices a,b] [strict]`: one `ask_user` call, then `answer: <result>`.
 * - `wait <seconds> <id>`: one `durable_wait` call, then `waited: <result>`.
 * - `think <id>`: reasoning with provider metadata and a `lookup_order` call, then an answer.
 * - `ask-null <id>`: an `ask_user` call whose `choices` and `allows_free_text` are `null`, then `answer: <result>`.
 * - `loop <id>`: a new `lookup_order` call while tools are offered, then `no tools after <n> calls`.
 * - `stubborn <id>`: a new `lookup_order` call on every model call, even when no tools are offered.
 * - `bad <id>`: a `lookup_order` call whose `order_id` is a number, then the result.
 * - `slow <id>`: partial text, the `model` FileGate, then `slow done`.
 * - `history`: every earlier user and assistant text.
 * - anything else: `echo: <text>`.
 * Every call is appended to `model-calls.jsonl` in the marker directory.
 */
export function createStepwiseScriptedLanguageModel(markerDirectory: string): MockLanguageModelV4 {
  return new MockLanguageModelV4({
    provider: "scripted.messages",
    modelId: "scripted-model",
    doStream: async (options) => ({
      stream: readableStreamFromAsyncIterable(streamScriptedResponse(markerDirectory, options)),
    }),
  });
}

/**
 * Reads the logged model calls whose first user message contains a token.
 * @param markerDirectory - The model's marker directory.
 * @param token - Text unique to one session's first message.
 * @returns The calls in order.
 */
export function readLoggedModelCalls(markerDirectory: string, token: string): LoggedModelCall[] {
  const path = join(markerDirectory, "model-calls.jsonl");
  if (!existsSync(path)) {
    return [];
  }
  return readFileSync(path, "utf8")
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as LoggedModelCall)
    .filter((call) => call.firstUserText.includes(token));
}

async function* streamScriptedResponse(
  markerDirectory: string,
  options: LanguageModelCallOptions,
): AsyncGenerator<LanguageModelStreamPart> {
  const prompt = options.prompt;
  const lastUserIndex = findLastUserMessageIndex(prompt);
  const userText = readPromptText(prompt[lastUserIndex]);
  const toolNames = (options.tools ?? []).map((tool) => tool.name);
  const call: LoggedModelCall = {
    firstUserText: readPromptText(prompt.find((message) => message.role === "user")),
    prompt,
    toolNames,
    toolChoice: options.toolChoice ?? null,
  };
  appendFileSync(join(markerDirectory, "model-calls.jsonl"), `${JSON.stringify(call)}\n`);
  const turnNumber = prompt.filter((message) => message.role === "user").length;
  const round = prompt.slice(lastUserIndex + 1).filter((message) => message.role === "assistant").length;
  const toolResults = readToolResultsAfter(prompt, lastUserIndex);
  const [command = "", ...words] = userText.split(" ");
  const toolCallId = (toolName: string) => `call-${turnNumber}-${round}-${toolName}`;
  yield { type: "stream-start", warnings: [] };
  yield { type: "response-metadata", modelId: "scripted-model-2026" };

  const callOnceThenAnswer = function* (
    toolName: string,
    input: Record<string, unknown>,
    label: string,
  ): Generator<LanguageModelStreamPart> {
    const result = toolResults.get(toolName);
    if (result === undefined) {
      yield { type: "tool-call", toolCallId: toolCallId(toolName), toolName, input: JSON.stringify(input) };
      yield finishPart("tool-calls");
      return;
    }
    yield* streamAnswer(`${label}: ${result}`);
  };

  switch (command) {
    case "lookup": {
      const [orderId = "", then] = [words[0], words.slice(1).join(" ")];
      if (toolResults.has("lookup_order") && then === "then fail") {
        throw new Error(`scripted model failure after the lookup of ${orderId}`);
      }
      if (toolResults.has("lookup_order") && then === "then pause") {
        yield* streamPartialText(`partial answer for ${orderId}`);
        await pauseAtModelGate(markerDirectory, options.abortSignal);
      }
      yield* callOnceThenAnswer("lookup_order", { order_id: orderId }, "lookup_order");
      return;
    }
    case "refund":
      yield* callOnceThenAnswer("refund_order", { order_id: words[0] ?? "" }, "refund_order");
      return;
    case "charge":
      yield* callOnceThenAnswer("charge_card", { order_id: words[0] ?? "" }, "charge_card");
      return;
    case "ask": {
      const choicesIndex = words.indexOf("choices");
      const choices = choicesIndex >= 0 ? (words[choicesIndex + 1] ?? "").split(",") : undefined;
      yield* callOnceThenAnswer(
        "ask_user",
        {
          question: `Which color for ${words[0]}?`,
          ...(choices === undefined ? {} : { choices }),
          ...(words.includes("strict") ? { allows_free_text: false } : {}),
        },
        "answer",
      );
      return;
    }
    case "ask-null":
      yield* callOnceThenAnswer(
        "ask_user",
        { question: `Which color for ${words[0]}?`, choices: null, allows_free_text: null },
        "answer",
      );
      return;
    case "stubborn":
      yield {
        type: "tool-call",
        toolCallId: toolCallId("lookup_order"),
        toolName: "lookup_order",
        input: JSON.stringify({ order_id: `${words[0]}-${round}` }),
      };
      yield finishPart("tool-calls");
      return;
    case "wait":
      yield* callOnceThenAnswer(
        "durable_wait",
        { duration_seconds: Number(words[0]), reason: `cool down ${words[1]}` },
        "waited",
      );
      return;
    case "think":
      if (!toolResults.has("lookup_order")) {
        yield { type: "reasoning-start", id: "r1", providerMetadata: { scripted: { itemId: `rs-${words[0]}` } } };
        yield { type: "reasoning-delta", id: "r1", delta: `thinking about ${words[0]}` };
        yield {
          type: "reasoning-end",
          id: "r1",
          providerMetadata: { scripted: { itemId: `rs-${words[0]}`, signature: `sig-${words[0]}`, encrypted: "e+/=" } },
        };
        yield { type: "text-start", id: "t1" };
        yield { type: "text-delta", id: "t1", delta: "Checking the order." };
        yield { type: "text-end", id: "t1" };
        yield {
          type: "tool-call",
          toolCallId: toolCallId("lookup_order"),
          toolName: "lookup_order",
          input: JSON.stringify({ order_id: words[0] }),
          providerMetadata: { scripted: { itemId: `fc-${words[0]}` } },
        };
        yield finishPart("tool-calls");
        return;
      }
      yield* streamAnswer(`thought: ${toolResults.get("lookup_order")}`);
      return;
    case "loop":
      if (toolNames.includes("lookup_order")) {
        yield {
          type: "tool-call",
          toolCallId: toolCallId("lookup_order"),
          toolName: "lookup_order",
          input: JSON.stringify({ order_id: `${words[0]}-${round}` }),
        };
        yield finishPart("tool-calls");
        return;
      }
      yield* streamAnswer(`no tools after ${round} calls`);
      return;
    case "bad":
      yield* callOnceThenAnswer("lookup_order", { order_id: 42 }, "lookup_order");
      return;
    case "slow":
      yield* streamPartialText(`partial text for ${words[0]}`);
      await pauseAtModelGate(markerDirectory, options.abortSignal);
      yield* streamAnswer("slow done");
      return;
    case "history": {
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
    default:
      yield* streamAnswer(`echo: ${userText}`);
  }
}

async function pauseAtModelGate(markerDirectory: string, abortSignal: AbortSignal | undefined): Promise<void> {
  const gate = new FileGate(markerDirectory, "model");
  gate.markReached();
  await gate.waitForRelease(abortSignal);
}

function* streamPartialText(text: string): Generator<LanguageModelStreamPart> {
  yield { type: "text-start", id: "partial" };
  yield { type: "text-delta", id: "partial", delta: `${text}. ` };
  yield { type: "text-end", id: "partial" };
}

function* streamAnswer(text: string): Generator<LanguageModelStreamPart> {
  yield { type: "text-start", id: "answer" };
  yield { type: "text-delta", id: "answer", delta: text };
  yield { type: "text-end", id: "answer" };
  yield finishPart("stop");
}

function finishPart(unified: "stop" | "tool-calls"): LanguageModelStreamPart {
  return {
    type: "finish",
    usage: {
      inputTokens: { ...scriptedStepwiseModelUsage.inputTokens },
      outputTokens: { ...scriptedStepwiseModelUsage.outputTokens },
    },
    finishReason: { unified, raw: unified },
  };
}

function findLastUserMessageIndex(prompt: LanguageModelPrompt): number {
  for (let index = prompt.length - 1; index >= 0; index -= 1) {
    if (prompt[index]?.role === "user") {
      return index;
    }
  }
  throw new Error("the scripted model received no user message");
}

function readToolResultsAfter(prompt: LanguageModelPrompt, userIndex: number): Map<string, string> {
  const toolResults = new Map<string, string>();
  for (const message of prompt.slice(userIndex + 1)) {
    if (message.role !== "tool") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-result") {
        toolResults.set(part.toolName, describeToolResultOutput(part.output));
      }
    }
  }
  return toolResults;
}

function describeToolResultOutput(output: PromptToolResultOutput): string {
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

function readPromptText(message: LanguageModelPromptMessage | undefined): string {
  if (message === undefined) {
    return "";
  }
  if (message.role === "system") {
    return message.content;
  }
  return message.content.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("");
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
