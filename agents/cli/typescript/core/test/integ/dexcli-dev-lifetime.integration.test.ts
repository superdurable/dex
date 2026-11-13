// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { constants } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { findFreeLoopbackPorts, pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { readCliEnvironment, readStartedDexcliDev } from "../../src/index.js";
import { CliProcess, TestHome, assertNoStartedServerLeft, enginePorts } from "../fixtures/cli-test-support.js";

const config = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
/** An hour-long lease, so only an explicit release frees a session within a test. */
const longLease = { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3600" };
const stoppedLine = "Stopped the dexcli dev this command started.";
const startedLine = /dexcli dev is ready \(pid (\d+)\); it stops when this command exits\. To share one dexcli dev across sessions, run dexcli dev yourself first\./;

describe("The dexcli dev a command started stops when it exits; a reused one keeps running", () => {
  let engine: PrivateDexEngine;
  const processes: CliProcess[] = [];
  const startedServers: number[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  afterEach(async () => {
    // SIGTERM lets each command stop the dexcli dev it started; the test cannot prove it owns that group.
    for (const cli of processes.splice(0)) {
      await cli.terminate();
    }
  });

  after(async () => {
    await engine?.stop();
    assertNoStartedServerLeft(startedServers);
  });

  function track(cli: CliProcess): CliProcess {
    processes.push(cli);
    return cli;
  }

  async function newPrivateHome(): Promise<TestHome> {
    const [dexPort, webPort] = await findFreeLoopbackPorts(2);
    const home = new TestHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 });
    home.writeConfig(config);
    return home;
  }

  /** Reads the pid of the dexcli dev that a command reported it started. */
  async function startedPid(output: CliProcess["stdout"]): Promise<number> {
    const pid = Number(startedLine.exec(await output.waitFor(startedLine, 60_000))?.[1]);
    startedServers.push(pid);
    return pid;
  }

  async function assertStopped(home: TestHome, pid: number): Promise<void> {
    await pollUntil(() => !isProcessGroupAlive(pid) || undefined, {
      timeoutMs: 30_000,
      description: `the dexcli dev process group ${pid} to exit`,
    });
    assert.equal(await isAnswering(home), false);
    assert.equal(readStartedDexcliDev(readCliEnvironment(home.environment())), undefined);
  }

  test("/exit stops the started dexcli dev, and a later --resume starts one that still has the session", async () => {
    const home = await newPrivateHome();
    const first = track(CliProcess.start(home));
    const pid = await startedPid(first.stderr);
    assert.equal(readStartedDexcliDev(readCliEnvironment(home.environment()))?.pid, pid);
    const sessionId = await first.waitForSession();
    first.writeLine("remember teal");
    await first.stdout.waitFor("echo: remember teal");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    assert.match(first.stderr.all, new RegExp(stoppedLine.replaceAll(".", "\\.")));
    await assertStopped(home, pid);

    const resumed = track(CliProcess.start(home, ["--resume", sessionId]));
    const resumedPid = await startedPid(resumed.stderr);
    await resumed.waitForSession();
    resumed.writeLine("history");
    await resumed.stdout.waitFor("remember teal");
    resumed.writeLine("/exit");
    assert.equal(await resumed.waitForExit(), 0);
    await assertStopped(home, resumedPid);
  });

  test("at a terminal, Ctrl-C twice during a turn exits and stops the started dexcli dev", async () => {
    const home = await newPrivateHome();
    const cli = track(CliProcess.startInTerminal(home));
    const pid = await startedPid(cli.stdout);
    await cli.waitForSession();
    cli.writeLine("wait slow-model");
    await pollUntil(() => existsSync(join(home.markerDirectory, "slow-model.reached")) || undefined, {
      description: "the model call to start",
    });
    cli.typeCtrlC();
    await cli.stdout.waitFor("Cancelling the turn; press Ctrl-C again to exit.");
    cli.typeCtrlC();
    await cli.stdout.waitFor(stoppedLine);
    assert.equal(await cli.waitForExit(), 0);
    writeFileSync(join(home.markerDirectory, "slow-model.release"), "");
    await assertStopped(home, pid);
  });

  test("SIGTERM stops the started dexcli dev and exits with 143", async () => {
    const home = await newPrivateHome();
    const cli = track(CliProcess.start(home));
    const pid = await startedPid(cli.stderr);
    await cli.waitForSession();
    cli.signal("SIGTERM");
    assert.equal(await cli.waitForExit(), 143);
    assert.match(cli.stderr.all, /Stopped the dexcli dev this command started\./);
    await assertStopped(home, pid);
  });

  /** Starts a later command on the session; only an explicit release lets it open without waiting an hour. */
  async function assertLeaseReleased(home: TestHome, sessionId: string): Promise<void> {
    const resumed = track(CliProcess.start(home, ["--resume", sessionId], longLease));
    await startedPid(resumed.stderr);
    assert.equal(await resumed.waitForSession(), sessionId);
    assert.doesNotMatch(resumed.stderr.all, /is open in another terminal/);
    resumed.writeLine("/exit");
    assert.equal(await resumed.waitForExit(), 0);
  }

  /** Waits until the command logged that its release RPC left, the point where a paused server holds it. */
  async function waitForReleaseStart(home: TestHome): Promise<void> {
    await pollUntil(() => readLog(home).includes("releasing the session lease") || undefined, {
      timeoutMs: 30_000,
      description: "the command to start releasing the session lease",
    });
  }

  for (const signal of ["SIGTERM", "SIGHUP"] as const) {
    test(`${signal} releases the session lease before the started dexcli dev stops`, async () => {
      const home = await newPrivateHome();
      const cli = track(CliProcess.start(home, [], longLease));
      const pid = await startedPid(cli.stderr);
      const sessionId = await cli.waitForSession();
      cli.signal(signal);
      assert.equal(await cli.waitForExit(), 128 + constants.signals[signal]);
      await assertStopped(home, pid);
      await assertLeaseReleased(home, sessionId);
    });
  }

  test("a second SIGTERM while the release waits kills the started dexcli dev and exits 143", async () => {
    const home = await newPrivateHome();
    const cli = track(CliProcess.start(home, [], longLease));
    const pid = await startedPid(cli.stderr);
    await cli.waitForSession();
    // A paused server holds the release RPC, so the cleanup cannot finish before the second signal.
    process.kill(-pid, "SIGSTOP");
    try {
      cli.signal("SIGTERM");
      await waitForReleaseStart(home);
      cli.signal("SIGTERM");
      assert.equal(await cli.waitForExit(15_000), 143);
      await pollUntil(() => !isProcessGroupAlive(pid) || undefined, {
        timeoutMs: 10_000,
        description: `the dexcli dev process group ${pid} to be killed`,
      });
      assert.equal(readStartedDexcliDev(readCliEnvironment(home.environment())), undefined);
    } finally {
      if (isProcessGroupAlive(pid)) {
        process.kill(-pid, "SIGKILL");
      }
    }
  });

  test("a SIGTERM while the chat's own close releases the lease waits for that release and exits 143", async () => {
    const home = await newPrivateHome();
    const gate = join(home.directory, "lease-gate");
    mkdirSync(gate);
    writeFileSync(join(gate, "acquire.go"), "");
    const cli = track(CliProcess.start(home, [], { ...longLease, DEX_AGENTS_CLI_TEST_LEASE_GATE_DIR: gate }));
    const pid = await startedPid(cli.stderr);
    const sessionId = await cli.waitForSession();
    // The end of input closes the command, and its release waits at the gate.
    cli.endInput();
    await pollUntil(() => existsSync(join(gate, "release.reached")) || undefined, {
      description: "the chat's release to reach the gate",
    });
    cli.signal("SIGTERM");
    await pollUntil(() => readLog(home).includes("ending the command on SIGTERM") || undefined, {
      description: "the command to start its signal exit",
    });
    // The signal's close must wait for the same release, not close the Client under it.
    writeFileSync(join(gate, "release.go"), "");
    assert.equal(await cli.waitForExit(), 143);
    await assertStopped(home, pid);
    await assertLeaseReleased(home, sessionId);
  });

  test("the sessions subcommand stops the dexcli dev it started", async () => {
    const home = await newPrivateHome();
    const cli = track(CliProcess.start(home, ["sessions"]));
    const pid = await startedPid(cli.stderr);
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stderr.all, /Stopped the dexcli dev this command started\./);
    await assertStopped(home, pid);
  });

  test("a reused dexcli dev keeps running after the command exits", async () => {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(config);
    const cli = track(CliProcess.start(home));
    await cli.waitForSession();
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stderr.all, /Starting dexcli dev|Stopped the dexcli dev/);
    assert.equal(await isAnswering(home), true);
  });

  test("a second session reuses the first one's dexcli dev and fails with a short error after the first exits", async () => {
    const home = await newPrivateHome();
    const first = track(CliProcess.start(home));
    const pid = await startedPid(first.stderr);
    await first.waitForSession();
    const second = track(CliProcess.start(home));
    await second.waitForSession();
    assert.doesNotMatch(second.stderr.all, /Starting dexcli dev/);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    await assertStopped(home, pid);

    second.writeLine("hello");
    assert.equal(await second.waitForExit(), 1);
    const webUrl = `http://127.0.0.1:${readCliEnvironment(home.environment()).dexcliDevWebPort}`;
    assert.equal(
      second.stderr.all.trim().split("\n").at(-1),
      `dex-agents-scripted: the dexcli dev at ${webUrl} stopped. A dexcli dev that another command started stops when that command exits. To share one dexcli dev across sessions, run dexcli dev yourself first.`,
    );
    assert.doesNotMatch(second.stderr.all, /Worker task stream disconnected|\n\s+at /);
  });

  test("a signal while the stop waits for a dexcli dev that ignores SIGTERM kills it at once and exits", async () => {
    const [dexPort, webPort] = await findFreeLoopbackPorts(2);
    const home = new SigtermIgnoringDexcliHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 });
    home.writeConfig(config);
    const cli = track(CliProcess.start(home));
    const pid = await startedPid(cli.stderr);
    await cli.waitForSession();
    cli.writeLine("/exit");
    await pollUntil(() => existsSync(home.termMarkerPath) || undefined, {
      timeoutMs: 30_000,
      description: "the dexcli dev wrapper to receive SIGTERM",
    });
    const signalledAt = Date.now();
    cli.signal("SIGTERM");
    assert.equal(await cli.waitForExit(), 143);
    assert.ok(Date.now() - signalledAt < 10_000, "the command must not wait out the 20-second grace period");
    await pollUntil(() => !isProcessGroupAlive(pid) || undefined, {
      timeoutMs: 10_000,
      description: `the dexcli dev process group ${pid} to be killed`,
    });
  });

  test("closing the terminal stops the started dexcli dev", async () => {
    const home = await newPrivateHome();
    const cli = CliProcess.startInTerminal(home);
    const pid = await startedPid(cli.stdout);
    await cli.waitForSession();
    // Killing the bridge closes the pseudo-terminal, which sends the command SIGHUP.
    await cli.kill();
    await assertStopped(home, pid);
  });
});

