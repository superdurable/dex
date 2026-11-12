// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** How many keys the table keeps before it forgets finished ones early. */
export const requestOutcomeTableCapacity = 256;
/** How long a finished key is remembered, in milliseconds. */
export const requestOutcomeRetentionMs = 600_000;

/** The outcome of one local request: exactly one of `result` or `error`. */
export type LocalRequestOutcome =
  | { readonly result: Readonly<Record<string, unknown>> }
  | { readonly error: { readonly code: string; readonly message: string } };

/** One remembered key: the request it came with, and its outcome once it finished. */
export interface RequestOutcomeEntry {
  readonly method: string;
  /** The request's `params`, as canonical JSON, to compare a repeated key with. */
  readonly paramsText: string;
  /** Settles with the outcome. */
  readonly outcome: Promise<LocalRequestOutcome>;
  /** The outcome once it finished. */
  finished?: { readonly outcome: LocalRequestOutcome; readonly atMs: number };
}

/**
 * What the launcher side remembers of keyed requests, as terminal UI contract 1 bounds it: a running key is never
 * forgotten; a finished key is forgotten 10 minutes after it finished, or earlier, the one that finished first
 * first, while more than 256 keys are kept.
 */
export class RequestOutcomeTable {
  private readonly entries = new Map<string, RequestOutcomeEntry>();

  /**
   * Creates the table.
   * @param now - The clock, in milliseconds.
   */
  public constructor(private readonly now: () => number = Date.now) {}

  /**
   * Returns the entry of a key, or `undefined` when the table does not remember it.
   * @param key - The idempotency key.
   */
  public find(key: string): RequestOutcomeEntry | undefined {
    this.forgetExpired();
    return this.entries.get(key);
  }

  /**
   * Records a running request under a key the table does not hold, and its outcome once the operation settles.
   * @param key - The idempotency key.
   * @param entry - The request and its pending outcome.
   */
  public remember(key: string, entry: RequestOutcomeEntry): void {
    this.forgetExpired();
    this.entries.set(key, entry);
    this.forgetOverCapacity();
    void entry.outcome.then((outcome) => {
      if (this.entries.get(key) === entry) {
        entry.finished = { outcome, atMs: this.now() };
        this.forgetOverCapacity();
      }
    });
  }

  /**
   * Forgets a key, such as one whose Worker host stopped before the outcome was known.
   * @param key - The idempotency key.
   */
  public forget(key: string): void {
    this.entries.delete(key);
  }

  /**
   * Returns what `get_request_result` answers for a key.
   * @param key - The idempotency key.
   */
  public state(key: string): "done" | "running" | "unknown" {
    const entry = this.find(key);
    return entry === undefined ? "unknown" : entry.finished === undefined ? "running" : "done";
  }

  private forgetExpired(): void {
    const nowMs = this.now();
    for (const [key, entry] of this.entries) {
      if (entry.finished !== undefined && nowMs - entry.finished.atMs >= requestOutcomeRetentionMs) {
        this.entries.delete(key);
      }
    }
  }

  private forgetOverCapacity(): void {
    let excess = this.entries.size - requestOutcomeTableCapacity;
    if (excess <= 0) {
      return;
    }
    const finished = [...this.entries]
      .filter(([, entry]) => entry.finished !== undefined)
      .sort(([leftKey, left], [rightKey, right]) => {
        const byTime = (left.finished?.atMs ?? 0) - (right.finished?.atMs ?? 0);
        return byTime !== 0 ? byTime : leftKey < rightKey ? -1 : leftKey > rightKey ? 1 : 0;
      });
    for (const [key] of finished) {
      if (excess <= 0) {
        return;
      }
      this.entries.delete(key);
      excess -= 1;
    }
  }
}
