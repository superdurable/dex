// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
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
import { runWithTerminalUi } from "../fixtures/terminal-ui-handoff.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { MockMessagesApi } from "../fixtures/mock-messages-api.js";

const noConnectors = { connectors: { enabled: [] }, local_tools: { browser: false } };
const printClaudeCodeArguments =
  'p=$PPID; for i in 1 2 3; do ps -ww -o command= -p "$p"; p=$(ps -o ppid= -p "$p" | tr -d " "); done';

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

  test("only the Dex connector tools come from MCP, never the user's Claude Code MCP configuration", async () => {
    const home = newHome({
      connectors: { mcp: connectorsUrl },
      local_tools: { browser: false, shell: { enabled: true, allow: [printClaudeCodeArguments] } },
    });
    const inheritedServer = (name: string) => ({ mcpServers: { [name]: { type: "http", url: connectorsUrl } } });
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, ".mcp.json"), JSON.stringify(inheritedServer("project-inherited")));
    writeFileSync(
      join(home.directory, ".claude.json"),
      JSON.stringify({
        ...inheritedServer("user-inherited"),
        projects: { [realpathSync(home.workspace)]: { enableAllProjectMcpServers: true, hasTrustDialogAccepted: true } },
      }),
    );
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("Bash", { command: printClaudeCodeArguments }));
    await cli.stdout.waitFor("Bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    // Only the CLI arguments prove the two options: settingSources: [] already drops every MCP source
    // this test can create, and claude.ai connectors need a real claude.ai login.
    const bashOutput = api
      .modelRequests()
      .flatMap((request) => (request["messages"] as { content?: unknown }[] | undefined) ?? [])
      .flatMap((message) => (Array.isArray(message.content) ? (message.content as Record<string, unknown>[]) : []))
      .filter((block) => block["type"] === "tool_result")
      .map((block) =>
        typeof block["content"] === "string"
          ? block["content"]
          : ((block["content"] as { text?: string }[] | undefined) ?? []).map((part) => part.text ?? "").join("\n"),
      )
      .join("\n");
    // The Bash tool's ancestors include the Claude Code CLI; its settings JSON has no spaces.
    const claudeCodeArguments = bashOutput
      .split("\n")
      .find((line) => line.split(/\s+/).includes("--settings"))
      ?.split(/\s+/);
    assert.ok(claudeCodeArguments !== undefined, bashOutput);
    assert.ok(claudeCodeArguments.includes("--strict-mcp-config"), bashOutput);
    const settings = JSON.parse(claudeCodeArguments[claudeCodeArguments.indexOf("--settings") + 1] ?? "") as Record<
      string,
      unknown
    >;
    assert.equal(settings["disableClaudeAiConnectors"], true);
    const mcpToolNames = offeredToolNames().filter((toolName) => toolName.startsWith("mcp__"));
    assert.ok(mcpToolNames.some((toolName) => toolName.endsWith("__hackernews_get_top_stories")), `${mcpToolNames}`);
    assert.deepEqual(mcpToolNames.filter((toolName) => toolName.includes("inherited")), []);
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
    await cli.stdout.waitFor("Bash: The user rejected this Bash call: not now");

    cli.writeLine(call("Bash", { command: "echo broken; exit 3" }));
    await cli.stdout.waitFor("Approve Bash?");
    cli.writeLine("y");
    await cli.stdout.waitFor("[tool] Bash failed");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(existsSync(join(home.workspace, "rejected.txt")), false);

    // The tool Step starts an approved call; the CLI's run in the resumed turn finishes it.
    // A tool line can follow the prompt on the same line.
    const toolLines = cli.stdout.all
      .split(/\r?\n/)
      .filter((line) => line.includes("[tool] "))
      .map((line) => line.slice(line.indexOf("[tool] ")).trim());
    assert.deepEqual(toolLines, [
      "[tool] Read started",
      "[tool] Read succeeded",
      "[tool] Write started",
      "[tool] Write succeeded",
      "[tool] Bash started",
      "[tool] Bash succeeded",
      "[tool] Bash rejected",
      "[tool] Bash started",
      "[tool] Bash failed",
    ]);
    const rejectedResults = api
      .modelRequests()
      .flatMap((request) => (request["messages"] as { content?: unknown }[] | undefined) ?? [])
      .flatMap((message) => (Array.isArray(message.content) ? (message.content as Record<string, unknown>[]) : []))
      .filter((block) => block["type"] === "tool_result" && block["is_error"] === true)
      .map((block) => block["content"]);
    assert.ok(rejectedResults.includes("The user rejected this Bash call: not now"));
    assert.equal(rejectedResults.filter((result) => String(result).includes("hook error")).length, 0);
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

  test("a Write through a dangling link to outside the workspace is refused", async () => {
    const outside = mkdtempSync(join(tmpdir(), "dex-agents-claude-sdk-outside-"));
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    symlinkSync(join(outside, "created.txt"), join(home.workspace, "link.txt"));
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("Write", { file_path: join(home.workspace, "link.txt"), content: "x" }));
    await cli.stdout.waitFor("Write may only use paths inside the workspace");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve Write/);
    assert.equal(existsSync(join(outside, "created.txt")), false);
  });

  test("Glob and Grep run at once inside the workspace only", async () => {
    const outside = mkdtempSync(join(tmpdir(), "dex-agents-claude-sdk-outside-"));
    writeFileSync(join(outside, "secret.txt"), "do not read");
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("Glob", { pattern: "*.txt" }));
    await cli.stdout.waitFor("Glob: ");
    await cli.stdout.waitFor("notes.txt");
    cli.writeLine(call("Grep", { pattern: "milk" }));
    await cli.stdout.waitFor("Grep: ");
    await cli.stdout.waitFor("notes.txt");
    const outsideName = outside.split("/").at(-1) ?? "";
    for (const pattern of [`*/../../${outsideName}/secret.txt`, `**/../../${outsideName}/secret.txt`]) {
      cli.writeLine(call("Glob", { pattern }));
      await cli.stdout.waitFor("Glob may only use paths inside the workspace");
    }
    cli.writeLine(call("Grep", { pattern: "read", glob: `../${outsideName}/*` }));
    await cli.stdout.waitFor("Grep may only use paths inside the workspace");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve (Glob|Grep)/);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
  });

  test("Edit waits for approval and runs after it", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    const notes = join(home.workspace, "notes.txt");
    writeFileSync(notes, "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(
      `${call("Read", { file_path: notes })} && ${call("Edit", { file_path: notes, old_string: "milk", new_string: "bread" })}`,
    );
    await cli.stdout.waitFor("Approve Edit?");
    assert.equal(readFileSync(notes, "utf8"), "buy milk\n");
    cli.writeLine("y");
    await cli.stdout.waitFor("Edit: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(notes, "utf8"), "buy bread\n");
  });

  test("WebFetch runs without approval", async () => {
    const cli = start(newHome(noConnectors));
    await cli.waitForSession();
    // The refusing proxy fails the fetch itself; the test checks only that no approval gates it.
    cli.writeLine(call("WebFetch", { url: "https://example.com/", prompt: "the title" }));
    await cli.stdout.waitFor("WebFetch: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve WebFetch/);
    assert.ok(offeredToolNames().includes("WebFetch"));
  });

  test("an allow-listed background command still waits for approval", async () => {
    const cli = start(newHome({ ...noConnectors, local_tools: { browser: false, shell: { enabled: true, allow: ["sleep 0"] } } }));
    await cli.waitForSession();
    cli.writeLine(call("Bash", { command: "sleep 0", run_in_background: true }));
    await cli.stdout.waitFor("Approve Bash?");
    cli.writeLine("n not in the background");
    await cli.stdout.waitFor("Bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("an approved call is checked again by the Worker that runs it", async () => {
    const home = newHome(noConnectors);
    const first = start(home);
    const sessionId = await first.waitForSession();
    const todoPath = join(home.workspace, "todo.txt");
    first.writeLine(call("Write", { file_path: todoPath, content: "call mom" }));
    await first.stdout.waitFor("Approve Write?");
    // Killed while it waits: the approval stays pending in the session.
    await first.kill();

    // The file now switches the file tools off; the Worker that runs the approved call applies it.
    home.writeConfig({ ...noConnectors, local_tools: { browser: false, files: false } });
    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor("Approve Write?");
    second.writeLine("y");
    await second.stdout.waitFor("Write is switched off for this session; the call did not run");
    await second.stdout.waitFor("> ");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(existsSync(todoPath), false);
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

  test("at a terminal with the terminal UI switched on, the chat runs in the terminal UI and nothing prints over it", async () => {
    const home = newHome(noConnectors);
    const run = await runWithTerminalUi(home.environment());
    assert.equal(run.exitCode, 0, run.terminal);
    assert.equal(run.launchCount, 1, run.terminal);
    assert.equal(run.whileTerminalUiRan, "", run.terminal);
    assert.match(run.terminal, /keeps running\. Resume it with: dex-agents-claude-sdk --resume /);
  });
});
