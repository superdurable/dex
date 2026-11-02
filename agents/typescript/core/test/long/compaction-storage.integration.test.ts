// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { test } from "node:test";

import { agentMessageCodec, contextSummaryCodec } from "../../src/agent-session-codecs.js";
import { runPaddedSession } from "../fixtures/compaction-test-session.js";

const longSessionTurns = 2_000;
const longSessionRunBudgetBytes = 16 * 1024 * 1024;

// Runs for minutes, so only `npm run test:long` and the nightly job run it.
test("a 2,000-turn session with compaction reports its storage against the run budget", async () => {
  // Calibrates the deployment guide: 100-character questions and 600-character answers.
  const session = await runPaddedSession(
    longSessionTurns,
    // The engine refuses a run budget below one maximum-sized value, so both shrink together.
    `values:\n  maxValueBytes: 1048576\nlimits:\n  maxFlowStateBytes: ${longSessionRunBudgetBytes}\n`,
    1_500_000,
  );

  // Every turn completed inside the 16 MiB run budget, so no Step crossed it.
  const state = session.snapshot.state;
  assert.equal(session.flowStatus, "running");
  assert.equal(state?.turnCount, longSessionTurns);
  assert.equal(state?.lastTurnError, null);
  assert.equal(session.messages.length, 2 * longSessionTurns);
  const summary = session.snapshot.contextSummary;
  assert.ok(summary !== null && summary !== undefined);
  assert.ok(summary.generation >= 100);
  const storedMessageBytes = session.messages.reduce(
    (total, message) => total + String(agentMessageCodec.encode(message).data).length,
    0,
  );
  console.log(
    "A7-5 storage:",
    JSON.stringify({
      turns: longSessionTurns,
      run_budget_bytes: longSessionRunBudgetBytes,
      stored_message_bytes: storedMessageBytes,
      stored_message_bytes_per_turn: Math.floor(storedMessageBytes / longSessionTurns),
      summary_generations: summary.generation,
      summary_bytes: String(contextSummaryCodec.encode(summary).data).length,
      model_calls: session.snapshot.usage.modelCalls,
      elapsed_seconds: Math.round(session.elapsedSeconds * 10) / 10,
    }),
  );
  assert.ok(storedMessageBytes < longSessionRunBudgetBytes / 2);
});
