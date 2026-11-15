// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import type { AgentImageReference } from "@superdurable/dex-agents";

import {
  buildResumeTranscript,
  buildUnfinishedToolResultEntries,
  type ClaudeTranscriptEntry,
} from "../../src/claude-transcript.js";

// The Python adapter's tests read the same file and must build the same bytes.
const parityFixturePath = fileURLToPath(
  new URL("../../../../../conformance/claude_transcript_parity.json", import.meta.url),
);
const isUpdatingFixture = process.env["UPDATE_CLAUDE_TRANSCRIPT_PARITY"] === "1";

interface FixtureImage {
  readonly uri: string;
  readonly mime_type: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

interface UnfinishedCase {
  readonly name: string;
  readonly session_id: string;
  readonly turn_entries: ClaudeTranscriptEntry[];
  readonly results: Record<string, { readonly text: string; readonly is_error: boolean; readonly images?: FixtureImage[] }>;
  expected_entries?: string;
}

interface ResumeCase {
  readonly name: string;
  readonly session_id: string | null;
  readonly entries: ClaudeTranscriptEntry[];
  readonly ended_turn_entries: ClaudeTranscriptEntry[];
  readonly answer_text: string;
  expected_entries?: string;
  expected_new_answer_entries?: string;
}

interface ParityFixture {
  readonly unfinished: UnfinishedCase[];
  readonly resume: ResumeCase[];
}

describe("the Claude transcript entries both adapters build", () => {
  const fixture = JSON.parse(readFileSync(parityFixturePath, "utf8")) as ParityFixture;

  test("unfinished-turn results match the shared fixture", () => {
    for (const testCase of fixture.unfinished) {
      const entries = JSON.stringify(
        buildUnfinishedToolResultEntries(testCase.session_id, testCase.turn_entries, (toolUse) => {
          const result = testCase.results[toolUse.toolUseId];
          assert.ok(result !== undefined, `${testCase.name}: no result for ${toolUse.toolUseId}`);
          return { text: result.text, isError: result.is_error, images: (result.images ?? []).map(toImageReference) };
        }),
      );
      if (isUpdatingFixture) {
        testCase.expected_entries = entries;
      } else {
        assert.equal(entries, testCase.expected_entries, testCase.name);
      }
    }
  });

  test("resume transcripts and ended-turn answers match the shared fixture", () => {
    for (const testCase of fixture.resume) {
      const entriesBefore = JSON.stringify(testCase.entries);
      const transcript = buildResumeTranscript(
        testCase.session_id,
        testCase.entries,
        testCase.ended_turn_entries,
        testCase.answer_text,
      );
      assert.equal(JSON.stringify(testCase.entries), entriesBefore, `${testCase.name}: stored entries changed`);
      if (isUpdatingFixture) {
        testCase.expected_entries = JSON.stringify(transcript.entries);
        testCase.expected_new_answer_entries = JSON.stringify(transcript.newAnswerEntries);
      } else {
        assert.equal(JSON.stringify(transcript.entries), testCase.expected_entries, testCase.name);
        assert.equal(
          JSON.stringify(transcript.newAnswerEntries),
          testCase.expected_new_answer_entries,
          testCase.name,
        );
      }
    }
    if (isUpdatingFixture) {
      writeFileSync(parityFixturePath, `${JSON.stringify(fixture, null, 2)}\n`);
    }
  });
});

function toImageReference(image: FixtureImage): AgentImageReference {
  return { uri: image.uri, mimeType: image.mime_type, sha256: image.sha256, sizeBytes: image.size_bytes, type: "image" };
}
