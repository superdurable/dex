// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { outcomeUnknownToolResult } from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { renamedToolsVariable, slowWriteGateName } from "../fixtures/claude-test-agent.js";
import { assertUniqueTranscriptEntries } from "../fixtures/transcript-assertions.js";

describe("A Worker restarts with a deploy that renamed a tool the open turn already called", () => {
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

  async function startHarness(): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, false);
    return harness;
  }

  test("a renamed completed write replays its recorded result", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-completed");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5} ;; pause model-gate');
    const modelGate = agent.gate("model-gate");
    await modelGate.waitUntilReached();

    await firstWorker.kill();
    modelGate.release();
    await agent.startWorker({ [renamedToolsVariable]: "charge=bill" });

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.equal(toolResults.length, 1);
    assert.equal(toolResults[0]?.toolName, "charge");
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `charge: charged 5 with key ${sessionId}/turn-1/${toolResults[0]?.toolCallId}`,
    );
    assert.equal(agent.invocations.count("charge"), 1);
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("a renamed in-flight single-attempt write records outcome_unknown", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-in-flight");
    await agent.sessions.startSession(sessionId, 'call slow_write {"message":"ledger"}');
    const writeGate = agent.gate(slowWriteGateName);
    await writeGate.waitUntilReached();

    await firstWorker.kill();
    await agent.startWorker({ [renamedToolsVariable]: "slow_write=save" });

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `slow_write: ${outcomeUnknownToolResult}`);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolResults.map((message) => [message.toolName, message.toolOutput]),
      [["slow_write", outcomeUnknownToolResult]],
    );
    assert.equal(agent.invocations.count("slow_write"), 1);
    assert.equal(writeGate.reachedCount(), 1);
    assert.ok((await agent.executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("a renamed idempotent write retried after a kill records outcome_unknown", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-retry");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5,"gate":"charge-gate"}');
    const chargeGate = agent.gate("charge-gate");
    await chargeGate.waitUntilReached();

    await firstWorker.kill();
    await agent.startWorker({ [renamedToolsVariable]: "charge=bill" });

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `charge: ${outcomeUnknownToolResult}`);
    assert.equal(agent.invocations.count("charge"), 1);
  });
});
