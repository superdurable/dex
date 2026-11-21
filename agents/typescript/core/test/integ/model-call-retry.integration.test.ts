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

import {
  createAgentSessionFlow,
  type AgentFrameworkRunner,
  type AgentSessionOptions,
  type AgentTurnContext,
  type DeferredToolOutcome,
} from "../../src/index.js";
import {
  classifyModelError,
  decideModelCallRetry,
  resolveModelRetryPolicy,
} from "../../src/model-call-retry.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import { ScriptedAgentRunner } from "../fixtures/scripted-agent-runner.js";

/** An error shaped like the OpenAI and Anthropic SDKs' `APIError`. */
class FakeApiError extends Error {
  public constructor(
    public readonly status: number,
    public readonly headers: Headers = new Headers(),
  ) {
    super(`${status} model API error`);
    this.name = "APIError";
  }
}

/** Fails every model call with `failure` until `outageMs` after the first call, then delegates. */
class OutageRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  public readonly callTimes: number[] = [];
  public constructor(
    private readonly scripted: ScriptedAgentRunner,
    private readonly outageMs: number,
    private readonly failure: () => unknown,
  ) {}

  public buildUserMessage(text: string) {
    return this.scripted.buildUserMessage(text);
  }

  public async runModelStep(turn: AgentTurnContext) {
    const now = Date.now();
    this.callTimes.push(now);
    if (now - this.callTimes[0]! < this.outageMs) {
      throw this.failure();
    }
    return this.scripted.runModelStep(turn);
  }

  public executeDeferredToolCall(call: Parameters<ScriptedAgentRunner["executeDeferredToolCall"]>[0]) {
    return this.scripted.executeDeferredToolCall(call);
  }
}

