// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Session forks: the rules of agents/conformance/session_fork.json, and forks on an engine; the twin of the
// Python core's tests/test_session_fork.py.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import {
  AgentSessionForkError,
  forkAgentSession,
  planAgentSessionForkPoint,
  startAgentSession,
  type AgentSessionStatus,
} from "../../src/index.js";
import { AgentSessionTestClient, AgentTestEnvironment, FileGate, PrivateDexEngine } from "../../src/testing/index.js";
import { readConformanceJson } from "../fixtures/conformance-suite.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

interface PointCase {
  readonly name: string;
  readonly after_message: number | null;
  readonly state: { next_message_sequence: number; active_turn_id: string | null; status: AgentSessionStatus };
  readonly turn_starts: readonly { sequence: number; step_execution_id: string }[];
  readonly expected:
    | { after_message: number; step_execution_id: string | null }
    | { error: string; values: Record<string, string | number> };
}

const suite = readConformanceJson<{ errors: Record<string, string>; points: PointCase[] }>("session_fork.json");
const sourceSessionId = "source-1";

function format(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (_, name: string) => String(values[name]));
}

describe("session fork points", () => {
  for (const point of suite.points) {
    test(point.name, () => {
      const plan = () =>
        planAgentSessionForkPoint(
          sourceSessionId,
          point.after_message,
          point.state.next_message_sequence,
          point.state.active_turn_id,
          point.state.status,
          point.turn_starts.map((start) => ({ sequence: start.sequence, stepExecutionId: start.step_execution_id })),
        );
      const expected = point.expected;
      if ("error" in expected) {
        assert.throws(plan, (error: unknown) => {
          assert.ok(error instanceof AgentSessionForkError);
          assert.equal(error.code, expected.error);
          assert.equal(error.message, format(suite.errors[expected.error] ?? "", expected.values));
          return true;
        });
        return;
      }
      assert.deepEqual(plan(), { afterMessage: expected.after_message, stepExecutionId: expected.step_execution_id });
    });
  }
});

describe("session forks on an engine", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createScriptedAgentFlow>;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    flow = createScriptedAgentFlow(markerDirectory);
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

  async function sessionWithTwoTurns(): Promise<string> {
    const sessionId = `fork-source-${randomUUID()}`;
    await startAgentSession(environment.client, flow, sessionId);
    await sessions.waitForCompletedTurns(sessionId, 0);
    assert.equal(await sessions.sendUserMessage(sessionId, "first"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "second"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    return sessionId;
  }

  test("a fork after an earlier turn continues on its own while the source keeps working", async () => {
    const sourceId = await sessionWithTwoTurns();
    const lease = await environment.client.invokeRPC(flow.acquireSessionTerminalLease, sourceId, {
      holderId: "holder-a",
      hostName: "host-a",
      processId: 7,
      ttlSeconds: 600,
      environment: { host_name: "host-a", workspace: "/work" },
    });
    assert.equal(lease.isAcquired, true);
    const forkId = `fork-${randomUUID()}`;

    const fork = await forkAgentSession(environment.client, flow, sourceId, forkId, { afterMessage: 2 });

    assert.equal(fork.sessionId, forkId);
    assert.equal(fork.afterMessage, 2);
    const [state, storedLease, lastEnvironment] = await environment.client.getAttributes(
      forkId,
      flow.agentState,
      flow.sessionTerminalLease,
      flow.sessionLastTerminalEnvironment,
    );
    assert.equal(storedLease ?? null, null);
    assert.equal(lastEnvironment ?? null, null);
    assert.equal(state?.nextMessageSequence, 3);
    assert.equal(state?.turnCount, 1);
    assert.equal(state?.activeTurnId, null);

    assert.equal(await sessions.sendUserMessage(forkId, "history"), true);
    await sessions.waitForCompletedTurns(forkId, 2);
    const forked = await sessions.getAgentSnapshot(forkId);
    assert.deepEqual(
      forked.messages.map((message) => `${message.role} ${message.text}`),
      ["user first", "assistant echo: first", "user history", "assistant history | user: first | assistant: echo: first"],
    );

    assert.equal(await sessions.sendUserMessage(sourceId, "third"), true);
    await sessions.waitForCompletedTurns(sourceId, 3);
    const source = await sessions.getAgentSnapshot(sourceId);
    assert.equal(source.messages.at(-1)?.text, "echo: third");
    const [sourceLease] = await environment.client.getAttributes(sourceId, flow.sessionTerminalLease);
    assert.deepEqual(sourceLease, lease.lease);
  });

  test("a fork of the current state keeps every message and takes its task queue", async () => {
    const sourceId = await sessionWithTwoTurns();
    const forkId = `fork-${randomUUID()}`;

    const fork = await forkAgentSession(environment.client, flow, sourceId, forkId, { taskQueue: "s-fork-test" });

    assert.equal(fork.afterMessage, 4);
    const flowState = await environment.client.getFlowState(forkId);
    assert.equal(flowState.taskQueue, "s-fork-test");
    const [state] = await environment.client.getAttributes(forkId, flow.agentState);
    assert.equal(state?.nextMessageSequence, 5);
  });

  test("a fork inside a turn or during a running turn is refused", async () => {
    const sourceId = await sessionWithTwoTurns();
    await assert.rejects(
      forkAgentSession(environment.client, flow, sourceId, `fork-${randomUUID()}`, { afterMessage: 1 }),
      (error: unknown) => error instanceof AgentSessionForkError && error.code === "inside_turn",
    );

    const gate = new FileGate(markerDirectory, "turn");
    assert.equal(await sessions.sendUserMessage(sourceId, "slow"), true);
    await gate.waitUntilReached();
    await assert.rejects(
      forkAgentSession(environment.client, flow, sourceId, `fork-${randomUUID()}`),
      (error: unknown) => error instanceof AgentSessionForkError && error.code === "turn_in_progress",
    );

    const fork = await forkAgentSession(environment.client, flow, sourceId, `fork-${randomUUID()}`, {
      afterMessage: 4,
    });
    assert.equal(fork.afterMessage, 4);
    gate.release();
    await sessions.waitForCompletedTurns(sourceId, 3);
  });
});
