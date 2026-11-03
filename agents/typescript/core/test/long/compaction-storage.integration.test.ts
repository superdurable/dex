// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { test } from "node:test";

import { contextSummaryCodec } from "../../src/agent-session-codecs.js";
import { defaultMessageChunkSize, defaultRetainedMessageLimit } from "../../src/agent-session-flow.js";
import {
  guardMaxUnsummarizedMessages,
  runPaddedSession,
  storedBytesPerMessage,
} from "../fixtures/compaction-test-session.js";

const longSessionMilestones = [500, 1_000, 1_500, 2_000];
const longSessionRunBudgetBytes = 16 * 1024 * 1024;

// Runs for minutes, so only `npm run test:long` and the nightly job run it.
// The index, state, usage, padded summary, and other small Attributes the session holds besides its messages.
const liveStateOverheadBytes = 16_384;

test("a 2,000-turn session with compaction keeps its storage bounded", async () => {
  // Calibrates the deployment guide: 100-character questions and 600-character answers, default options.
  const session = await runPaddedSession(
    longSessionMilestones,
    // The engine refuses a run budget below one maximum-sized value, so both shrink together.
    `values:\n  maxValueBytes: 1048576\nlimits:\n  maxFlowStateBytes: ${longSessionRunBudgetBytes}\n`,
    1_500_000,
  );

  // Every turn completed inside the 16 MiB run budget, so no Step crossed it.
  const state = session.snapshot.state;
  const index = session.messageIndex;
  const summary = session.snapshot.contextSummary;
  assert.equal(session.flowStatus, "running");
  assert.equal(state?.turnCount, longSessionMilestones.at(-1));
  assert.equal(state?.lastTurnError, null);
  assert.ok(index !== undefined && summary !== null && summary !== undefined);
  assert.ok(summary.generation >= 100);
  const bytesPerMessage = storedBytesPerMessage(session.messages);
  console.log(
    "A7-5 storage:",
    JSON.stringify({
      turns: longSessionMilestones.at(-1),
      run_budget_bytes: longSessionRunBudgetBytes,
      stored_message_bytes_by_turn: Object.fromEntries(
        session.measurements.map((measurement) => [measurement.turns, measurement.storedBytes]),
      ),
      live_state_bytes_by_turn: Object.fromEntries(
        session.measurements.map((measurement) => [measurement.turns, measurement.liveStateBytes]),
      ),
      stored_bytes_per_turn: Math.round(2 * bytesPerMessage),
      kept_messages: session.messages.length,
      first_kept_sequence: index.firstSequence,
      summary_generations: summary.generation,
      summary_bytes: Buffer.byteLength(String(contextSummaryCodec.encode(summary).data), "utf8"),
      model_calls: session.snapshot.usage.modelCalls,
      elapsed_seconds: Math.round(session.elapsedSeconds * 10) / 10,
    }),
  );
  // The default limit keeps 1,000 messages, plus a partly kept chunk and the unsummarized ones.
  const bound = (defaultRetainedMessageLimit + defaultMessageChunkSize + guardMaxUnsummarizedMessages) * bytesPerMessage;
  const stored = session.measurements.map((measurement) => measurement.storedBytes);
  assert.equal(stored.length, longSessionMilestones.length);
  assert.ok(stored.every((measured) => measured <= bound), `${JSON.stringify(stored)} over ${bound}`);
  assert.ok((stored.at(-1) ?? 0) <= (stored[1] ?? 0) + defaultMessageChunkSize * bytesPerMessage);
  // The whole live Flow state is the messages plus a few small Attributes, and it stays flat too.
  const live = session.measurements.map((measurement) => measurement.liveStateBytes);
  assert.ok(
    session.measurements.every((measurement) => measurement.liveStateBytes - measurement.storedBytes <= liveStateOverheadBytes),
    JSON.stringify(live),
  );
  assert.ok((live.at(-1) ?? 0) <= (live[1] ?? 0) + defaultMessageChunkSize * bytesPerMessage + liveStateOverheadBytes);
});