describe("model call error classification", () => {
  test("429, 5xx, 408, 409, and connection errors are transient; other 4xx are permanent", () => {
    for (const status of [408, 409, 429, 500, 502, 503, 529]) {
      assert.equal(classifyModelError(new FakeApiError(status)).kind, "transient", `status ${status}`);
    }
    for (const status of [400, 401, 403, 404, 413, 422]) {
      assert.equal(classifyModelError(new FakeApiError(status)).kind, "permanent", `status ${status}`);
    }
    const aiSdkCallError = Object.assign(new Error("Service Unavailable"), {
      name: "AI_APICallError",
      statusCode: 503,
      isRetryable: true,
    });
    assert.equal(classifyModelError(aiSdkCallError).kind, "transient");
    const aiSdkRetryError = Object.assign(new Error("Failed after 3 attempts"), {
      name: "AI_RetryError",
      lastError: Object.assign(new Error("Bad Request"), { name: "AI_APICallError", statusCode: 400 }),
    });
    assert.equal(classifyModelError(aiSdkRetryError).kind, "permanent");
    const fetchFailed = new TypeError("fetch failed", {
      cause: Object.assign(new Error("other side closed"), { code: "UND_ERR_SOCKET" }),
    });
    assert.equal(classifyModelError(fetchFailed).kind, "transient");
    assert.equal(classifyModelError(Object.assign(new Error("Connection error."), { name: "APIConnectionError" })).kind, "transient");
    assert.equal(classifyModelError(Object.assign(new Error("503 overloaded"), { isRetryable: true })).kind, "transient");
    assert.equal(classifyModelError(Object.assign(new Error("400 bad"), { isRetryable: false })).kind, "unknown");
    assert.equal(classifyModelError(new Error("the runner has a bug")).kind, "unknown");
    assert.equal(classifyModelError("a string").kind, "unknown");
  });

  test("Retry-After and retry-after-ms are read in seconds, milliseconds, or an HTTP date", () => {
    assert.equal(classifyModelError(new FakeApiError(429, new Headers({ "retry-after": "7" }))).retryAfterMs, 7_000);
    assert.equal(classifyModelError(new FakeApiError(429, new Headers({ "retry-after-ms": "2500" }))).retryAfterMs, 2_500);
    const date = new Date(Date.now() + 20_000).toUTCString();
    const fromDate = classifyModelError(new FakeApiError(503, new Headers({ "retry-after": date }))).retryAfterMs!;
    assert.ok(fromDate > 15_000 && fromDate <= 20_000, `${fromDate}`);
    const aiSdk = { name: "AI_APICallError", statusCode: 429, responseHeaders: { "Retry-After": "4" } };
    assert.equal(classifyModelError(aiSdk).retryAfterMs, 4_000);
  });

  test("the default policy waits from 1 to 30 seconds, about two minutes in all", () => {
    const retry = resolveModelRetryPolicy();
    assert.deepEqual(retry, {
      initialIntervalMs: 1_000,
      maximumIntervalMs: 30_000,
      totalDurationMs: 120_000,
      // 1 + 2 + 4 + 8 + 16 + 30 + 30 = 91 seconds; one more wait passes 120.
      transientRetries: 7,
    });
    assert.equal(resolveModelRetryPolicy({ totalDurationMs: 0 }).transientRetries, 0);
    for (const policy of [
      { initialIntervalMs: 0 },
      { initialIntervalMs: 1_500 },
      { maximumIntervalMs: 500 },
      { initialIntervalMs: 10_000, maximumIntervalMs: 5_000 },
      { totalDurationMs: -1_000 },
    ]) {
      assert.throws(() => resolveModelRetryPolicy(policy), RangeError, JSON.stringify(policy));
    }
  });

  test("a transient error waits with jitter and at least Retry-After", () => {
    const retry = resolveModelRetryPolicy();
    const waits = (random: number) =>
      [1, 2, 3, 4, 5, 6, 7, 8].map((attempt) => {
        const decision = decideModelCallRetry(new FakeApiError(503), attempt, 3, retry, random);
        assert.equal(decision.kind, "retry_after");
        return decision.kind === "retry_after" ? decision.afterSeconds : 0;
      });
    assert.deepEqual(waits(0.999), [1, 2, 4, 8, 16, 30, 30, 30]);
    assert.deepEqual(waits(0), [1, 1, 2, 4, 8, 15, 15, 15]);
    const limited = new FakeApiError(429, new Headers({ "retry-after": "12" }));
    assert.deepEqual(decideModelCallRetry(limited, 1, 3, retry, 0), { kind: "retry_after", afterSeconds: 12 });
  });

  test("a permanent error fails the turn, and an unknown one goes to the attempt limit", () => {
    const retry = resolveModelRetryPolicy();
    assert.deepEqual(decideModelCallRetry(new FakeApiError(401), 1, 3, retry), { kind: "fail_turn" });
    assert.deepEqual(decideModelCallRetry(new Error("bug"), 1, 3, retry), { kind: "rethrow" });
    assert.deepEqual(decideModelCallRetry(new Error("bug"), 3, 3, retry), { kind: "rethrow" });
    // One attempt disables every retry; the Dex retry policy then allows no more attempts.
    assert.deepEqual(decideModelCallRetry(new FakeApiError(503), 1, 1, retry), { kind: "rethrow" });
  });
});

