// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine, pollUntil } from "@superdurable/dex-agents/testing";
import type { SessionTerminalLease } from "@superdurable/dex-agents";

import {
  cancelledTurnLine,
  otherHarnessSessionError,
  refusedModelCallError,
  sessionElsewhereReason,
  takeoverLine,
} from "../../src/session-environment-fence.js";
import { describeLeaseHolder, leaseLostLine } from "../../src/session-terminal-lease.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { shellReplacingScriptedHarness } from "../fixtures/scripted-harness.js";
import { FakeTerminalUi, PseudoTerminalCli, readTranscript, type Transcript } from "../fixtures/terminal-ui-test-support.js";

const quietConfig = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
/** A short TTL, so the tests that wait for an expiry take seconds. */
const shortTtl = { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3" };

describe("sessions taken over from another execution environment", () => {
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

  /** A home of its own is an execution environment of its own: another configuration file and workspace. */
  function newEnvironment(): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(quietConfig);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = [], environment: NodeJS.ProcessEnv = {}): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
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

  async function writeFileThroughChat(cli: CliProcess, path: string): Promise<void> {
    cli.writeLine(`call write_file {"path":"${path}","content":"x"}`);
    await cli.stdout.waitFor("Approve write_file?");
    cli.writeLine("y");
    await cli.stdout.waitFor(`result: wrote 1 bytes to ${path}`);
  }

  /** Starts a session in an environment and exits, which releases its lease. */
  async function startAndLeaveSession(home: TestHome): Promise<string> {
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    return sessionId;
  }

  async function waitForFailedTurn(home: TestHome, sessionId: string, error: string): Promise<void> {
    const sessions = await sessionClients.of(home);
    await pollUntil(
      async () => {
        const events = await sessions.listAgentEvents(sessionId);
        return events.some((event) => event.type === "turn_failed" && event.payload.includes(error))
          ? true
          : undefined;
      },
      { timeoutMs: 60_000, description: `a turn of ${sessionId} failed with: ${error}` },
    );
  }

  test("a session continues in another environment, keeps its Flow type, and runs new tool calls there", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const cliA = start(environmentA);
    const sessionId = await cliA.waitForSession();
    await writeFileThroughChat(cliA, "from-a.txt");
    cliA.writeLine("/exit");
    assert.equal(await cliA.waitForExit(), 0);

    const cliB = start(environmentB, ["--resume", sessionId]);
    await cliB.waitForSession();
    const workspaceB = realpathSync.native(environmentB.workspace);
    assert.ok(
      cliB.stderr.all.includes(
        `${takeoverLine(sessionId, environmentA.sessionFlowType(), environmentB.sessionFlowType(), workspaceB)}\n`,
      ),
      cliB.stderr.all,
    );
    assert.doesNotMatch(cliB.stderr.all, new RegExp(cancelledTurnLine.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
    await writeFileThroughChat(cliB, "from-b.txt");
    assert.equal(existsSync(join(environmentB.workspace, "from-b.txt")), true);
    assert.equal(existsSync(join(environmentA.workspace, "from-b.txt")), false);
    assert.equal(existsSync(join(environmentB.workspace, "from-a.txt")), false);
    const heldLease = await readLease(environmentA, sessionId);
    assert.ok(heldLease?.holderId.startsWith(`${environmentB.sessionFlowType()}/`), heldLease?.holderId);
    const sessions = await sessionClients.of(environmentA);
    assert.equal((await sessions.client.describeFlow(sessionId)).flowType, environmentA.sessionFlowType());
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);
    // Handed back by expiry: the stored holder still names environment B.
    const leftLease = await readLease(environmentA, sessionId);
    assert.ok(leftLease?.holderId.startsWith(`${environmentB.sessionFlowType()}/`), leftLease?.holderId);

    const sessionsOfB = start(environmentB, ["sessions"]);
    assert.equal(await sessionsOfB.waitForExit(), 0);
    assert.doesNotMatch(sessionsOfB.stdout.all, new RegExp(sessionId), "sessions lists this environment's sessions");

    // Back home: environment A takes it over again and runs the next call in its own workspace.
    const cliAAgain = start(environmentA, ["--resume", sessionId]);
    await cliAAgain.waitForSession();
    assert.ok(
      cliAAgain.stderr.all.includes(
        `${takeoverLine(sessionId, environmentB.sessionFlowType(), environmentA.sessionFlowType(), realpathSync.native(environmentA.workspace))}\n`,
      ),
      cliAAgain.stderr.all,
    );
    await writeFileThroughChat(cliAAgain, "from-a-again.txt");
    assert.equal(existsSync(join(environmentA.workspace, "from-a-again.txt")), true);
    cliAAgain.writeLine("/exit");
    assert.equal(await cliAAgain.waitForExit(), 0);
    assert.equal(await readLease(environmentA, sessionId), undefined, "a session of this environment is released");
  });

  test("a paused holder in the first environment loses the session to the second and stops", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const cliA = start(environmentA, [], shortTtl);
    const sessionId = await cliA.waitForSession();
    await waitForLeaseHolder(environmentA, sessionId, cliA.pid);

    cliA.signal("SIGSTOP");
    const cliB = start(environmentB, ["--resume", sessionId], shortTtl);
    let leaseOfB: SessionTerminalLease;
    try {
      await cliB.waitForSession();
      leaseOfB = await waitForLeaseHolder(environmentA, sessionId, cliB.pid);
    } finally {
      cliA.signal("SIGCONT");
    }
    assert.equal(await cliA.waitForExit(30_000), 1);
    assert.ok(cliA.stderr.all.includes(`${leaseLostLine(sessionId, describeLeaseHolder(leaseOfB))}\n`), cliA.stderr.all);
    await writeFileThroughChat(cliB, "after-takeover.txt");
    assert.equal(existsSync(join(environmentB.workspace, "after-takeover.txt")), true);
    assert.equal(existsSync(join(environmentA.workspace, "after-takeover.txt")), false);
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);
  });

  test("a takeover cancels the turn in progress, so nothing it asked for runs in either environment", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const cliA = start(environmentA, [], shortTtl);
    const sessionId = await cliA.waitForSession();
    cliA.writeLine('call write_file {"path":"pending.txt","content":"x"}');
    await cliA.stdout.waitFor("Approve write_file?");
    await cliA.kill();

    const cliB = start(environmentB, ["--resume", sessionId], shortTtl);
    await cliB.waitForSession();
    assert.ok(cliB.stderr.all.includes(`${cancelledTurnLine}\n`), cliB.stderr.all);
    const sessions = await sessionClients.of(environmentA);
    await pollUntil(async () => ((await sessions.readAgentState(sessionId))?.activeTurnId === null ? true : undefined), {
      timeoutMs: 30_000,
      description: "the cancelled turn ended",
    });
    assert.equal(existsSync(join(environmentA.workspace, "pending.txt")), false);
    assert.equal(existsSync(join(environmentB.workspace, "pending.txt")), false);
    await writeFileThroughChat(cliB, "next-turn.txt");
    assert.equal(existsSync(join(environmentB.workspace, "next-turn.txt")), true);
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);
  });

  test("a command of the session's own environment refuses the calls while another environment holds it", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const sessionId = await startAndLeaveSession(environmentA);
    const cliB = start(environmentB, ["--resume", sessionId]);
    await cliB.waitForSession();
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);

    // The only Worker of the session's Flow type now is this new command of environment A.
    const cliA = start(environmentA);
    await cliA.waitForSession();
    const sessions = await sessionClients.of(environmentA);
    await sessions.sendUserMessage(sessionId, 'call write_file {"path":"refused.txt","content":"x"}');
    await waitForFailedTurn(environmentA, sessionId, refusedModelCallError(sessionElsewhereReason(sessionId)));
    assert.equal(existsSync(join(environmentA.workspace, "refused.txt")), false);
    assert.equal(existsSync(join(environmentB.workspace, "refused.txt")), false);
    cliA.writeLine("/exit");
    assert.equal(await cliA.waitForExit(), 0);
  });

  test("a command that took one session over refuses the other sessions of that environment", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const takenSessionId = await startAndLeaveSession(environmentA);
    const otherSessionId = await startAndLeaveSession(environmentA);
    // The only Worker of environment A's Flow type now is this command of environment B.
    const cliB = start(environmentB, ["--resume", takenSessionId]);
    await cliB.waitForSession();
    const sessions = await sessionClients.of(environmentA);
    await sessions.sendUserMessage(otherSessionId, 'call write_file {"path":"other.txt","content":"x"}');
    await waitForFailedTurn(environmentA, otherSessionId, refusedModelCallError(sessionElsewhereReason(otherSessionId)));
    assert.equal(existsSync(join(environmentB.workspace, "other.txt")), false);
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);
  });

  test("--resume of another harness's session exits with 2", async () => {
    const shellEnvironment = new TestHome(enginePorts(engine), shellReplacingScriptedHarness);
    shellEnvironment.writeConfig(quietConfig);
    const shellSessions = await sessionClients.of(shellEnvironment);
    const sessionId = "shell-session-of-another-harness";
    await shellSessions.startSession(sessionId);
    const cli = start(newEnvironment(), ["--resume", sessionId]);
    assert.equal(await cli.waitForExit(), 2);
    assert.ok(
      cli.stderr.all.includes(otherHarnessSessionError(sessionId, shellEnvironment.sessionFlowType(), "dex-agents-scripted")),
      cli.stderr.all,
    );
  });

  test("the terminal UI of a session taken over from another environment offers no /new", async () => {
    const environmentA = newEnvironment();
    const environmentB = newEnvironment();
    const sessionId = await startAndLeaveSession(environmentA);
    const fake = new FakeTerminalUi([resumedWithoutNewSessionTranscript()]);
    const terminal = PseudoTerminalCli.start(environmentB, ["--resume", sessionId], { DEXCLI: fake.executable });
    terminals.push(terminal);
    await terminal.terminal.waitFor(`Session ${sessionId} was last open in execution environment ${environmentA.sessionFlowType()}`);
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    assert.equal(fake.launchCount, 1);
    assert.deepEqual(fake.record(1).filter((event) => event.event === "mismatch"), []);
    const lease = await readLease(environmentA, sessionId);
    assert.ok(lease?.holderId.startsWith(`${environmentB.sessionFlowType()}/`), "the Worker host handed it back by expiry");
  });
});

/** Returns `chat_ready_then_exit` for a resumed session whose launch document leaves out `start_session`. */
function resumedWithoutNewSessionTranscript(): Transcript {
  const transcript = readTranscript("chat_ready_then_exit");
  const [launch, ...rest] = transcript.steps;
  const message = launch?.message as { session: Record<string, unknown>; methods: readonly string[] };
  const resumed = {
    ...launch,
    message: {
      ...message,
      session: { ...message.session, is_resumed: true },
      methods: message.methods.filter((method) => method !== "start_session"),
    },
  };
  return { ...transcript, steps: [resumed, ...rest] };
}
