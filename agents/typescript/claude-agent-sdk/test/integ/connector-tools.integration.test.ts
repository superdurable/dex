// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Connector tools from a ConnectorToolSource: every call runs in its own tool Step. The twin of the Python
// adapter's tests/test_connector_tools.py.

import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { tool } from "@anthropic-ai/claude-agent-sdk";
import {
  outcomeUnknownToolResult,
  type AgentSnapshot,
  type AgentToolCall,
  type AgentToolCallRecord,
  type AgentTurnContext,
} from "@superdurable/dex-agents";
import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import { ClaudeAgentRunner, createClaudeAgentSessionFlow } from "../../src/index.js";
import { ListedSourceTool, withSourceReference } from "../../src/claude-tool-sources.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import {
  createLedgerToolSource,
  denyFlagFileName,
  hideSourceToolsFileName,
  raisedPayoutAmount,
} from "../fixtures/claude-test-agent.js";
import {
  CloseAccountInputSchema,
  CloseAccountOperation,
  FakeLedgerProvider,
  GetBalanceInputSchema,
  GetBalanceOperation,
  fakeLedgerConnection,
  fakeLedgerManifest,
} from "../fixtures/fake-ledger.js";

const balance = '{"accountId":"a-1","amount":100}';
const firstPayout = '{"amount":5,"payoutId":"createPayout-1"}';

type ToolArguments = Readonly<Record<string, unknown>>;

function call(toolName: string, toolArguments: ToolArguments): string {
  return `call ${toolName} ${JSON.stringify(toolArguments)}`;
}

function readManifest(): ConnectorPackageManifest {
  return structuredClone(fakeLedgerManifest) as ConnectorPackageManifest;
}

function toolOutputs(snapshot: AgentSnapshot, toolName: string): string[] {
  return snapshot.messages
    .filter((message) => message.role === "tool" && message.toolName === toolName)
    .map((message) => message.toolOutput ?? "");
}

