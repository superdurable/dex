// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { test } from "node:test";

import { defaultMessageChunkSize } from "../../src/agent-session-flow.js";
import { compactionRetainedMessageLimitVariable } from "../fixtures/compaction-test-agent.js";
import {
  guardMaxUnsummarizedMessages,
  runPaddedSession,
  storedBytesPerMessage,
} from "../fixtures/compaction-test-session.js";

const guardSessionMilestones = [200, 400, 600, 800];
const guardSessionServerConfig = "values:\n  maxValueBytes: 1048576\n";
const guardRetainedMessageLimit = 100;
// Measured about 2,885 bytes per turn in both cores; the band catches a change in what a turn stores.
const guardMinStoredBytesPerTurn = 2_600;
const guardMaxStoredBytesPerTurn = 3_200;

// The index, state, usage, padded summary, and other small Attributes the session holds besides its messages.
const liveStateOverheadBytes = 16_384;

test("retention keeps the stored messages of a long session bounded", async () => {
  // Guards the deployment guide's bound: with retention, stored messages stop growing.
  const session = await runPaddedSession(guardSessionMilestones, guardSessionServerConfig, 300_000, {
    [compactionRetainedMessageLimitVariable]: String(guardRetainedMessageLimit),
  });

  const state = session.snapshot.state;
  const index = session.messageIndex;
  const summary = session.snapshot.contextSummary;
  const bytesPerMessage = storedBytesPerMessage(session.messages);
  console.log(
    "A7-5 guard:",
    JSON.stringify({
      completed_turns: state?.turnCount,
      stored_message_bytes_by_turn: Object.fromEntries(
        session.measurements.map((measurement) => [measurement.turns, measurement.storedBytes]),
      ),
      live_state_bytes_by_turn: Object.fromEntries(
        session.measurements.map((measurement) => [measurement.turns, measurement.liveStateBytes]),
      ),
      stored_bytes_per_turn: Math.round(2 * bytesPerMessage),
      kept_messages: session.messages.length,
      first_kept_sequence: index?.firstSequence ?? null,
      elapsed_seconds: Math.round(session.elapsedSeconds * 10) / 10,
    }),
  );
  const lastMilestone = guardSessionMilestones.at(-1) ?? 0;
  assert.equal(session.flowStatus, "running");
  assert.equal(state?.turnCount, lastMilestone);
  assert.equal(state?.lastTurnError, null);
  assert.ok(
    2 * bytesPerMessage >= guardMinStoredBytesPerTurn && 2 * bytesPerMessage <= guardMaxStoredBytesPerTurn,
    `${Math.round(2 * bytesPerMessage)} stored bytes per turn`,
  );
  assert.ok(index !== undefined && summary !== null && summary !== undefined);
  assert.ok(index.firstSequence - 1 <= summary.lastSequence);
  assert.deepEqual(
    session.messages.map((message) => message.sequence),
    Array.from({ length: 2 * lastMilestone + 1 - index.firstSequence }, (_unused, offset) => index.firstSequence + offset),
  );
  // The kept messages are the limit, a partly kept chunk, and those after the summary.
  const bound = (guardRetainedMessageLimit + defaultMessageChunkSize + guardMaxUnsummarizedMessages) * bytesPerMessage;
  const stored = session.measurements.map((measurement) => measurement.storedBytes);
  assert.equal(stored.length, guardSessionMilestones.length);
  assert.ok(stored.every((measured) => measured <= bound), `${JSON.stringify(stored)} over ${bound}`);
  // Four times the turns store no more than the first milestone plus one chunk's worth.
  assert.ok((stored.at(-1) ?? 0) <= (stored[0] ?? 0) + defaultMessageChunkSize * bytesPerMessage);
  // The whole live Flow state is the messages plus a few small Attributes, and it stays flat too.
  const live = session.measurements.map((measurement) => measurement.liveStateBytes);
  assert.ok(
    session.measurements.every((measurement) => measurement.liveStateBytes - measurement.storedBytes <= liveStateOverheadBytes),
    JSON.stringify(live),
  );
  assert.ok((live.at(-1) ?? 0) <= (live[0] ?? 0) + defaultMessageChunkSize * bytesPerMessage + liveStateOverheadBytes);
});
