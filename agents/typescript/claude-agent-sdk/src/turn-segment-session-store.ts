// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { SessionKey, SessionStore, SessionStoreEntry } from "@anthropic-ai/claude-agent-sdk";

import type { ClaudeTranscriptEntry } from "./claude-transcript.js";

interface SegmentTranscript {
  readonly sessionId: string;
  readonly entries: readonly ClaudeTranscriptEntry[];
}

// load serves committed Dex state for resume; append collects the entries this segment commits.
export class TurnSegmentSessionStore implements SessionStore {
  private readonly committedEntries: readonly ClaudeTranscriptEntry[];
  private readonly knownEntryUuids = new Set<string>();
  private readonly appendedEntriesBySession = new Map<string, ClaudeTranscriptEntry[]>();

  public constructor(
    private readonly resumedSessionId: string | null,
    committedEntries: readonly ClaudeTranscriptEntry[],
  ) {
    this.committedEntries = committedEntries;
    for (const entry of committedEntries) {
      if (typeof entry["uuid"] === "string") {
        this.knownEntryUuids.add(entry["uuid"]);
      }
    }
  }

  public async append(key: SessionKey, entries: SessionStoreEntry[]): Promise<void> {
    // Subagent transcripts are not stored; resume needs only the main transcript.
    if (key.subpath !== undefined && key.subpath !== "") {
      return;
    }
    let sessionEntries = this.appendedEntriesBySession.get(key.sessionId);
    if (sessionEntries === undefined) {
      sessionEntries = [];
      this.appendedEntriesBySession.set(key.sessionId, sessionEntries);
    }
    for (const entry of entries) {
      // Retried mirror batches may overlap; the SDK asks stores to deduplicate by uuid.
      if (typeof entry.uuid === "string") {
        if (this.knownEntryUuids.has(entry.uuid)) {
          continue;
        }
        this.knownEntryUuids.add(entry.uuid);
      }
      sessionEntries.push({ ...entry });
    }
  }

  public async load(key: SessionKey): Promise<SessionStoreEntry[] | null> {
    if ((key.subpath !== undefined && key.subpath !== "") || key.sessionId !== this.resumedSessionId) {
      return null;
    }
    if (this.committedEntries.length === 0) {
      return null;
    }
    return this.committedEntries.map((entry) => ({ ...entry }) as SessionStoreEntry);
  }

  // Fails when the CLI mirrored nothing, several sessions, or a session it did not resume.
  public takeSegmentTranscript(): SegmentTranscript {
    if (this.appendedEntriesBySession.size === 0) {
      throw new Error(
        "the Claude Code CLI mirrored no transcript entries; the adapter needs session mirroring " +
          "(--session-mirror) to store the conversation",
      );
    }
    if (this.appendedEntriesBySession.size > 1) {
      const sessionIds = [...this.appendedEntriesBySession.keys()].sort().join(", ");
      throw new Error(`the Claude Code CLI wrote more than one session: ${sessionIds}`);
    }
    const [[sessionId, entries]] = [...this.appendedEntriesBySession.entries()] as [
      [string, ClaudeTranscriptEntry[]],
    ];
    if (this.resumedSessionId !== null && sessionId !== this.resumedSessionId) {
      throw new Error(
        `the Claude Code CLI resumed session ${this.resumedSessionId} but wrote session ${sessionId}`,
      );
    }
    return { sessionId, entries };
  }
}
