// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A Stepwise agent whose tools are the fake ledger Connector's operations, the twin of the Python core's
// tests/connector_agent.py.
//
// The scripted model reads `calls <json>` from the user text, a JSON list of [toolName, arguments] pairs, and
// calls them in one response with IDs call-0, call-1, and so on. Once results arrive it answers one
// `<toolName>: <output>` line per result. A `hold:<gate>` word before `calls` makes that answer wait at a
// FileGate, so a test can read the turn's records before the turn ends. post_note_retried is postNote under a
// manifest that claims retry: safe, so the fenced write runs in CallTool with retries. payout_checked is
// createPayout with an approval predicate: amounts above 50 wait for approval. send_message is the limited
// sendMessage Mutation, a bounded_write; send_message_capped is the same tool under a tool policy that raises its
// send cap to 4, and send_message_unreviewed runs it under a manifest from before repeatOnUnknown.

import type { ConnectionRef } from "@superdurable/connectors-sdk";

import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "../../src/connectors/index.js";
import type { AgentSessionFlow } from "../../src/index.js";
import { FileGate } from "../../src/testing/index.js";
import {
  ScriptedModelClient,
  createBuiltinAgentSessionFlow,
  type ModelRequest,
  type ScriptedModelReply,
  type BuiltinLoopAgentRunner,
} from "../../src/builtin-loop/index.js";
import {
  CreatePayoutInputSchema,
  CreatePayoutOperation,
  FakeLedgerProvider,
  GetBalanceByIdOperation,
  GetBalanceInputSchema,
  GetBalanceOperation,
  PostNoteInputSchema,
  PostNoteOperation,
  SendMessageInputSchema,
  SendMessageOperation,
  fakeLedgerConnection,
  fakeLedgerManifest,
} from "./fake-ledger.js";

export const connectorAgentFlowType = "ConnectorAgent";
export const connectorAgentMaxResultBytesVariable = "CONNECTOR_AGENT_MAX_RESULT_BYTES";
export const connectorAgentDroppedToolsVariable = "CONNECTOR_AGENT_DROPPED_TOOLS";
export const ledgerConnection: ConnectionRef = fakeLedgerConnection;
// The name dex-agents 0.4.0 gave getBalanceByID; 0.5.0 defaults to fake-ledger_get_balance_by_id.
export const previousByIdToolName = "fake-ledger_get_balance_by_i_d";

export function readFakeLedgerManifest(): ConnectorPackageManifest {
  return structuredClone(fakeLedgerManifest) as ConnectorPackageManifest;
}

export function retriedNoteManifest(): ConnectorPackageManifest {
  const manifest = structuredClone(fakeLedgerManifest) as { factories: { operationId: string; agent?: Record<string, unknown> }[] };
  for (const factory of manifest.factories) {
    if (factory.operationId === "postNote" && factory.agent !== undefined) {
      factory.agent["retry"] = "safe";
    }
  }
  return manifest as unknown as ConnectorPackageManifest;
}

/** The fake ledger manifest as a Connector package built before repeatOnUnknown publishes it. */
export function unreviewedSendManifest(): ConnectorPackageManifest {
  const manifest = structuredClone(fakeLedgerManifest) as { factories: { operationId: string; agent?: Record<string, unknown> }[] };
  for (const factory of manifest.factories) {
    if (factory.operationId === "sendMessage" && factory.agent !== undefined) {
      delete factory.agent["repeatOnUnknown"];
      delete factory.agent["maxSends"];
    }
  }
  return manifest as unknown as ConnectorPackageManifest;
}

/** The tool policy that raises send_message_capped's send cap from the manifest's 3 to 4. */
export const cappedSendToolPolicies = { send_message_capped: { effect: "bounded_write", maxSends: 4 } } as const;

export function byIdManifest(): ConnectorPackageManifest {
  const manifest = structuredClone(fakeLedgerManifest) as { factories: { operationId: string }[] };
  const factory = structuredClone(manifest.factories.find((entry) => entry.operationId === "getBalance"));
  if (factory === undefined) {
    throw new Error("the fake ledger manifest has no getBalance entry");
  }
  manifest.factories.push({ ...factory, operationId: "getBalanceByID" });
  return manifest as unknown as ConnectorPackageManifest;
}

