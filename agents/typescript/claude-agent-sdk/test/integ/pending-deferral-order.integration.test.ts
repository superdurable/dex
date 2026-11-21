// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The transcript a resumed segment's CLI starts from, when a refused sibling follows the deferral (#2111).

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { withPendingDeferralLast, type ClaudeTranscriptEntry } from "../../src/claude-transcript.js";

const replyMessageId = "msg_reply";

function prompt(uuid: string): ClaudeTranscriptEntry {
  return { type: "user", uuid, parentUuid: null, message: { role: "user", content: "refund order A-1001" } };
}

function toolUse(uuid: string, parentUuid: string, toolUseId: string, messageId = replyMessageId): ClaudeTranscriptEntry {
  return {
    type: "assistant",
    uuid,
    parentUuid,
    message: { id: messageId, role: "assistant", content: [{ type: "tool_use", id: toolUseId, name: "mcp__tools__w", input: {} }] },
  };
}

function deferral(uuid: string, parentUuid: string, toolUseId: string): ClaudeTranscriptEntry {
  return { type: "attachment", uuid, parentUuid, attachment: { type: "hook_deferred_tool", toolUseID: toolUseId } };
}

function toolResult(uuid: string, parentUuid: string, toolUseId: string, sourceUuid: string): ClaudeTranscriptEntry {
  return {
    type: "user",
    uuid,
    parentUuid,
    sourceToolAssistantUUID: sourceUuid,
    message: { role: "user", content: [{ type: "tool_result", tool_use_id: toolUseId, content: "refused", is_error: true }] },
  };
}

function chainOf(entries: readonly ClaudeTranscriptEntry[]): [unknown, unknown][] {
  return entries.map((entry) => [entry["uuid"], entry["parentUuid"]]);
}

describe("the pending deferral of a reply with a refused sibling", () => {
  test("moves to the end, and the sibling continues from the deferred tool use", () => {
    // The shape a live model's stream leaves: the deferral is written before the second tool use arrives.
    const entries = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      deferral("d1", "a1", "toolu_refund"),
      toolUse("a2", "d1", "toolu_email"),
      toolResult("r2", "a2", "toolu_email", "a2"),
      { type: "cost-state", sessionId: "s" },
    ];
    const resumed = withPendingDeferralLast(entries);
    assert.deepEqual(chainOf(resumed), [
      ["p", null],
      ["a1", "p"],
      ["a2", "a1"],
      ["r2", "a2"],
      [undefined, undefined],
      ["d1", "r2"],
    ]);
    // The stored entries never change.
    assert.equal(entries[3]?.["parentUuid"], "d1");
  });

  test("keeps a transcript whose deferral is already last, answered, or followed by another reply", () => {
    const deferralLast = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      toolUse("a2", "a1", "toolu_email"),
      toolResult("r2", "a2", "toolu_email", "a2"),
      deferral("d1", "r2", "toolu_refund"),
    ];
    assert.deepEqual(withPendingDeferralLast(deferralLast), deferralLast);

    const onlyAttachmentsAfter = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      deferral("d1", "a1", "toolu_refund"),
      { type: "attachment", uuid: "s1", parentUuid: "d1", attachment: { type: "prompt_snapshot" } },
    ];
    assert.deepEqual(withPendingDeferralLast(onlyAttachmentsAfter), onlyAttachmentsAfter);

    const answered = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      deferral("d1", "a1", "toolu_refund"),
      toolUse("a2", "d1", "toolu_email"),
      toolResult("r2", "a2", "toolu_email", "a2"),
      toolResult("r1", "a1", "toolu_refund", "a1"),
    ];
    assert.deepEqual(withPendingDeferralLast(answered), answered);

    const laterReply = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      deferral("d1", "a1", "toolu_refund"),
      toolUse("a2", "d1", "toolu_email", "msg_other"),
    ];
    assert.deepEqual(withPendingDeferralLast(laterReply), laterReply);

    const metaAfter = [
      prompt("p"),
      toolUse("a1", "p", "toolu_refund"),
      deferral("d1", "a1", "toolu_refund"),
      toolUse("a2", "d1", "toolu_email"),
      { type: "user", uuid: "m1", parentUuid: "a2", isMeta: true, message: { role: "user", content: "note" } },
    ];
    assert.deepEqual(withPendingDeferralLast(metaAfter), metaAfter);
  });
});
