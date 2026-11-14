// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import type { Worker } from "@superdurable/dex";

import {
  monotonicPermitClock,
  SessionPullPermit,
  SessionQueueWorker,
  type SessionPullPermitClock,
} from "../../src/session-opener.js";

/** A clock the test moves by hand; the timers that come due fire in order as it moves. */
class ManualPermitClock implements SessionPullPermitClock {
  private readonly timers: { dueMs: number; callback: () => void; isCancelled: boolean }[] = [];

  public constructor(private currentMs = 0) {}

  public nowMs(): number {
    return this.currentMs;
  }

  public callAfter(delayMs: number, callback: () => void): () => void {
    const timer = { dueMs: this.currentMs + delayMs, callback, isCancelled: false };
    this.timers.push(timer);
    return () => {
      timer.isCancelled = true;
    };
  }

  /** Moves the clock to `targetMs`, firing each timer due by then at its own time. */
  public advanceTo(targetMs: number): void {
    for (;;) {
      const due = this.timers
        .filter((timer) => !timer.isCancelled && timer.dueMs <= targetMs)
        .sort((first, second) => first.dueMs - second.dueMs)[0];
      if (due === undefined) {
        break;
      }
      due.isCancelled = true;
      this.currentMs = Math.max(this.currentMs, due.dueMs);
      due.callback();
    }
    this.currentMs = targetMs;
  }
}

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

describe("session pull permit", () => {
  test("a permit armed when pulling starts expires a fifth of the TTL before the TTL, and stops pulling first", () => {
    const clock = new ManualPermitClock();
    const events: string[] = [];
    const permit = new SessionPullPermit(1_000, clock.nowMs(), () => events.push("stopped pulling"), clock);
    permit.whenExpired(() => events.push(`told the chat at ${clock.nowMs()}`));
    clock.advanceTo(799);
    assert.equal(permit.isExpired, false);
    assert.equal(permit.remainingMs(), 1);
    clock.advanceTo(800);
    assert.deepEqual(events, ["stopped pulling", "told the chat at 800"]);
    assert.equal(permit.isExpired, true);
    assert.equal(permit.remainingMs(), 0);
  });

  test("only a confirmation extends the permit, to the TTL minus the margin after the confirmed request's send", () => {
    // Pulling started 700 ms ago, so 100 ms remain.
    const clock = new ManualPermitClock(700);
    const events: string[] = [];
    const permit = new SessionPullPermit(1_000, 0, () => undefined, clock);
    permit.whenExpired(() => events.push(`expired at ${clock.nowMs()}`));
    assert.equal(permit.confirm(700), true);
    // A confirmation of an older request never shortens the permit.
    assert.equal(permit.confirm(200), true);
    clock.advanceTo(1_499);
    assert.deepEqual(events, []);
    clock.advanceTo(1_500);
    assert.deepEqual(events, ["expired at 1500"]);
  });

  test("an answer that arrives after the permit expired grants nothing", () => {
    const clock = new ManualPermitClock();
    const permit = new SessionPullPermit(1_000, 0, () => undefined, clock);
    clock.advanceTo(800);
    assert.equal(permit.isExpired, true);
    assert.equal(permit.confirm(800), false);
    assert.equal(permit.isExpired, true);
    let isTold = false;
    permit.whenExpired(() => {
      isTold = true;
    });
    assert.equal(isTold, true, "a listener of an expired permit hears at once");
  });

  test("a confirmation that arrives after the deadline, before the timer fires, expires the permit at once", () => {
    // The deadline passed 200 ms ago; the timer that is due now has not fired.
    const clock = new ManualPermitClock(1_000);
    const events: string[] = [];
    const permit = new SessionPullPermit(1_000, 0, () => events.push("stopped pulling"), clock);
    permit.whenExpired(() => events.push("told the chat"));
    assert.equal(permit.confirm(1_000), false);
    assert.equal(permit.isExpired, true);
    assert.deepEqual(events, ["stopped pulling", "told the chat"]);
    assert.equal(permit.confirm(1_000), false);
  });

  test("a cancelled permit takes no confirmation, never arms again, and never stops pulling", () => {
    const clock = new ManualPermitClock(700);
    const events: string[] = [];
    const permit = new SessionPullPermit(1_000, 0, () => events.push("stopped pulling"), clock);
    permit.whenExpired(() => events.push("told the chat"));
    permit.cancel();
    // A renewal in flight when the session was left answers now.
    assert.equal(permit.confirm(700), false);
    clock.advanceTo(10_000);
    assert.deepEqual(events, []);
    assert.equal(permit.isExpired, false);
  });

  test("an older permit's expiry never stops a newer Worker of the same task queue", async () => {
    const clock = new ManualPermitClock(700);
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    // 100 ms remain; leaving stops its Worker without cancelling it, to isolate the Worker check.
    const olderPermit = new SessionPullPermit(1_000, 0, () => sessionWorker.stopIfPullingUnder(olderPermit), clock);
    await sessionWorker.pull("s-queue", "session", olderPermit);
    sessionWorker.stop();
    const newerPermit = new SessionPullPermit(60_000, 700, () => sessionWorker.stopIfPullingUnder(newerPermit), clock);
    await sessionWorker.pull("s-queue", "session", newerPermit);
    clock.advanceTo(800);
    assert.equal(olderPermit.isExpired, true);
    assert.equal(sessionWorker.taskQueue, "s-queue", "the newer Worker still pulls");
    assert.deepEqual(events, ["closed worker-1 of s-queue"]);
    newerPermit.cancel();
    await sessionWorker.close();
  });

  test("a new permit gets a new Worker even for the same task queue", async () => {
    const clock = new ManualPermitClock();
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    const firstPermit = new SessionPullPermit(60_000, 0, () => undefined, clock);
    const secondPermit = new SessionPullPermit(60_000, 0, () => undefined, clock);
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
    await sessionWorker.close();
  });

  test("expiry closes the Worker, which takes no new task from then on, before it tells the listeners", async () => {
    const clock = new ManualPermitClock(700);
    const events: string[] = [];
    const sessionWorker = recordingQueueWorker(events);
    const permit = new SessionPullPermit(1_000, 0, () => sessionWorker.stopIfPullingUnder(permit), clock);
    permit.whenExpired(() => events.push("told the chat"));
    await sessionWorker.pull("s-queue", "session", permit);
    clock.advanceTo(800);
    assert.deepEqual(events, ["closed worker-1 of s-queue", "told the chat"]);
    await sessionWorker.close();
  });

  test("the permit's default clock is monotonic, so a wall clock jump does not move it", () => {
    const wallClock = Date.now;
    const before = monotonicPermitClock.nowMs();
    try {
      Date.now = () => wallClock() + 3_600_000;
      const afterForwardJump = monotonicPermitClock.nowMs();
      Date.now = () => wallClock() - 3_600_000;
      const afterBackwardJump = monotonicPermitClock.nowMs();
      assert.ok(afterForwardJump - before < 60_000, "a forward jump does not move it");
      assert.ok(afterBackwardJump >= afterForwardJump, "a backward jump does not move it back");
    } finally {
      Date.now = wallClock;
    }
  });
});