export function createConnectorSource(
  stateDirectory: string,
  maxResultBytes?: number,
  droppedTools: ReadonlySet<string> = new Set(),
): ConnectorToolSource {
  const provider = new FakeLedgerProvider(stateDirectory);
  const payouts = new CreatePayoutOperation(provider);
  const notes = new PostNoteOperation(provider);
  const messages = new SendMessageOperation(provider);
  const manifest = readFakeLedgerManifest();
  const tools = [
    connectorTool(new GetBalanceOperation(provider), ledgerConnection, {
      manifest,
      inputValidator: GetBalanceInputSchema,
      name: "get_balance",
    }),
    connectorTool(payouts, ledgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "create_payout",
      requiresApproval: false,
    }),
    connectorTool(payouts, ledgerConnection, { manifest, inputValidator: CreatePayoutInputSchema, name: "approved_payout" }),
    connectorTool(payouts, ledgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "payout_checked",
      requiresApproval: (toolArguments) => Number(toolArguments["amount"]) > 50,
    }),
    connectorTool(notes, ledgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "post_note" }),
    connectorTool(notes, ledgerConnection, {
      manifest: retriedNoteManifest(),
      inputValidator: PostNoteInputSchema,
      name: "post_note_retried",
    }),
    connectorTool(messages, ledgerConnection, { manifest, inputValidator: SendMessageInputSchema, name: "send_message" }),
    connectorTool(messages, ledgerConnection, {
      manifest,
      inputValidator: SendMessageInputSchema,
      name: "send_message_capped",
    }),
    connectorTool(messages, ledgerConnection, {
      manifest: unreviewedSendManifest(),
      inputValidator: SendMessageInputSchema,
      name: "send_message_unreviewed",
    }),
    connectorTool(new GetBalanceByIdOperation(provider), ledgerConnection, {
      manifest: byIdManifest(),
      inputValidator: GetBalanceInputSchema,
      name: previousByIdToolName,
      requiresApproval: true,
    }),
  ].filter((tool) => !droppedTools.has(tool.name));
  return new ConnectorToolSource(tools, maxResultBytes === undefined ? {} : { maxResultBytes });
}

export function createConnectorAgentFlow(
  stateDirectory: string,
  maxResultBytes?: number,
  droppedTools: ReadonlySet<string> = new Set(),
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const reply = async (request: ModelRequest): Promise<ScriptedModelReply> => {
    let command = ScriptedModelClient.userTexts(request).at(-1) ?? "";
    const firstWord = command.split(" ", 1)[0] ?? "";
    const hold = firstWord.startsWith("hold:") ? firstWord.slice("hold:".length) : undefined;
    if (hold !== undefined) {
      command = command.slice(firstWord.length + 1);
    }
    const results = ScriptedModelClient.toolResults(request);
    if (results.length > 0) {
      if (hold !== undefined) {
        const gate = new FileGate(stateDirectory, hold);
        gate.markReached();
        await gate.waitForRelease();
      }
      return { text: results.map((result) => `${result.toolName}: ${result.output}`).join("\n") };
    }
    if (!command.startsWith("calls ")) {
      return { text: `said: ${command}` };
    }
    const calls = JSON.parse(command.slice("calls ".length)) as [string, Record<string, unknown>][];
    return {
      toolCalls: calls.map(([toolName, toolArguments], index) => ({
        toolName,
        arguments: JSON.stringify(toolArguments),
        toolCallId: `call-${index}`,
      })),
    };
  };
  return createBuiltinAgentSessionFlow(
    {
      name: "connector-agent",
      instructions: "You are a scripted test agent.",
      model: new ScriptedModelClient(reply),
      toolSources: [createConnectorSource(stateDirectory, maxResultBytes, droppedTools)],
      toolPolicies: cappedSendToolPolicies,
    },
    connectorAgentFlowType,
  );
}
