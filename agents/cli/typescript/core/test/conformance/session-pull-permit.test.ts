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

import type { Worker } from "@superdurable/dex";

import { SessionPullPermit, SessionQueueWorker } from "../../src/session-opener.js";
import { monotonicNowMs } from "../../src/session-terminal-lease.js";

/** A session Worker holder whose Workers only record when they close. */
function recordingQueueWorker(events: string[]): SessionQueueWorker {
  return new SessionQueueWorker(
    (taskQueue, appWorkerId) =>
      ({
        close: () => {
          events.push(`closed ${appWorkerId} of ${taskQueue}`);
          return Promise.resolve();
        },
      }) as unknown as Worker,
    (incarnation) => `worker-${incarnation}`,
    () => Promise.resolve(),
  );
}

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

  test("a confirmation that arrives after the deadline, before the timer fires, expires the permit at once", () => {
    const events: string[] = [];
    const permit = new SessionPullPermit(1_000, monotonicNowMs() - 1_000, () => events.push("stopped pulling"));
    permit.whenExpired(() => events.push("told the chat"));
    // The timer cannot fire before this synchronous call returns.
    assert.equal(permit.confirm(monotonicNowMs()), false);
    assert.equal(permit.isExpired, true);
    assert.deepEqual(events, ["stopped pulling", "told the chat"]);
    assert.equal(permit.confirm(monotonicNowMs()), false);
  });

  test("a cancelled permit takes no confirmation, never arms again, and never stops pulling", async () => {
    const events: string[] = [];
    // 100 ms remain before the old deadline.
    const permit = new SessionPullPermit(1_000, monotonicNowMs() - 700, () => events.push("stopped pulling"));
    permit.whenExpired(() => events.push("told the chat"));
    permit.cancel();
    // A renewal in flight when the session was left answers now.
    assert.equal(permit.confirm(monotonicNowMs()), false);
    await delay(300);
    assert.deepEqual(events, []);
    assert.equal(permit.isExpired, false);
  });

  test("an older permit's expiry never stops a newer Worker of the same task queue", async () => {
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    // 100 ms remain; leaving stops its Worker without cancelling it, to isolate the Worker check.
    const olderPermit = new SessionPullPermit(1_000, monotonicNowMs() - 700, () =>
      sessionWorker.stopIfPullingUnder(olderPermit),
    );
    await sessionWorker.pull("s-queue", "session", olderPermit);
    sessionWorker.stop();
    const newerPermit = new SessionPullPermit(60_000, monotonicNowMs(), () =>
      sessionWorker.stopIfPullingUnder(newerPermit),
    );
    try {
      await sessionWorker.pull("s-queue", "session", newerPermit);
      await waitFor(() => (olderPermit.isExpired ? true : undefined), 1_000);
      assert.equal(olderPermit.isExpired, true);
      assert.equal(sessionWorker.taskQueue, "s-queue", "the newer Worker still pulls");
      assert.deepEqual(events, ["closed worker-1 of s-queue"]);
    } finally {
      newerPermit.cancel();
      await sessionWorker.close();
    }
  });

  test("a new permit gets a new Worker even for the same task queue", async () => {
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    const firstPermit = new SessionPullPermit(60_000, monotonicNowMs(), () => undefined);
    const secondPermit = new SessionPullPermit(60_000, monotonicNowMs(), () => undefined);
    try {
      await sessionWorker.pull("s-queue", "session", firstPermit);
      await sessionWorker.pull("s-queue", "session", firstPermit);
      assert.deepEqual(events, [], "the same permit keeps its Worker");
      await sessionWorker.pull("s-queue", "session", secondPermit);
      assert.deepEqual(events, ["closed worker-1 of s-queue"]);
      sessionWorker.stopIfPullingUnder(firstPermit);
      assert.equal(sessionWorker.taskQueue, "s-queue");
      sessionWorker.stopIfPullingUnder(secondPermit);
      assert.equal(sessionWorker.taskQueue, undefined);
      assert.deepEqual(events, ["closed worker-1 of s-queue", "closed worker-2 of s-queue"]);
    } finally {
      firstPermit.cancel();
      secondPermit.cancel();
      await sessionWorker.close();
    }
  });

  test("expiry closes the Worker, which takes no new task from then on, before it tells the listeners", async () => {
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    const permit = new SessionPullPermit(1_000, monotonicNowMs() - 700, () => sessionWorker.stopIfPullingUnder(permit));
    permit.whenExpired(() => events.push("told the chat"));
    await sessionWorker.pull("s-queue", "session", permit);
    await waitFor(() => (permit.isExpired ? true : undefined), 1_000);
    assert.deepEqual(events, ["closed worker-1 of s-queue", "told the chat"]);
    await sessionWorker.close();
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
