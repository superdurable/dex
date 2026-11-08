// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Connector tools on a real Dex engine: mapping, validation, ordering, approval, result size, and Worker kills.
// The twin of the Python core's tests/test_connector_tools.py and tests/test_connector_tool_crashes.py.

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { after, afterEach, before, describe, test } from "node:test";

import { connectorResultTruncatedNote } from "../../src/connectors/index.js";
import {
  outcomeUnknownToolResult,
  toolCallNotRunErrorPrefix,
  toolCallSkippedAfterWriteDetail,
  type AgentToolCallRecord,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
} from "../../src/testing/index.js";
import {
  connectorAgentDroppedToolsVariable,
  connectorAgentMaxResultBytesVariable,
  createConnectorAgentFlow,
} from "../fixtures/connector-test-agent.js";
import {
  FakeLedgerProvider,
  fakeLedgerSecretVariable,
  fakeLedgerStateDirectoryVariable,
} from "../fixtures/fake-ledger.js";
import { convertAgentSnapshotToJson } from "../../src/ui-stream/index.js";

const execFileAsync = promisify(execFile);

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/connector-test-agent-worker.js", import.meta.url));
const skippedError = `${toolCallNotRunErrorPrefix}: ${toolCallSkippedAfterWriteDetail}`;
const failed = "The tool call failed: ";

type ToolArguments = Readonly<Record<string, unknown>>;

