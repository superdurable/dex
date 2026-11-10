// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { DexServiceError, FlowNotActiveOrNotFoundError } from "@superdurable/dex";

import { isStateLimitFailure } from "../../src/agent-session-steps.js";

import {
  AgentSessionFailedError,
  createAgentSessionFlow,
  readAgentSnapshot,
  sendAgentUserMessage,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentSnapshot,
  type DeferredToolOutcome,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "../../src/testing/index.js";
import {
  readAgentSessionUIMessages,
  streamAgentEventSseFrames,
  streamAgentUIMessageChunks,
  type AgentUIMessageChunk,
} from "../../src/ui-stream/index.js";
import { createTurnReplayCursor } from "../../src/ui-stream/agent-ui-message-stream.js";
import { encodeAgentUIStreamCursor } from "../../src/ui-stream/agent-ui-stream-cursor.js";
import { StateLimitRunner } from "../fixtures/state-limit-runner.js";

const stateLimitBytes = 131_072;
// A stored answer costs about twice its characters: two of these leave room for a large write.
const largeAnswer = "pad 20000";
// The gRPC status code of a write that does not fit.
const resourceExhaustedCode = 8;
const noRoomSessions = 24;
const noRoomSmallTurns = 40;
const smallToolTurn = "call bulk 40";
const stateLimitReason = new RegExp(`^(active )?run state of \\d+ bytes exceeds the maximum of ${stateLimitBytes} bytes$`);

interface Harness {
  readonly engine: PrivateDexEngine;
  readonly environment: AgentTestEnvironment;
  readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  readonly runner: StateLimitRunner;
  readonly sessions: AgentSessionTestClient;
}

async function withHarness(
  run: (harness: Harness) => Promise<void>,
  limits: { readonly stateBytes: number; readonly valueBytes: number } = {
    stateBytes: stateLimitBytes,
    valueBytes: stateLimitBytes,
  },
): Promise<void> {
  const directory = mkdtempSync(join(tmpdir(), "dex-agents-state-limit-"));
  const configPath = join(directory, "server-zd.yaml");
  writeFileSync(
    configPath,
    `values:\n  maxValueBytes: ${limits.valueBytes}\nlimits:\n  maxFlowStateBytes: ${limits.stateBytes}\n` +
      "dev:\n  durabilityMode: processCrashSafe\n",
  );
  const runner = new StateLimitRunner();
  const flow = createAgentSessionFlow(runner, "StateLimitAgent", {
    toolPolicies: {
      bulk: { effect: "read" },
      bulk_write: { effect: "non_idempotent_write", requiresApproval: false },
    },
  });
  const engine = await PrivateDexEngine.start({ serverZdConfigPath: configPath });
  const environment = await AgentTestEnvironment.start({
    serverAddress: engine.serverAddress,
    flows: [flow],
    isWorkerInProcess: true,
  });
  try {
    await run({ engine, environment, flow, runner, sessions: new AgentSessionTestClient(environment.client, flow) });
  } finally {
    await environment.close();
    await engine.stop();
    rmSync(directory, { recursive: true, force: true });
  }
}

async function closeReason(harness: Harness, sessionId: string): Promise<string> {
  const result = await harness.environment.client.waitForFlow(sessionId);
  assert.equal(result.status, "failed");
  assert.match(result.errorMessage ?? "", stateLimitReason);
  return result.errorMessage ?? "";
}

function storedSnapshot(harness: Harness, sessionId: string): Promise<AgentSnapshot> {
  return harness.environment.client.invokeRPC(harness.flow.getAgentSnapshot, sessionId);
}

function send(harness: Harness, sessionId: string, text: string): Promise<boolean> {
  return sendAgentUserMessage(harness.environment.client, harness.flow, sessionId, { text });
}

async function assertNewMessagesAreRejected(harness: Harness, sessionId: string, reason: string): Promise<void> {
  await assert.rejects(send(harness, sessionId, "say hello"), (error: unknown) => {
    assert.ok(error instanceof AgentSessionFailedError);
    assert.equal(error.reason, reason);
    assert.ok(error.message.includes(reason));
    return true;
  });
}

/** Reads the event stream to its end; a stream that never ends times the test out. */
async function readAllEventFrames(harness: Harness, sessionId: string): Promise<string[]> {
  const frames: string[] = [];
  for await (const frame of streamAgentEventSseFrames({
    client: harness.environment.client,
    flow: harness.flow,
    flowId: sessionId,
    pollTimeoutMs: 1_000,
  })) {
    frames.push(frame);
  }
  return frames;
}

/** A browser that follows the stale turn ends with the Flow's reason. */
async function assertUIStreamReportsFailure(
  harness: Harness,
  sessionId: string,
  activeTurnId: string,
  reason: string,
): Promise<void> {
  const { client } = harness.environment;
  const cursor = await createTurnReplayCursor(client, harness.flow, sessionId, activeTurnId, null);
  assert.ok(cursor !== undefined);
  const chunks: AgentUIMessageChunk[] = [];
  for await (const chunk of streamAgentUIMessageChunks({
    client,
    flow: harness.flow,
    flowId: sessionId,
    resumeToken: encodeAgentUIStreamCursor(cursor),
    pollTimeoutMs: 1_000,
  })) {
    chunks.push(chunk);
  }
  assert.deepEqual(
    chunks.filter((chunk) => chunk.type === "error" || chunk.type === "finish"),
    [
      { type: "error", errorText: reason },
      { type: "finish", finishReason: "error" },
    ],
  );
}

async function fill(harness: Harness, sessionId: string, turns: number): Promise<void> {
  for (let turn = 1; turn <= turns; turn += 1) {
    assert.equal(await send(harness, sessionId, largeAnswer), true);
    await harness.sessions.waitForCompletedTurns(sessionId, turn);
  }
}

describe("a session at limits.maxFlowStateBytes", () => {
  test("a model answer over the limit fails the session and keeps queued messages", async () => {
    await withHarness(async (harness) => {
      const sessionId = `session-${randomUUID()}`;
      await harness.sessions.startSession(sessionId);
      // Two answers fit; the third crosses the limit by far more than the failure's own commit.
      const queued = Array.from({ length: 8 }, (_unused, offset) => `pad ${30_000 + offset}`);
      for (const text of queued) {
        assert.equal(await send(harness, sessionId, text), true);
      }

      const reason = await closeReason(harness, sessionId);

      // The commit that stores the failure closes the Flow, so the stored state says it all.
      const stored = await storedSnapshot(harness, sessionId);
      assert.equal(stored.state?.status, "failed");
      assert.equal(stored.state?.lastTurnError, reason);
      assert.equal(stored.state?.activeTurnId, null);
      assert.deepEqual(await readAgentSnapshot(harness.environment.client, harness.flow, sessionId), stored);

      // Only one paid model call was lost; the session did not go on to the queued messages.
      const turnCount = stored.state?.turnCount ?? 0;
      assert.equal(harness.runner.modelCalls, turnCount);
      const waiting = (stored.queuedMessages ?? []).map((pending) => pending.message.text);
      assert.deepEqual(waiting, queued.slice(turnCount));
      assert.ok(waiting.length > 0);
      assert.equal(stored.queuedMessageCount, waiting.length);

      await assertNewMessagesAreRejected(harness, sessionId, reason);
    });
  });

  for (const [toolName, recoveryStep] of [
    ["bulk", "RecordToolFailure"],
    ["bulk_write", "RecordToolOutcomeUnknown"],
  ] as const) {
    test(`a ${toolName} result over the limit fails the session`, async () => {
      await withHarness(async (harness) => {
        const sessionId = `session-${randomUUID()}`;
        await harness.sessions.startSession(sessionId);
        await fill(harness, sessionId, 2);
        const modelCallsBefore = harness.runner.modelCalls;
        assert.equal(await send(harness, sessionId, `call ${toolName} 70000`), true);
        assert.equal(await send(harness, sessionId, "say after the tool"), true);

        const reason = await closeReason(harness, sessionId);

        const history = await readStepHistory(harness.engine.serverAddress, sessionId);
        assert.ok(
          history.some((entry) => entry.eventType === executeCompletedEvent && entry.stepType === recoveryStep),
        );
        assert.equal(harness.runner.toolRuns.get(toolName), 1);
        // The model never saw a tool failure, so it cannot decide to call the tool again.
        assert.equal(harness.runner.modelCalls, modelCallsBefore + 1);
        const stored = await storedSnapshot(harness, sessionId);
        assert.equal(stored.state?.status, "failed");
        assert.equal(stored.state?.lastTurnError, reason);
        assert.equal(stored.state?.activeTurnId, null);
        assert.deepEqual(
          (stored.queuedMessages ?? []).map((pending) => pending.message.text),
          ["say after the tool"],
        );
        await assertNewMessagesAreRejected(harness, sessionId, reason);
      });
    });
  }

  test("a session with no room to store its failure still reports it", async () => {
    await withHarness(async (harness) => {
      // Sessions differ in how much room the last commit left, so some cannot store the failure.
      // Small tool turns fail some sessions mid-turn.
      const runToFailure = async (firstAnswerCharacters: number): Promise<boolean> => {
        const sessionId = `session-${randomUUID()}`;
        await harness.sessions.startSession(sessionId);
        // All sent while there is room: a later send could be refused instead of failing a Step.
        const texts = ["pad 25000", `pad ${firstAnswerCharacters}`, ...Array<string>(noRoomSmallTurns).fill(smallToolTurn)];
        for (const text of texts) {
          assert.equal(await send(harness, sessionId, text), true);
        }

        const reason = await closeReason(harness, sessionId);

        const snapshot = await readAgentSnapshot(harness.environment.client, harness.flow, sessionId);
        assert.equal(snapshot.state?.status, "failed");
        assert.equal(snapshot.state?.lastTurnError, reason);
        assert.equal(snapshot.state?.activeTurnId, null);
        assert.deepEqual(snapshot.pendingInterrupts, []);
        assert.deepEqual(snapshot.toolCalls, []);
        await assertNewMessagesAreRejected(harness, sessionId, reason);
        const uiMessages = await readAgentSessionUIMessages({
          client: harness.environment.client,
          flow: harness.flow,
          flowId: sessionId,
        });
        assert.equal(uiMessages.failure, reason);
        await readAllEventFrames(harness, sessionId);
        const stored = await storedSnapshot(harness, sessionId);
        if (stored.state?.activeTurnId != null) {
          await assertUIStreamReportsFailure(harness, sessionId, stored.state.activeTurnId, reason);
        }
        return stored.state?.activeTurnId != null;
      };

      const storedActiveTurns = await Promise.all(
        Array.from({ length: noRoomSessions }, (_unused, offset) => runToFailure(25_000 + 37 * offset)),
      );

      // A Step without a failure route, or a route whose commit did not fit, left its turn active.
      assert.ok(storedActiveTurns.includes(true));
    });
  });

  test("the event stream of a failed session ends", async () => {
    await withHarness(async (harness) => {
      const sessionId = `session-${randomUUID()}`;
      await harness.sessions.startSession(sessionId);
      for (let turn = 0; turn < 3; turn += 1) {
        assert.equal(await send(harness, sessionId, "pad 30000"), true);
      }
      await closeReason(harness, sessionId);

      const frames = await readAllEventFrames(harness, sessionId);

      assert.ok(frames.some((frame) => frame.includes("turn_failed")));
    });
  });

  test("a message to a session that does not exist keeps the Dex error", async () => {
    await withHarness(async (harness) => {
      await assert.rejects(send(harness, "no-such-session", "say hello"), (error: unknown) => {
        assert.ok(error instanceof FlowNotActiveOrNotFoundError);
        assert.equal(error.constructor, FlowNotActiveOrNotFoundError);
        return true;
      });
    });
  });

  test("the state limit failure is told from other failures", () => {
    const failure = (errorType: string, detail: string): boolean => isStateLimitFailure({ detail, errorType });
    assert.equal(failure("ValueTooLarge", "run state of 9 bytes exceeds the maximum of 8 bytes"), true);
    // The text of a server that counts only active state, server-zd 0.1.21 and later.
    assert.equal(failure("ValueTooLarge", "active run state of 9 bytes exceeds the maximum of 8 bytes"), true);
    assert.equal(failure("ValueTooLarge", "value of 9 bytes exceeds the maximum of 8 bytes"), false);
    assert.equal(failure("WorkerError", "run state of 9 bytes exceeds the maximum of 8 bytes"), false);
  });

  test("a message that does not fit is refused and the session stays usable", async () => {
    await withHarness(async (harness) => {
      const sessionId = `session-${randomUUID()}`;
      await harness.sessions.startSession(sessionId);
      await fill(harness, sessionId, 2);

      // No Step fails here, so the session does not fail: only the caller learns of it.
      await assert.rejects(send(harness, sessionId, `say ${"u".repeat(60_000)}`), (error: unknown) => {
        assert.ok(error instanceof DexServiceError);
        assert.equal(error.code, resourceExhaustedCode);
        assert.match(error.detail, stateLimitReason);
        return true;
      });

      const snapshot = await readAgentSnapshot(harness.environment.client, harness.flow, sessionId);
      assert.equal(snapshot.state?.status, "waiting_for_user_message");
      assert.equal(snapshot.queuedMessageCount, 0);
      assert.equal(await send(harness, sessionId, "say still here"), true);
      await harness.sessions.waitForCompletedTurns(sessionId, 3);
    });
  });

  test("one value over its own limit fails only the turn", async () => {
    await withHarness(
      async (harness) => {
        const sessionId = `session-${randomUUID()}`;
        await harness.sessions.startSession(sessionId, "pad 40000");
        const failed = await pollUntil(
          async () => {
            const state = (await harness.sessions.getAgentSnapshot(sessionId)).state;
            return state?.lastTurnError == null ? undefined : state;
          },
          { description: "the turn to fail", timeoutMs: 60_000 },
        );
        assert.equal(failed.status, "waiting_for_user_message");

        assert.equal(await send(harness, sessionId, "say next"), true);
        await harness.sessions.waitForCompletedTurns(sessionId, 2);
        assert.equal((await harness.sessions.getAgentSnapshot(sessionId)).state?.lastTurnError, null);
      },
      { stateBytes: 1_048_576, valueBytes: 65_536 },
    );
  });
});
