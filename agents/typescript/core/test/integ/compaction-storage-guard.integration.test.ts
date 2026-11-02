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

const guardSessionTurns = 300;
// Measured about 2,885 bytes per turn in both cores; the band catches a change in what a turn stores.
const guardMinStoredBytesPerTurn = 2_600;
const guardMaxStoredBytesPerTurn = 3_200;

// The TypeScript Worker's writes never crossed the run budget with dexcli 1.30.0, so this checks storage.
test("a 300-turn session with compaction stores about 2.9 KB of messages per turn", async () => {
  const session = await runPaddedSession(guardSessionTurns, undefined, 300_000);

  assert.equal(session.snapshot.state?.turnCount, guardSessionTurns);
  assert.equal(session.snapshot.state?.lastTurnError, null);
  const storedMessageBytes = session.messages.reduce(
    (total, message) => total + String(agentMessageCodec.encode(message).data).length,
    0,
  );
  const storedBytesPerTurn = storedMessageBytes / guardSessionTurns;
  const summary = session.snapshot.contextSummary;
  assert.ok(summary !== null && summary !== undefined);
  const summaryBytes = String(contextSummaryCodec.encode(summary).data).length;
  console.log(
    "A7-5 guard:",
    JSON.stringify({
      stored_bytes_per_turn: Math.round(storedBytesPerTurn),
      summary_generations: summary.generation,
      summary_bytes: summaryBytes,
      elapsed_seconds: Math.round(session.elapsedSeconds * 10) / 10,
    }),
  );
  assert.ok(
    storedBytesPerTurn >= guardMinStoredBytesPerTurn && storedBytesPerTurn <= guardMaxStoredBytesPerTurn,
    `${Math.round(storedBytesPerTurn)} stored bytes per turn`,
  );
  // Summaries stay about 1,000 tokens however long the session runs, so superseded ones stay bounded.
  assert.ok(summary.generation >= 10);
  assert.ok(summaryBytes < 6_000);
});
