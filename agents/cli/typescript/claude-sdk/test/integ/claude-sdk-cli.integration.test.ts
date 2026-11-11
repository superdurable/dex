// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import {
  CliProcess,
  RefusingProxy,
  TestHome,
  enginePorts,
  listFiles,
  mockApiKey,
  openSessionClient,
} from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { MockMessagesApi } from "../fixtures/mock-messages-api.js";

const noConnectors = { connectors: { enabled: [] }, local_tools: { browser: false } };

function call(toolName: string, toolInput: Readonly<Record<string, unknown>>): string {
  return `call ${toolName} ${JSON.stringify(toolInput)}`;
}

describe("dex-agents-claude-sdk with the bundled Claude Code CLI against a scripted Messages API", () => {
  let engine: PrivateDexEngine;
  let api: MockMessagesApi;
  let proxy: RefusingProxy;
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    api = await MockMessagesApi.start();
    proxy = await RefusingProxy.start();
    connectors = new FakeConnectorsMcpServer();
    connectorsUrl = await connectors.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
    api.requests.splice(0);
  });

  after(async () => {
    await connectors?.stop();
    await proxy?.stop();
    await api?.stop();
    await engine?.stop();
  });

  function newHome(config: Readonly<Record<string, unknown>>): TestHome {
    const home = new TestHome(enginePorts(engine), api, proxy.url);
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  function offeredToolNames(): string[] {
    const tools = (api.modelRequests().at(-1)?.["tools"] as Record<string, unknown>[] | undefined) ?? [];
    return tools.map((tool) => String(tool["name"]));
  }

  test("a turn streams the answer and --resume continues the session", async () => {
    const home = newHome(noConnectors);
    const first = start(home);
    const sessionId = await first.waitForSession();
    first.writeLine("say hello");
    await first.stdout.waitFor("said: hello");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const second = start(home, ["--resume", sessionId]);
    assert.equal(await second.waitForSession(), sessionId);
    second.writeLine("recall");
    await second.stdout.waitFor("history: say hello");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(api.modelRequests()[0]?.["model"], "claude-opus-5-5");
    assert.deepEqual([...new Set(api.requests.map((request) => request.apiKey))], [mockApiKey]);
  });

  test("a connector read runs at once and a write runs once after approval", async () => {
    const cli = start(newHome({ connectors: { mcp: connectorsUrl }, local_tools: { browser: false } }));
    await cli.waitForSession();
    cli.writeLine(call("hackernews_get_top_stories", {}));
    await cli.stdout.waitFor("hackernews_get_top_stories: 1. Durable agents; 2. Local first");
    assert.equal(connectors.runCount("hackernews_get_top_stories"), 1);

    cli.writeLine(call("slack_post_thread_reply", { thread: "T1", text: "on it" }));
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    assert.equal(connectors.runCount("slack_post_thread_reply"), 0);
    cli.writeLine("y");
    await cli.stdout.waitFor("slack_post_thread_reply: posted on it in T1");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(connectors.runCount("slack_post_thread_reply"), 1);
  });

  test("Read runs at once, and Write and Bash run after approval", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("Read", { file_path: join(home.workspace, "notes.txt") }));
    await cli.stdout.waitFor("buy milk");
    assert.doesNotMatch(cli.stdout.all, /Approve Read/);
    for (const toolName of ["Read", "Grep", "Glob", "Write", "Edit", "Bash", "WebFetch"]) {
      assert.ok(offeredToolNames().includes(toolName), `${toolName} is offered`);
    }

    const todoPath = join(home.workspace, "todo.txt");
    cli.writeLine(call("Write", { file_path: todoPath, content: "call mom" }));
    await cli.stdout.waitFor("Approve Write?");
    assert.equal(existsSync(todoPath), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("Write: ");
    assert.equal(readFileSync(todoPath, "utf8"), "call mom");

    cli.writeLine(call("Bash", { command: "echo ran > bash.txt" }));
    await cli.stdout.waitFor("Approve Bash?");
    assert.equal(existsSync(join(home.workspace, "bash.txt")), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("Bash: ");
    assert.equal(readFileSync(join(home.workspace, "bash.txt"), "utf8"), "ran\n");

    cli.writeLine(call("Bash", { command: "echo no > rejected.txt" }));
    await cli.stdout.waitFor("Approve Bash?");
    cli.writeLine("n not now");
    await cli.stdout.waitFor("Bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(existsSync(join(home.workspace, "rejected.txt")), false);
  });

  test("an allow-listed command runs at once in the workspace", async () => {
    const home = newHome({ ...noConnectors, local_tools: { browser: false, shell: { enabled: true, allow: ["pwd -P"] } } });
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("Bash", { command: "pwd -P" }));
    await cli.stdout.waitFor(`Bash: ${realpathSync(home.workspace)}`);
    assert.doesNotMatch(cli.stdout.all, /Approve Bash/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("file tools refuse paths outside the workspace", async () => {
    const outside = mkdtempSync(join(tmpdir(), "dex-agents-claude-sdk-outside-"));
    writeFileSync(join(outside, "secret.txt"), "do not read");
    const cli = start(newHome(noConnectors));
    await cli.waitForSession();
    cli.writeLine(call("Read", { file_path: join(outside, "secret.txt") }));
    await cli.stdout.waitFor("Read may only use paths inside the workspace");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
  });

  test("session settings that switch the shell off remove Bash", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    const { environment, sessions } = await openSessionClient(engine, home.sessionFlowType());
    try {
      await sessions.updateSessionSettings(sessionId, { local_tools: { shell: { enabled: false } } });
    } finally {
      await environment.close();
    }
    cli.writeLine(call("Bash", { command: "echo ran > bash.txt" }));
    await cli.stdout.waitFor("Bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.ok(!offeredToolNames().includes("Bash"));
    assert.ok(offeredToolNames().includes("Read"));
    assert.equal(existsSync(join(home.workspace, "bash.txt")), false);
  });

  test("with a key the Claude Code CLI uses it, and the command never writes it down", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("say keyed");
    await cli.stdout.waitFor("said: keyed");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.deepEqual([...new Set(api.requests.map((request) => request.apiKey))], [mockApiKey]);
    assert.doesNotMatch(cli.stdout.all + cli.stderr.all, new RegExp(mockApiKey));
    assert.doesNotMatch(home.stateText(), new RegExp(mockApiKey));
  });

  test("without a key the environment and CLAUDE_CONFIG_DIR pass through unchanged", async () => {
    const home = newHome(noConnectors);
    delete home.claudeEnvironment["ANTHROPIC_API_KEY"];
    home.claudeEnvironment["ANTHROPIC_AUTH_TOKEN"] = "mock-login-token";
    const claudeConfigDirectory = join(home.directory, "claude-config");
    home.claudeEnvironment["CLAUDE_CONFIG_DIR"] = claudeConfigDirectory;
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("say token");
    await cli.stdout.waitFor("said: token");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.deepEqual([...new Set(api.requests.map((request) => request.apiKey))], [undefined]);
    assert.deepEqual([...new Set(api.requests.map((request) => request.authorization))], ["Bearer mock-login-token"]);
    // The first segment writes Claude Code's own transcript where CLAUDE_CONFIG_DIR says.
    assert.ok(listFiles(join(claudeConfigDirectory, "projects")).some((path) => path.endsWith(".jsonl")));
    assert.equal(existsSync(join(home.directory, ".claude", "projects")), false);
  });

  test("without CLAUDE_CONFIG_DIR the Claude Code CLI keeps its default", async () => {
    const home = newHome(noConnectors);
    delete home.claudeEnvironment["ANTHROPIC_API_KEY"];
    home.claudeEnvironment["ANTHROPIC_AUTH_TOKEN"] = "mock-login-token";
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("say default");
    await cli.stdout.waitFor("said: default");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.ok(listFiles(join(home.directory, ".claude", "projects")).some((path) => path.endsWith(".jsonl")));
  });

  test("a key in the claude section is a configuration error", async () => {
    const cli = start(newHome({ ...noConnectors, claude: { max_turns: 3 } }));
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /schema at "\/claude\/max_turns"/);
    assert.deepEqual(api.modelRequests(), []);
  });
});
