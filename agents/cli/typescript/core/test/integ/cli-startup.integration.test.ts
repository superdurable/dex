// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { createServer } from "node:http";
import { PassThrough } from "node:stream";
import { setTimeout as delay } from "node:timers/promises";
import { after, afterEach, before, describe, test } from "node:test";

import { findFreeLoopbackPorts, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { parseConfigYaml, readCliEnvironment, readStartedDexcliDev, runAgentCli } from "../../src/index.js";
import { CliProcess, TestHome, TextCollector, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { PseudoTerminalCliProcess } from "../fixtures/pseudo-terminal-cli-process.js";
import { fixedToolSetScriptedHarness, scriptedHarness } from "../fixtures/scripted-harness.js";

describe("CLI startup: configuration, the Dex server, and connectors", () => {
  let engine: PrivateDexEngine;
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  const processes: (CliProcess | PseudoTerminalCliProcess)[] = [];
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

  function startAtTerminal(home: TestHome, commandArguments: readonly string[] = []): PseudoTerminalCliProcess {
    const cli = PseudoTerminalCliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  test("Ctrl-C at the first init question exits with 130 and writes no file", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = startAtTerminal(home, ["init"]);
    await cli.terminal.waitFor("Model [scripted/default]: ");
    cli.type("\x03");
    assert.equal(await cli.waitForExit(), 130);
    assert.match(cli.terminal.all, /Cancelled; no configuration written\./);
    assert.equal(home.hasConfig(), false);
  });

  test("Ctrl-C at a later question of the first-run setup exits with 130 and writes no file", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = startAtTerminal(home);
    await cli.terminal.waitFor("Model [scripted/default]: ");
    cli.type("\r");
    await cli.terminal.waitFor("System prompt");
    cli.type("Answer briefly.\r");
    await cli.terminal.waitFor("Connectors");
    cli.type("\x03");
    assert.equal(await cli.waitForExit(), 130);
    assert.match(cli.terminal.all, /Cancelled; no configuration written\./);
    assert.doesNotMatch(cli.terminal.all, /Type a message/);
    assert.equal(home.hasConfig(), false);
  });

  test("after the first-run setup at a terminal, the chat reads a message typed with the last answer", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = startAtTerminal(home);
    for (const question of ["Model", "System prompt", "Connectors", "Workspace"]) {
      await cli.terminal.waitFor(question);
      cli.type("\r");
    }
    await cli.terminal.waitFor("Local tools");
    cli.type("\rhello\r");
    await cli.terminal.waitFor(/Session \S+\. Type a message/);
    assert.equal(home.hasConfig(), true);
    await cli.terminal.waitFor("echo: hello");
    cli.type("again\r");
    await cli.terminal.waitFor("echo: again");
    cli.type("/exit\r");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("Ctrl-D at an init question exits with 1 and writes no file", async () => {
    const home = new TestHome(enginePorts(engine));
    const cli = startAtTerminal(home, ["init"]);
    await cli.terminal.waitFor("Model [scripted/default]: ");
    cli.type("\r");
    await cli.terminal.waitFor("System prompt");
    cli.type("\x04");
    assert.equal(await cli.waitForExit(), 1);
    assert.match(cli.terminal.all, /Cancelled; no configuration written\./);
    assert.equal(home.hasConfig(), false);
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

  test("an unavailable connectors server prints one short line across turns, and its errors go to the log file", async () => {
    // The fake answers initialize but no listing, so the explicit URL only warns.
    connectors.listingFailureReason = "the catalog is not ready";
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(`connectors: {mcp: "${connectorsUrl}"}\nlocal_tools: {browser: false}\n`);
    const cli = start(home);
    try {
      await cli.waitForSession();
      cli.writeLine("hello");
      await cli.stdout.waitFor("echo: hello");
      cli.writeLine("again");
      await cli.stdout.waitFor("echo: again");
      cli.writeLine("/exit");
      assert.equal(await cli.waitForExit(), 0);
    } finally {
      connectors.listingFailureReason = undefined;
    }

    const logPath = join(home.directory, ".dex", "agents", "scripted.log");
    const connectorLines = cli.stderr.all.split("\n").filter((line) => /Connector tools|MCP server|tool listing/i.test(line));
    assert.equal(connectorLines.length, 1, cli.stderr.all);
    assert.match(
      connectorLines[0] ?? "",
      /^Connector tools are unavailable for now; each turn tries again\. See (.+) for the error and http:\/\/\S+\/v1\/connectors for connections\.$/,
    );
    assert.ok(connectorLines[0]?.includes(logPath), connectorLines[0]);
    const log = readFileSync(logPath, "utf8");
    assert.match(log, /connectors MCP server \S+ is unavailable: /);
    assert.match(log, /left MCP server connectors out of the tool listing: /);
  });

  test("a harness that fixes its tools at build says a restart brings connector tools back", async () => {
    const home = new TestHome(enginePorts(engine), fixedToolSetScriptedHarness);
    const cli = start(home);
    await cli.waitForSession();
    await cli.stderr.waitFor(
      "dex-agents-scripted fixes its tools when it starts, so this run has no connector tools. " +
        "Restart it once the connectors MCP server answers.",
    );
    assert.doesNotMatch(cli.stderr.all, /each turn tries again/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a harness that fixes its tools at build warns and continues when the startup catalog read fails", async () => {
    connectors.listingFailureReason = "the catalog is not ready";
    try {
      const home = new TestHome(enginePorts(engine), fixedToolSetScriptedHarness);
      home.writeConfig(`connectors: {mcp: "${connectorsUrl}"}\n`);
      const cli = start(home);
      await cli.waitForSession();
      await cli.stderr.waitFor("The connectors MCP server is unavailable. See ");
      await cli.stderr.waitFor(
        "dex-agents-scripted fixes its tools when it starts, so this run has no connector tools. " +
          "Restart it once the connectors MCP server answers.",
      );
      cli.writeLine("/exit");
      assert.equal(await cli.waitForExit(), 0);
    } finally {
      connectors.listingFailureReason = undefined;
    }
  });

  test("a harness that fixes its tools at build prints one line when its first listing fails", async () => {
    // The startup catalog read lists; the listing that fixes the tools then fails.
    connectors.listingFailureReason = "the catalog is not ready";
    connectors.listingsBeforeFailure = 1;
    try {
      const home = new TestHome(enginePorts(engine), fixedToolSetScriptedHarness);
      home.writeConfig(`connectors: {mcp: "${connectorsUrl}"}\nlocal_tools: {browser: false}\n`);
      const cli = start(home);
      await cli.waitForSession();
      cli.writeLine("hello");
      await cli.stdout.waitFor("echo: hello");
      cli.writeLine("/exit");
      assert.equal(await cli.waitForExit(), 0);
      const connectorLines = (cli.stdout.all + cli.stderr.all)
        .split("\n")
        .filter((line) => /Connector tools|MCP server|tool listing/i.test(line));
      assert.equal(connectorLines.length, 1, cli.stderr.all);
      assert.match(connectorLines[0] ?? "", /^The connectors MCP server is unavailable\. See .* Restart it once the connectors MCP server answers\.$/);
    } finally {
      connectors.listingFailureReason = undefined;
      connectors.listingsBeforeFailure = 0;
    }
  });

  test("a server that answers initialize but fails its listing leaves connector tools unavailable, and the chat runs", async () => {
    connectors.listingFailureReason = "the catalog is not ready";
    try {
      const home = new TestHome(enginePorts(engine));
      home.writeConfig(`connectors: {mcp: "${connectorsUrl}"}\nlocal_tools: {browser: false}\n`);
      const cli = start(home);
      await cli.waitForSession();
      cli.writeLine("hello");
      await cli.stdout.waitFor("echo: hello");
      cli.writeLine("/exit");
      assert.equal(await cli.waitForExit(), 0);
      const connectorLines = cli.stderr.all.split("\n").filter((line) => line.includes("Connector tools are unavailable"));
      assert.equal(connectorLines.length, 1, cli.stderr.all);
      assert.doesNotMatch(cli.stderr.all, /the catalog is not ready/);
      const logFile = readFileSync(join(home.directory, ".dex", "agents", "scripted.log"), "utf8");
      assert.match(logFile, /connectors MCP server \S+ could not be listed: .*the catalog is not ready/);
    } finally {
      connectors.listingFailureReason = undefined;
    }
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

  test("at a terminal, an unauthorized connection opens the Connectors page unless DEX_AGENTS_OPEN_BROWSER is 0", async () => {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(
      `connectors:\n  mcp: "${connectorsUrl}"\n  enabled: [hackernews, gmail/work]\nlocal_tools: {browser: false}\n`,
    );
    // A fake open and xdg-open on PATH record the URL instead of opening a real browser.
    const fakeBinDirectory = join(home.directory, "fake-bin");
    const openedUrlsPath = join(home.directory, "opened-urls.txt");
    mkdirSync(fakeBinDirectory);
    for (const name of ["open", "xdg-open"]) {
      writeFileSync(join(fakeBinDirectory, name), `#!/bin/sh\necho "$1" >> '${openedUrlsPath}'\n`, { mode: 0o755 });
    }
    const runInteractive = async (environment: NodeJS.ProcessEnv): Promise<string> => {
      const stdin = new PassThrough();
      const stdout = new PassThrough();
      const stderr = new PassThrough();
      const output = new TextCollector(stdout);
      const errors = new TextCollector(stderr);
      const run = runAgentCli(scriptedHarness, { argv: [], stdin, stdout, stderr, isInteractive: true, environment });
      await output.waitFor(/Session \S+\. Type a message/);
      stdin.write("/exit\n");
      assert.equal(await run, 0);
      return errors.all;
    };
    const originalPath = process.env.PATH;
    process.env.PATH = `${fakeBinDirectory}:${originalPath ?? ""}`;
    try {
      const suppressed = await runInteractive(home.environment());
      assert.match(suppressed, /Not authorized yet: gmail\/work\. Add them with "\+ Add" on http:\/\/127\.0\.0\.1:\d+\/v1\/connectors/);
      await delay(1_000);
      assert.equal(existsSync(openedUrlsPath), false);

      const opening = home.environment();
      delete opening.DEX_AGENTS_OPEN_BROWSER;
      await runInteractive(opening);
      for (let attempt = 0; attempt < 100 && !existsSync(openedUrlsPath); attempt += 1) {
        await delay(100);
      }
      assert.match(readFileSync(openedUrlsPath, "utf8").trim(), /^http:\/\/127\.0\.0\.1:\d+\/v1\/connectors$/);
    } finally {
      process.env.PATH = originalPath;
    }
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
