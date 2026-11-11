// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Transcripts that adapter 0.4.0 stored with last-prompt entries resume without them. The test drives the calls
// runModelStep makes to rebuild a segment's transcript; no CLI run can store a leaf pointer any more.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { AgentMessage } from "@superdurable/dex-agents";

import {
  claudeTranscriptItemKind,
  decodeRunState,
  encodeRunState,
  loadSessionTranscript,
  projectTurnEntries,
  type ClaudeTranscriptEntry,
} from "../../src/claude-transcript.js";
import { TurnSegmentSessionStore } from "../../src/turn-segment-session-store.js";

const sessionId = "6f0c5f8e-2f5d-4f4e-9b1e-2d6a0c1f9a11";

function entry(type: "user" | "assistant", uuid: string, parentUuid: string | null): ClaudeTranscriptEntry {
  const message =
    type === "user"
      ? { role: "user", content: `prompt ${uuid}` }
      : { id: `msg_${uuid}`, role: "assistant", content: [{ type: "text", text: `answer ${uuid}` }] };
  return { type, uuid, parentUuid, sessionId, message };
}

describe("transcripts stored with leaf pointers", () => {
  test("committed messages and run state resume without leaf pointers", async () => {
    const committedEntries = [
      entry("user", "u1", null),
      entry("assistant", "a1", "u1"),
      { type: "last-prompt", leafUuid: "u1", sessionId },
      entry("user", "u2", "a1"),
      entry("assistant", "a2", "u2"),
    ];
    const storedMessage: AgentMessage = {
      sequence: 1,
      turnId: "turn-1",
      role: "assistant",
      text: null,
      toolCallId: null,
      toolName: null,
      toolArguments: null,
      toolOutput: null,
      frameworkItem: JSON.stringify({ kind: claudeTranscriptItemKind, session_id: sessionId, entries: committedEntries }),
      createdAt: "2026-11-10T00:00:00Z",
    };
    const runState = encodeRunState(sessionId, [
      entry("user", "u3", "a2"),
      { type: "last-prompt", leafUuid: "a2", sessionId },
      entry("assistant", "a3", "u3"),
    ]);

    const committed = loadSessionTranscript([storedMessage]);
    const suspended = decodeRunState(runState);
    const store = new TurnSegmentSessionStore(sessionId, [...committed.entries, ...suspended.turnEntries]);
    const replayed = await store.load({ projectKey: "workspace", sessionId });

    assert.deepEqual(
      (replayed ?? []).map((replayedEntry) => replayedEntry["uuid"]),
      ["u1", "a1", "u2", "a2", "u3", "a3"],
    );
    // Entries of the suspended turn are stored again when it completes; the pointer must not return.
    const storedAgain = projectTurnEntries(sessionId, suspended.turnEntries, (name) => name).flatMap(
      (message) => (JSON.parse(message.frameworkItem) as { entries: ClaudeTranscriptEntry[] }).entries,
    );
    assert.ok(storedAgain.every((storedEntry) => storedEntry["type"] !== "last-prompt"));
  });
});
