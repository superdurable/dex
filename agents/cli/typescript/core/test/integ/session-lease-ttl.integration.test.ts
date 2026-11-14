// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine, pollUntil } from "@superdurable/dex-agents/testing";
import type { SessionTerminalLease } from "@superdurable/dex-agents";

import { tookOverLine } from "../../src/session-terminal-lease.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import {
  FakeTerminalUi,
  PseudoTerminalCli,
  chatTranscript,
  isReceived,
  type Transcript,
} from "../fixtures/terminal-ui-test-support.js";

const quietConfig = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
const configuredTtlSeconds = 45;
/** The shortest lease the file takes; its renewals every 3 1/3 s show within a test. */
const shortTtlSeconds = 10;

function withLeaseTtl(seconds: string): string {
  return `${quietConfig}sessions: {lease_ttl_seconds: ${seconds}}\n`;
}

/** Returns the relaunch transcript after a switch: the resumed session's launch and `ready`, then a long pause. */
function resumedTranscriptThatHolds(): Transcript {
  const transcript = chatTranscript("chat_resumed_holds_the_session", [{ pause_ms: 300_000, role: "tui" }]);
  const [launch, ...rest] = transcript.steps;
  const message = launch?.message as { session: Record<string, unknown> };
  // A relaunch after a switch named the left session before 0.6.8, which this test does not fix.
  const ignore = [...((launch as { ignore?: string[] }).ignore ?? []), "session.previous_session_id"];
  const resumed = { ...launch, ignore, message: { ...message, session: { ...message.session, is_resumed: true } } };
  return { ...transcript, steps: [resumed, ...rest] };
}

/** Returns how long a lease lasts past its first acquire; a renewal keeps `openedAt` and moves `expiresAt`. */
function leaseSeconds(lease: SessionTerminalLease): number {
  return (Date.parse(lease.expiresAt) - Date.parse(lease.openedAt)) / 1_000;
}

