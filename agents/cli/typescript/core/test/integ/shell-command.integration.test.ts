// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import type { ChildProcess } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";

import { runShellCommand, runWindowsShellCommand } from "../../src/local-tools/shell-command.js";
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

    test("a process left in the group that closed its output and ignores SIGTERM gets the full grace before SIGKILL", async () => {
      const originalKill = process.kill.bind(process);
      const groupSignals: { readonly signal: string; readonly atMs: number }[] = [];
      process.kill = ((pid: number, name?: NodeJS.Signals | number) => {
        if (pid < 0) {
          groupSignals.push({ signal: String(name), atMs: Date.now() });
        }
        return originalKill(pid, name);
      }) as typeof process.kill;
      try {
        const command =
          "perl -e '$SIG{TERM} = \"IGNORE\"; close STDOUT; close STDERR; sleep 30' & pid=$!; sleep 0.3; echo $pid";
        const result = await runShellCommand(command, tmpdir(), 60, new AbortController().signal, registered()?.registry);
        const pid = Number(/^exit code 0\n--- stdout ---\n(\d+)\n/.exec(result)?.[1] ?? 0);
        assert.ok(pid > 0, result);
        assert.ok(await isGoneWithin(pid, 2_000), "SIGKILL ended the process that ignored SIGTERM");
      } finally {
        process.kill = originalKill;
      }
      const term = groupSignals.find((entry) => entry.signal === "SIGTERM");
      const kill = groupSignals.find((entry) => entry.signal === "SIGKILL");
      assert.ok(term !== undefined && kill !== undefined, JSON.stringify(groupSignals));
      assert.ok(kill.atMs - term.atMs >= 450, `SIGKILL came ${kill.atMs - term.atMs} ms after SIGTERM`);
    });

    test("a command that leaves nothing behind answers without waiting for the grace", async () => {
      const startedAt = Date.now();
      assert.equal(
        await runShellCommand("echo hi", tmpdir(), 60, new AbortController().signal, registered()?.registry),
        "exit code 0\n--- stdout ---\nhi\n\n--- stderr ---\n",
      );
      assert.ok(Date.now() - startedAt < 400, `answered after ${Date.now() - startedAt} ms`);
    });

    test("a timeout kills the whole group, background jobs included", async () => {
      const host = registered();
      const result = await runShellCommand("sleep 30 & echo $!; sleep 30", tmpdir(), 1, new AbortController().signal, host?.registry);
      const match = /^killed: timed out after 1 seconds\n--- stdout ---\n(\d+)\n/.exec(result);
      assert.ok(match !== null, result);
      assert.ok(await isGoneWithin(Number(match[1]), 2_000), "the background sleep is gone");
    });

    test("a process that left the group with setsid and holds the output does not hold up the call", async () => {
      // perl calls setsid itself, so the process leaves the command's group but keeps its stdout and stderr.
      const detached = "perl -MPOSIX -e 'POSIX::setsid(); sleep 30' & pid=$!; sleep 0.5; echo $pid";
      for (const [command, timeoutSeconds, status] of [
        [detached, 60, /^exit code 0\n/],
        [`${detached}; sleep 30`, 1, /^killed: timed out after 1 seconds\n/],
      ] as const) {
        const startedAt = Date.now();
        const result = await runShellCommand(command, tmpdir(), timeoutSeconds, new AbortController().signal, registered()?.registry);
        const pid = Number(/--- stdout ---\n(\d+)\n/.exec(result)?.[1] ?? 0);
        try {
          assert.match(result, status);
          // The group ends, then the call stops waiting for the output 1 s later.
          assert.ok(Date.now() - startedAt < timeoutSeconds * 1000 + 3_000, `answered after ${Date.now() - startedAt} ms`);
          assert.ok(pid > 0 && isAlive(pid), "the tool leaves a process outside its group alone");
        } finally {
          if (pid > 0 && isAlive(pid)) {
            process.kill(pid, "SIGKILL");
          }
        }
      }
    });

    test("a command that a signal ended reports only its own output, not the shell's notice about the signal", async () => {
      assert.equal(
        await runShellCommand("kill -TERM $$", tmpdir(), 60, new AbortController().signal, registered()?.registry),
        "exit code 143\n--- stdout ---\n\n--- stderr ---\n",
      );
      assert.equal(
        await runShellCommand("echo out; echo err >&2; kill -KILL $$", tmpdir(), 60, new AbortController().signal, registered()?.registry),
        "exit code 137\n--- stdout ---\nout\n\n--- stderr ---\nerr\n",
      );
    });

    test("a working directory that does not exist reports the system's text", async () => {
      await assert.rejects(
        runShellCommand("true", join(tmpdir(), "dex-agents-no-such-directory"), 60, new AbortController().signal, registered()?.registry),
        { message: "the shell could not start: No such file or directory" },
      );
    });

    test("the exit status of the command is reported", async () => {
      assert.match(await runShellCommand("exit 3", tmpdir(), 60, new AbortController().signal, registered()?.registry), /^exit code 3\n/);
      // The shell reports a command that SIGTERM ended as 143, the same as an explicit exit 143.
      assert.match(
        await runShellCommand("kill -TERM $$", tmpdir(), 60, new AbortController().signal, registered()?.registry),
        /^exit code 143\n/,
      );
      assert.match(await runShellCommand("exit 143", tmpdir(), 60, new AbortController().signal, registered()?.registry), /^exit code 143\n/);
      assert.match(await runShellCommand("exit 130", tmpdir(), 60, new AbortController().signal, registered()?.registry), /^exit code 130\n/);
    });
  });
}

describe("the shell tool on Windows", () => {
  /** A shell that exits at once while a background child it started keeps its pipes open. */
  function simulatedShell(): ChildProcess {
    const child = new EventEmitter() as ChildProcess;
    const stdout = new PassThrough();
    const stderr = new PassThrough();
    Object.assign(child, { stdout, stderr, pid: 4242, exitCode: null, signalCode: null });
    let closedStreams = 0;
    for (const stream of [stdout, stderr]) {
      stream.once("close", () => {
        closedStreams += 1;
        if (closedStreams === 2) {
          child.emit("close", 0, null);
        }
      });
    }
    setImmediate(() => {
      stdout.write("started\n");
      Object.assign(child, { exitCode: 0 });
      child.emit("exit", 0, null);
    });
    return child;
  }

  test("answers 1 s after the shell exited even while a background child holds the pipes", async () => {
    const startedAt = Date.now();
    const result = await runWindowsShellCommand("start /b server", tmpdir(), 60, new AbortController().signal, () =>
      simulatedShell(),
    );
    assert.match(result, /^exit code 0\n--- stdout ---\nstarted\n/);
    const elapsed = Date.now() - startedAt;
    assert.ok(elapsed >= 900 && elapsed < 2_500, `answered after ${elapsed} ms`);
  });
});
