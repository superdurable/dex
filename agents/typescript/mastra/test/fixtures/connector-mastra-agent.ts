// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A Mastra agent whose tools come from a ConnectorToolSource over the fake ledger Connector.
// The scripted model reads `calls <json>` from the last user text, a JSON list of [toolName, arguments] pairs, and
// calls them in one response with IDs call-0, call-1, and so on. Once results arrive it answers one
// `<toolName>: <output>` line per result. A `hold:<gate>` word before `calls` makes that answer wait at a FileGate,
// so a test can read the turn's records before the turn ends.

import type {
  LanguageModelV3,
  LanguageModelV3CallOptions,
  LanguageModelV3GenerateResult,
  LanguageModelV3Prompt,
  LanguageModelV3StreamPart,
  LanguageModelV3StreamResult,
  LanguageModelV3ToolResultOutput,
  LanguageModelV3Usage,
} from "@ai-sdk/provider";
import { Agent } from "@mastra/core/agent";
import { createTool } from "@mastra/core/tools";
import { z } from "zod";

import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import { FileGate } from "@superdurable/dex-agents/testing";

import { createMastraAgentSessionFlow, type MastraAgentSessionOptions } from "../../src/index.js";
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

import type { AgentSessionFlow } from "@superdurable/dex-agents";

export const connectorMastraAgentFlowType = "ConnectorMastraAgent";

const connectorCallUsage: LanguageModelV3Usage = {
  inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
  outputTokens: { total: 5, text: 5, reasoning: 0 },
};

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
  return new ConnectorToolSource(
    [
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
    ],
    sourceName === undefined ? {} : { sourceName },
  );
}

/** The Mastra agent, with one tool of its own next to the source tools. */
export function createConnectorMastraAgent(stateDirectory: string, extraToolNames: readonly string[] = []): Agent {
  const tools: Record<string, ReturnType<typeof createTool>> = {};
  for (const toolName of ["echo_text", ...extraToolNames]) {
    tools[toolName] = createTool({
      id: toolName,
      description: "Echo a text",
      inputSchema: z.object({ text: z.string() }),
      execute: async ({ text }) => text,
    });
  }
  return new Agent({
    id: "connector-agent",
    name: "Connector agent",
    instructions: "Answer with the scripted model.",
    model: new ConnectorLanguageModel(stateDirectory),
    tools,
  });
}

/** Creates the connector agent Flow; tests and the Worker child build it identically. */
export function createConnectorMastraAgentFlow(
  stateDirectory: string,
  options: MastraAgentSessionOptions = {},
): AgentSessionFlow {
  return createMastraAgentSessionFlow(createConnectorMastraAgent(stateDirectory), connectorMastraAgentFlowType, {
    toolPolicies: { echo_text: { effect: "read" } },
    toolSources: [createLedgerToolSource(stateDirectory)],
    ...options,
  });
}

export const collidingConnectorMastraAgentFlowType = "CollidingConnectorMastraAgent";

/** A Flow whose agent has its own `get_balance` tool, which the source also lists. */
export function createCollidingConnectorMastraAgentFlow(stateDirectory: string): AgentSessionFlow {
  return createMastraAgentSessionFlow(
    createConnectorMastraAgent(stateDirectory, ["get_balance"]),
    collidingConnectorMastraAgentFlowType,
    { toolSources: [createLedgerToolSource(stateDirectory)] },
  );
}

/** An AI SDK language model (specification v3) that decides only from its prompt. */
class ConnectorLanguageModel implements LanguageModelV3 {
  public readonly specificationVersion = "v3";
  public readonly provider = "scripted";
  public readonly modelId = "connector-model";
  public readonly supportedUrls = {};

  public constructor(private readonly stateDirectory: string) {}

  public async doGenerate(): Promise<LanguageModelV3GenerateResult> {
    throw new Error("the scripted model only streams");
  }

  public async doStream(options: LanguageModelV3CallOptions): Promise<LanguageModelV3StreamResult> {
    const prompt = options.prompt;
    const lastUserIndex = prompt.map((message) => message.role).lastIndexOf("user");
    let command = readUserText(prompt, lastUserIndex);
    const firstWord = command.split(" ", 1)[0] ?? "";
    const hold = firstWord.startsWith("hold:") ? firstWord.slice("hold:".length) : undefined;
    if (hold !== undefined) {
      command = command.slice(firstWord.length + 1);
    }
    const results = readToolResultsAfter(prompt, lastUserIndex);
    if (results.length > 0 && hold !== undefined) {
      const gate = new FileGate(this.stateDirectory, hold);
      gate.markReached();
      await gate.waitForRelease(options.abortSignal);
    }
    const calls =
      results.length === 0 && command.startsWith("calls ")
        ? (JSON.parse(command.slice("calls ".length)) as [string, Record<string, unknown>][])
        : [];
    const answer = results.length > 0 ? results.join("\n") : `said: ${command}`;
    return {
      stream: new ReadableStream<LanguageModelV3StreamPart>({
        start(controller) {
          controller.enqueue({ type: "stream-start", warnings: [] });
          if (calls.length === 0) {
            controller.enqueue({ type: "text-start", id: "answer" });
            controller.enqueue({ type: "text-delta", id: "answer", delta: answer });
            controller.enqueue({ type: "text-end", id: "answer" });
            controller.enqueue({ type: "finish", usage: connectorCallUsage, finishReason: { unified: "stop", raw: "stop" } });
          } else {
            for (const [index, [toolName, toolArguments]] of calls.entries()) {
              controller.enqueue({
                type: "tool-call",
                toolCallId: `call-${index}`,
                toolName,
                input: JSON.stringify(toolArguments),
              });
            }
            controller.enqueue({
              type: "finish",
              usage: connectorCallUsage,
              finishReason: { unified: "tool-calls", raw: "tool_calls" },
            });
          }
          controller.close();
        },
      }),
    };
  }
}

function readUserText(prompt: LanguageModelV3Prompt, userIndex: number): string {
  const message = prompt[userIndex];
  if (message?.role !== "user") {
    throw new Error("the scripted prompt has no user message");
  }
  return message.content.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("");
}

/** Each tool result after the last user message as `<toolName>: <output>`, in prompt order. */
function readToolResultsAfter(prompt: LanguageModelV3Prompt, userIndex: number): string[] {
  const results: string[] = [];
  for (const message of prompt.slice(userIndex + 1)) {
    if (message.role !== "tool" && message.role !== "assistant") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-result") {
        results.push(`${part.toolName}: ${describeToolResultOutput(part.output)}`);
      }
    }
  }
  return results;
}

function describeToolResultOutput(output: LanguageModelV3ToolResultOutput): string {
  switch (output.type) {
    case "text":
    case "error-text":
      return output.value;
    case "execution-denied":
      return output.reason ?? "execution denied";
    default:
      return JSON.stringify(output.value);
  }
}