describe("connector tools served by the Claude agent adapter", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
  });

  after(async () => {
    await engine?.stop();
  });

  async function startHarness(isWorkerInProcess = true): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, isWorkerInProcess);
    return harness;
  }

  function ledgerOf(agent: ClaudeAgentTestHarness): FakeLedgerProvider {
    return new FakeLedgerProvider(agent.stateDirectory);
  }

  /** Returns the turn's records by tool name while the final answer waits at the gate. */
  async function heldRecords(
    agent: ClaudeAgentTestHarness,
    sessionId: string,
    gateName: string,
  ): Promise<Map<string, AgentToolCallRecord>> {
    const gate = agent.gate(gateName);
    await gate.waitUntilReached();
    const snapshot = await agent.sessions.getAgentSnapshot(sessionId);
    gate.release();
    return new Map(snapshot.toolCalls.map((record) => [record.toolName, record]));
  }

  test("a source call gets its operation's timeout and bounded retries", async () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "step-options-"));
    const source = createLedgerToolSource(stateDirectory);
    const runner = new ClaudeAgentRunner({}, { toolSources: [source] });
    const definition = (await source.listTools()).find((listed) => listed.name === "post_note");
    assert.ok(definition !== undefined);
    // ConnectorToolSource reads only toolPolicies from the turn.
    const turn = { toolPolicies: {} } as unknown as AgentTurnContext;
    const deferred = new ListedSourceTool(source, definition).buildDeferredToolCall(turn, "toolu_1", '{"accountId":"a-1"}');
    const record: AgentToolCall = {
      toolCallId: deferred.toolCallId,
      turnId: "turn-1",
      toolName: deferred.toolName,
      arguments: deferred.arguments,
      effect: deferred.policy.effect,
      requiresApproval: deferred.policy.requiresApproval,
      status: "approved",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      ...(deferred.toolSourceReference === undefined ? {} : { toolSourceReference: deferred.toolSourceReference }),
    };

    const options = runner.toolCallStepOptions(record);
    assert.equal(options?.executeMethodTimeout, 30_000);
    assert.equal(options?.executeRetry?.maximumAttempts, 3);
    // The runner unwraps its envelope and asks the source with the source's own reference.
    assert.deepEqual(options, source.toolCallStepOptions(withSourceReference(record, '{"tool": "post_note"}')));
    // Custom tools and CLI tools keep the tool Steps' defaults.
    assert.equal(runner.toolCallStepOptions(withSourceReference(record, undefined)), undefined);
  });

  test("a Query tool is deferred and runs in its tool Step", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("query");
    await agent.sessions.startSession(sessionId, `${call("get_balance", { accountId: "a-1" })} ;; pause held`);
    const record = (await heldRecords(agent, sessionId, "held")).get("get_balance");
    assert.deepEqual([record?.effect, record?.status, record?.output], ["read", "succeeded", balance]);
    assert.equal(record?.requiresApproval, false);
    assert.notEqual(record?.orderedWrite, true);

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `get_balance: ${balance}`);
    assert.deepEqual(toolOutputs(snapshot, "get_balance"), [balance]);
    assert.deepEqual(await agent.executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a Mutation with manifest approval runs once after approval across a Worker restart", async () => {
    const agent = await startHarness(false);
    const firstWorker = await agent.startWorker();
    const ledger = ledgerOf(agent);
    const sessionId = agent.newSessionId("approved-payout");
    await agent.sessions.startSession(sessionId, call("approved_payout", { accountId: "a-1", amount: 5 }));
    const interrupt = await agent.waitForNewInterrupt(sessionId, "approved_payout");
    const waiting = await agent.sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waiting.toolCalls.map((record) => [record.effect, record.status, record.orderedWrite]),
      [["idempotent_write", "awaiting_approval", true]],
    );

    await firstWorker.kill();
    await agent.startWorker();
    assert.deepEqual(ledger.sends(), []);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `approved_payout: ${firstPayout}`);
    assert.equal(ledger.writes("createPayout").length, 1);
    assert.equal(ledger.sends("createPayout").length, 1);
  });

  test("a session policy changes only the approval of a connector tool", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("policy");
    await agent.sessions.startSession(sessionId, call("policy_note", { accountId: "a-1", text: "hello" }));
    const interrupt = await agent.waitForNewInterrupt(sessionId, "policy_note");
    const waiting = await agent.sessions.getAgentSnapshot(sessionId);
    // The policy claims read, but the effect class always comes from the manifest.
    assert.deepEqual(
      waiting.toolCalls.map((record) => [record.toolName, record.effect]),
      [["policy_note", "non_idempotent_write"]],
    );

    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not now"), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, "policy_note: not now");
    assert.deepEqual(ledgerOf(agent).sends(), []);
  });

  test("an approval predicate decides on the arguments the Step runs", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("predicate");
    await agent.sessions.startSession(sessionId, call("payout_checked", { accountId: "a-1", amount: 10 }));
    let snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, 'payout_checked: {"amount":10,"payoutId":"createPayout-1"}');

    // An application hook raises the amount; the predicate sees the raised amount.
    await agent.sessions.sendUserMessage(
      sessionId,
      call("payout_checked", { accountId: "a-1", amount: 10, memo: "raise" }),
    );
    const interrupt = await agent.waitForNewInterrupt(sessionId, "payout_checked");
    assert.equal((JSON.parse(interrupt.arguments) as { amount: number }).amount, raisedPayoutAmount);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    snapshot = await agent.waitForTurns(sessionId, 2);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `payout_checked: {"amount":${raisedPayoutAmount},"payoutId":"createPayout-2"}`,
    );
  });

  test("a retry: safe Mutation retries with the same Call ID", async () => {
    const agent = await startHarness();
    const ledger = ledgerOf(agent);
    const sessionId = agent.newSessionId("retry-safe");
    await agent.sessions.startSession(
      sessionId,
      call("create_payout", { accountId: "a-1", amount: 5, memo: "rate-limit-once" }),
    );
    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `create_payout: ${firstPayout}`);
    const refused = ledger.sends("rate-limited");
    const paid = ledger.writes("createPayout");
    assert.equal(refused.length, 1);
    assert.equal(paid.length, 1);
    // The Call ID, and so the idempotency key, is the same on both attempts of the tool Step.
    assert.equal(refused[0]?.key, paid[0]?.key);
    assert.ok((await agent.executedStepTypes(sessionId)).includes("CallTool"));
  });

  test("a killed retry: safe Mutation retries with the same key and takes one effect", async () => {
    const agent = await startHarness(false);
    const firstWorker = await agent.startWorker();
    const ledger = ledgerOf(agent);
    const sessionId = agent.newSessionId("retry-killed");
    await agent.sessions.startSession(
      sessionId,
      call("create_payout", { accountId: "a-1", amount: 5, memo: "gate-after-accept:kill" }),
    );
    await agent.gate("kill").waitUntilReached();
    await firstWorker.kill();
    await agent.startWorker();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `create_payout: ${firstPayout}`);
    assert.equal(ledger.writes("createPayout").length, 1);
    const sends = ledger.sends("createPayout");
    assert.equal(sends.length, 2);
    assert.equal(new Set(sends.map((send) => send.key)).size, 1);
  });

  for (const [toolName, toolArguments, stepType] of [
    ["create_payout", { accountId: "a-1", amount: 5, memo: "uncertain" }, "CallTool"],
    ["post_note", { accountId: "a-1", text: "uncertain" }, "CallSingleAttemptTool"],
  ] as const) {
    test(`an uncertain ${toolName} records outcome_unknown and never runs again`, async () => {
      const agent = await startHarness();
      const ledger = ledgerOf(agent);
      const sessionId = agent.newSessionId("uncertain");
      await agent.sessions.startSession(sessionId, `${call(toolName, toolArguments)} ;; pause held`);
      const record = (await heldRecords(agent, sessionId, "held")).get(toolName);
      assert.equal(record?.status, "outcome_unknown");
      assert.match(record?.error ?? "", /uncertain/);

      const snapshot = await agent.waitForTurns(sessionId, 1);
      assert.equal(snapshot.messages.at(-1)?.text, `${toolName}: ${outcomeUnknownToolResult}`);
      assert.equal(ledger.writes().length, 1);
      assert.equal(ledger.sends().length, 1);
      assert.ok((await agent.executedStepTypes(sessionId)).includes(stepType));
    });
  }

  test("a rejected branch and bad arguments fail without reaching the provider", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("rejected");
    await agent.sessions.startSession(sessionId, call("post_note", { accountId: "a-1", text: "reject" }));
    let snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "post_note: The tool call failed: fake-ledger postNote returned providerRejected: " +
        "PROVIDER_REJECTION: channel archived",
    );

    await agent.sessions.sendUserMessage(sessionId, call("get_balance", { accountId: 5 }));
    snapshot = await agent.waitForTurns(sessionId, 2);
    assert.match(
      snapshot.messages.at(-1)?.text ?? "",
      /^get_balance: The tool call failed: invalid arguments for get_balance/,
    );
    assert.deepEqual(ledgerOf(agent).sends(), []);
  });

  test("two writes in one reply run one at a time in the model's order", async () => {
    const agent = await startHarness();
    const ledger = ledgerOf(agent);
    const sessionId = agent.newSessionId("ordered");
    await agent.sessions.startSession(
      sessionId,
      `${call("post_note", { accountId: "a-1", text: "first gate-after-accept:first-write" })} && ` +
        call("create_payout", { accountId: "a-1", amount: 5 }),
    );
    const firstWrite = agent.gate("first-write");
    await firstWrite.waitUntilReached();
    const running = await agent.sessions.getAgentSnapshot(sessionId);
    // The second write never left the turn while the first one runs.
    assert.deepEqual(running.toolCalls.map((record) => record.toolName), ["post_note"]);
    assert.deepEqual(ledger.sends().map((entry) => entry.operation), ["postNote"]);
    firstWrite.release();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.deepEqual(ledger.writes().map((entry) => entry.operation), ["postNote", "createPayout"]);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      'post_note: {"noteId":"postNote-1"}; create_payout: {"amount":5,"payoutId":"createPayout-2"}',
    );
    const payoutOutputs = toolOutputs(snapshot, "create_payout");
    assert.match(payoutOutputs[0] ?? "", /Writes run one at a time in your order/);
    assert.deepEqual(payoutOutputs.slice(1), ['{"amount":5,"payoutId":"createPayout-2"}']);
  });

  test("an application hook that denies on resume cannot hide a source result", async () => {
    const agent = await startHarness();
    const ledger = ledgerOf(agent);
    const sessionId = agent.newSessionId("hook-deny-source");
    await agent.sessions.startSession(sessionId, call("post_note", { accountId: "a-1", text: "once gate-after-accept:deny" }));
    const gate = agent.gate("deny");
    await gate.waitUntilReached();
    // The call passed the hook when it was deferred; the hook now denies every post_note.
    writeFileSync(join(agent.stateDirectory, denyFlagFileName), "");
    gate.release();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, 'post_note: {"noteId":"postNote-1"}');
    assert.equal(ledger.sends("postNote").length, 1);
    assert.equal((await agent.executedStepTypes(sessionId)).filter((stepType) => stepType === "CallSingleAttemptTool").length, 1);
  });

  for (const listing of ["empty", "raise"]) {
    test(`a tool whose source stops listing it (${listing}) still replays and refuses new calls`, async () => {
      const agent = await startHarness();
      const ledger = ledgerOf(agent);
      const sessionId = agent.newSessionId("hidden");
      await agent.sessions.startSession(
        sessionId,
        `${call("hiding_note", { accountId: "a-1", text: "first gate-after-accept:hide-now" })} && ` +
          call("hiding_note", { accountId: "a-1", text: "second" }),
      );
      const gate = agent.gate("hide-now");
      await gate.waitUntilReached();
      // The resumed segment's listing lacks the tool whose record it must replay.
      writeFileSync(join(agent.stateDirectory, hideSourceToolsFileName), listing);
      gate.release();

      const snapshot = await agent.waitForTurns(sessionId, 1);
      // The model calls the refused second write again by itself, under a new tool use ID.
      assert.equal(
        snapshot.messages.at(-1)?.text,
        'hiding_note: {"noteId":"postNote-1"}; ' +
          "hiding_note: PreToolUse:mcp__tools__hiding_note hook error: " +
          "hiding_note did not run: its tool source does not offer this tool now.",
      );
      assert.equal(ledger.sends("postNote").length, 1);
      assert.equal(ledger.writes("postNote").length, 1);
      const stepTypes = await agent.executedStepTypes(sessionId);
      assert.equal(stepTypes.filter((stepType) => stepType === "CallSingleAttemptTool").length, 1);
    });
  }

  test("an allowedTools entry for a source tool fails the turn", async () => {
    harness = await ClaudeAgentTestHarness.start(
      engine,
      true,
      () => ({ flowType: "AllowedSourceToolAgent", allowedTools: ["mcp__tools__get_balance"] }),
      { runModelLimit: { methodTimeoutMs: 60_000, maximumAttempts: 1 } },
    );
    const sessionId = harness.newSessionId("allowed-tools");
    await harness.sessions.startSession(sessionId, call("get_balance", { accountId: "a-1" }));
    const snapshot = await harness.waitForTurns(sessionId, 1);
    assert.match(snapshot.state?.lastTurnError ?? "", /allowedTools entry "mcp__tools__get_balance"/);
    assert.deepEqual(ledgerOf(harness).sends(), []);
  });

  test("an unexposed operation is refused when the agent is defined", () => {
    assert.throws(
      () =>
        createClaudeAgentSessionFlow(
          {
            options: {},
            toolSources: [
              new ConnectorToolSource([
                connectorTool(new CloseAccountOperation(), fakeLedgerConnection, {
                  manifest: readManifest(),
                  inputValidator: CloseAccountInputSchema,
                }),
              ]),
            ],
          },
          "UnexposedConnectorAgent",
        ),
      { name: "TypeError", message: /not exposed to agents/ },
    );
  });

  test("a source tool named like a custom or Claude Code tool is refused", () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "collision-"));
    const balanceOperation = new GetBalanceOperation(new FakeLedgerProvider(stateDirectory));
    const lookup = tool("lookup", "Look up a record", { query: z.string() }, async () => ({ content: [] }));
    for (const toolName of ["lookup", "Bash", "WebFetch"]) {
      const source = new ConnectorToolSource([
        connectorTool(balanceOperation, fakeLedgerConnection, {
          manifest: readManifest(),
          inputValidator: GetBalanceInputSchema,
          name: toolName,
        }),
      ]);
      assert.throws(() => new ClaudeAgentRunner({}, { customTools: [lookup], toolSources: [source] }), {
        name: "TypeError",
        message: new RegExp(`tool ${toolName} of a tool source`),
      });
    }
  });

  test("two sources with one name or one tool name are refused", () => {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "two-sources-"));
    const balanceOperation = new GetBalanceOperation(new FakeLedgerProvider(stateDirectory));
    const source = (sourceName: string, toolName: string): ConnectorToolSource =>
      new ConnectorToolSource(
        [
          connectorTool(balanceOperation, fakeLedgerConnection, {
            manifest: readManifest(),
            inputValidator: GetBalanceInputSchema,
            name: toolName,
          }),
        ],
        { sourceName },
      );

    assert.throws(
      () => new ClaudeAgentRunner({}, { toolSources: [source("ledger", "balance_a"), source("ledger", "balance_b")] }),
      { name: "TypeError", message: /two tool sources are named ledger/ },
    );
    assert.throws(
      () => new ClaudeAgentRunner({}, { toolSources: [source("ledger-a", "balance"), source("ledger-b", "balance")] }),
      { name: "TypeError", message: /two tool sources list a tool named balance/ },
    );
  });
});
