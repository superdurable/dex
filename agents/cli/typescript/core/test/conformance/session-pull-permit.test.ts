// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { setTimeout as delay } from "node:timers/promises";
import { describe, test } from "node:test";

import { SessionPullPermit } from "../../src/session-opener.js";
import { monotonicNowMs } from "../../src/session-terminal-lease.js";

/** Waits until `read` returns a value, at most `timeoutMs`. */
async function waitFor<T>(read: () => T | undefined, timeoutMs: number): Promise<T | undefined> {
  const deadline = monotonicNowMs() + timeoutMs;
  for (let value = read(); monotonicNowMs() < deadline; value = read()) {
    if (value !== undefined) {
      return value;
    }
    await delay(10);
  }
  return read();
}

describe("session pull permit", () => {
  test("a permit armed when pulling starts expires a fifth of the TTL before the TTL, and stops pulling first", async () => {
    const events: string[] = [];
    let expiredAt: number | undefined;
    const startedAt = monotonicNowMs();
    const permit = new SessionPullPermit(1_000, startedAt, () => events.push("stopped pulling"));
    permit.whenExpired(() => {
      events.push("told the chat");
      expiredAt = monotonicNowMs();
    });
    const firedAt = await waitFor(() => expiredAt, 2_000);
    assert.ok(firedAt !== undefined);
    assert.ok(firedAt - startedAt >= 790 && firedAt - startedAt < 1_000, `expired ${Math.round(firedAt - startedAt)} ms in`);
    assert.deepEqual(events, ["stopped pulling", "told the chat"]);
    assert.equal(permit.isExpired, true);
    assert.equal(permit.remainingMs(), 0);
  });

  test("only a confirmation extends the permit, to the TTL minus the margin after the confirmed request's send", async () => {
    let expiredAt: number | undefined;
    // Pulling started 700 ms ago, so 100 ms remain.
    const permit = new SessionPullPermit(1_000, monotonicNowMs() - 700, () => undefined);
    permit.whenExpired(() => {
      expiredAt = monotonicNowMs();
    });
    const sentAt = monotonicNowMs();
    assert.equal(permit.confirm(sentAt), true);
    // A confirmation of an older request never shortens the permit.
    assert.equal(permit.confirm(sentAt - 500), true);
    const firedAt = await waitFor(() => expiredAt, 2_000);
    assert.ok(firedAt !== undefined);
    assert.ok(firedAt - sentAt >= 790 && firedAt - sentAt < 1_000, `expired ${Math.round(firedAt - sentAt)} ms after the send`);
  });

  test("an answer that arrives after the permit expired grants nothing", async () => {
    let isStopped = false;
    const permit = new SessionPullPermit(1_000, monotonicNowMs() - 1_000, () => {
      isStopped = true;
    });
    await waitFor(() => (isStopped ? true : undefined), 1_000);
    assert.equal(permit.isExpired, true);
    assert.equal(permit.confirm(monotonicNowMs()), false);
    assert.equal(permit.isExpired, true);
    let isTold = false;
    permit.whenExpired(() => {
      isTold = true;
    });
    assert.equal(isTold, true, "a listener of an expired permit hears at once");
  });

  test("the permit runs on a monotonic clock, so a wall clock jump does not move it", async () => {
    const wallClock = Date.now;
    let expiredAt: number | undefined;
    const startedAt = monotonicNowMs();
    const permit = new SessionPullPermit(1_000, startedAt, () => undefined);
    permit.whenExpired(() => {
      expiredAt = monotonicNowMs();
    });
    try {
      Date.now = () => wallClock() - 3_600_000;
      assert.equal(permit.confirm(startedAt + 100), true);
      Date.now = () => wallClock() + 3_600_000;
      const firedAt = await waitFor(() => expiredAt, 2_000);
      assert.ok(firedAt !== undefined, "the permit expired despite the wall clock jumps");
      assert.ok(firedAt - startedAt >= 890 && firedAt - startedAt < 1_200, `expired ${Math.round(firedAt - startedAt)} ms in`);
    } finally {
      Date.now = wallClock;
      permit.cancel();
    }
  });
});
