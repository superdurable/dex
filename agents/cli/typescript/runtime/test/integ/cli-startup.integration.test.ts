// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { createServer } from "node:http";
import { PassThrough } from "node:stream";
import { after, afterEach, before, describe, test } from "node:test";

import { findFreeLoopbackPorts, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { parseConfigYaml, readCliEnvironment, readStartedDexcliDev, runAgentCli } from "../../src/index.js";
import { CliProcess, TestHome, TextCollector, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { scriptedHarness } from "../fixtures/scripted-harness.js";

describe("CLI startup: configuration, the Dex server, and connectors", () => {
  let engine: PrivateDexEngine;
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  const processes: CliProcess[] = [];
  const startedServers: number[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    connectors = new FakeConnectorsMcpServer();
    connectorsUrl = await connectors.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    for (const pid of startedServers) {
      try {
        process.kill(-pid, "SIGKILL");
      } catch {
        // Already gone.
      }
    }
    await connectors?.stop();
    await engine?.stop();
  });

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  test("at a terminal without a configuration, init asks its questions, writes the file, and the chat starts", async () => {
    const home = new TestHome(enginePorts(engine));
    const stdin = new PassThrough();
    const stdout = new PassThrough();
    const stderr = new PassThrough();
    const output = new TextCollector(stdout);
    const errors = new TextCollector(stderr);
    const run = runAgentCli(scriptedHarness, {
      argv: [],
      stdin,
      stdout,
      stderr,
      isInteractive: true,
      environment: home.environment(),
    });
    await output.waitFor("Model [scripted/default]: ");
    stdin.write("scripted/custom\n");
    await output.waitFor("System prompt");
    stdin.write("Answer briefly.\n");
    await output.waitFor("Connectors");
    stdin.write("hackernews\n");
    await output.waitFor("Workspace");
    stdin.write("\n");
    await output.waitFor("Local tools");
    stdin.write("files, shell\n");
    await output.waitFor(/Session \S+\. Type a message/);
    assert.match(errors.all, /Put provider API keys in environment variables/);
    assert.deepEqual(parseConfigYaml(readFileSync(home.configPath, "utf8")), {
      model: "scripted/custom",
      system_prompt: "Answer briefly.",
      dex: { server: "auto" },
      connectors: { mcp: "auto", enabled: ["hackernews"] },
      workspace: "~/dex-agent-workspace",
      local_tools: { files: true, shell: { enabled: true, allow: [] }, web_fetch: false, browser: false },
      media: { store: "auto" },
      builtin: {},
    });
    stdin.write("model\n");
    await output.waitFor("model: scripted/custom");
    stdin.write("instructions\n");
    await output.waitFor("instructions: Answer briefly.");
    stdin.write("tools\n");
    const tools = await output.waitFor(/tools: [^\n]*/);
    assert.match(tools, /read_file/);
    assert.doesNotMatch(tools, /fetch_web_page|browser_navigate/);
    stdin.write("/exit\n");
    assert.equal(await run, 0);
  });

  test("without a terminal or a configuration, the command runs on the defaults and writes no file", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = start(home);
    await cli.waitForSession();
    await cli.stderr.waitFor("running with the defaults");
    await cli.stderr.waitFor("Connector tools are unavailable for now; each turn tries again");
    assert.equal(home.hasConfig(), false);
    cli.writeLine("hello");
    await cli.stdout.waitFor("echo: hello");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("--yes writes the default configuration and init --yes writes it again", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = start(home, ["--yes"]);
    await cli.waitForSession();
    assert.equal(home.hasConfig(), true);
    const written = parseConfigYaml(readFileSync(home.configPath, "utf8"));
    assert.deepEqual(written, {
      model: "scripted/default",
      dex: { server: "auto" },
      connectors: { mcp: "auto" },
      workspace: "~/dex-agent-workspace",
      local_tools: { files: true, shell: { enabled: true, allow: [] }, web_fetch: true, browser: true },
      media: { store: "auto" },
      builtin: {},
    });
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    home.writeConfig("model: scripted/old\n");
    const init = start(home, ["init", "--yes"]);
    assert.equal(await init.waitForExit(), 0);
    assert.deepEqual(parseConfigYaml(readFileSync(home.configPath, "utf8")), written);
  });

  test("an invalid configuration file stops the command with its error code and JSON Pointer", async () => {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig("local_tools:\n  files: yes\n");
    const cli = start(home);
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /is invalid: schema at \/local_tools\/files \(type\)/);
  });

  test("dex.server auto starts dexcli dev when none runs, and the next command reuses it", async () => {
    const [dexPort, webPort] = await findFreeLoopbackPorts(2);
    const home = new TestHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 });
    home.writeConfig("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const first = start(home);
    await first.stderr.waitFor("Starting dexcli dev", 30_000);
    await first.waitForSession();
    const record = readStartedDexcliDev(readCliEnvironment(home.environment()));
    assert.ok(record !== undefined);
    startedServers.push(record.pid);
    assert.deepEqual([record.dexPort, record.webPort], [dexPort, webPort]);
    first.writeLine("hello");
    await first.stdout.waitFor("echo: hello");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const second = start(home);
    await second.waitForSession();
    assert.doesNotMatch(second.stderr.all, /Starting dexcli dev/);
    second.writeLine("hello again");
    await second.stdout.waitFor("echo: hello again");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("dex.server auto refuses a server on its ports that is not a dexcli dev", async () => {
    const [dexPort, webPort] = await findFreeLoopbackPorts(2);
    let definitionCount = 0;
    const impostor = createServer((request, response) => {
      if (request.url === "/readyz") {
        response
          .writeHead(200, { "content-type": "application/json; charset=utf-8", etag: JSON.stringify("sha256:r1") })
          .end(JSON.stringify({ status: "ready", definitionRevision: "sha256:r1", source: "local", definitionCount }));
        return;
      }
      response.writeHead(404).end();
    });
    await new Promise<void>((resolve) => impostor.listen(webPort, "127.0.0.1", resolve));
    try {
      const home = new TestHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 });
      home.writeConfig("connectors: {enabled: []}\n");
      // A stale record whose pid is alive does not make the impostor trusted.
      writeFileSync(
        join(home.directory, ".dex", "agents", "dexcli-dev.json"),
        JSON.stringify({ pid: process.pid, dexPort, webPort, logPath: "unused" }),
      );
      const cli = start(home);
      assert.equal(await cli.waitForExit(), 1);
      assert.match(cli.stderr.all, /answers, but it is not a dexcli dev \(127\.0\.0\.1:\d+ does not answer as a Dex server/);
      assert.doesNotMatch(cli.stderr.all, /Starting dexcli dev/);
      assert.doesNotMatch(cli.stdout.all, /Session /);

      definitionCount = -1;
      const wrongValues = start(home);
      assert.equal(await wrongValues.waitForExit(), 1);
      assert.match(wrongValues.stderr.all, /its \/readyz does not answer with Dex Web's readiness JSON and entity tag/);
    } finally {
      impostor.closeAllConnections();
      impostor.close();
    }
  });

  test("an unreachable or unready connectors.mcp URL stops the command with the reason", async () => {
    const [closedPort] = await findFreeLoopbackPorts(1);
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(`connectors: {mcp: "http://127.0.0.1:${closedPort}/mcp"}\n`);
    const unreachable = start(home);
    assert.equal(await unreachable.waitForExit(), 1);
    assert.match(unreachable.stderr.all, /connectors MCP server is unavailable: nothing answers at/);

    connectors.unavailableReason = "connectors MCP server is starting";
    try {
      home.writeConfig(`connectors: {mcp: "${connectorsUrl}"}\n`);
      const unready = start(home);
      assert.equal(await unready.waitForExit(), 1);
      assert.match(unready.stderr.all, /answered HTTP 503: connectors MCP server is starting/);
    } finally {
      connectors.unavailableReason = undefined;
    }
  });

  test("the connectors subcommand lists authorized connections and the configured ones that are not", async () => {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(`connectors:\n  mcp: "${connectorsUrl}"\n  enabled: [hackernews, gmail/work]\n`);
    const cli = start(home, ["connectors"]);
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stdout.all, /hackernews\/default: get_top_stories/);
    assert.match(cli.stdout.all, /slack\/support-slack \(not enabled in the configuration\): post_thread_reply/);
    assert.match(cli.stdout.all, /Configured but not authorized: gmail\/work/);
    assert.match(cli.stderr.all, /Add them with "\+ Add" on http:\/\/127\.0\.0\.1:\d+\/v1\/connectors/);
  });

  test("the sessions subcommand lists the command's sessions", async () => {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const chat = start(home);
    const sessionId = await chat.waitForSession();
    chat.writeLine("/exit");
    assert.equal(await chat.waitForExit(), 0);
    const list = start(home, ["sessions"]);
    assert.equal(await list.waitForExit(), 0);
    assert.match(list.stdout.all, new RegExp(`${sessionId}\\trunning`));
  });
});
