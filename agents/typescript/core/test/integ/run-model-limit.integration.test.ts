// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { after, before, describe, test } from "node:test";
import { promisify } from "node:util";

import {
  createAgentSessionFlow,
  type AgentFrameworkRunner,
  type AgentSessionOptions,
  type AgentTurnContext,
  type DeferredToolOutcome,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import { ScriptedAgentRunner } from "../fixtures/scripted-agent-runner.js";

const execFileAsync = promisify(execFile);
const stallCommand = "stall";
// Past RunModel's two-minute heartbeat timeout.
const silentCallMs = 135_000;
const silentCommand = "silent";

interface RunModelMethodOptions {
  readonly timeoutSeconds: number;
  readonly heartbeatTimeoutSeconds: number;
  readonly retryPolicy: { readonly maximumAttempts: number; readonly totalDurationSeconds: number };
}

/** Delegates to the scripted runner, except for model calls that stall or stay silent. */
class StallingRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  public stalledCalls = 0;
  public constructor(private readonly scripted: ScriptedAgentRunner) {}

  public buildUserMessage(text: string) {
    return this.scripted.buildUserMessage(text);
  }

  public async runModelStep(turn: AgentTurnContext) {
    const text = turn.messages.at(-1)?.text;
    if (text === stallCommand) {
      this.stalledCalls += 1;
      await new Promise((_, reject) => {
        turn.cancellationSignal.addEventListener("abort", () => reject(new Error("model call cancelled")));
      });
    }
    if (text === silentCommand) {
      await delay(silentCallMs);
    }
    return this.scripted.runModelStep(turn);
  }

  public executeDeferredToolCall(call: Parameters<ScriptedAgentRunner["executeDeferredToolCall"]>[0]) {
    return this.scripted.executeDeferredToolCall(call);
  }
}

describe("RunModel limit", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  const runners: Record<string, StallingRunner> = {};
  const clients: Record<string, AgentSessionTestClient> = {};

  before(async () => {
    engine = await PrivateDexEngine.start();
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flows = Object.entries<AgentSessionOptions>({
      LimitDefault: {},
      LimitRaised: { runModelLimit: { methodTimeoutMs: 3_600_000, maximumAttempts: 4 } },
      LimitBudget: { runModelLimit: { methodTimeoutMs: 5_000, maximumAttempts: 3 } },
      LimitTwoAttempts: { runModelLimit: { methodTimeoutMs: 3_000, maximumAttempts: 2 } },
      LimitOneAttempt: { runModelLimit: { methodTimeoutMs: 2_000, maximumAttempts: 1 } },
    }).map(([flowType, options]) => {
      const runner = new StallingRunner(new ScriptedAgentRunner(markerDirectory, "throw"));
      runners[flowType] = runner;
      return createAgentSessionFlow(runner, flowType, {
        ...options,
        toolPolicies: { get_weather: { effect: "read" } },
      });
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows,
      isWorkerInProcess: true,
    });
    for (const flow of flows) {
      clients[flow.getFlowType()] = new AgentSessionTestClient(environment.client, flow);
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("the default limit is what every session had before", async () => {
    const sessions = clients.LimitDefault!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    for (const options of await readRunModelMethodOptions(sessionId)) {
      assert.equal(options.timeoutSeconds, 900);
      assert.equal(options.heartbeatTimeoutSeconds, 120);
      assert.equal(options.retryPolicy.maximumAttempts, 3);
      assert.equal(options.retryPolicy.totalDurationSeconds, 1800);
    }
  });

  test("every RunModel of a session gets the limit, and the budget moves with it", async () => {
    const sessions = clients.LimitRaised!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    await sessions.sendUserMessage(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 2);

    const everyOptions = await readRunModelMethodOptions(sessionId);
    assert.equal(everyOptions.length, 4);
    for (const options of everyOptions) {
      assert.equal(options.timeoutSeconds, 3600);
      assert.equal(options.heartbeatTimeoutSeconds, 120);
      assert.equal(options.retryPolicy.maximumAttempts, 4);
      assert.equal(options.retryPolicy.totalDurationSeconds, 3 * 3600);
    }
  });

  test("a call past the limit ends its attempt, and the budget stops the retries", async () => {
    const sessions = clients.LimitBudget!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, stallCommand);

    const state = await sessions.waitForCompletedTurns(sessionId, 1);

    assert.ok(state.lastTurnError);
    // A 10-second budget: attempt 2 ends past it, so the third attempt never starts.
    assert.equal(runners.LimitBudget!.stalledCalls, 2);
  });

  test("two attempts retry a call that timed out", async () => {
    const sessions = clients.LimitTwoAttempts!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, stallCommand);

    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(runners.LimitTwoAttempts!.stalledCalls, 2);
  });

  test("one attempt means a failed call is not repeated", async () => {
    const sessions = clients.LimitOneAttempt!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, stallCommand);

    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(runners.LimitOneAttempt!.stalledCalls, 1);
  });

  test("a silent call past the heartbeat timeout stays on its first attempt", { timeout: 200_000 }, async () => {
    const sessions = clients.LimitDefault!;
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, silentCommand);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 180_000 });

    assert.equal(state.lastTurnError, null);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    const runModel = history.filter((entry) => entry.stepType === "RunModel" && entry.eventType === executeCompletedEvent);
    assert.deepEqual(runModel.map((entry) => entry.finalAttempt), [1]);
  });

  test("an invalid limit is rejected at startup", () => {
    const runner = runners.LimitDefault!;
    for (const runModelLimit of [
      { methodTimeoutMs: 0 },
      { methodTimeoutMs: 1_500 },
      { maximumAttempts: 0 },
      { methodTimeoutMs: 2_147_483_647_000, maximumAttempts: 3 },
    ]) {
      assert.throws(() => createAgentSessionFlow(runner, "LimitInvalid", { runModelLimit }), RangeError);
    }
  });

  async function readRunModelMethodOptions(sessionId: string): Promise<RunModelMethodOptions[]> {
    const { stdout } = await execFileAsync(
      process.env.DEXCLI ?? "dexcli",
      ["flow", "history", sessionId, "--server", engine.serverAddress, "--all", "--output", "json"],
      { maxBuffer: 64 * 1024 * 1024 },
    );
    const { events } = JSON.parse(stdout) as {
      readonly events: readonly {
        readonly type: string;
        readonly payload: { readonly context: { readonly stepType: string; readonly methodOptions: RunModelMethodOptions } };
      }[];
    };
    return events
      .filter((event) => event.type === executeCompletedEvent && event.payload.context.stepType === "RunModel")
      .map((event) => event.payload.context.methodOptions);
  }
});

function newSessionId(): string {
  return `limit-${randomUUID()}`;
}
