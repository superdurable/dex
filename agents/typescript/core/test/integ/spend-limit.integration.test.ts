// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { after, before, beforeEach, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import {
  modelCallRefusedErrorPrefix,
  turnFailureNoteHeader,
  type ModelCallCheck,
  type ModelCallDecision,
  type ModelSpendLimit,
  type ModelUsageReport,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import { ScriptedModelClient, createStepwiseAgentSessionFlow } from "../../src/stepwise/index.js";
import { createCompactionTestAgentFlow, readLoggedCompactionModelCalls } from "../fixtures/compaction-test-agent.js";
import { createScriptedAgentFlow, scriptedAgentMarkerDirectoryVariable } from "../fixtures/scripted-agent-runner.js";
import {
  spendLimitKillFlowType,
  spendLimitReportGateName,
  spendLimitReportLogName,
} from "../fixtures/spend-limit-test-agent-worker.js";

const killWorkerEntryModulePath = fileURLToPath(
  new URL("../fixtures/spend-limit-test-agent-worker.js", import.meta.url),
);
const padTurnCommands = [1, 2, 3, 4, 5, 6].map((turn) => `pad 400 t${turn}`);

/** A spend limit a test steers directly: the Worker runs in this process. */
class ControlledSpendLimit implements ModelSpendLimit {
  public readonly checks: ModelCallCheck[] = [];
  public readonly reports: ModelUsageReport[] = [];
  public decide: (check: ModelCallCheck) => Promise<ModelCallDecision> = () => Promise.resolve({ isAllowed: true });
  public afterReport: () => Promise<void> = () => Promise.resolve();

  public checkModelCall(check: ModelCallCheck): Promise<ModelCallDecision> {
    this.checks.push(check);
    return this.decide(check);
  }

  public async reportModelUsage(report: ModelUsageReport): Promise<void> {
    this.reports.push(report);
    await this.afterReport();
  }

  public reset(): void {
    this.checks.length = 0;
    this.reports.length = 0;
    this.decide = () => Promise.resolve({ isAllowed: true });
    this.afterReport = () => Promise.resolve();
  }
}

