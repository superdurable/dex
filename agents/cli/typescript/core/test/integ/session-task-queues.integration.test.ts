// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { hostname, userInfo } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine, pollUntil, type AgentSessionTestClient } from "@superdurable/dex-agents/testing";
import { startAgentSession, type SessionTerminalLease } from "@superdurable/dex-agents";

import { sessionTaskQueue } from "../../src/index.js";
import { leaseUnconfirmedLine, tookOverLine } from "../../src/session-terminal-lease.js";
import {
  cancelledTurnLine,
  describeSessionEnvironmentIdentity,
  otherEnvironmentLegacySessionError,
  takeoverLine,
  turnNotCancelledLine,
} from "../../src/session-task-queues.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import {
  FakeTerminalUi,
  PseudoTerminalCli,
  chatTranscript,
  isReceived,
  readTranscript,
  type Transcript,
} from "../fixtures/terminal-ui-test-support.js";

const shellConfig = 'connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: ["pwd"]}}\n';
/** A short TTL, so a killed holder's lease expires within the test. */
const shortTtl = { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3" };

describe("session task queues", () => {
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

  /** A home with its own configuration file and workspace: another execution environment on this host. */
  function newEnvironment(): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(shellConfig);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = [], environment: NodeJS.ProcessEnv = {}): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
  }

  function place(home: TestHome): string {
    mkdirSync(home.workspace, { recursive: true });
    return `${hostname()}:${realpathSync.native(home.workspace)}`;
  }

  /** The takeover line's text of a home's environment, with its configuration file or `configPath`. */
  function identity(home: TestHome, configPath = home.configPath): string {
    mkdirSync(home.workspace, { recursive: true });
    return describeSessionEnvironmentIdentity({
      host_name: hostname(),
      user_id: typeof process.getuid === "function" ? String(process.getuid()) : userInfo().username,
      user_name: userInfo().username,
      config_identity: realpathSync(configPath),
      workspace: realpathSync.native(home.workspace),
    });
  }

  /** Returns a new directory for the test CLI's lease faults. */
  function faultDirectory(home: TestHome): string {
    const directory = join(home.directory, `lease-faults-${Math.floor(Math.random() * 1e9)}`);
    mkdirSync(directory, { recursive: true });
    return directory;
  }

  async function runPwd(cli: CliProcess, home: TestHome): Promise<void> {
    cli.writeLine('call run_shell_command {"command":"pwd"}');
    await cli.stdout.waitFor(`result: exit code 0\n--- stdout ---\n${realpathSync.native(home.workspace)}`);
  }

  async function readLease(home: TestHome, sessionId: string): Promise<SessionTerminalLease | undefined> {
    const sessions = await sessionClients.of(home);
    const [lease] = await sessions.client.getAttributes(sessionId, sessions.flow.sessionTerminalLease);
    return lease;
  }

  async function readLastEnvironment(home: TestHome, sessionId: string): Promise<Readonly<Record<string, string>> | undefined> {
    const sessions = await sessionClients.of(home);
    const [environment] = await sessions.client.getAttributes(sessionId, sessions.flow.sessionLastTerminalEnvironment);
    return environment;
  }

  async function startLegacySession(home: TestHome): Promise<{ sessionId: string; sessions: AgentSessionTestClient }> {
    const sessions = await sessionClients.ofLegacy(home);
    const sessionId = `scripted-legacy-${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
    await startAgentSession(sessions.client, sessions.flow, sessionId);
    return { sessionId, sessions };
  }

  test("a session started in one environment continues in another, whose tool Steps then run there", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const first = start(homeA);
    const sessionId = await first.waitForSession();
    await runPwd(first, homeA);
    const sessions = await sessionClients.of(homeA);
    const info = await sessions.client.describeFlow(sessionId);
    assert.equal(info.flowType, homeA.harness.flowType, "a new session has the harness's own Flow type");
    assert.equal((await readLastEnvironment(homeA, sessionId))?.workspace, realpathSync.native(homeA.workspace));
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const second = start(homeB, ["--resume", sessionId]);
    await second.stdout.waitFor(`Resumed session ${sessionId}.`);
    assert.ok(second.stderr.all.includes(`${takeoverLine(sessionId, identity(homeA), identity(homeB))}\n`), second.stderr.all);
    await runPwd(second, homeB);
    assert.equal((await readLastEnvironment(homeA, sessionId))?.workspace, realpathSync.native(homeB.workspace));
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(await readLease(homeA, sessionId), undefined, "the exit released the lease");
  });

  test("a second terminal from another environment waits without pulling, then takes over after the first exits", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const first = start(homeA);
    const sessionId = await first.waitForSession();
    const second = start(homeB, ["--resume", sessionId]);
    await second.stderr.waitFor(`Session ${sessionId} is open in another terminal (`);

    // The waiting command pulls nothing, so the session's tool Steps keep running in the first environment.
    await runPwd(first, homeA);
    await runPwd(first, homeA);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    await second.stderr.waitFor(tookOverLine(sessionId));
    await second.stderr.waitFor(takeoverLine(sessionId, identity(homeA), identity(homeB)));
    await second.waitForSession();
    await runPwd(second, homeB);
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("a takeover cancels the turn that was in progress in the other environment", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const first = start(homeA, [], shortTtl);
    const sessionId = await first.waitForSession();
    first.writeLine("wait held");
    await pollUntil(async () => (await sessionClients.of(homeA).then((sessions) => sessions.readAgentState(sessionId)))?.activeTurnId ?? undefined, {
      timeoutMs: 30_000,
      description: "the turn to start in the first environment",
    });
    await first.kill();

    const second = start(homeB, ["--resume", sessionId], shortTtl);
    await second.stderr.waitFor(takeoverLine(sessionId, identity(homeA), identity(homeB)), 60_000);
    await second.stderr.waitFor(cancelledTurnLine);
    await second.waitForSession();
    const state = await (await sessionClients.of(homeA)).waitForCompletedTurns(sessionId, 1);
    assert.equal(state.activeTurnId, null);
    second.writeLine("after the takeover");
    await second.stdout.waitFor("echo: after the takeover");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  /** Starts a turn that waits on a gate in `home`'s first command, then kills that command. */
  async function killDuringTurn(first: CliProcess, home: TestHome, sessionId: string): Promise<void> {
    first.writeLine("wait held");
    await pollUntil(async () => (await (await sessionClients.of(home)).readAgentState(sessionId))?.activeTurnId ?? undefined, {
      timeoutMs: 30_000,
      description: "the turn to start in the first environment",
    });
    await first.kill();
  }

  test("a takeover with only another configuration file also cancels the turn in progress", async () => {
    const home = newEnvironment();
    const otherConfig = join(home.directory, "other.yaml");
    writeFileSync(otherConfig, shellConfig);
    const first = start(home, [], shortTtl);
    const sessionId = await first.waitForSession();
    await killDuringTurn(first, home, sessionId);

    const second = start(home, ["--config", otherConfig, "--resume", sessionId], shortTtl);
    await second.stderr.waitFor(takeoverLine(sessionId, identity(home), identity(home, otherConfig)), 60_000);
    await second.stderr.waitFor(cancelledTurnLine);
    await second.waitForSession();
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("a takeover whose cancel_turn fails says so and keeps refusing that turn's calls", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const first = start(homeA, [], shortTtl);
    const sessionId = await first.waitForSession();
    await killDuringTurn(first, homeA, sessionId);
    const faults = faultDirectory(homeB);
    writeFileSync(join(faults, "fail-cancel"), "");

    const second = start(homeB, ["--resume", sessionId], { ...shortTtl, DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR: faults });
    await second.stderr.waitFor(takeoverLine(sessionId, identity(homeA), identity(homeB)), 60_000);
    await second.stderr.waitFor(turnNotCancelledLine);
    assert.ok(!second.stderr.all.includes(cancelledTurnLine), second.stderr.all);
    // The refused turn's model call never runs here, so its gate is never reached in this environment.
    await delay(3_000);
    assert.equal(existsSync(join(homeB.markerDirectory, "held.reached")), false);
    second.writeLine("/exit");
    // The refused turn may fail while the chat runs, which makes the exit code 1.
    assert.ok([0, 1].includes((await second.waitForExit()) ?? -1), second.stderr.all);
  });

  test("renewals that cannot be confirmed end the chat before the lease expires, and another terminal then takes over", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const faults = faultDirectory(homeA);
    const first = start(homeA, [], { ...shortTtl, DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR: faults });
    const sessionId = await first.waitForSession();
    await pollUntil(async () => (await readLease(homeA, sessionId)) ?? undefined, {
      timeoutMs: 30_000,
      description: "the first command to hold the lease",
    });

    writeFileSync(join(faults, "fail-acquire"), "");
    assert.equal(await first.waitForExit(15_000), 1);
    assert.ok(first.stderr.all.includes(`${leaseUnconfirmedLine(sessionId)}\n`), first.stderr.all);
    const leftLease = await readLease(homeA, sessionId);
    assert.ok(leftLease !== undefined, "the stopped chat released nothing");
    const logLine = readFileSync(join(homeA.directory, ".dex", "agents", "scripted.log"), "utf8")
      .split("\n")
      .find((line) => line.includes(`session lease unconfirmed; stopped pulling its task queue: session=${sessionId}`));
    const stoppedAt = Date.parse(logLine?.split(" ", 1)[0] ?? "");
    assert.ok(stoppedAt < Date.parse(leftLease.expiresAt), `stopped pulling at ${logLine ?? "never"}, lease ${leftLease.expiresAt}`);

    const second = start(homeB, ["--resume", sessionId], shortTtl);
    await second.stderr.waitFor(takeoverLine(sessionId, identity(homeA), identity(homeB)), 60_000);
    await second.waitForSession();
    await runPwd(second, homeB);
    const taken = await readLease(homeA, sessionId);
    assert.ok(taken !== undefined && Date.parse(taken.openedAt) >= Date.parse(leftLease.expiresAt), JSON.stringify(taken));
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("in the terminal UI, renewals that cannot be confirmed stop the terminal UI with one line and exit 1", async () => {
    const home = newEnvironment();
    const faults = faultDirectory(home);
    const fake = new FakeTerminalUi([pausingTranscript(60_000)]);
    const terminal = PseudoTerminalCli.start(home, [], {
      DEXCLI: fake.executable,
      ...shortTtl,
      DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR: faults,
    });
    terminals.push(terminal);
    await fake.waitForRecord((event) => event.event === "sent" && (event.message as { type?: string }).type === "ready");
    writeFileSync(join(faults, "fail-acquire"), "");
    assert.equal(await terminal.waitForExit(30_000), 1, terminal.terminal.all);
    assert.match(terminal.terminal.all, /Session \S+ could not renew its lease in time; this chat stopped so that another terminal can take the session\./);
  });

  test("an acquire whose answer was lost still opens the session and releases it at exit", async () => {
    const home = newEnvironment();
    const faults = faultDirectory(home);
    writeFileSync(join(faults, "lose-next-acquire"), "");
    const cli = start(home, [], { DEX_AGENTS_CLI_TEST_LEASE_FAULT_DIR: faults });
    const sessionId = await cli.waitForSession();
    assert.equal(existsSync(join(faults, "lose-next-acquire")), false, "the first acquire lost its answer");
    cli.writeLine("hello after the lost answer");
    await cli.stdout.waitFor("echo: hello after the lost answer");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(await readLease(home, sessionId), undefined, "the session Worker served the release");
  });

  test("a message sent while no command has the session open waits for the command that opens it", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const first = start(homeA);
    const sessionId = await first.waitForSession();
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    const sessions = await sessionClients.of(homeA);

    // Dex holds the RPC until a Worker pulls the session's task queue, within its dispatch wait.
    const second = start(homeB, ["--resume", sessionId]);
    assert.equal(await sessions.sendUserMessage(sessionId, "sent from Dex Web"), true);
    await second.waitForSession();
    const state = await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.turnCount, 1);
    const page = await sessions.listAgentMessages(sessionId, null, 50);
    assert.ok(JSON.stringify(page.messages).includes("echo: sent from Dex Web"), JSON.stringify(page.messages));
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("an environment-hashed session resumes in its own environment, and another environment refuses it", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const { sessionId } = await startLegacySession(homeA);

    const first = start(homeA, ["--resume", sessionId]);
    await first.stdout.waitFor(`Resumed session ${sessionId}.`);
    first.writeLine("still here");
    await first.stdout.waitFor("echo: still here");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const other = start(homeB, ["--resume", sessionId]);
    assert.equal(await other.waitForExit(), 2);
    assert.ok(
      other.stderr.all.includes(otherEnvironmentLegacySessionError(sessionId, homeA.legacySessionFlowType())),
      other.stderr.all,
    );
  });

  test("sessions lists every environment's sessions with where each was last open", async () => {
    const homeA = newEnvironment();
    const homeB = newEnvironment();
    const fromA = start(homeA);
    const sessionA = await fromA.waitForSession();
    fromA.writeLine("/exit");
    assert.equal(await fromA.waitForExit(), 0);
    const fromB = start(homeB);
    const sessionB = await fromB.waitForSession();
    fromB.writeLine("/exit");
    assert.equal(await fromB.waitForExit(), 0);
    const { sessionId: legacyA } = await startLegacySession(homeA);
    const sessions = await sessionClients.of(homeA);
    await pollUntil(
      async () => {
        const page = await sessions.client.searchFlows(
          `FlowType IN ("${homeA.sessionFlowType()}", "${homeA.legacySessionFlowType()}")`,
          250,
        );
        const ids = new Set(page.flows.map((flow) => flow.flowId));
        return [sessionA, sessionB, legacyA].every((id) => ids.has(id)) || undefined;
      },
      { timeoutMs: 30_000, description: "the sessions to show in search" },
    );

    const listFromA = start(homeA, ["sessions"]);
    assert.equal(await listFromA.waitForExit(), 0, listFromA.stderr.all);
    const rowsFromA = rowsByIdIn(listFromA.stdout.all);
    assert.equal(rowsFromA.get(sessionA)?.[5], place(homeA));
    assert.equal(rowsFromA.get(sessionB)?.[5], place(homeB));
    assert.equal(rowsFromA.get(legacyA)?.[5], place(homeA), "an environment-hashed session runs only where it started");

    const listFromB = start(homeB, ["sessions"]);
    assert.equal(await listFromB.waitForExit(), 0, listFromB.stderr.all);
    const rowsFromB = rowsByIdIn(listFromB.stdout.all);
    assert.equal(rowsFromB.get(sessionA)?.[5], place(homeA));
    assert.equal(rowsFromB.get(sessionB)?.[5], place(homeB));
    assert.equal(rowsFromB.has(legacyA), false, "another environment's hashed sessions stay out of the list");
  });

  test("a terminal UI switch moves the session Worker to the chosen session's task queue", async () => {
    const home = newEnvironment();
    const plain = start(home);
    const chosenSessionId = await plain.waitForSession();
    plain.writeLine("/exit");
    assert.equal(await plain.waitForExit(), 0);
    const switching = chatTranscript("chat_switch_session", [
      { from: "tui", message: { type: "switch_session", session_id: chosenSessionId, is_resumed: true } },
    ]);
    // The relaunch must name the chosen session with its own Flow type and no previous_session_id.
    const relaunch = pausingResumedTranscript(20_000, chosenSessionId, home.harness.flowType);
    const fake = new FakeTerminalUi([switching, relaunch]);
    const terminal = PseudoTerminalCli.start(home, [], { DEXCLI: fake.executable });
    terminals.push(terminal);
    const sessions = await sessionClients.of(home);
    await pollUntil(async () => (fake.launchCount >= 2 && (await readLease(home, chosenSessionId)) !== undefined) || undefined, {
      timeoutMs: 60_000,
      description: "the relaunch on the chosen session",
    }).catch((error: unknown) => {
      throw new Error(`${String(error)}\n${terminal.terminal.all}\n${JSON.stringify(fake.record(fake.launchCount))}`);
    });
    await fake.waitForRecord(isReceived("launch"), 2);
    assert.deepEqual(fake.record(2).filter((event) => event.event === "mismatch"), []);
    const leftSessionId = await pollUntil(
      async () => {
        const page = await sessions.client.searchFlows(`FlowType = "${home.sessionFlowType()}"`, 50);
        return page.flows.find((flow) => flow.flowId !== chosenSessionId)?.flowId;
      },
      { timeoutMs: 30_000, description: "the session the terminal UI left" },
    );
    assert.equal(await readLease(home, leftSessionId), undefined, "the switch released the left session");

    assert.equal(await sessions.sendUserMessage(chosenSessionId, "after the switch"), true);
    await sessions.waitForCompletedTurns(chosenSessionId, 1);
    const unanswered = await Promise.race([
      sessions.sendUserMessage(leftSessionId, "nobody pulls this").then(() => "answered"),
      delay(3_000).then(() => "waiting"),
    ]);
    assert.equal(unanswered, "waiting", "no Worker pulls the left session's task queue");
    assert.notEqual(sessionTaskQueue(chosenSessionId), sessionTaskQueue(leftSessionId));
  });
});

/** Returns `chat_ready_then_exit` for a resumed session that stays open for `pauseMs` before it exits. */
/** Returns `chat_ready_then_exit` for a new session that stays open for `pauseMs` before it exits. */
function pausingTranscript(pauseMs: number): Transcript {
  const transcript = readTranscript("chat_ready_then_exit");
  const [launch, ...rest] = transcript.steps;
  const readyIndex = rest.findIndex((step) => (step.message as { type?: string } | undefined)?.type === "ready");
  const steps = [...rest];
  steps.splice(readyIndex + 1, 0, { pause_ms: pauseMs, role: "tui" });
  return { ...transcript, steps: [launch ?? {}, ...steps] };
}

function pausingResumedTranscript(pauseMs: number, sessionId: string, flowType: string): Transcript {
  const transcript = readTranscript("chat_ready_then_exit");
  const [launch, ...rest] = transcript.steps;
  const message = launch?.message as { session: Record<string, unknown> };
  const ignore = ((launch as { ignore?: string[] }).ignore ?? []).filter(
    (path) => path !== "session.session_id" && path !== "session.flow_type",
  );
  const session = { flow_type: flowType, session_id: sessionId, is_resumed: true };
  const resumed = { ...launch, ignore, message: { ...message, session } };
  const readyIndex = rest.findIndex((step) => (step.message as { type?: string } | undefined)?.type === "ready");
  const steps = [...rest];
  steps.splice(readyIndex + 1, 0, { pause_ms: pauseMs, role: "tui" });
  return { ...transcript, steps: [resumed, ...steps] };
}

/** Returns the tab-separated rows of a `sessions` listing by session ID. */
function rowsByIdIn(output: string): Map<string, string[]> {
  const rows = new Map<string, string[]>();
  for (const line of output.split("\n")) {
    const columns = line.split("\t");
    if (columns.length >= 7 && columns[0] !== undefined) {
      rows.set(columns[0], columns);
    }
  }
  return rows;
}

