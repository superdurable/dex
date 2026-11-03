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
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import {
  MAX_SESSION_SETTINGS_BYTES,
  SESSION_SETTINGS_SIZE_TOLERANCE_BYTES,
  invalidStartSessionSettingsReason,
  sessionSettingsSize,
  validateSessionSettings,
  type AgentSnapshot,
} from "../../src/index.js";
import { selectTurnSessionSettings } from "../../src/agent-session-transitions.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
  pollUntil,
  readStepHistory,
} from "../../src/testing/index.js";
import {
  createSessionSettingsTestAgentFlow,
  readModelCalls,
  sessionSettingsMarkerDirectoryVariable,
  sessionSettingsTestInstructions,
} from "../fixtures/session-settings-test-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/session-settings-test-agent-worker.js", import.meta.url));

describe("per-session settings", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let flow: ReturnType<typeof createSessionSettingsTestAgentFlow>;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = createSessionSettingsTestAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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

  async function startWorker(markerDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [sessionSettingsMarkerDirectoryVariable]: markerDirectory },
    });
    workers.push(worker);
    return worker;
  }

  async function lastAnswer(sessionId: string, turnCount: number): Promise<AgentSnapshot> {
    await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 60_000 });
    return sessions.getAgentSnapshot(sessionId);
  }

  test("settings from the start input choose the model and the instructions", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-start-${randomUUID()}`;
    await sessions.startSession(sessionId, "which-model", {}, { model: "alpha", persona: "pirate" });

    const snapshot = await lastAnswer(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `model alpha: ${sessionSettingsTestInstructions} Persona: pirate.`);
    assert.deepEqual(snapshot.sessionSettings, { model: "alpha", persona: "pirate" });
    const [stored, turnStored] = await environment.client.getAttributes(
      sessionId,
      flow.sessionSettings,
      flow.turnSessionSettings,
    );
    assert.deepEqual(stored, { model: "alpha", persona: "pirate" });
    assert.deepEqual(turnStored, { turnId: "turn-1", sessionSettings: { model: "alpha", persona: "pirate" } });
  });

  test("a session without settings uses the default model", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-none-${randomUUID()}`;
    await sessions.startSession(sessionId, "which-model");

    const snapshot = await lastAnswer(sessionId, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `model default: ${sessionSettingsTestInstructions}`);
    assert.equal(snapshot.sessionSettings, null);
  });

  test("a session started with the 0.1.0 start input runs without settings until one is set", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-released-${randomUUID()}`;
    // dex-agents 0.1.0 had no sessionSettings field.
    await environment.client.startFlow(flow, sessionId, { initialMessage: "lookup gated-3" });
    const gate = new FileGate(markerDirectory, "lookup");
    await gate.waitUntilReached();

    assert.equal(await sessions.updateSessionSettings(sessionId, { model: "beta" }), true);
    gate.release();
    await lastAnswer(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "which-model"), true);
    await lastAnswer(sessionId, 2);
    assert.deepEqual(readModelCalls(markerDirectory, sessionId), [
      "turn-1 1 default",
      "turn-1 2 default",
      "turn-2 1 beta",
    ]);
  });

  test("an update between turns applies to the next turn, and {} clears the settings", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-update-${randomUUID()}`;
    await sessions.startSession(sessionId, "which-model", {}, { model: "alpha" });
    await lastAnswer(sessionId, 1);

    assert.equal(await sessions.updateSessionSettings(sessionId, { model: "beta" }), true);
    assert.deepEqual((await sessions.getAgentSnapshot(sessionId)).sessionSettings, { model: "beta" });
    assert.equal(await sessions.sendUserMessage(sessionId, "which-model"), true);
    assert.equal((await lastAnswer(sessionId, 2)).messages.at(-1)?.text, `model beta: ${sessionSettingsTestInstructions}`);

    assert.equal(await sessions.updateSessionSettings(sessionId, {}), true);
    assert.equal(await sessions.sendUserMessage(sessionId, "which-model"), true);
    const third = await lastAnswer(sessionId, 3);
    assert.equal(third.messages.at(-1)?.text, `model default: ${sessionSettingsTestInstructions}`);
    assert.equal(third.sessionSettings, null);
  });

  test("a running turn keeps the settings it started with", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-running-${randomUUID()}`;
    await sessions.startSession(sessionId, "lookup gated-7", {}, { model: "alpha" });
    const gate = new FileGate(markerDirectory, "lookup");
    await gate.waitUntilReached();

    assert.equal(await sessions.updateSessionSettings(sessionId, { model: "beta" }), true);
    gate.release();
    const first = await lastAnswer(sessionId, 1);
    assert.equal(first.messages.at(-1)?.text, "lookup_order: found gated-7");
    assert.deepEqual(readModelCalls(markerDirectory, sessionId), ["turn-1 1 alpha", "turn-1 2 alpha"]);

    assert.equal(await sessions.sendUserMessage(sessionId, "which-model"), true);
    assert.equal((await lastAnswer(sessionId, 2)).messages.at(-1)?.text, `model beta: ${sessionSettingsTestInstructions}`);
  });

  test("a Worker killed after an update retries the model call with the turn's settings", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `settings-kill-${randomUUID()}`;
    await sessions.startSession(sessionId, "lookup-then-stall order-7", {}, { model: "alpha" });
    const gate = new FileGate(markerDirectory, "model");
    await gate.waitUntilReached();
    assert.equal(await sessions.updateSessionSettings(sessionId, { model: "beta" }), true);

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    const first = await lastAnswer(sessionId, 1);

    assert.equal(first.messages.at(-1)?.text, "lookup_order: found order-7");
    assert.deepEqual(readModelCalls(markerDirectory, sessionId), [
      "turn-1 1 alpha",
      "turn-1 2 alpha",
      "turn-1 2 alpha",
    ]);
    assert.deepEqual(first.sessionSettings, { model: "beta" });
    assert.equal(await sessions.sendUserMessage(sessionId, "which-model"), true);
    assert.equal((await lastAnswer(sessionId, 2)).messages.at(-1)?.text, `model beta: ${sessionSettingsTestInstructions}`);
  });

  test("settings over the limit are refused before and inside the session", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    // {"note":"<padding>"} is 11 bytes plus the padding.
    const largest = { note: "x".repeat(MAX_SESSION_SETTINGS_BYTES - 11) };
    const tooLarge = { note: "x".repeat(MAX_SESSION_SETTINGS_BYTES - 10) };
    assert.equal(sessionSettingsSize(largest), MAX_SESSION_SETTINGS_BYTES);
    validateSessionSettings(largest);
    assert.throws(() => validateSessionSettings(tooLarge), /more than the 16384 allowed/);
    assert.throws(() => validateSessionSettings([1]), TypeError);
    // Non-ASCII counts as UTF-8 bytes, not characters.
    assert.equal(sessionSettingsSize({ note: "é" }), 13);

    const sessionId = `settings-limit-${randomUUID()}`;
    await sessions.startSession(sessionId, null, {}, largest);
    await pollUntil(async () => (await sessions.readAgentState(sessionId)) ?? undefined, {
      description: "the started session",
    });
    // The session accepts a tolerance over the Client's limit, never more.
    const beyondTolerance = { note: "x".repeat(MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES) };
    assert.equal(await sessions.updateSessionSettings(sessionId, beyondTolerance), false);
    assert.deepEqual((await sessions.getAgentSnapshot(sessionId)).sessionSettings, largest);
    assert.equal(await sessions.updateSessionSettings(sessionId, tooLarge), true);
    assert.deepEqual((await sessions.getAgentSnapshot(sessionId)).sessionSettings, tooLarge);
  });

  test("an update without settings returns false and changes nothing", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `settings-null-${randomUUID()}`;
    await sessions.startSession(sessionId, "which-model", {}, { model: "alpha" });
    await lastAnswer(sessionId, 1);

    assert.equal(
      await environment.client.invokeRPC(flow.updateSessionSettings, sessionId, { sessionSettings: null }),
      false,
    );
    assert.deepEqual((await sessions.getAgentSnapshot(sessionId)).sessionSettings, { model: "alpha" });
  });

  test("a start whose settings the session refuses fails the session at once", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const oversized = { note: "x".repeat(MAX_SESSION_SETTINGS_BYTES + SESSION_SETTINGS_SIZE_TOLERANCE_BYTES) };
    await assert.rejects(sessions.startSession(`settings-refused-${randomUUID()}`, null, {}, oversized), RangeError);
    for (const [name, sessionSettings] of [
      ["oversized", oversized],
      ["array", [1, 2]],
    ] as const) {
      const sessionId = `settings-refused-${name}-${randomUUID()}`;
      // The raw Client skips validateSessionSettings.
      await environment.client.startFlow(flow, sessionId, {
        initialMessage: "which-model",
        sessionSettings: sessionSettings as never,
      });
      const result = await environment.client.waitForFlow(sessionId, 60_000);
      assert.equal(result.status, "failed", name);
      assert.match(result.errorMessage ?? "", new RegExp(invalidStartSessionSettingsReason), name);
      const starts = (await readStepHistory(engine.serverAddress, sessionId)).filter(
        (entry) => entry.stepType === "StartAgentSession",
      );
      assert.deepEqual([...new Set(starts.map((entry) => entry.finalAttempt))], [1], name);
    }
  });

  test("a copy an earlier turn left reads as no settings", () => {
    const stored = { turnId: "turn-1", sessionSettings: { model: "alpha" } };
    assert.deepEqual(selectTurnSessionSettings(stored, "turn-1"), { model: "alpha" });
    // A Worker without session settings began turn-2 and left turn-1's copy in place.
    assert.deepEqual(selectTurnSessionSettings(stored, "turn-2"), {});
    assert.deepEqual(selectTurnSessionSettings(stored, null), {});
    assert.deepEqual(selectTurnSessionSettings(null, "turn-1"), {});
  });
});

function newMarkerDirectory(engine: PrivateDexEngine): string {
  return mkdtempSync(join(engine.stateDirectory, "markers-"));
}
