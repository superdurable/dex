// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Connector tools served by the Vercel AI adapter through a ConnectorToolSource, on a real Dex engine with a
// Worker process per test and the fake ledger Connector.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { tool } from "ai";
import { z } from "zod";

import {
  outcomeUnknownToolResult,
  toolCallNotRunErrorPrefix,
  toolCallSkippedAfterWriteDetail,
  type AgentToolCall,
  type AgentToolCallRecord,
} from "@superdurable/dex-agents";
import { connectorTool } from "@superdurable/dex-agents/connectors";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "@superdurable/dex-agents/testing";

import { createVercelAiAgentSessionFlow } from "../../src/index.js";
import {
  createConnectorVercelAiAgentFlow,
  createLedgerToolSource,
  readFakeLedgerManifest,
} from "../fixtures/connector-vercel-ai-agent.js";
import {
  CloseAccountInputSchema,
  CloseAccountOperation,
  FakeLedgerProvider,
  fakeLedgerConnection,
  fakeLedgerStateDirectoryVariable,
} from "../fixtures/fake-ledger.js";
import { createScriptedLanguageModel } from "../fixtures/scripted-language-model.js";

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/connector-vercel-ai-agent-worker.js", import.meta.url),
);
const skippedError = `${toolCallNotRunErrorPrefix}: ${toolCallSkippedAfterWriteDetail}`;
const failed = "The tool call failed: ";

type ToolArguments = Readonly<Record<string, unknown>>;

describe("connector tools refused when the Vercel AI agent is defined", () => {
  const stateDirectory = mkdtempSync(join(tmpdir(), "vercel-ai-connector-definition-"));

  test("an operation the manifest does not expose to agents is refused", () => {
    assert.throws(
      () =>
        connectorTool(new CloseAccountOperation(), fakeLedgerConnection, {
          manifest: readFakeLedgerManifest(),
          inputValidator: CloseAccountInputSchema,
        }),
      TypeError,
    );
  });

  test("a source tool that repeats an AI SDK tool name is refused", () => {
    assert.throws(
      () =>
        createVercelAiAgentSessionFlow(
          {
            model: createScriptedLanguageModel(stateDirectory),
            tools: {
              get_balance: tool({
                description: "A local balance tool",
                inputSchema: z.object({ accountId: z.string() }),
                execute: async () => "local",
              }),
            },
            toolSources: [createLedgerToolSource(stateDirectory)],
          },
          "CollidingConnectorAgent",
        ),
      { name: "TypeError", message: "tool get_balance of a tool source uses the name of an AI SDK tool of the agent" },
    );
  });

  test("two sources that declare one tool name are refused", () => {
    assert.throws(
      () =>
        createVercelAiAgentSessionFlow(
          {
            model: createScriptedLanguageModel(stateDirectory),
            toolSources: [createLedgerToolSource(stateDirectory), createLedgerToolSource(stateDirectory, "second")],
          },
          "TwoSourcesConnectorAgent",
        ),
      { name: "TypeError", message: "two tool sources list a tool named approved_payout" },
    );
  });
});

