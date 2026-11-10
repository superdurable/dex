// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// An AI SDK agent whose tools come from a ConnectorToolSource over the fake ledger Connector.
// The scripted model reads `calls <json>` from the last user text, a JSON list of [toolName, arguments] pairs, and
// calls them in one response with IDs call-0, call-1, and so on. Once results arrive it answers one
// `<toolName>: <output>` line per result. A `hold:<gate>` word before `calls` makes that answer wait at a FileGate,
// so a test can read the turn's records before the turn ends.

import { tool } from "ai";
import { MockLanguageModelV4 } from "ai/test";
import { z } from "zod";

import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import { FileGate } from "@superdurable/dex-agents/testing";

import { createVercelAiAgentSessionFlow, type AgentSessionFlow } from "../../src/index.js";
import {
  CreatePayoutInputSchema,
  CreatePayoutOperation,
  FakeLedgerProvider,
  GetBalanceInputSchema,
  GetBalanceOperation,
  PostNoteInputSchema,
  PostNoteOperation,
  fakeLedgerConnection,
  fakeLedgerManifest,
} from "./fake-ledger.js";

export const connectorVercelAiAgentFlowType = "ConnectorVercelAiAgent";

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

export function readFakeLedgerManifest(): ConnectorPackageManifest {
  return structuredClone(fakeLedgerManifest) as ConnectorPackageManifest;
}

/** The fake ledger's postNote under a manifest that claims `retry: safe`, so the fenced write runs in CallTool. */
export function retriedNoteManifest(): ConnectorPackageManifest {
  const manifest = structuredClone(fakeLedgerManifest) as { factories: { operationId: string; agent?: Record<string, unknown> }[] };
  for (const factory of manifest.factories) {
    if (factory.operationId === "postNote" && factory.agent !== undefined) {
      factory.agent["retry"] = "safe";
    }
  }
  return manifest as unknown as ConnectorPackageManifest;
}

/** The ledger tools: a Query, a payout without and with approval, a fenced note, and that note retried. */
export function createLedgerToolSource(stateDirectory: string, sourceName?: string): ConnectorToolSource {
  const provider = new FakeLedgerProvider(stateDirectory);
  const payouts = new CreatePayoutOperation(provider);
  const notes = new PostNoteOperation(provider);
  const manifest = readFakeLedgerManifest();
  return new ConnectorToolSource([
    connectorTool(new GetBalanceOperation(provider), fakeLedgerConnection, {
      manifest,
      inputValidator: GetBalanceInputSchema,
      name: "get_balance",
    }),
    connectorTool(payouts, fakeLedgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "create_payout",
      requiresApproval: false,
    }),
    connectorTool(payouts, fakeLedgerConnection, { manifest, inputValidator: CreatePayoutInputSchema, name: "approved_payout" }),
    connectorTool(notes, fakeLedgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "post_note" }),
    connectorTool(notes, fakeLedgerConnection, {
      manifest: retriedNoteManifest(),
      inputValidator: PostNoteInputSchema,
      name: "post_note_retried",
    }),
  ], sourceName === undefined ? {} : { sourceName });
}

/**
 * Creates the connector agent Flow; tests and the Worker child build it identically. Its one AI SDK tool,
 * `echo_text`, shows that source tools sit next to the agent's own tools.
 */
export function createConnectorVercelAiAgentFlow(stateDirectory: string): AgentSessionFlow {
  return createVercelAiAgentSessionFlow(
    {
      model: createConnectorLanguageModel(stateDirectory),
      instructions: "You are a scripted test agent.",
      tools: {
        echo_text: tool({
          description: "Echo a text",
          inputSchema: z.object({ text: z.string() }),
          execute: async ({ text }) => text,
        }),
      },
      toolPolicies: { echo_text: { effect: "read" } },
      toolSources: [createLedgerToolSource(stateDirectory)],
    },
    connectorVercelAiAgentFlowType,
  );
}

function createConnectorLanguageModel(stateDirectory: string): MockLanguageModelV4 {
  return new MockLanguageModelV4({
    provider: "dex-test",
    modelId: "connector-model",
    doStream: async (options) => ({
      stream: readableStreamFromAsyncIterable(streamConnectorResponse(stateDirectory, options)),
    }),
  });
}

async function* streamConnectorResponse(
  stateDirectory: string,
  options: LanguageModelCallOptions,
): AsyncGenerator<LanguageModelStreamPart> {
  const prompt = options.prompt;
  const lastUserIndex = prompt.map((message) => message.role).lastIndexOf("user");
  const lastUserMessage = prompt[lastUserIndex];
  if (lastUserMessage === undefined) {
    throw new Error("the scripted model received no user message");
  }
  let command = readPromptText(lastUserMessage);
  const firstWord = command.split(" ", 1)[0] ?? "";
  const hold = firstWord.startsWith("hold:") ? firstWord.slice("hold:".length) : undefined;
  if (hold !== undefined) {
    command = command.slice(firstWord.length + 1);
  }
  yield { type: "stream-start", warnings: [] };
  const results = readToolResultsAfter(prompt, lastUserIndex);
  if (results.length > 0) {
    if (hold !== undefined) {
      const gate = new FileGate(stateDirectory, hold);
      gate.markReached();
      await gate.waitForRelease(options.abortSignal);
    }
    yield* streamAnswer(results.join("\n"));
    return;
  }
  if (!command.startsWith("calls ")) {
    yield* streamAnswer(`said: ${command}`);
    return;
  }
  const calls = JSON.parse(command.slice("calls ".length)) as [string, Record<string, unknown>][];
  for (const [index, [toolName, toolArguments]] of calls.entries()) {
    yield { type: "tool-call", toolCallId: `call-${index}`, toolName, input: JSON.stringify(toolArguments) };
  }
  yield finishPart("tool-calls");
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
      inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
      outputTokens: { total: 5, text: 5, reasoning: 0 },
    },
    finishReason: { unified: unifiedFinishReason, raw: unifiedFinishReason },
  };
}

/** Each tool result after the last user message as `<toolName>: <output>`, in prompt order. */
function readToolResultsAfter(prompt: LanguageModelPrompt, userIndex: number): string[] {
  const results: string[] = [];
  for (const message of prompt.slice(userIndex + 1)) {
    if (message.role !== "tool") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-result") {
        results.push(`${part.toolName}: ${describePromptToolResultOutput(part.output)}`);
      }
    }
  }
  return results;
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
