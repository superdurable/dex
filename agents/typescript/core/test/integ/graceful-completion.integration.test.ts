// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import { StopType, type FlowStatus } from "@superdurable/dex";

import type {
  AgentFrameworkRunner,
  AgentTurnContext,
  AgentTurnOutcome,
  DeferredToolExecution,
  DeferredToolOutcome,
  FrameworkMessage,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import { ScriptedAgentRunner, createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

describe("a graceful complete request ends a session at its next wait for a user message", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedAgentFlow(markerDirectory, {
      flowType: "GracefulCompletionAgent",
      runner: new SessionCompletingRunner(new ScriptedAgentRunner(markerDirectory), () => environment),
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a tool that stops its own session completes it after the turn", async () => {
    const sessionId = newSessionId("tool-stop");
    await sessions.startSession(sessionId, "call complete_session");

    assert.equal(await waitForClosedStatus(sessionId), "completed");
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.turnCount, 1);
    assert.equal(snapshot.state?.activeTurnId, null);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.toolName]),
      [
        ["user", null],
        ["assistant", "complete_session"],
        ["tool", "complete_session"],
        ["assistant", null],
      ],
    );
    const [toolResult, finalAnswer] = snapshot.messages.slice(-2);
    assert.equal(toolResult?.toolOutput, `completing ${sessionId}`);
    assert.equal(finalAnswer?.text, `complete_session -> completing ${sessionId}`);
  });

  test("a message queued before the request still runs its turn", async () => {
    const sessionId = newSessionId("queued");
    const gate = new FileGate(markerDirectory, "turn");
    await sessions.startSession(sessionId, "slow");
    await gate.waitUntilReached();

    assert.equal(await sessions.sendUserMessage(sessionId, "queued"), true);
    await environment.client.stopFlow(sessionId, { type: StopType.GRACEFUL_COMPLETE });
    gate.release();

    assert.equal(await waitForClosedStatus(sessionId), "completed");
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.turnCount, 2);
    assert.equal(snapshot.queuedMessageCount, 0);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.text]),
      [
        ["user", "slow"],
        ["assistant", "slow turn done"],
        ["user", "queued"],
        ["assistant", "echo: queued"],
      ],
    );
  });

  test("without a request the session keeps waiting for messages", async () => {
    const sessionId = newSessionId("no-request");
    await sessions.startSession(sessionId, "first");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await environment.client.describeFlow(sessionId)).status, "running");

    assert.equal(await sessions.sendUserMessage(sessionId, "second"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "echo: second");
    assert.equal((await environment.client.describeFlow(sessionId)).status, "running");
  });

  function waitForClosedStatus(sessionId: string): Promise<FlowStatus> {
    return pollUntil(
      async () => {
        const info = await environment.client.describeFlow(sessionId);
        return info.status === "running" ? undefined : info.status;
      },
      { description: `session ${sessionId} to close` },
    );
  }
});

/** Runs the scripted agent, and handles `complete_session` by asking Dex to gracefully complete the session. */
class SessionCompletingRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  public constructor(
    private readonly scriptedRunner: ScriptedAgentRunner,
    private readonly readEnvironment: () => AgentTestEnvironment,
  ) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return this.scriptedRunner.buildUserMessage(text);
  }

  public runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    return this.scriptedRunner.runModelStep(turn);
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    if (call.toolCall.toolName !== "complete_session") {
      return this.scriptedRunner.executeDeferredToolCall(call);
    }
    await this.readEnvironment().client.stopFlow(call.flowId, { type: StopType.GRACEFUL_COMPLETE });
    return { output: `completing ${call.flowId}` };
  }
}

function newSessionId(label: string): string {
  return `graceful-completion-${label}-${randomUUID()}`;
}
