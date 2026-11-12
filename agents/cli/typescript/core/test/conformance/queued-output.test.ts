// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { PassThrough, Writable } from "node:stream";
import { describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { queuedOutputWaitMs, waitForQueuedOutput } from "../../src/queued-output.js";

const unreadOutputCliPath = fileURLToPath(new URL("../fixtures/unread-output-cli.js", import.meta.url));

describe("waiting for queued output before exit", () => {
  test("the default bound is 2 seconds", () => {
    assert.equal(queuedOutputWaitMs, 2_000);
  });

  test("a stream with nothing queued does not wait", async () => {
    const started = Date.now();
    await waitForQueuedOutput([new PassThrough()], 10_000);
    assert.ok(Date.now() - started < 1_000);
  });

  test("a Writable that never calls back stops the wait at the bound", async () => {
    const stuck = new Writable({
      write: () => undefined,
    });
    stuck.write("queued");
    const started = Date.now();
    await waitForQueuedOutput([stuck], 200);
    const elapsedMs = Date.now() - started;
    assert.ok(elapsedMs >= 150 && elapsedMs < 5_000, `waited ${elapsedMs} ms`);
    assert.equal(stuck.listenerCount("close"), 0);
    assert.equal(stuck.listenerCount("finish"), 0);
  });

  test("a stream that fails ends the wait without an uncaught error", async () => {
    const failing = new Writable({
      write: (_chunk, _encoding, callback) => setTimeout(() => callback(new Error("write failed")), 20),
    });
    failing.write("queued");
    const started = Date.now();
    await waitForQueuedOutput([failing], 10_000);
    assert.ok(Date.now() - started < 5_000);
  });

  test("a process whose pipe is never read exits within the bound", async () => {
    const child = spawn(process.execPath, [unreadOutputCliPath, "500"], { stdio: ["ignore", "pipe", "pipe"] });
    const started = Date.now();
    try {
      const exitCode = await new Promise<number | null>((resolve) => child.once("exit", (code) => resolve(code)));
      const elapsedMs = Date.now() - started;
      assert.equal(exitCode, 0);
      assert.ok(elapsedMs < 10_000, `exited after ${elapsedMs} ms`);
    } finally {
      child.stdout.destroy();
      child.stderr.destroy();
    }
  });
});