function isProcessGroupAlive(pid: number): boolean {
  try {
    process.kill(-pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code !== "ESRCH";
  }
}

/** Runs `dexcli dev` as the child of a shell that records SIGTERM and keeps running, as a stuck server would. */
const sigtermIgnoringDexcliScript = `#!/bin/sh
if [ "$1" != "dev" ]; then exec "$DEX_TEST_REAL_DEXCLI" "$@"; fi
trap 'touch "$DEX_TEST_TERM_MARKER"' TERM
"$DEX_TEST_REAL_DEXCLI" "$@" &
while :; do sleep 1; done
`;

/** A test home whose `DEXCLI` is {@link sigtermIgnoringDexcliScript}. */
class SigtermIgnoringDexcliHome extends TestHome {
  public readonly termMarkerPath = join(this.directory, "dexcli-wrapper.terminated");
  private readonly wrapperPath = join(this.directory, "dexcli-wrapper.sh");

  public constructor(ports: { readonly dexPort: number; readonly webPort: number }) {
    super(ports);
    writeFileSync(this.wrapperPath, sigtermIgnoringDexcliScript, { mode: 0o755 });
  }

  public override environment(): NodeJS.ProcessEnv {
    const environment = super.environment();
    return {
      ...environment,
      DEXCLI: this.wrapperPath,
      DEX_TEST_REAL_DEXCLI: environment.DEXCLI,
      DEX_TEST_TERM_MARKER: this.termMarkerPath,
    };
  }
}

async function isAnswering(home: TestHome): Promise<boolean> {
  const webPort = readCliEnvironment(home.environment()).dexcliDevWebPort;
  try {
    await fetch(`http://127.0.0.1:${webPort}/readyz`, { signal: AbortSignal.timeout(2_000) });
    return true;
  } catch {
    return false;
  }
}

function readLog(home: TestHome): string {
  const path = join(home.directory, ".dex", "agents", "scripted.log");
  return existsSync(path) ? readFileSync(path, "utf8") : "";
}
