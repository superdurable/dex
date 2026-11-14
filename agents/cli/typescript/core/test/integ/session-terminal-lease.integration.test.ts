// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { hostname } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine, pollUntil } from "@superdurable/dex-agents/testing";
import type { SessionTerminalLease } from "@superdurable/dex-agents";

import {
  describeLeaseHolder,
  leaseLostLine,
  plainWaitingLine,
  tookOverLine,
  waitingRedrawPrefix,
} from "../../src/session-terminal-lease.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import {
  FakeTerminalUi,
  PseudoTerminalCli,
  chatTranscript,
  readTranscript,
  runRecords,
  waitUntil,
  type Transcript,
} from "../fixtures/terminal-ui-test-support.js";

const quietConfig = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
/** A short TTL, so the tests that wait for an expiry take seconds. */
const shortTtl = { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3" };
/** An hour-long lease, so only an explicit release frees a session within a test. */
const longLease = { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3600" };

describe("one terminal per session", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  const processes: CliProcess[] = [];
  const terminals: PseudoTerminalCli[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
  });

  afterEach(async () => {
    for (const terminal of terminals.splice(0)) {
      await terminal.kill();
    }
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    await sessionClients?.close();
    await engine?.stop();
  });

  function newHome(): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(quietConfig);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = [], environment: NodeJS.ProcessEnv = {}): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
  }

  function startOnTerminal(
    home: TestHome,
    commandArguments: readonly string[],
    environment: NodeJS.ProcessEnv = {},
  ): PseudoTerminalCli {
    const terminal = PseudoTerminalCli.start(home, commandArguments, environment);
    terminals.push(terminal);
    return terminal;
  }

  async function readLease(home: TestHome, sessionId: string): Promise<SessionTerminalLease | undefined> {
    const sessions = await sessionClients.of(home);
    const [lease] = await sessions.client.getAttributes(sessionId, sessions.flow.sessionTerminalLease);
    return lease;
  }

  async function waitForLeaseHolder(home: TestHome, sessionId: string, processId: number): Promise<SessionTerminalLease> {
    return pollUntil(
      async () => {
        const lease = await readLease(home, sessionId);
        return lease?.processId === processId ? lease : undefined;
      },
      { timeoutMs: 30_000, description: `pid ${processId} holds the lease of ${sessionId}` },
    );
  }

  test("a second terminal waits with one animated line and takes over right after the first exits", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);

    // The relay's pseudo-terminal reports no width, so the line is cut to 80 columns.
    const second = startOnTerminal(home, ["--plain", "--resume", sessionId]);
    await second.terminal.waitFor(`${waitingRedrawPrefix}⠋ Session ${sessionId} is open in another terminal (`);
    await waitUntil(() => countOf(second.terminal.all, waitingRedrawPrefix) >= 12, "the line redraws in place");
    const waitingText = second.terminal.all.slice(second.terminal.all.indexOf(waitingRedrawPrefix));
    assert.doesNotMatch(waitingText, /\n/, "no new line per redraw");
    const shownFrames = new Set(waitingText.split(waitingRedrawPrefix).map((line) => line.charAt(0)).filter((frame) => frame !== ""));
    assert.ok(shownFrames.size >= 10, `the spinner turns: ${[...shownFrames].join("")}`);
    assert.ok(waitingText.split(waitingRedrawPrefix).every((line) => [...line].length <= 79), "each frame fits 80 columns");

    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    const exitedAt = Date.now();
    await second.terminal.waitFor(`${waitingRedrawPrefix}${tookOverLine(sessionId)}`);
    assert.ok(Date.now() - exitedAt < 5_000, "the release lets the waiting terminal in at once");
    await second.terminal.waitFor(`Session ${sessionId}. Type a message`);
    second.type("/exit\r");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(await readLease(home, sessionId), undefined, "the exit released the lease");
  });

  test("after the holder is killed, a waiting terminal takes over once the lease expires", async () => {
    const home = newHome();
    const first = start(home, [], shortTtl);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);
    const second = start(home, ["--resume", sessionId], shortTtl);
    await second.stderr.waitFor("is open in another terminal");

    await first.kill();
    const killedAt = Date.now();
    await second.stderr.waitFor(tookOverLine(sessionId), 20_000);
    assert.ok(Date.now() - killedAt < 10_000, "the expired lease goes to the waiting terminal");
    await second.waitForSession();
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("Ctrl-C while waiting exits 130 without a stack trace, and the holder keeps the session", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);
    const second = startOnTerminal(home, ["--plain", "--resume", sessionId]);
    await second.terminal.waitFor("is open in another terminal");

    second.type("\x03");
    assert.equal(await second.waitForExit(), 130);
    assert.doesNotMatch(second.terminal.all, /at .*\.js:\d+|Error:|Took over/);
    assert.equal((await readLease(home, sessionId))?.processId, first.pid);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
  });

  test("without a terminal, the wait prints one waiting line and one took over line", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    const lease = await waitForLeaseHolder(home, sessionId, first.pid);
    const second = start(home, ["--resume", sessionId]);
    await second.stderr.waitFor("is open in another terminal");
    // Several attempts pass before the release; none adds a line.
    await new Promise((resolve) => setTimeout(resolve, 3_000));

    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    await second.waitForSession();
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    const leaseLines = second.stderr.all
      .split("\n")
      .filter((line) => line.includes("another terminal") || line.startsWith("Took over"));
    assert.deepEqual(leaseLines, [plainWaitingLine(sessionId, describeLeaseHolder(lease)), tookOverLine(sessionId)]);
    assert.doesNotMatch(second.stderr.all, /\r|\u001b/);
  });

  test("a chat whose lease another terminal took while it was paused stops with one line and exits 1", async () => {
    const home = newHome();
    const first = start(home, [], shortTtl);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);

    first.signal("SIGSTOP");
    // The paused command's streams stay registered, so the acquire may wait out Dex's sticky wait first; the
    // default lease outlasts that wait, while a 3-second lease would count as unconfirmed at once.
    const second = start(home, ["--resume", sessionId]);
    let secondLease: SessionTerminalLease;
    try {
      // The first lease may expire while the second starts, so it may take over without waiting.
      await second.waitForSession();
      secondLease = await waitForLeaseHolder(home, sessionId, second.pid);
      assert.equal(secondLease.hostName, hostname());
    } finally {
      first.signal("SIGCONT");
    }
    assert.equal(await first.waitForExit(30_000), 1);
    assert.ok(first.stderr.all.includes(`${leaseLostLine(sessionId, describeLeaseHolder(secondLease))}\n`), first.stderr.all);
    assert.doesNotMatch(first.stderr.all, /at .*\.js:\d+/);
    assert.equal((await readLease(home, sessionId))?.processId, second.pid, "the stopped chat released nothing");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0, second.stderr.all);
  });

  test("the terminal UI launcher waits for the lease before the handoff", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);
    const fake = new FakeTerminalUi([resumedTranscript()]);
    const second = startOnTerminal(home, ["--resume", sessionId], { DEXCLI: fake.executable });
    await second.terminal.waitFor("is open in another terminal");
    assert.equal(fake.launchCount, 0, "no handoff while another terminal has the session");

    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    await second.terminal.waitFor(tookOverLine(sessionId));
    assert.equal(await second.waitForExit(), 0, second.terminal.all);
    assert.equal(fake.launchCount, 1);
  });

  test("Ctrl-C after the first lease call took the lease but before its answer arrived releases it", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    const gate = join(home.directory, "lease-gate");
    mkdirSync(gate);
    writeFileSync(join(gate, "release.go"), "");
    const second = start(home, ["--resume", sessionId], { ...longLease, DEX_AGENTS_CLI_TEST_LEASE_GATE_DIR: gate });
    await pollUntil(() => existsSync(join(gate, "acquire.reached")) || undefined, {
      timeoutMs: 30_000,
      description: "the first lease call to take the lease",
    });
    assert.equal((await readLease(home, sessionId))?.processId, second.pid);

    second.interrupt();
    assert.equal(await second.waitForExit(), 130);
    assert.doesNotMatch(second.stderr.all, /at .*\.js:\d+/);
    assert.equal(await readLease(home, sessionId), undefined, "the interrupted command released what it took");
  });

  test("a Worker host that dies while the launcher waits for the lease ends the command with one line", async () => {
    const home = newHome();
    const first = start(home);
    const sessionId = await first.waitForSession();
    await waitForLeaseHolder(home, sessionId, first.pid);
    const fake = new FakeTerminalUi([resumedTranscript()]);
    const second = startOnTerminal(home, ["--resume", sessionId], { DEXCLI: fake.executable });
    await second.terminal.waitFor("is open in another terminal");
    const [record] = await pollUntil(
      async () => {
        const records = runRecords(home);
        return records.length === 1 ? records : undefined;
      },
      { timeoutMs: 10_000, description: "the waiting launcher's Worker host run record" },
    );

    process.kill(record!.hostPid, "SIGKILL");
    assert.equal(await second.waitForExit(15_000), 1, second.terminal.all);
    assert.ok(second.terminal.all.includes("The Worker host stopped."), second.terminal.all);
    assert.doesNotMatch(second.terminal.all, /at .*\.js:\d+/);
    assert.equal(fake.launchCount, 0);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
  });

  test("a switch_session from the terminal UI moves the lease and relaunches it on the chosen session", async () => {
    const home = newHome();
    const other = start(home);
    const otherSessionId = await other.waitForSession();
    await waitForLeaseHolder(home, otherSessionId, other.pid);
    const switching = chatTranscript("chat_switch_session", [
      { from: "tui", message: { type: "switch_session", session_id: otherSessionId, is_resumed: true } },
    ]);
    const fake = new FakeTerminalUi([switching, resumedTranscript()]);
    const terminal = startOnTerminal(home, [], { DEXCLI: fake.executable });
    await terminal.terminal.waitFor(`Session ${otherSessionId} is open in another terminal`);
    assert.equal(fake.launchCount, 1);

    other.writeLine("/exit");
    assert.equal(await other.waitForExit(), 0);
    await terminal.terminal.waitFor(tookOverLine(otherSessionId));
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    assert.equal(fake.launchCount, 2);
    // The relaunch's transcript expects is_resumed true, so a mismatch would show a wrong launch document.
    assert.deepEqual(fake.record(2).filter((event) => event.event === "mismatch"), []);
    assert.equal(await readLease(home, otherSessionId), undefined, "the Worker host released the lease at exit");
  });
});

/** Returns `chat_ready_then_exit` for a resumed session: its launch document has `is_resumed` true. */
function resumedTranscript(): Transcript {
  const transcript = readTranscript("chat_ready_then_exit");
  const [launch, ...rest] = transcript.steps;
  const message = launch?.message as { session: Record<string, unknown> };
  // A relaunch after a switch named the left session before 0.6.7, which these tests do not fix.
  const ignore = [...((launch as { ignore?: string[] }).ignore ?? []), "session.previous_session_id"];
  const resumed = { ...launch, ignore, message: { ...message, session: { ...message.session, is_resumed: true } } };
  return { ...transcript, steps: [resumed, ...rest] };
}

function countOf(text: string, part: string): number {
  return text.split(part).length - 1;
}
