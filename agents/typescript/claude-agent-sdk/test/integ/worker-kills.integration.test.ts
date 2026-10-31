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
import { slowWriteGateName } from "../fixtures/claude-test-agent.js";

describe("Claude agent sessions survive a Worker killed with SIGKILL", () => {
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

  test("a write waiting for approval survives a Worker restart and runs once after approval", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("approval-restart");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"hello"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");

    await firstWorker.kill();
    await agent.startWorker();

    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, "publish: published hello");
    assert.equal(agent.invocations.count("publish"), 1);
  });

  test("a Worker killed during a single-attempt write records outcome_unknown and never reruns the tool", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("outcome-unknown");
    await agent.sessions.startSession(sessionId, 'call slow_write {"message":"report"}');
    const writeGate = agent.gate(slowWriteGateName);
    await writeGate.waitUntilReached();
    const executingState = await agent.sessions.readAgentState(sessionId);
    assert.equal(executingState?.status, "running_tools");
    await agent.waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    // Releasing first means a wrongful rerun would finish and log a second invocation.
    writeGate.release();
    await agent.startWorker();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    const toolResult = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(toolResult?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, `slow_write: ${outcomeUnknownToolResult}`);
    assert.equal(agent.invocations.count("slow_write"), 1);
    assert.equal(writeGate.reachedCount(), 1);
    const finishedStatuses = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
  });

  test("a Worker killed during RunModel reruns the segment, including its read tool", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("turn-rerun");
    await agent.sessions.startSession(sessionId, "stall turn");
    const turnGate = agent.gate("turn");
    await turnGate.waitUntilReached();
    await agent.waitForEvent(sessionId, "text_delta");

    await firstWorker.kill();
    turnGate.release();
    await agent.startWorker();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, "stalled at turn");
    assert.equal(agent.invocations.count("lookup"), 2);
    assert.equal(agent.invocations.count("cli:new"), 2);
    assert.deepEqual(await agent.textByAttempt(sessionId), [
      [1, "thinking after found stall"],
      [2, "thinking after found stallstalled at turn"],
    ]);
    const completedAttempts = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "turn_completed")
      .map((event) => event.attempt);
    assert.deepEqual(completedAttempts, [2]);
  });

  test("a Worker killed while a resumed segment runs replays the recorded write instead of rerunning it", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("resumed-rerun");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"hello"} ;; pause answer');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const answerGate = agent.gate("answer");
    await answerGate.waitUntilReached();

    await firstWorker.kill();
    answerGate.release();
    await agent.startWorker();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, "publish: published hello");
    assert.equal(agent.invocations.count("publish"), 1);
    assert.equal(agent.invocations.count("cli:resume"), 2);
    assert.equal(answerGate.reachedCount(), 2);
  });
});
