// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import {
  modelCallRefusedErrorPrefix,
  type ModelCallCheck,
  type ModelCallDecision,
  type ModelSpendLimit,
  type ModelUsageReport,
} from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";

class RecordingSpendLimit implements ModelSpendLimit {
  public readonly checks: ModelCallCheck[] = [];
  public readonly reports: ModelUsageReport[] = [];
  public refusal: string | undefined;

  public checkModelCall(check: ModelCallCheck): Promise<ModelCallDecision> {
    this.checks.push(check);
    return Promise.resolve(
      this.refusal === undefined ? { isAllowed: true } : { isAllowed: false, reason: this.refusal },
    );
  }

  public reportModelUsage(report: ModelUsageReport): Promise<void> {
    this.reports.push(report);
    return Promise.resolve();
  }
}

describe("the spend limit of a Claude agent session", () => {
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

  test("one segment is one check and one report of all its model calls", async () => {
    const spendLimit = new RecordingSpendLimit();
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, { spendLimit });
    const sessionId = harness.newSessionId("spend-segment");
    await harness.sessions.startSession(sessionId, 'call lookup {"query":"x"}');
    await harness.waitForTurns(sessionId, 1);

    assert.equal(spendLimit.checks.length, 1);
    assert.equal(spendLimit.reports.length, 1);
    assert.equal(spendLimit.reports[0]?.usage.modelCalls, 2);
    assert.ok((spendLimit.reports[0]?.usage.inputTokens ?? 0) > 0);
  });

  test("a refusal before the next segment ends the turn and keeps the tool result", async () => {
    const spendLimit = new RecordingSpendLimit();
    harness = await ClaudeAgentTestHarness.start(engine, true, undefined, { spendLimit });
    const sessionId = harness.newSessionId("spend-refusal");
    await harness.sessions.startSession(sessionId, 'call publish {"message":"hello"}');
    const interrupt = await harness.waitForNewInterrupt(sessionId, "publish");
    spendLimit.refusal = "out of budget";

    assert.equal(await harness.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await harness.waitForTurns(sessionId, 1);

    assert.equal(snapshot.state?.lastTurnError, `${modelCallRefusedErrorPrefix}: out of budget`);
    assert.equal(harness.invocations.count("publish"), 1);
    assert.equal(spendLimit.reports.length, 1);
  });
});
