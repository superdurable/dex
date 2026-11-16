// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Session forks from the command line: fork then open on an engine; the twin of the Python CLI core's
// tests/test_session_forks.py.

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { sessionTaskQueue } from "../../src/index.js";
import { forkUsageError, openPrompt, resumeHint } from "../../src/session-forks.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";

const noConnectorsConfig = "connectors: {enabled: []}\n";
const commandName = "dex-agents-scripted";

describe("session forks", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
  });

  afterEach(async () => {
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
    home.writeConfig(noConnectorsConfig);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  async function readFork(
    home: TestHome,
    sessionId: string,
  ): Promise<{ nextMessageSequence: number | undefined; hasLease: boolean; taskQueue: string }> {
    const sessions = await sessionClients.of(home);
    const [state, lease] = await sessions.client.getAttributes(
      sessionId,
      sessions.flow.agentState,
      sessions.flow.sessionTerminalLease,
    );
    const flowState = await sessions.client.getFlowState(sessionId);
    return { nextMessageSequence: state?.nextMessageSequence, hasLease: lease !== undefined, taskQueue: flowState.taskQueue };
  }

  test("/fork in the chat opens the fork, and the source keeps working", async () => {
    const home = newHome();
    const chat = start(home);
    const sourceId = await chat.waitForSession();
    chat.writeLine("first");
    await chat.stdout.waitFor("echo: first");
    chat.writeLine("second");
    await chat.stdout.waitFor("echo: second");
    chat.writeLine("/fork 1");
    await chat.stdout.waitFor(`[not forked] message 1 of session ${sourceId} is inside a turn`);

    chat.writeLine("/fork 2");
    const forked = await chat.stdout.waitFor(/Forked session \S+ after message 2 into session \S+\./);
    const forkId = /into session (\S+)\.$/.exec(forked)?.[1] ?? "";
    await chat.stdout.waitFor(openPrompt(forkId));
    chat.writeLine("y");
    assert.equal(await chat.waitForSession(), forkId);
    chat.writeLine("third");
    await chat.stdout.waitFor("echo: third");
    chat.writeLine("/exit");
    assert.equal(await chat.waitForExit(), 0);

    assert.deepEqual(await readFork(home, forkId), {
      nextMessageSequence: 5,
      hasLease: false,
      taskQueue: sessionTaskQueue(forkId),
    });

    const resumed = start(home, ["--resume", sourceId]);
    assert.equal(await resumed.waitForSession(), sourceId);
    resumed.writeLine("fourth");
    await resumed.stdout.waitFor("echo: fourth");
    resumed.writeLine("/exit");
    assert.equal(await resumed.waitForExit(), 0);
    assert.equal((await readFork(home, sourceId)).nextMessageSequence, 7);
  });

  test("the fork subcommand says how to open the fork, or opens it at a terminal", async () => {
    const home = newHome();
    const chat = start(home);
    const sourceId = await chat.waitForSession();
    chat.writeLine("first");
    await chat.stdout.waitFor("echo: first");
    chat.writeLine("/exit");
    assert.equal(await chat.waitForExit(), 0);

    const fork = start(home, ["fork", sourceId]);
    assert.equal(await fork.waitForExit(), 0, fork.stderr.all);
    const forkId = new RegExp(`Forked session ${sourceId} after message 2 into session (\\S+)\\.`).exec(fork.stdout.all)?.[1];
    assert.ok(forkId !== undefined, fork.stdout.all);
    assert.ok(fork.stdout.all.includes(resumeHint(commandName, forkId)), fork.stdout.all);
    assert.deepEqual(await readFork(home, forkId), {
      nextMessageSequence: 3,
      hasLease: false,
      taskQueue: sessionTaskQueue(forkId),
    });

    const refused = start(home, ["fork", sourceId, "--at", "1"]);
    assert.equal(await refused.waitForExit(), 1);
    assert.ok(refused.stderr.all.includes("is inside a turn"), refused.stderr.all);

    const missing = start(home, ["fork"]);
    assert.equal(await missing.waitForExit(), 2);
    assert.ok(missing.stderr.all.includes(forkUsageError(commandName)), missing.stderr.all);

    const opening = CliProcess.startInTerminal(home, ["fork", sourceId]);
    processes.push(opening);
    const forkedLine = await opening.stdout.waitFor(
      new RegExp(`Forked session ${sourceId} after message 2 into session \\S+\\.`),
    );
    const openedId = /into session (\S+)\.$/.exec(forkedLine)?.[1] ?? "";
    await opening.stdout.waitFor(openPrompt(openedId));
    opening.writeLine("");
    assert.equal(await opening.waitForSession(), openedId);
    opening.writeLine("second");
    await opening.stdout.waitFor("echo: second");
    opening.writeLine("/exit");
    assert.equal(await opening.waitForExit(), 0);
    assert.deepEqual(await readFork(home, openedId), {
      nextMessageSequence: 5,
      hasLease: false,
      taskQueue: sessionTaskQueue(openedId),
    });
  });
});
