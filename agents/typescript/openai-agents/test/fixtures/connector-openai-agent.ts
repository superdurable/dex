// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// An OpenAI Agents SDK agent whose tools come from a ConnectorToolSource over the fake ledger Connector.
// The scripted model reads `calls <json>` from the last user text, a JSON list of [toolName, arguments] pairs, and
// calls them in one response with IDs call-0, call-1, and so on. Once results arrive it answers one
// `<toolName>: <output>` line per result. A `hold:<gate>` word before `calls` makes that answer wait at a FileGate,
// so a test can read the turn's records before the turn ends.

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

import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import { FileGate } from "@superdurable/dex-agents/testing";

import { createOpenAIAgentsSessionFlow, type AgentSessionFlow } from "../../src/index.js";
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

export const connectorOpenAIAgentFlowType = "ConnectorOpenAIAgent";

const scriptedUsage = {
  requests: 1,
  inputTokens: 10,
  outputTokens: 5,
  totalTokens: 15,
  inputTokensDetails: { cached_tokens: 0 },
  outputTokensDetails: { reasoning_tokens: 0 },
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

/** The starting agent, with one function tool next to the source tools. */
export function createConnectorStartingAgent(stateDirectory: string): Agent {
  return new Agent({
    name: "Connector agent",
    instructions: "You are a scripted test agent.",
    model: new ConnectorScriptedModel(stateDirectory),
    tools: [
      tool({
        name: "echo_text",
        description: "Echo a text.",
        parameters: z.object({ text: z.string() }),
        errorFunction: null,
        execute: async ({ text }) => text,
      }),
    ],
  });
}

/** Creates the connector agent Flow; tests and the Worker child build it identically. */
export function createConnectorOpenAIAgentFlow(stateDirectory: string): AgentSessionFlow {
  return createOpenAIAgentsSessionFlow(createConnectorStartingAgent(stateDirectory), connectorOpenAIAgentFlowType, {
    toolPolicies: { echo_text: { effect: "read" } },
    toolSources: [createLedgerToolSource(stateDirectory)],
  });
}

/** A deterministic `Model` that decides only from the request input. */
class ConnectorScriptedModel implements Model {
  public constructor(private readonly stateDirectory: string) {}

  public async getResponse(request: ModelRequest): Promise<ModelResponse> {
    const output = await this.decideOutput(request);
    return { usage: new Usage(scriptedUsage), output, responseId: `resp_connector_${readInput(request).length}` };
  }

  public async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    yield { type: "response_started" };
    const output = await this.decideOutput(request);
    for (const item of output) {
      if (item.type === "message" && item.role === "assistant") {
        for (const part of item.content) {
          if (part.type === "output_text") {
            yield { type: "output_text_delta", delta: part.text };
          }
        }
      }
    }
    yield {
      type: "response_done",
      response: { id: `resp_connector_${readInput(request).length}`, usage: scriptedUsage, output },
    };
  }

  private async decideOutput(request: ModelRequest): Promise<protocol.OutputModelItem[]> {
    const input = readInput(request);
    const lastUserIndex = input.map((item) => isUserMessage(item)).lastIndexOf(true);
    let command = readText(input[lastUserIndex]);
    const firstWord = command.split(" ", 1)[0] ?? "";
    const hold = firstWord.startsWith("hold:") ? firstWord.slice("hold:".length) : undefined;
    if (hold !== undefined) {
      command = command.slice(firstWord.length + 1);
    }
    const results = input
      .slice(lastUserIndex + 1)
      .flatMap((item) =>
        item.type === "function_call_result" ? [`${item.name}: ${readFunctionCallOutput(item.output)}`] : [],
      );
    if (results.length > 0) {
      if (hold !== undefined) {
        const gate = new FileGate(this.stateDirectory, hold);
        gate.markReached();
        await gate.waitForRelease(request.signal);
      }
      return [answerItem(results.join("\n"))];
    }
    if (!command.startsWith("calls ")) {
      return [answerItem(`said: ${command}`)];
    }
    const calls = JSON.parse(command.slice("calls ".length)) as [string, Record<string, unknown>][];
    return calls.map(([toolName, toolArguments], index) => ({
      type: "function_call",
      id: `fc_call_${index}`,
      callId: `call-${index}`,
      name: toolName,
      arguments: JSON.stringify(toolArguments),
      status: "completed",
    }));
  }
}

function answerItem(text: string): protocol.OutputModelItem {
  return {
    type: "message",
    id: "msg_connector",
    role: "assistant",
    status: "completed",
    content: [{ type: "output_text", text }],
  };
}

function readInput(request: ModelRequest): AgentInputItem[] {
  return typeof request.input === "string" ? [{ type: "message", role: "user", content: request.input }] : request.input;
}

function isUserMessage(item: AgentInputItem): boolean {
  return (item.type === "message" || item.type === undefined) && "role" in item && item.role === "user";
}

function readText(item: AgentInputItem | undefined): string {
  if (item === undefined || !("content" in item)) {
    return "";
  }
  const content: unknown = item.content;
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content.map((part: { text?: unknown }) => (typeof part.text === "string" ? part.text : "")).join("");
}

function readFunctionCallOutput(output: unknown): string {
  if (typeof output === "string") {
    return output;
  }
  if (typeof output === "object" && output !== null && "text" in output && typeof output.text === "string") {
    return output.text;
  }
  return JSON.stringify(output);
}
