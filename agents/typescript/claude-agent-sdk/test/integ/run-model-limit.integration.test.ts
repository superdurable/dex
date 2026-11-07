// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";

describe("the RunModel limit of a Claude agent session", () => {
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

  test("a segment past the limit reruns whole, then fails the turn", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, {
      runModelLimit: { methodTimeoutMs: 4_000, maximumAttempts: 2 },
    });
    const sessionId = harness.newSessionId("segment-limit");
    await harness.sessions.startSession(sessionId, "stall never-released");

    const snapshot = await harness.waitForTurns(sessionId, 1);

    assert.ok(snapshot.state?.lastTurnError);
    assert.equal(harness.gate("never-released").reachedCount(), 2);
  });

  test("a segment within a raised limit finishes", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, {
      runModelLimit: { methodTimeoutMs: 3_600_000, maximumAttempts: 1 },
    });
    const sessionId = harness.newSessionId("segment-raised");
    await harness.sessions.startSession(sessionId, "stall turn-gate");
    const gate = harness.gate("turn-gate");
    await gate.waitUntilReached();

    gate.release();
    const snapshot = await harness.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "stalled at turn-gate");
    assert.equal(gate.reachedCount(), 1);
  });
});
