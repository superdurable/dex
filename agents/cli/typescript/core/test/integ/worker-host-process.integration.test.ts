// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { spawn, type ChildProcess } from "node:child_process";
import { EventEmitter } from "node:events";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { WorkerHostProcess } from "../../src/worker-host/worker-host-process.js";

describe("the launcher's Worker host stop", () => {
  test("gives up on a Worker host that never exits, within its bounds", async () => {
    // The signals go to a real process group of the test's own, so they reach nothing else.
    const target = spawn("sleep", ["30"], { detached: true, stdio: "ignore" });
    const signals: string[] = [];
    try {
      const simulated = new EventEmitter() as ChildProcess;
      Object.assign(simulated, { pid: target.pid, connected: false, send: () => true });
      target.once("exit", (_code, signal) => signals.push(signal ?? "exit"));
      const host = new WorkerHostProcess(
        join(mkdtempSync(join(tmpdir(), "dex-agents-host-")), "scripted.log"),
        {},
        () => undefined,
        () => simulated,
        { shutdownWaitMs: 100, terminateGraceMs: 100, killWaitMs: 100 },
      );
      const startedAt = Date.now();
      assert.equal(await host.shutdown(), undefined);
      assert.ok(Date.now() - startedAt < 2_000, `gave up after ${Date.now() - startedAt} ms`);
      assert.equal(host.hasExited, false);
    } finally {
      target.kill("SIGKILL");
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
    // The stop sent SIGTERM to the process group first.
    assert.deepEqual(signals, ["SIGTERM"]);
  });
});
