// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { tmpdir } from "node:os";
import { afterEach, describe, test } from "node:test";

import { runShellCommand } from "../../src/local-tools/shell-command.js";

describe("the shell tool's process group", () => {
  const originalKill = process.kill.bind(process);
  let groupSignals: number[] = [];

  afterEach(() => {
    process.kill = originalKill;
  });

  function recordGroupSignals(): void {
    groupSignals = [];
    process.kill = ((pid: number, signal?: NodeJS.Signals | number) => {
      if (pid < 0) {
        groupSignals.push(pid);
      }
      return originalKill(pid, signal);
    }) as typeof process.kill;
  }

  test("a timeout ends a running command's whole group", async () => {
    recordGroupSignals();
    const result = await runShellCommand("sleep 5", tmpdir(), 1, new AbortController().signal);
    assert.match(result, /^killed: timed out after 1 seconds/);
    assert.equal(groupSignals.length, 1);
  });

  test("after the shell exited, its group is never signaled, even while a background child holds its output", async () => {
    recordGroupSignals();
    const startedAt = Date.now();
    const result = await runShellCommand("sleep 3 & echo started", tmpdir(), 1, new AbortController().signal);
    assert.match(result, /^exit code 0\n--- stdout ---\nstarted\n/);
    // The result comes about 1 s after the shell exited, not when the background child closes the pipes.
    assert.ok(Date.now() - startedAt < 2_500, `answered after ${Date.now() - startedAt} ms`);
    // The timeout would have fired by now had it not been cleared when the shell exited.
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    assert.deepEqual(groupSignals, []);
  });
});
