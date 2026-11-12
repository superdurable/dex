// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import { runShellCommand } from "../../src/local-tools/shell-command.js";
import { readProcessIdentity } from "../../src/worker-host/process-identity.js";
import { RecordedToolProcessGroups } from "../../src/worker-host/tool-process-groups.js";
import { readWorkerHostRunRecord } from "../../src/worker-host/worker-host-run-record.js";

/** Returns whether a process exists. */
function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
  }
}

async function isGoneWithin(pid: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (isAlive(pid) && Date.now() < deadline) {
    await delay(20);
  }
  return !isAlive(pid);
}

/** The tool process group registry of a Worker host, with its run record in a temporary directory. */
function workerHostRegistry(): { registry: RecordedToolProcessGroups; recordPath: string } {
  const self = readProcessIdentity(process.pid);
  assert.ok(self !== undefined);
  const recordPath = join(mkdtempSync(join(tmpdir(), "dex-agents-shell-run-")), "scripted-1.json");
  return { registry: new RecordedToolProcessGroups(recordPath, self, self), recordPath };
}

for (const mode of ["plain chat", "Worker host"] as const) {
  describe(`the shell tool's process group (${mode})`, () => {
    const registered = () => (mode === "Worker host" ? workerHostRegistry() : undefined);

    test("a command that starts a background job with & answers at once, and the job ends with it", async () => {
      const host = registered();
      const startedAt = Date.now();
      const result = await runShellCommand("sleep 30 & echo $!", tmpdir(), 60, new AbortController().signal, host?.registry);
      assert.ok(Date.now() - startedAt < 3_000, `answered after ${Date.now() - startedAt} ms`);
      const match = /^exit code 0\n--- stdout ---\n(\d+)\n/.exec(result);
      assert.ok(match !== null, result);
      assert.ok(await isGoneWithin(Number(match[1]), 2_000), "the background sleep is gone");
      if (host !== undefined) {
        assert.deepEqual(readWorkerHostRunRecord(host.recordPath)?.tool_groups, []);
      }
    });

    test("a command that exits while a grandchild still holds its output answers at once, and the grandchild ends", async () => {
      const host = registered();
      const startedAt = Date.now();
      const result = await runShellCommand(
        "(sleep 30; echo late) & echo $!; echo started",
        tmpdir(),
        60,
        new AbortController().signal,
        host?.registry,
      );
      assert.ok(Date.now() - startedAt < 3_000, `answered after ${Date.now() - startedAt} ms`);
      const match = /^exit code 0\n--- stdout ---\n(\d+)\nstarted\n/.exec(result);
      assert.ok(match !== null, result);
      assert.doesNotMatch(result, /late/);
      assert.ok(await isGoneWithin(Number(match[1]), 2_000), "the subshell is gone");
    });

    test("a timeout kills the whole group, background jobs included", async () => {
      const host = registered();
      const result = await runShellCommand("sleep 30 & echo $!; sleep 30", tmpdir(), 1, new AbortController().signal, host?.registry);
      const match = /^killed: timed out after 1 seconds\n--- stdout ---\n(\d+)\n/.exec(result);
      assert.ok(match !== null, result);
      assert.ok(await isGoneWithin(Number(match[1]), 2_000), "the background sleep is gone");
    });

    test("the exit status and the signal that ended the command are reported", async () => {
      assert.match(await runShellCommand("exit 3", tmpdir(), 60, new AbortController().signal, registered()?.registry), /^exit code 3\n/);
      assert.match(
        await runShellCommand("kill -TERM $$", tmpdir(), 60, new AbortController().signal, registered()?.registry),
        /^ended by signal SIGTERM\n/,
      );
    });
  });
}
