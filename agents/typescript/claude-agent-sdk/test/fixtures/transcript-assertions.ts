// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";

import {
  turnCancelledNoteHeader,
  turnFailureNoteHeader,
  turnSteeredNoteHeader,
  type AgentMessage,
  type AgentTurnOutcome,
  type FrameworkMessage,
} from "@superdurable/dex-agents";

import { claudeTranscriptItemKind } from "../../src/index.js";

/** Asserts that no stored message is a steered, cancelled, or failed turn's note. */
export function assertNoTurnNote(messages: readonly AgentMessage[]): void {
  for (const message of messages) {
    assert.notEqual(message.origin, "turn_steered_note");
    assert.notEqual(message.origin, "turn_cancelled_note");
    for (const header of [turnSteeredNoteHeader, turnCancelledNoteHeader, turnFailureNoteHeader]) {
      assert.equal(message.text?.startsWith(header) === true, false, `a note was stored: ${message.text ?? ""}`);
    }
  }
}

/** Returns the Claude transcript entries of every message, in order. */
export function storedTranscriptEntries(
  messages: readonly { readonly frameworkItem: string }[],
): Record<string, unknown>[] {
  return messages.flatMap((message) => {
    const item = JSON.parse(message.frameworkItem) as { readonly kind: string; readonly entries?: unknown[] };
    return item.kind === claudeTranscriptItemKind ? ((item.entries ?? []) as Record<string, unknown>[]) : [];
  });
}

/** Returns how many stored deferral attachments name the tool use. */
export function deferralsOf(messages: readonly AgentMessage[], toolUseId: string): number {
  return storedTranscriptEntries(messages).filter((entry) => {
    const attachment = entry["attachment"] as Record<string, unknown> | undefined;
    return attachment?.["type"] === "hook_deferred_tool" && attachment["toolUseID"] === toolUseId;
  }).length;
}

/** Returns how many stored tool results answer the tool use. */
export function resultsOf(messages: readonly AgentMessage[], toolUseId: string): number {
  return storedTranscriptEntries(messages)
    .flatMap((entry) => {
      const content = (entry["message"] as { readonly content?: unknown } | undefined)?.content;
      return Array.isArray(content) ? (content as Record<string, unknown>[]) : [];
    })
    .filter((block) => block["type"] === "tool_result" && block["tool_use_id"] === toolUseId).length;
}

/** Returns the first message a `RunModel` outcome stores. */
export function firstNewMessageOf(outcome: AgentTurnOutcome | undefined): FrameworkMessage | undefined {
  return outcome?.newMessages?.[0];
}

/** Asserts that every stored transcript entry with a `uuid` is stored once. */
export function assertUniqueTranscriptEntries(messages: readonly AgentMessage[]): void {
  const uuids = storedTranscriptEntries(messages)
    .map((entry) => entry["uuid"])
    .filter((uuid) => typeof uuid === "string");
  assert.ok(uuids.length > 0, "no transcript entry was stored");
  assert.equal(new Set(uuids).size, uuids.length, "a transcript entry was stored twice");
}