describe("the session lease TTL", () => {
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

  function newHome(config: string): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = [], environment: NodeJS.ProcessEnv = {}): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
  }

  async function waitForLease(home: TestHome, sessionId: string): Promise<SessionTerminalLease> {
    const sessions = await sessionClients.of(home);
    return pollUntil(
      async () => {
        const [lease] = await sessions.client.getAttributes(sessionId, sessions.flow.sessionTerminalLease);
        return lease ?? undefined;
      },
      { timeoutMs: 30_000, description: `a lease of ${sessionId}` },
    );
  }

  async function leaseSecondsOfNewChat(home: TestHome, environment: NodeJS.ProcessEnv = {}): Promise<number> {
    const cli = start(home, [], environment);
    const sessionId = await cli.waitForSession();
    const seconds = leaseSeconds(await waitForLease(home, sessionId));
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    return seconds;
  }

  test("a session lease lasts 600 seconds by default", async () => {
    assert.equal(await leaseSecondsOfNewChat(newHome(quietConfig)), 600);
  });

  test("sessions.lease_ttl_seconds sets the lease", async () => {
    const seconds = await leaseSecondsOfNewChat(newHome(withLeaseTtl(String(configuredTtlSeconds))));
    assert.ok(seconds >= configuredTtlSeconds && seconds < 2 * configuredTtlSeconds, `${seconds} s`);
  });

  test("DEX_AGENTS_SESSION_LEASE_TTL_SECONDS overrides sessions.lease_ttl_seconds", async () => {
    const home = newHome(withLeaseTtl(String(configuredTtlSeconds)));
    assert.equal(await leaseSecondsOfNewChat(home, { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3600" }), 3600);
  });

  test("an invalid DEX_AGENTS_SESSION_LEASE_TTL_SECONDS leaves sessions.lease_ttl_seconds", async () => {
    const home = newHome(withLeaseTtl(String(configuredTtlSeconds)));
    const seconds = await leaseSecondsOfNewChat(home, { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "3601" });
    assert.ok(seconds >= configuredTtlSeconds && seconds < 2 * configuredTtlSeconds, `${seconds} s`);
  });

  test("the terminal UI's Worker host uses sessions.lease_ttl_seconds", async () => {
    const home = newHome(withLeaseTtl(String(configuredTtlSeconds)));
    const fake = new FakeTerminalUi([chatTranscript("chat_holds_the_session", [{ pause_ms: 300_000, role: "tui" }])]);
    const terminal = PseudoTerminalCli.start(home, [], { DEXCLI: fake.executable });
    terminals.push(terminal);
    await pollUntil(() => fake.record().find(isReceived("launch")), {
      timeoutMs: 60_000,
      description: "the terminal UI launch",
    });
    // The fake terminal UI records no session ID, so the sessions subcommand names the one session.
    const listing = start(home, ["sessions"]);
    assert.equal(await listing.waitForExit(), 0, listing.stderr.all);
    const [sessionId] = /scripted-\d{8}-\d{6}-[0-9a-f]{6}/.exec(listing.stdout.all) ?? [];
    assert.ok(sessionId !== undefined, listing.stdout.all);
    const seconds = leaseSeconds(await waitForLease(home, sessionId));
    assert.ok(seconds >= configuredTtlSeconds && seconds < 2 * configuredTtlSeconds, `${seconds} s`);
  });

  /** Waits until renewals kept the lease a whole TTL: expiry minus opening is twice the TTL. */
  async function waitForRenewalsPastTheTtl(home: TestHome, sessionId: string, ttlSeconds: number): Promise<void> {
    const first = await waitForLease(home, sessionId);
    const sessions = await sessionClients.of(home);
    await pollUntil(
      async () => {
        const [lease] = await sessions.client.getAttributes(sessionId, sessions.flow.sessionTerminalLease);
        assert.equal(lease?.holderId, first.holderId);
        return lease !== undefined && leaseSeconds(lease) >= 2 * ttlSeconds ? lease : undefined;
      },
      { timeoutMs: 6 * ttlSeconds * 1_000, description: `renewals of ${sessionId} past one TTL` },
    );
  }

  test("the plain chat renews a short configured lease and keeps running", async () => {
    const home = newHome(withLeaseTtl(String(shortTtlSeconds)));
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    await waitForRenewalsPastTheTtl(home, sessionId, shortTtlSeconds);
    cli.writeLine("still here");
    await cli.stdout.waitFor("echo: still here");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
  });

  test("the terminal UI's Worker host renews a short configured lease", async () => {
    const home = newHome(withLeaseTtl(String(shortTtlSeconds)));
    const fake = new FakeTerminalUi([chatTranscript("chat_holds_the_session", [{ pause_ms: 300_000, role: "tui" }])]);
    const terminal = PseudoTerminalCli.start(home, [], { DEXCLI: fake.executable });
    terminals.push(terminal);
    await pollUntil(() => fake.record().find(isReceived("launch")), { timeoutMs: 60_000, description: "the launch" });
    const listing = start(home, ["sessions"]);
    assert.equal(await listing.waitForExit(), 0, listing.stderr.all);
    const [sessionId] = /scripted-\d{8}-\d{6}-[0-9a-f]{6}/.exec(listing.stdout.all) ?? [];
    assert.ok(sessionId !== undefined, listing.stdout.all);
    await waitForRenewalsPastTheTtl(home, sessionId, shortTtlSeconds);
    // Renewals past the first permit's end mean each one was confirmed, so the chat still runs.
    assert.doesNotMatch(terminal.terminal.all, /could not renew its lease in time/);
  });

  test("a switch_session in the terminal UI takes the chosen session with the configured lease", async () => {
    const home = newHome(withLeaseTtl(String(configuredTtlSeconds)));
    const other = start(home);
    const otherSessionId = await other.waitForSession();
    other.writeLine("/exit");
    assert.equal(await other.waitForExit(), 0, other.stderr.all);
    const switching = chatTranscript("chat_switch_session", [
      { from: "tui", message: { type: "switch_session", session_id: otherSessionId, is_resumed: true } },
    ]);
    const fake = new FakeTerminalUi([switching, resumedTranscriptThatHolds()]);
    const terminal = PseudoTerminalCli.start(home, [], { DEXCLI: fake.executable });
    terminals.push(terminal);

    const lease = await waitForLease(home, otherSessionId);
    const seconds = leaseSeconds(lease);
    assert.ok(seconds >= configuredTtlSeconds && seconds < 2 * configuredTtlSeconds, `${seconds} s`);
  });

  test("a lease TTL outside 10 to 3600 stops the command and names the field", async () => {
    for (const [value, keyword] of [
      ["9", "minimum"],
      ["3601", "maximum"],
      ['"600"', "type"],
    ] as const) {
      const cli = start(newHome(withLeaseTtl(value)));
      assert.equal(await cli.waitForExit(), 2, cli.stderr.all);
      assert.ok(cli.stderr.all.includes(`is invalid: schema at /sessions/lease_ttl_seconds (${keyword})`), cli.stderr.all);
    }
  });

  test("a clean exit hands a 600-second lease to a waiting command at once", async () => {
    const home = newHome(quietConfig);
    const first = start(home);
    const sessionId = await first.waitForSession();
    await waitForLease(home, sessionId);
    const second = start(home, ["--plain", "--resume", sessionId]);
    await pollUntil(() => second.stderr.all.includes("is open in another terminal (") || undefined, {
      timeoutMs: 30_000,
      description: "the second command waits",
    });

    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    const exitedAt = Date.now();
    // The waiting command asks every second, however long the lease lasts.
    await pollUntil(() => second.stderr.all.includes(tookOverLine(sessionId)) || undefined, {
      timeoutMs: 10_000,
      description: "the second command takes over",
    });
    assert.ok(Date.now() - exitedAt < 5_000, "the release lets the waiting command in at once");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0, second.stderr.all);
  });
});