describe("connector tools on a Dex engine", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createConnectorAgentFlow(join(engine.stateDirectory, "client-unused-ledger"));
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
    readonly stateDirectory: string;
    readonly ledger: FakeLedgerProvider;
    gate(name: string): FileGate;
    startWorker(environmentVariables?: Readonly<Record<string, string>>): Promise<KillableWorkerProcess>;
  }

  function newHarness(): Harness {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "connector-"));
    return {
      stateDirectory,
      ledger: new FakeLedgerProvider(stateDirectory),
      gate: (name) => new FileGate(stateDirectory, name),
      startWorker: async (environmentVariables = {}) => {
        const worker = await KillableWorkerProcess.start({
          entryModulePath: workerEntryModulePath,
          serverAddress: engine.serverAddress,
          logPath: join(stateDirectory, `worker-${workers.length}.log`),
          environment: { [fakeLedgerStateDirectoryVariable]: stateDirectory, ...environmentVariables },
        });
        workers.push(worker);
        return worker;
      },
    };
  }

  function calls(entries: readonly (readonly [string, ToolArguments])[], hold?: string): string {
    const command = `calls ${JSON.stringify(entries)}`;
    return hold === undefined ? command : `hold:${hold} ${command}`;
  }

  async function answerLines(sessionId: string, turnCount: number): Promise<string[]> {
    await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 120_000 });
    return ((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "").split("\n");
  }

  async function runTurn(entries: readonly (readonly [string, ToolArguments])[]): Promise<[string, string[]]> {
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls(entries));
    return [sessionId, await answerLines(sessionId, 1)];
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

  test("a query tool returns its result to the model", async () => {
    await newHarness().startWorker();
    const [, lines] = await runTurn([["get_balance", { accountId: "a-1" }]]);
    assert.deepEqual(lines, ['get_balance: {"accountId":"a-1","amount":100}']);
  });

  test("each branch kind maps to its record and the session continues", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls(
        [
          ["get_balance", { accountId: "missing" }],
          ["get_balance", { accountId: "garbled" }],
          ["create_payout", { accountId: "a-1", amount: 5, memo: "reject" }],
        ],
        "mapped",
      ),
    );
    const records = await heldRecords(harness, sessionId, "mapped", 3);
    assert.deepEqual(
      records.map((record) => record.status),
      ["failed", "failed", "failed"],
    );
    assert.equal(records[0]?.error, "fake-ledger getBalance returned notFound: PROVIDER_REJECTION: no account");
    assert.equal(records[1]?.error, "fake-ledger getBalance returned invalidResponse: PROVIDER_REJECTION: bad body");
    assert.equal(
      records[2]?.error,
      "fake-ledger createPayout returned providerRejected: PROVIDER_REJECTION: insufficient funds",
    );
    assert.equal(records[2]?.skipped, false);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    // An unlisted optional branch of a Mutation and uncertain both fail closed.
    await sessions.sendUserMessage(sessionId, calls([["create_payout", { accountId: "a-1", amount: 5, memo: "invalid" }]], "unknown"));
    const [unknown] = await heldRecords(harness, sessionId, "unknown", 1);
    assert.equal(unknown?.status, "outcome_unknown");
    assert.equal(unknown?.error, "fake-ledger createPayout returned invalidResponse: PROVIDER_REJECTION: bad body");
    assert.deepEqual(await answerLines(sessionId, 2), [`create_payout: ${outcomeUnknownToolResult}`]);
    assert.equal(harness.ledger.writes("createPayout").length, 1);

    await sessions.sendUserMessage(sessionId, calls([["create_payout", { accountId: "a-1", amount: 5, memo: "uncertain" }]]));
    assert.deepEqual(await answerLines(sessionId, 3), [`create_payout: ${outcomeUnknownToolResult}`]);
    assert.equal((await sessions.readAgentState(sessionId))?.lastTurnError, null);
  });

  test("arguments that break the schema fail without running the operation", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const [, lines] = await runTurn([
      ["get_balance", { accountId: 7 }],
      ["get_balance", { accountId: "a-1", acountId: "typo" }],
      ["create_payout", { accountId: "a-1" }],
    ]);
    assert.deepEqual(lines, [
      `get_balance: ${failed}invalid arguments for get_balance: arguments.accountId must be string`,
      `get_balance: ${failed}invalid arguments for get_balance: arguments.acountId is not allowed`,
      `create_payout: ${failed}invalid arguments for create_payout: arguments.amount is required`,
    ]);
    assert.deepEqual(harness.ledger.sends(), []);
  });

  test("writes run in the model order and reads run in parallel", async () => {
    const harness = newHarness();
    await harness.startWorker();
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
    assert.equal(queued?.writeQueued, true);
    assert.equal(queued?.orderedWrite, true);
    assert.deepEqual(harness.ledger.writes("createPayout"), []);
    harness.gate("first-write").release();
    const lines = await answerLines(sessionId, 1);
    assert.deepEqual(
      harness.ledger.writes().map((entry) => entry.operation),
      ["postNote", "createPayout"],
    );
    assert.equal(lines[2], 'create_payout: {"amount":5,"payoutId":"createPayout-2"}');
  });

  test("a failed or unknown write skips the later writes of the response", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls(
        [
          ["create_payout", { accountId: "a-1", amount: 5, memo: "reject" }],
          ["get_balance", { accountId: "a-1" }],
          ["post_note", { accountId: "a-1", text: "refunded" }],
          ["create_payout", { accountId: "a-1", amount: 7 }],
        ],
        "skipped",
      ),
    );
    const records = await heldRecords(harness, sessionId, "skipped", 4);
    assert.equal(records[1]?.status, "succeeded");
    for (const skipped of [records[2], records[3]]) {
      assert.equal(skipped?.status, "failed");
      assert.equal(skipped?.skipped, true);
      assert.equal(skipped?.writeQueued, false);
      assert.equal(skipped?.error, skippedError);
    }
    assert.equal(records[0]?.skipped, false);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.deepEqual(harness.ledger.sends(), []);

    await sessions.sendUserMessage(
      sessionId,
      calls([
        ["create_payout", { accountId: "a-1", amount: 5, memo: "uncertain" }],
        ["post_note", { accountId: "a-1", text: "paid" }],
      ]),
    );
    assert.deepEqual(await answerLines(sessionId, 2), [
      `create_payout: ${outcomeUnknownToolResult}`,
      `post_note: ${failed}${skippedError}`,
    ]);
    assert.deepEqual(harness.ledger.writes("postNote"), []);
  });

  test("the manifest approval and an override both apply", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["approved_payout", { accountId: "a-1", amount: 5 }]]));
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    assert.equal(interrupt.toolName, "approved_payout");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(harness.ledger.writes("createPayout").length, 1);

    // create_payout overrides the manifest's recommendation; post_note has none.
    await sessions.sendUserMessage(
      sessionId,
      calls([
        ["create_payout", { accountId: "a-1", amount: 5 }],
        ["post_note", { accountId: "a-1", text: "done" }],
      ]),
    );
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    assert.deepEqual(await sessions.readPendingInterrupts(sessionId), []);
    assert.equal(harness.ledger.writes().length, 3);
  });

  test("an argument predicate decides approval per call", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const [sessionId] = await runTurn([["payout_checked", { accountId: "a-1", amount: 10 }]]);
    assert.equal(harness.ledger.writes("createPayout").length, 1);

    await sessions.sendUserMessage(sessionId, calls([["payout_checked", { accountId: "a-1", amount: 100 }]]));
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    assert.equal(interrupt.toolName, "payout_checked");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "too much"), true);
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    assert.equal(harness.ledger.writes("createPayout").length, 1);
  });

  test("a queued write asks for approval only when its turn comes", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([
        ["post_note", { accountId: "a-1", text: "first gate-after-accept:note" }],
        ["approved_payout", { accountId: "a-1", amount: 5 }],
        ["post_note", { accountId: "a-1", text: "after the payout" }],
      ]),
    );
    await harness.gate("note").waitUntilReached();
    assert.deepEqual(await sessions.readPendingInterrupts(sessionId), []);
    harness.gate("note").release();
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    assert.equal(interrupt.toolCallId, "call-1");
    assert.equal(harness.ledger.writes("postNote").length, 1);

    // A rejected approval stops the later writes too.
    assert.equal(await sessions.resolveInterrupt(sessionId, "call-1", false), true);
    assert.equal((await answerLines(sessionId, 1))[2], `post_note: ${failed}${skippedError}`);
    assert.equal(harness.ledger.writes().length, 1);
  });

  test("cancel_turn cancels the queued writes", async () => {
    const harness = newHarness();
    await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      calls([
        ["post_note", { accountId: "a-1", text: "first gate-after-accept:cancel" }],
        ["create_payout", { accountId: "a-1", amount: 5 }],
      ]),
    );
    await harness.gate("cancel").waitUntilReached();
    assert.equal(await sessions.cancelTurn(sessionId), true);
    harness.gate("cancel").release();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.deepEqual(
      harness.ledger.writes().map((entry) => entry.operation),
      ["postNote"],
    );
  });

  test("a long result reaches the model truncated and the record says so", async () => {
    const harness = newHarness();
    await harness.startWorker({ [connectorAgentMaxResultBytesVariable]: "40" });
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["get_balance", { accountId: "a-1", padding: 100 }]], "big"));
    const [record] = await heldRecords(harness, sessionId, "big", 1);
    const full = JSON.stringify({ accountId: "a-1", amount: 100, filler: "x".repeat(100) });
    const note = connectorResultTruncatedNote.replace("{total_bytes}", String(full.length));
    assert.equal(record?.output, `${full.slice(0, 40)}\n${note}`);
    assert.equal(record?.truncatedOutputBytes, full.length);
  });

  test("a removed tool ends its stored call by its effect class", async () => {
    const harness = newHarness();
    const worker = await harness.startWorker();
    const sessionId = `connector-${randomUUID()}`;
    await sessions.startSession(sessionId, calls([["approved_payout", { accountId: "a-1", amount: 5 }]]));
    await sessions.waitForPendingInterrupt(sessionId);
    await worker.kill();
    await harness.startWorker({ [connectorAgentDroppedToolsVariable]: "approved_payout" });
    assert.equal(await sessions.resolveInterrupt(sessionId, "call-0", true), true);
    // approved_payout is idempotent_write: retries run out and the write may have run.
    assert.deepEqual(await answerLines(sessionId, 1), [`approved_payout: ${outcomeUnknownToolResult}`]);
    assert.deepEqual(harness.ledger.sends(), []);
  });

  test("a connection secret never reaches session state, records, streams, or history", async () => {
    const harness = newHarness();
    const secret = `ledger-secret-${randomUUID()}`;
    await harness.startWorker({ [fakeLedgerSecretVariable]: secret });
    const sessionId = `connector-secret-${randomUUID()}`;
    const seen: string[] = [];
    const snapshotText = async () => {
      const snapshot = await sessions.getAgentSnapshot(sessionId);
      return [JSON.stringify(convertAgentSnapshotToJson(snapshot)), JSON.stringify(snapshot)];
    };
    const turn = async (command: string, turnCount: number) => {
      if (turnCount === 1) {
        await sessions.startSession(sessionId, command);
      } else {
        await sessions.sendUserMessage(sessionId, command);
      }
      await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 120_000 });
      seen.push(...(await snapshotText()));
    };

    await turn(calls([["get_balance", { accountId: "a-1" }]]), 1);
    await turn(calls([["create_payout", { accountId: "a-1", amount: 5 }]]), 2);
    await turn(calls([["post_note", { accountId: "a-1", text: "paid" }]]), 3);
    await turn(
      calls([
        ["create_payout", { accountId: "a-1", amount: 5, memo: "reject" }],
        ["create_payout", { accountId: "a-1", amount: 5, memo: "uncertain" }],
      ]),
      4,
    );
    await sessions.sendUserMessage(sessionId, calls([["approved_payout", { accountId: "a-1", amount: 6 }]], "held"));
    const interrupt = await sessions.waitForPendingInterrupt(sessionId);
    seen.push(...(await snapshotText()));
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const records = await heldRecords(harness, sessionId, "held", 1);
    seen.push(...records.map((record) => JSON.stringify(record)));
    await sessions.waitForCompletedTurns(sessionId, 5, { timeoutMs: 120_000 });

    seen.push(...(await sessions.listAgentEvents(sessionId)).map((event) => JSON.stringify(event)));
    const { stdout } = await execFileAsync(
      process.env["DEXCLI"] ?? "dexcli",
      ["flow", "history", sessionId, "--server", engine.serverAddress, "--all", "--output", "json"],
      { maxBuffer: 64 * 1024 * 1024 },
    );
    assert.ok(stdout.includes(sessionId));
    seen.push(stdout);
    for (const text of seen) {
      assert.equal(text.includes(secret), false);
    }
    // The secret was in use: every request the provider received was authorized with it.
    // Turn 4's second payout was skipped after the rejected one, so three requests were sent.
    const sends = harness.ledger.sends();
    assert.deepEqual(
      sends.map((send) => send.operation),
      ["createPayout", "postNote", "createPayout"],
    );
    assert.ok(sends.every((send) => send.authorized === "True"));
  });

  describe("Worker kills during connector writes never take a second effect", () => {
    async function writeWithKill(
      harness: Harness,
      toolName: string,
      toolArguments: ToolArguments,
      gate: string | undefined,
    ): Promise<string> {
      const worker = await harness.startWorker();
      const sessionId = `connector-kill-${randomUUID()}`;
      await sessions.startSession(sessionId, calls([[toolName, toolArguments]]));
      if (gate !== undefined) {
        await harness.gate(gate).waitUntilReached();
        await worker.kill();
        await harness.startWorker();
      }
      const lines = await answerLines(sessionId, 1);
      return (lines[0] ?? "").slice(`${toolName}: `.length);
    }

    for (const point of ["gate-before-send", "gate-after-accept"]) {
      test(`a keyed write killed at ${point} retries with the same key and takes one effect`, async () => {
        const harness = newHarness();
        const answer = await writeWithKill(harness, "create_payout", { accountId: "a-1", amount: 5, memo: `${point}:kill` }, "kill");
        assert.equal(answer, '{"amount":5,"payoutId":"createPayout-1"}');
        assert.equal(harness.ledger.writes("createPayout").length, 1);
        // The Call ID, and so the key, is the same on every attempt of the tool Step.
        assert.equal(new Set(harness.ledger.sends("createPayout").map((send) => send.key)).size, 1);
        assert.equal(harness.ledger.sends("createPayout").length, point === "gate-after-accept" ? 2 : 1);
      });
    }

    test("a proven unsent keyed write is retried and sent", async () => {
      const harness = newHarness();
      const answer = await writeWithKill(harness, "create_payout", { accountId: "a-1", amount: 5, memo: "rate-limit-once" }, undefined);
      assert.equal(answer, '{"amount":5,"payoutId":"createPayout-1"}');
      assert.equal(harness.ledger.writes("createPayout").length, 1);
      assert.equal(harness.ledger.sends("rate-limited").length, 1);
    });

    const fencedCases: readonly [string, string, number][] = [
      // No marker reached Dex, so the retry reads back and never sends.
      ["gate-before-send:kill", outcomeUnknownToolResult, 0],
      ["gate-after-claim:kill", outcomeUnknownToolResult, 0],
      // The provider accepted it, and read-back finds it.
      ["gate-after-accept:kill", '{"noteId":"postNote-1"}', 1],
      // The provider accepted it, and read-back cannot find it.
      ["hide gate-after-accept:kill", outcomeUnknownToolResult, 1],
    ];
    for (const [words, expectedAnswer, expectedWrites] of fencedCases) {
      test(`a fenced write in CallTool killed with "${words}" never sends twice`, async () => {
        const harness = newHarness();
        const answer = await writeWithKill(harness, "post_note_retried", { accountId: "a-1", text: words }, "kill");
        assert.equal(answer, expectedAnswer);
        assert.equal(harness.ledger.writes("postNote").length, expectedWrites);
        assert.equal(harness.ledger.sends("postNote").length, expectedWrites);
      });
    }

    test("a fenced write marked not sent is retried and sent once", async () => {
      const harness = newHarness();
      // The retry reads the fence marker back through the session's text checkpoint.
      const answer = await writeWithKill(harness, "post_note_retried", { accountId: "a-1", text: "rate-limit-once" }, undefined);
      assert.equal(answer, '{"noteId":"postNote-1"}');
      assert.equal(harness.ledger.writes("postNote").length, 1);
      assert.equal(harness.ledger.sends("rate-limited").length, 1);
    });

    const singleAttemptCases: readonly [string, number][] = [
      ["gate-before-send:kill", 0],
      ["gate-after-claim:kill", 0],
      ["gate-after-accept:kill", 1],
    ];
    for (const [words, expectedWrites] of singleAttemptCases) {
      test(`a single-attempt write killed with "${words}" ends outcome_unknown and never sends again`, async () => {
        const harness = newHarness();
        const answer = await writeWithKill(harness, "post_note", { accountId: "a-1", text: words }, "kill");
        assert.equal(answer, outcomeUnknownToolResult);
        assert.equal(harness.ledger.writes("postNote").length, expectedWrites);
        assert.equal(harness.ledger.sends("postNote").length, expectedWrites);
      });
    }

    test("a single-attempt write that the provider rate-limits ends unknown and unsent", async () => {
      const harness = newHarness();
      const answer = await writeWithKill(harness, "post_note", { accountId: "a-1", text: "rate-limit-once" }, undefined);
      assert.equal(answer, outcomeUnknownToolResult);
      assert.deepEqual(harness.ledger.sends("postNote"), []);
    });
  });
});
