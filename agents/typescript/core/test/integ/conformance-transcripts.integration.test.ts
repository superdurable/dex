// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import { after, before, describe, test } from "node:test";

import { encodeAgentSnapshot } from "../../src/agent-session-codecs.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
  waitForCompletedEvent,
  type StepHistoryEntry,
} from "../../src/testing/index.js";
import {
  createConformanceAgents,
  readConformanceScenarios,
  type ConformanceAgents,
  type ConformanceScenario,
} from "../fixtures/conformance-agent.js";
import { compareByCodePoint, serializeCanonicalJson } from "../fixtures/conformance-suite.js";

const timestampPattern = /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z/g;
const settleIntervalMs = 500;
const scenarioTimeoutMs = 90_000;

describe("the conformance suite's scripted-turn transcripts", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let agents: ConformanceAgents;

  before(async () => {
    engine = await PrivateDexEngine.start();
    agents = createConformanceAgents();
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [agents.flows.chat, agents.flows.compacting],
      isWorkerInProcess: true,
    });
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  for (const scenario of readConformanceScenarios()) {
    // A codec mismatch would otherwise wait out every poll; fail the scenario instead.
    test(`${scenario.name} matches the expected transcript`, { timeout: scenarioTimeoutMs }, async () => {
      assert.deepEqual(await runScenario(scenario), scenario.expected);
    });
  }

  async function runScenario(scenario: ConformanceScenario): Promise<unknown> {
    const sessions = new AgentSessionTestClient(environment.client, agents.flows[scenario.agent]);
    const sessionId = `conformance-${scenario.name}`;
    const snapshots: Record<string, unknown> = {};
    for (const action of scenario.actions) {
      const result = await runAction(sessions, sessionId, action, snapshots);
      if ("returns" in action) {
        assert.equal(result, action["returns"], `${scenario.name}: ${JSON.stringify(action)}`);
      }
    }
    const [steps, events] = await readSettledHistory(sessions, sessionId);
    snapshots["final"] = encodeAgentSnapshot(await sessions.getAgentSnapshot(sessionId));
    return replaceTimestamps(
      JSON.parse(
        JSON.stringify({ steps, events, snapshots, model_requests: agents.modelRequests.get(sessionId) ?? [] }),
      ),
    );
  }

  async function runAction(
    sessions: AgentSessionTestClient,
    sessionId: string,
    action: Readonly<Record<string, unknown>>,
    snapshots: Record<string, unknown>,
  ): Promise<unknown> {
    switch (action["action"]) {
      case "start_session":
        return sessions.startSession(sessionId);
      case "send_user_message":
        return sessions.sendUserMessage(sessionId, action["text"] as string, (action["plan_mode"] as boolean) ?? false);
      case "resolve_interrupt":
        return sessions.resolveInterrupt(
          sessionId,
          action["interrupt_id"] as string,
          action["approved"] as boolean,
          action["rejection_message"] as string | null,
        );
      case "answer_question":
        return sessions.answerQuestion(sessionId, action["interrupt_id"] as string, action["answer"] as string);
      case "steer_user_message":
        return sessions.steerUserMessage(sessionId, action["text"] as string);
      case "execute_plan":
        return sessions.executePlan(sessionId, action["revision"] as number);
      case "snapshot":
        snapshots[action["label"] as string] = encodeAgentSnapshot(await sessions.getAgentSnapshot(sessionId));
        return undefined;
      case "wait_for_idle": {
        const turnCount = action["turn_count"] as number;
        return sessions.waitForAgentSnapshot(
          sessionId,
          (snapshot) =>
            snapshot.state !== null &&
            snapshot.state.turnCount >= turnCount &&
            snapshot.state.activeTurnId === null &&
            snapshot.queuedMessageCount === 0,
          `${turnCount} finished turns`,
        );
      }
      case "wait_for_interrupt": {
        const interruptId = action["interrupt_id"] as string;
        return sessions.waitForAgentSnapshot(sessionId, (snapshot) =>
          snapshot.pendingInterrupts.some((pending) => pending.interruptId === interruptId),
          `pending interrupt ${interruptId}`,
        );
      }
      default:
        throw new Error(`unknown scenario action ${String(action["action"])}`);
    }
  }

  /** Returns the Step history and events once two reads half a second apart agree. */
  async function readSettledHistory(sessions: AgentSessionTestClient, sessionId: string): Promise<[unknown, unknown[]]> {
    let previous: string | undefined;
    for (;;) {
      const history = await readStepHistory(engine.serverAddress, sessionId);
      const events = await sessions.listAgentEvents(sessionId);
      const current: [unknown, unknown[]] = [
        describeStepHistory(history),
        events.map((event) => ({
          type: event.type,
          turn_id: event.turnId,
          attempt: event.attempt,
          step_execution_id: event.stepExecutionId,
          payload: serializeCanonicalJson(JSON.parse(event.payload)),
        })),
      ];
      const serialized = JSON.stringify(current);
      if (serialized === previous) {
        return current;
      }
      previous = serialized;
      await delay(settleIntervalMs);
    }
  }
});

/** Execute completions in order, and WaitFor completions sorted: parallel Steps finish WaitFor in engine order. */
function describeStepHistory(history: readonly StepHistoryEntry[]): unknown {
  return {
    executed: history
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => [entry.stepType, entry.stepExecutionId, entry.finalAttempt]),
    waits_completed: history
      .filter((entry) => entry.eventType === waitForCompletedEvent)
      .map((entry) => [entry.stepType, entry.stepExecutionId])
      .sort((left, right) => compareByCodePoint(left.join("\u0000"), right.join("\u0000"))),
  };
}

function replaceTimestamps(value: unknown): unknown {
  if (typeof value === "string") {
    return value.replace(timestampPattern, "<timestamp>");
  }
  if (Array.isArray(value)) {
    return value.map(replaceTimestamps);
  }
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, replaceTimestamps(item)]));
  }
  return value;
}