describe("RunModel retries during a model API outage", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  const runners: Record<string, OutageRunner> = {};
  const clients: Record<string, AgentSessionTestClient> = {};

  before(async () => {
    engine = await PrivateDexEngine.start();
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flows = Object.entries<[AgentSessionOptions, number, () => unknown]>({
      // The issue's outage: a 429, then 503s for 20 seconds.
      OutageDefault: [{}, 20_000, () => new FakeApiError(503)],
      OutageShortBudget: [
        { runModelLimit: { retry: { maximumIntervalMs: 2_000, totalDurationMs: 4_000 } } },
        600_000,
        () => new FakeApiError(503),
      ],
      OutageNoRetry: [{ runModelLimit: { maximumAttempts: 1 } }, 600_000, () => new FakeApiError(503)],
      BadRequest: [{}, 600_000, () => new FakeApiError(400)],
      RunnerBug: [{}, 600_000, () => new Error("the runner has a bug")],
    }).map(([flowType, [options, outageMs, failure]]) => {
      const runner = new OutageRunner(new ScriptedAgentRunner(markerDirectory, "throw"), outageMs, failure);
      runners[flowType] = runner;
      return createAgentSessionFlow(runner, flowType, { ...options, toolPolicies: { get_weather: { effect: "read" } } });
    });
    environment = await AgentTestEnvironment.start({ serverAddress: engine.serverAddress, flows, isWorkerInProcess: true });
    for (const flow of flows) {
      clients[flow.getFlowType()] = new AgentSessionTestClient(environment.client, flow);
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a 20-second outage no longer fails the turn", { timeout: 120_000 }, async () => {
    const sessionId = newSessionId();
    await clients.OutageDefault!.startSession(sessionId, "hello");

    const state = await clients.OutageDefault!.waitForCompletedTurns(sessionId, 1, { timeoutMs: 90_000 });

    assert.equal(state.lastTurnError, null);
    const callTimes = runners.OutageDefault!.callTimes;
    const lastCallMs = callTimes.at(-1)! - callTimes[0]!;
    assert.ok(lastCallMs >= 20_000, `the last call came ${lastCallMs} ms after the first`);
    // Waits of 0.5-1, 1-2, 2-4, 4-8, and 8-16 seconds reach 20 seconds within six or seven calls.
    assert.ok(callTimes.length >= 5 && callTimes.length <= 9, `${callTimes.length} calls`);
  });

  test("retries stop at the policy's budget and the turn fails", { timeout: 60_000 }, async () => {
    const sessionId = newSessionId();
    await clients.OutageShortBudget!.startSession(sessionId, "hello");

    const state = await clients.OutageShortBudget!.waitForCompletedTurns(sessionId, 1, { timeoutMs: 45_000 });

    assert.equal(state.lastTurnError, "503 model API error");
    // Nominal waits 1 + 2 fit in 4 seconds, so 3 + 2 attempts in all.
    assert.equal(runners.OutageShortBudget!.callTimes.length, 5);
    assert.deepEqual(await readRunModelFinalAttempts(sessionId), []);
  });

  test("maximumAttempts 1 still disables every retry", { timeout: 60_000 }, async () => {
    const sessionId = newSessionId();
    await clients.OutageNoRetry!.startSession(sessionId, "hello");

    const state = await clients.OutageNoRetry!.waitForCompletedTurns(sessionId, 1, { timeoutMs: 45_000 });

    assert.equal(state.lastTurnError, "503 model API error");
    assert.equal(runners.OutageNoRetry!.callTimes.length, 1);
  });

  test("a 400 fails the turn without a retry", { timeout: 60_000 }, async () => {
    const sessionId = newSessionId();
    await clients.BadRequest!.startSession(sessionId, "hello");

    const state = await clients.BadRequest!.waitForCompletedTurns(sessionId, 1, { timeoutMs: 45_000 });

    assert.equal(state.lastTurnError, "400 model API error");
    assert.equal(runners.BadRequest!.callTimes.length, 1);
    // The attempt ends the turn itself, so RunModel completes on its first attempt.
    assert.deepEqual(await readRunModelFinalAttempts(sessionId), [1]);
  });

  test("an error that is not a model API error keeps three attempts", { timeout: 60_000 }, async () => {
    const sessionId = newSessionId();
    await clients.RunnerBug!.startSession(sessionId, "hello");

    const state = await clients.RunnerBug!.waitForCompletedTurns(sessionId, 1, { timeoutMs: 45_000 });

    assert.equal(state.lastTurnError, "the runner has a bug");
    assert.equal(runners.RunnerBug!.callTimes.length, 3);
  });

  async function readRunModelFinalAttempts(sessionId: string): Promise<number[]> {
    const history = await readStepHistory(engine.serverAddress, sessionId);
    return history
      .filter((entry) => entry.stepType === "RunModel" && entry.eventType === executeCompletedEvent)
      .map((entry) => entry.finalAttempt);
  }
});

function newSessionId(): string {
  return `retry-${randomUUID()}`;
}
