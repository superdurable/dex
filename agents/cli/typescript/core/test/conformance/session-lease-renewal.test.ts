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

import type { SessionTerminalLeaseResult } from "@superdurable/dex-agents";

import { SessionLeaseRenewal, monotonicNowMs } from "../../src/session-terminal-lease.js";

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

describe("session lease renewal deadline", () => {
  test("a renewal deadline starts at the acquire's send, so a slow answer leaves less time", async () => {
    let unconfirmedAt: number | undefined;
    const renewal = new SessionLeaseRenewal(
      "holder",
      1,
      () => Promise.reject(new Error("unreachable")),
      () => undefined,
      () => {
        unconfirmedAt = monotonicNowMs();
      },
      100,
    );
    // The acquire was sent 700 ms ago: 100 ms remain before 80 percent of the TTL.
    const sentAt = monotonicNowMs() - 700;
    renewal.renew("session", sentAt);
    const firedAt = await waitFor(() => unconfirmedAt, 2_000);
    renewal.stop();
    assert.ok(firedAt !== undefined, "the deadline fired");
    assert.ok(firedAt - sentAt < 950, `fired ${Math.round(firedAt - sentAt)} ms after the send`);
  });

  test("the deadline uses a monotonic clock, so a wall clock jump during a renewal does not move it", async () => {
    const wallClock = Date.now;
    let offsetMs = 0;
    Date.now = () => wallClock() + offsetMs;
    let unconfirmedAt: number | undefined;
    let calls = 0;
    const renew = (): Promise<SessionTerminalLeaseResult> => {
      calls += 1;
      if (calls === 1) {
        // The wall clock jumps an hour back while the one successful renewal is in flight.
        offsetMs -= 3_600_000;
        return Promise.resolve({ isAcquired: true, lease: null });
      }
      return Promise.reject(new Error("Dex is unreachable"));
    };
    const renewal = new SessionLeaseRenewal("holder", 1, renew, () => undefined, () => {
      unconfirmedAt = monotonicNowMs();
    }, 100);
    const startedAt = monotonicNowMs();
    try {
      renewal.renew("session", startedAt);
      const firedAt = await waitFor(() => unconfirmedAt, 3_000);
      assert.ok(firedAt !== undefined, "the deadline fired despite the wall clock jump");
      // The first renewal is sent about 100 ms in, so its deadline is about 900 ms in.
      assert.ok(firedAt - startedAt < 1_500, `fired ${Math.round(firedAt - startedAt)} ms in`);
      assert.ok(firedAt - startedAt >= 800, `fired ${Math.round(firedAt - startedAt)} ms in, before the confirmed deadline`);
    } finally {
      renewal.stop();
      Date.now = wallClock;
    }
  });
});
