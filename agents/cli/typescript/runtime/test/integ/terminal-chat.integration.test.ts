// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ConnectorsMcpToolSource, TurnConfigResolver, resolveConfig } from "../../src/index.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";

describe("terminal chat with connectors and session settings", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
    connectors = new FakeConnectorsMcpServer();
    connectorsUrl = await connectors.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    await connectors?.stop();
    await sessionClients?.close();
    await engine?.stop();
  });

  function newHome(config: string): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  const connectorConfig = (extra = ""): string =>
    [
      "connectors:",
      `  mcp: ${connectorsUrl}`,
      "  enabled: [slack/support-slack, hackernews]",
      "local_tools: {browser: false}",
      extra,
    ].join("\n");

  test("one chat turn streams the reply, reusing the running dexcli dev", async () => {
    const cli = start(newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n"));
    await cli.waitForSession();
    cli.writeLine("hello there");
    await cli.stdout.waitFor("echo: hello there");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stdout.all, /keeps running\. Resume it with: dex-agents-scripted --resume /);
    assert.doesNotMatch(cli.stderr.all, /Starting dexcli dev/);
  });

  test("an approved connector write runs once with an idempotency key, and a rejected one never runs", async () => {
    const cli = start(newHome(connectorConfig()));
    await cli.waitForSession();
    cli.writeLine('call slack_post_thread_reply {"text":"approved reply"}');
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    cli.writeLine("y");
    await cli.stdout.waitFor("result: posted approved reply");
    const runs = connectors.runsOf("slack_post_thread_reply").filter((run) => run.arguments.text === "approved reply");
    assert.equal(runs.length, 1);
    assert.notEqual(runs[0]?.idempotencyKey, undefined);

    cli.writeLine('call slack_post_thread_reply {"text":"rejected reply"}');
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    cli.writeLine("n not today");
    await cli.stdout.waitFor(/result: .*not today/);
    assert.equal(connectors.runsOf("slack_post_thread_reply").filter((run) => run.arguments.text === "rejected reply").length, 0);

    cli.writeLine('call hackernews_get_top_stories {}');
    await cli.stdout.waitFor("result: top stories: dex-1, dex-2");
    assert.doesNotMatch(cli.stdout.all, /Approve hackernews_get_top_stories/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("approval.always makes a read tool wait for approval", async () => {
    const cli = start(newHome(connectorConfig("approval: {always: [hackernews_get_top_stories]}")));
    await cli.waitForSession();
    cli.writeLine("call hackernews_get_top_stories {}");
    await cli.stdout.waitFor("Approve hackernews_get_top_stories?");
    cli.writeLine("yes");
    await cli.stdout.waitFor("result: top stories");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("Ctrl-C cancels the running turn, the session stays, and a second Ctrl-C exits", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("wait slow-model");
    await pollUntil(() => existsSync(join(home.markerDirectory, "slow-model.reached")) || undefined, {
      description: "the model call to start",
    });
    cli.interrupt();
    await cli.stdout.waitFor("Cancelling the turn");
    await cli.stdout.waitFor("Turn cancelled.");
    cli.writeLine("still here");
    await cli.stdout.waitFor("echo: still here\n\n> ");
    cli.interrupt();
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stdout.all, new RegExp(`--resume ${sessionId}`));
    writeFileSync(join(home.markerDirectory, "slow-model.release"), "");
    const state = await (await sessionClients.of(home)).readAgentState(sessionId);
    assert.equal(state?.status, "waiting_for_user_message");
  });

  test("--resume continues the conversation after the command exited", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const first = start(home);
    const sessionId = await first.waitForSession();
    first.writeLine("remember teal");
    await first.stdout.waitFor("echo: remember teal");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor(`Resumed session ${sessionId}`);
    second.writeLine("history");
    await second.stdout.waitFor("history: remember teal | history");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);

    const missing = start(home, ["--resume", "scripted-no-such-session"]);
    assert.equal(await missing.waitForExit(), 2);
    assert.match(missing.stderr.all, /does not exist/);
  });

  test("session settings narrow tools and change the model; widening fails the turn and the exit code", async () => {
    const home = newHome(connectorConfig());
    const sessions = await sessionClients.of(home);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("tools");
    await cli.stdout.waitFor(/tools: .*hackernews_get_top_stories.*slack_post_thread_reply/);

    assert.equal(
      await sessions.updateSessionSettings(sessionId, {
        model: "scripted/other",
        connectors: { enabled: ["hackernews"] },
        local_tools: { shell: { enabled: false } },
        persona: "kept for another reader",
      }),
      true,
    );
    cli.writeLine("tools");
    const narrowed = await cli.stdout.waitFor(/tools: [^\n]*/);
    assert.match(narrowed, /hackernews_get_top_stories/);
    assert.doesNotMatch(narrowed, /slack_post_thread_reply|gmail_get_message|run_shell_command/);
    await cli.stderr.waitFor("Session settings keys ignored: /persona");
    cli.writeLine("model");
    await cli.stdout.waitFor("model: scripted/other");

    assert.equal(await sessions.updateSessionSettings(sessionId, { connectors: { enabled: ["gmail/personal"] } }), true);
    cli.writeLine("tools");
    await cli.stdout.waitFor('Turn failed: widens_connectors at "/connectors/enabled/0"', 120_000);
    assert.equal(connectors.runsOf("gmail_get_message").length, 0);

    assert.equal(await sessions.updateSessionSettings(sessionId, {}), true);
    cli.writeLine("model");
    await cli.stdout.waitFor("model: scripted/default");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 1);
  });

  test("an operation limit needs the tool's operation keys in _meta", async () => {
    const cli = start(
      newHome(
        [
          "connectors:",
          `  mcp: ${connectorsUrl}`,
          "  enabled:",
          "    - gmail/personal: {tools: [get_message, send_message]}",
          "local_tools: {browser: false}",
        ].join("\n"),
      ),
    );
    await cli.waitForSession();
    cli.writeLine("tools");
    const tools = await cli.stdout.waitFor(/tools: [^\n]*/);
    assert.match(tools, /gmail_get_message/);
    assert.doesNotMatch(tools, /gmail_send_message|hackernews|slack/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("an unavailable connectors server offers no tools, and its tools return in a later listing", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    const source = new ConnectorsMcpToolSource(connectorsUrl, new TurnConfigResolver(effective, null, () => undefined), (line) =>
      lines.push(line),
    );
    connectors.unavailableReason = "the connectors MCP server is starting";
    try {
      assert.deepEqual(await source.listTools(), []);
      assert.match(lines.join("\n"), /Connector tools are unavailable for now/);
    } finally {
      connectors.unavailableReason = undefined;
    }
    await pollUntil(async () => ((await source.listTools()).length > 0 ? true : undefined), {
      description: "the connector tools to return",
      timeoutMs: 60_000,
    });
    assert.match(lines.join("\n"), /Connector tools are available again/);
  });

  test("commands with different configurations never run each other's Steps", async () => {
    const config = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
    const homeA = newHome(config);
    const homeB = newHome(config);
    assert.notEqual(homeA.sessionFlowType(), homeB.sessionFlowType());
    const cliA = start(homeA);
    const cliB = start(homeB);
    const sessionA = await cliA.waitForSession();
    await cliB.waitForSession();
    for (let index = 0; index < 4; index += 1) {
      cliA.writeLine(`call write_file {"path":"from-a-${index}.txt","content":"a"}`);
      await cliA.stdout.waitFor("Approve write_file?");
      cliA.writeLine("y");
      await cliA.stdout.waitFor(`result: wrote 1 bytes to from-a-${index}.txt`);
      assert.equal(existsSync(join(homeA.workspace, `from-a-${index}.txt`)), true);
      assert.equal(existsSync(join(homeB.workspace, `from-a-${index}.txt`)), false);
    }
    cliA.writeLine("/exit");
    assert.equal(await cliA.waitForExit(), 0);
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);

    const resumeInB = start(homeB, ["--resume", sessionA]);
    assert.equal(await resumeInB.waitForExit(), 2);
    assert.match(resumeInB.stderr.all, /resume it with the configuration that started it/);
    const sessionsOfB = start(homeB, ["sessions"]);
    assert.equal(await sessionsOfB.waitForExit(), 0);
    assert.doesNotMatch(sessionsOfB.stdout.all, new RegExp(sessionA));
  });
});