describe("the application's spend limit", () => {
  const spendLimit = new ControlledSpendLimit();
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let compactionSessions: AgentSessionTestClient;
  let killSessions: AgentSessionTestClient;
  let markerDirectory: string;
  let compactionMarkers: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    compactionMarkers = mkdtempSync(join(engine.stateDirectory, "compaction-"));
    const flow = createScriptedAgentFlow(markerDirectory, { flowType: "SpendLimitAgent", spendLimit });
    const compactionFlow = createCompactionTestAgentFlow(compactionMarkers, 600, 0, {
      flowType: "SpendLimitCompactionAgent",
      spendLimit,
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, compactionFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    compactionSessions = new AgentSessionTestClient(environment.client, compactionFlow);
    const killEnvironmentFlow = createScriptedAgentFlow(markerDirectory, { flowType: spendLimitKillFlowType });
    const killEnvironment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [killEnvironmentFlow],
      isWorkerInProcess: false,
    });
    closeKillEnvironment = () => killEnvironment.close();
    killSessions = new AgentSessionTestClient(killEnvironment.client, killEnvironmentFlow);
  });

  let closeKillEnvironment: () => Promise<void> = () => Promise.resolve();

  beforeEach(() => {
    spendLimit.reset();
  });

  after(async () => {
    await closeKillEnvironment();
    await environment?.close();
    await engine?.stop();
  });

  test("a Stepwise definition carries the spend limit", () => {
    const model = new ScriptedModelClient(() => ({ text: "hi" }));
    const flow = createStepwiseAgentSessionFlow(
      { name: "agent", instructions: "Help.", model, spendLimit },
      "SpendLimitStepwiseAgent",
    );

    assert.equal(flow.options.spendLimit, spendLimit);
  });

  test("every call is checked before and reported after", async () => {
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather", {}, { account: "acme" });
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.deepEqual(spendLimit.checks.map((check) => check.stepType), ["RunModel", "RunModel"]);
    assert.deepEqual(spendLimit.checks.map((check) => check.sessionSettings), [{ account: "acme" }, { account: "acme" }]);
    assert.equal(spendLimit.reports.length, 2);
    assert.equal(new Set(spendLimit.reports.map((report) => report.reportId)).size, 2);
    assert.ok(spendLimit.reports[0]?.reportId.startsWith(`${sessionId}/turn-1/`));
  });

  test("a refused call ends the turn with the reason, and a later message can run", async () => {
    spendLimit.decide = () => Promise.resolve({ isAllowed: false, reason: "account over its monthly limit" });
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");

    const state = await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(state.lastTurnError, `${modelCallRefusedErrorPrefix}: account over its monthly limit`);
    assert.deepEqual(spendLimit.reports, []);
    assert.deepEqual((await executedStepTypes(sessionId)).slice(-2), ["RunModel", "RecordTurnFailure"]);
    const events = await sessions.listAgentEvents(sessionId);
    assert.equal(events.filter((event) => event.type === "turn_failed").length, 1);

    spendLimit.decide = () => Promise.resolve({ isAllowed: true });
    assert.equal(await sessions.sendUserMessage(sessionId, "again"), true);
    const next = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(next.lastTurnError, null);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "echo: again");
  });

  test("a refusal after a tool call keeps the result for the next turn", async () => {
    let calls = 0;
    spendLimit.decide = () =>
      Promise.resolve((calls += 1) === 1 ? { isAllowed: true } : { isAllowed: false, reason: "out of budget" });
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");

    const state = await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(state.lastTurnError, `${modelCallRefusedErrorPrefix}: out of budget`);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.ok(snapshot.messages.at(-1)?.text?.includes(turnFailureNoteHeader));
    assert.equal(spendLimit.reports.length, 1);
  });

  test("a check that throws fails closed after the retries", async () => {
    spendLimit.decide = () => Promise.reject(new Error("the spend service is down"));
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");

    const state = await sessions.waitForCompletedTurns(sessionId, 1);

    assert.ok(state.lastTurnError?.includes("the spend service is down"));
    assert.ok(!state.lastTurnError?.startsWith(modelCallRefusedErrorPrefix));
    assert.deepEqual(spendLimit.checks.map((check) => check.attempt), [1, 2, 3]);
    assert.deepEqual(spendLimit.reports, []);
  });

  test("a check that recovers lets the retry call the model", async () => {
    let calls = 0;
    spendLimit.decide = () =>
      (calls += 1) === 1 ? Promise.reject(new Error("the spend service blinked")) : Promise.resolve({ isAllowed: true });
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");

    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "echo: hello");
    assert.deepEqual(spendLimit.reports.map((report) => report.attempt), [2]);
  });

  test("a check that hangs times out and fails closed", { timeout: 200_000 }, async () => {
    spendLimit.decide = () => new Promise(() => undefined);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 180_000 });

    assert.ok(state.lastTurnError?.includes("checkModelCall took more than"));
    assert.equal(spendLimit.checks.length, 3);
  });

  test("a report that throws does not repeat the paid call", async (context) => {
    context.mock.method(console, "warn", () => undefined);
    spendLimit.afterReport = () => Promise.reject(new Error("the spend service is down"));
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");

    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "echo: hello");
    assert.deepEqual(spendLimit.checks.map((check) => check.attempt), [1]);
    assert.equal(spendLimit.reports.length, 1);
  });

  test("a failed attempt reports nothing", async () => {
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather then fail");

    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.deepEqual(spendLimit.checks.map((check) => check.attempt), [1, 1, 2, 3]);
    assert.equal(spendLimit.reports.length, 1);
  });

  test("a Worker killed after the report reports the retry under a new ID", async () => {
    const start = (): Promise<KillableWorkerProcess> =>
      KillableWorkerProcess.start({
        entryModulePath: killWorkerEntryModulePath,
        serverAddress: engine.serverAddress,
        logPath: join(markerDirectory, "spend-worker.log"),
        environment: { [scriptedAgentMarkerDirectoryVariable]: markerDirectory },
      });
    const first = await start();
    const sessionId = newSessionId();
    await killSessions.startSession(sessionId, "hello");
    const gate = new FileGate(markerDirectory, spendLimitReportGateName);
    await gate.waitUntilReached();

    await first.kill();
    gate.release();
    const second = await start();
    try {
      await killSessions.waitForCompletedTurns(sessionId, 1);
      const reports = readFileSync(join(markerDirectory, spendLimitReportLogName), "utf8")
        .trim()
        .split("\n")
        .map((line) => JSON.parse(line) as ModelUsageReport);
      assert.deepEqual(reports.map((report) => report.attempt), [1, 2]);
      assert.equal(new Set(reports.map((report) => report.reportId)).size, 2);
      // Both calls were paid for, and only the committed one is in AgentUsage.
      assert.equal((await killSessions.getAgentSnapshot(sessionId)).usage.modelCalls, 1);
    } finally {
      await second.kill();
    }
  });

  test("CompactContext is checked and reported, and a refusal ends the turn", async () => {
    const runTurns = async (sessionId: string, commands: readonly string[]): Promise<void> => {
      await compactionSessions.startSession(sessionId, commands[0] ?? "");
      for (const command of commands.slice(1)) {
        assert.equal(await compactionSessions.sendUserMessage(sessionId, command), true);
      }
      await compactionSessions.waitForCompletedTurns(sessionId, commands.length, { timeoutMs: 120_000 });
    };
    const compacted = newSessionId();
    await runTurns(compacted, padTurnCommands);
    assert.equal(spendLimit.checks.filter((check) => check.stepType === "CompactContext").length, 1);
    const reports = spendLimit.reports.filter((report) => report.stepType === "CompactContext");
    assert.equal(reports.length, 1);
    assert.ok((reports[0]?.usage.modelCalls ?? 0) >= 1);

    const refused = newSessionId();
    await runTurns(refused, padTurnCommands.slice(0, -1));
    spendLimit.decide = (check) =>
      Promise.resolve(
        check.stepType === "CompactContext" ? { isAllowed: false, reason: "compact out of budget" } : { isAllowed: true },
      );
    const summariesBefore = readLoggedCompactionModelCalls(compactionMarkers).filter((call) => call.kind === "summary").length;
    assert.equal(await compactionSessions.sendUserMessage(refused, padTurnCommands.at(-1) ?? ""), true);
    const state = await compactionSessions.waitForCompletedTurns(refused, padTurnCommands.length);

    assert.equal(state.lastTurnError, `${modelCallRefusedErrorPrefix}: compact out of budget`);
    assert.equal(
      readLoggedCompactionModelCalls(compactionMarkers).filter((call) => call.kind === "summary").length,
      summariesBefore,
    );
  });

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }
});

function newSessionId(): string {
  return `spend-${randomUUID()}`;
}