describe("connector tools in a Vercel AI agent session on a Dex engine", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createConnectorVercelAiAgentFlow>;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = createConnectorVercelAiAgentFlow(join(engine.stateDirectory, "client-unused-ledger"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  interface Harness {
    readonly ledger: FakeLedgerProvider;
    gate(name: string): FileGate;
  }

  async function startHarness(): Promise<Harness> {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "connector-"));
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(stateDirectory, `worker-${workers.length}.log`),
      environment: { [fakeLedgerStateDirectoryVariable]: stateDirectory },
    });
    workers.push(worker);
    return { ledger: new FakeLedgerProvider(stateDirectory), gate: (name) => new FileGate(stateDirectory, name) };
  }

  function calls(entries: readonly (readonly [string, ToolArguments])[], hold?: string): string {
    const command = `calls ${JSON.stringify(entries)}`;
    return hold === undefined ? command : `hold:${hold} ${command}`;
  }

  async function answerLines(sessionId: string, turnCount: number): Promise<string[]> {
    const state = await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    return ((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "").split("\n");
  }

  async function heldRecords(
    harness: Harness,
    sessionId: string,
    gate: string,
    count: number,
  ): Promise<AgentToolCallRecord[]> {
    await harness.gate(gate).waitUntilReached();
    const records: AgentToolCallRecord[] = [];
    for (let index = 0; index < count; index += 1) {
      const record = await sessions.readToolCall(sessionId, `call-${index}`);
      assert.ok(record !== undefined);
      records.push(record);
    }
    harness.gate(gate).release();
    return records;
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  test("a connector Query runs in its own CallTool Step with the operation's Step options", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["get_balance", { accountId: "a-1" }]], "query"));
    const [record] = await heldRecords(harness, sessionId, "query", 1);
    assert.equal(record?.effect, "read");
    assert.equal(record?.status, "succeeded");
    const envelope = JSON.parse(record?.toolSourceReference ?? "{}") as Record<string, unknown>;
    assert.equal(envelope["vercel_ai_tool_source"], "connectors");
    assert.deepEqual(flow.runner.toolCallStepOptions?.(record as AgentToolCall), {
      executeMethodTimeout: 30_000,
      executeRetry: {
        initialIntervalMs: 1_000,
        backoffCoefficient: 2,
        maximumIntervalMs: 300_000,
        maximumAttempts: 3,
        totalDurationMs: 3_600_000,
      },
    });
    assert.deepEqual(await answerLines(sessionId, 1), ['get_balance: {"accountId":"a-1","amount":100}']);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a tool under its default connector name runs through its source, and a policy changes only approval", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["fake-ledger_get_balance", { accountId: "a-2" }]], "default-name"));
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    const waiting = await sessions.readToolCall(sessionId, "call-0");
    assert.equal(waiting?.effect, "read");
    assert.equal(waiting?.requiresApproval, true);
    assert.deepEqual(harness.ledger.sends(), []);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const [record] = await heldRecords(harness, sessionId, "default-name", 1);
    assert.equal(record?.toolName, "fake-ledger_get_balance");
    assert.equal(record?.status, "succeeded");
    assert.deepEqual(await answerLines(sessionId, 1), ['fake-ledger_get_balance: {"accountId":"a-2","amount":100}']);
  });

  test("an approved Mutation runs once after its approval", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["approved_payout", { accountId: "a-1", amount: 5 }]]));
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    assert.equal(interrupt.toolName, "approved_payout");
    assert.deepEqual(harness.ledger.sends(), []);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    assert.deepEqual(await answerLines(sessionId, 1), ['approved_payout: {"amount":5,"payoutId":"createPayout-1"}']);
    assert.equal(harness.ledger.writes("createPayout").length, 1);
    assert.equal(harness.ledger.sends("createPayout").length, 1);
  });

  test("a retry: safe Mutation retries under the same Call ID and writes once", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([["create_payout", { accountId: "a-1", amount: 5, memo: "rate-limit-once" }]]),
    );
    assert.deepEqual(await answerLines(sessionId, 1), ['create_payout: {"amount":5,"payoutId":"createPayout-1"}']);
    assert.equal(harness.ledger.writes("createPayout").length, 1);
    const rateLimited = harness.ledger.sends("rate-limited");
    assert.equal(rateLimited.length, 1);
    assert.deepEqual(
      harness.ledger.sends("createPayout").map((send) => send.key),
      [rateLimited[0]?.key],
    );
  });

  test("a fenced write retried after its notSent marker reads the marker back and sends once", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([["post_note_retried", { accountId: "a-1", text: "rate-limit-once" }]]),
    );
    assert.deepEqual(await answerLines(sessionId, 1), ['post_note_retried: {"noteId":"postNote-1"}']);
    assert.equal(harness.ledger.writes("postNote").length, 1);
    assert.equal(harness.ledger.sends("postNote").length, 1);
  });

  test("an uncertain Mutation records outcome_unknown and the model reads that", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([["create_payout", { accountId: "a-1", amount: 5, memo: "uncertain" }]], "unknown"),
    );
    const [record] = await heldRecords(harness, sessionId, "unknown", 1);
    assert.equal(record?.status, "outcome_unknown");
    assert.deepEqual(await answerLines(sessionId, 1), [`create_payout: ${outcomeUnknownToolResult}`]);
    assert.equal(harness.ledger.writes("createPayout").length, 1);
  });

  test("two writes of one response run in the model's order, and a failed write skips the later one", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([
        ["post_note", { accountId: "a-1", text: "first gate-after-accept:first-write" }],
        ["get_balance", { accountId: "a-1" }],
        ["create_payout", { accountId: "a-1", amount: 5 }],
      ]),
    );
    await harness.gate("first-write").waitUntilReached();
    const read = await sessions.waitForToolCall(sessionId, "call-1", (record) => record.status === "succeeded");
    assert.equal(read.status, "succeeded");
    const queued = await sessions.readToolCall(sessionId, "call-2");
    assert.equal(queued?.orderedWrite, true);
    assert.equal(queued?.writeQueued, true);
    assert.deepEqual(harness.ledger.writes("createPayout"), []);
    harness.gate("first-write").release();
    const lines = await answerLines(sessionId, 1);
    assert.deepEqual(
      harness.ledger.writes().map((entry) => entry.operation),
      ["postNote", "createPayout"],
    );
    assert.equal(lines[2], 'create_payout: {"amount":5,"payoutId":"createPayout-2"}');

    await sessions.sendUserMessage(
      sessionId,
      calls([
        ["create_payout", { accountId: "a-1", amount: 5, memo: "reject" }],
        ["post_note", { accountId: "a-1", text: "after the payout" }],
      ]),
    );
    assert.deepEqual(await answerLines(sessionId, 2), [
      `create_payout: ${failed}fake-ledger createPayout returned providerRejected: PROVIDER_REJECTION: insufficient funds`,
      `post_note: ${failed}${skippedError}`,
    ]);
    assert.equal(harness.ledger.writes().length, 2);
  });

  test("a call of an operation the source does not offer never reaches the provider", async () => {
    const harness = await startHarness();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["fake_ledger_close_account", { accountId: "a-1" }]]));
    const lines = await answerLines(sessionId, 1);
    // The AI SDK answers a call of a tool it was not offered with a tool error inside the model call.
    assert.match(lines[0] ?? "", /^fake_ledger_close_account: .*unavailable tool/);
    assert.deepEqual(harness.ledger.sends(), []);
    const stepTypes = await executedStepTypes(sessionId);
    assert.equal(stepTypes.includes("CallTool") || stepTypes.includes("CallSingleAttemptTool"), false);
  });
});
