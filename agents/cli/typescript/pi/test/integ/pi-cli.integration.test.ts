// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  rmSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { crc32, deflateSync } from "node:zlib";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, RefusingProxy, TestHome, enginePorts, mockApiKey, openSessionClient } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { MockMessagesApi } from "../fixtures/mock-messages-api.js";

const noConnectors = { connectors: { enabled: [] }, local_tools: { browser: false } };
const piToolNames = ["read", "grep", "find", "ls", "write", "edit", "bash"];

/** A valid 1x1 PNG. */
function pngBytes(): Buffer {
  const chunk = (kind: string, data: Buffer): Buffer => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(kind, "latin1"), data]);
    const checksum = Buffer.alloc(4);
    checksum.writeUInt32BE(crc32(body));
    return Buffer.concat([length, body, checksum]);
  };
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", Buffer.from([0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0])),
    chunk("IDAT", deflateSync(Buffer.from([0, 0xff, 0, 0]))),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function call(toolName: string, toolInput: Readonly<Record<string, unknown>>): string {
  return `call ${toolName} ${JSON.stringify(toolInput)}`;
}

describe("dex-agents-pi with pi-ai against a scripted Messages API", () => {
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

  function start(home: TestHome, commandArguments: readonly string[] = [], nodeArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments, nodeArguments);
    processes.push(cli);
    return cli;
  }

  function offeredToolNames(): string[] {
    const tools = (api.modelRequests().at(-1)?.["tools"] as Record<string, unknown>[] | undefined) ?? [];
    return tools.map((tool) => String(tool["name"]));
  }

  function offeredTool(toolName: string): Record<string, unknown> | undefined {
    const tools = (api.modelRequests().at(-1)?.["tools"] as Record<string, unknown>[] | undefined) ?? [];
    return tools.find((tool) => tool["name"] === toolName);
  }

  function lastToolResults(): string {
    const messages = (api.modelRequests().at(-1)?.["messages"] as Record<string, unknown>[] | undefined) ?? [];
    const blocks = messages.flatMap((message) =>
      Array.isArray(message["content"]) ? (message["content"] as Record<string, unknown>[]) : [],
    );
    return JSON.stringify(blocks.filter((block) => block["type"] === "tool_result"));
  }

  function lastSystemPrompt(): string {
    return JSON.stringify(api.modelRequests().at(-1)?.["system"] ?? "");
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
    assert.doesNotMatch(first.stdout.all + first.stderr.all + home.stateText(), new RegExp(mockApiKey));
  });

  function messageBlocks(body: Record<string, unknown> | undefined): Record<string, unknown>[] {
    const messages = (body?.["messages"] as Record<string, unknown>[] | undefined) ?? [];
    return messages.flatMap((message) =>
      Array.isArray(message["content"]) ? (message["content"] as Record<string, unknown>[]) : [],
    );
  }

  test("an image attached with /image reaches the Messages API as an image block", async () => {
    const home = newHome(noConnectors);
    const png = pngBytes();
    writeFileSync(join(home.directory, "chart.png"), png);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("/image ~/chart.png");
    await cli.stdout.waitFor("Attached chart.png (image/png");
    cli.writeLine("say hi");
    await cli.stdout.waitFor("said: hi");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const sha256 = createHash("sha256").update(png).digest("hex");
    assert.deepEqual(readFileSync(join(home.directory, ".dex", "agents", "media", "sha256", sha256)), png);
    const images = messageBlocks(api.modelRequests().at(-1)).filter((block) => block["type"] === "image");
    assert.deepEqual(
      images.map((block) => block["source"]),
      [{ type: "base64", media_type: "image/png", data: png.toString("base64") }],
    );
  });

  test("read of a PNG in the workspace reaches the next model call as an image in the tool result", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    const png = pngBytes();
    writeFileSync(join(home.workspace, "chart.png"), png);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: "chart.png" }));
    await cli.stdout.waitFor("read: Read image file [image/png]");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResults = messageBlocks(api.modelRequests().at(-1)).filter((block) => block["type"] === "tool_result");
    assert.equal(toolResults.length, 1, JSON.stringify(toolResults));
    const content = (toolResults[0]?.["content"] as Record<string, unknown>[] | undefined) ?? [];
    const images = content.filter((block) => block["type"] === "image");
    assert.equal(images.length, 1, JSON.stringify(content));
    const source = images[0]?.["source"] as Record<string, unknown>;
    assert.equal(source["type"], "base64");
    assert.equal(source["media_type"], "image/png");
    const stored = Buffer.from(String(source["data"]), "base64");
    const sha256 = createHash("sha256").update(stored).digest("hex");
    assert.deepEqual(readFileSync(join(home.directory, ".dex", "agents", "media", "sha256", sha256)), stored);
    assert.doesNotMatch(JSON.stringify(content), /image omitted/);
  });

  test("Pi's tools replace the CLI core's file and shell tools, and reads run at once", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: "notes.txt" }));
    await cli.stdout.waitFor("read: buy milk");
    cli.writeLine(call("ls", {}));
    await cli.stdout.waitFor("ls: notes.txt");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve (read|ls)/);
    const offered = offeredToolNames();
    for (const toolName of [...piToolNames, "fetch_web_page", "ask_user"]) {
      assert.ok(offered.includes(toolName), `${toolName} is offered`);
    }
    for (const toolName of ["read_file", "write_file", "edit_file", "list_directory", "search_files", "run_shell_command"]) {
      assert.ok(!offered.includes(toolName), `${toolName} is not offered`);
    }
    assert.match(JSON.stringify(offeredTool("bash")), /Timeout in seconds; defaults to 120/);
    assert.match(JSON.stringify(offeredTool("grep")), /Respects \.gitignore/);
  });

  test("write, edit, and bash change the workspace at once, as in Pi", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    const notes = join(home.workspace, "notes.txt");
    writeFileSync(notes, "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("write", { path: "todo/today.txt", content: "call mom" }));
    await cli.stdout.waitFor("write: Successfully wrote to todo/today.txt");
    cli.writeLine(call("edit", { path: "notes.txt", edits: [{ oldText: "milk", newText: "milk and eggs" }] }));
    await cli.stdout.waitFor("edit: Successfully replaced 1 block(s) in notes.txt.");
    cli.writeLine(call("bash", { command: "pwd -P; echo ran > bash.txt" }));
    await cli.stdout.waitFor(`bash: ${realpathSync(home.workspace)}`);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve/);
    assert.equal(readFileSync(join(home.workspace, "todo", "today.txt"), "utf8"), "call mom");
    assert.equal(readFileSync(notes, "utf8"), "buy milk and eggs\n");
    assert.equal(readFileSync(join(home.workspace, "bash.txt"), "utf8"), "ran\n");
  });

  test("approval.always makes write and bash wait, and a rejected call never runs", async () => {
    const home = newHome({ ...noConnectors, approval: { always: ["write", "bash"] } });
    const cli = start(home);
    await cli.waitForSession();
    const todo = join(home.workspace, "todo.txt");
    cli.writeLine(call("write", { path: "todo.txt", content: "call mom" }));
    await cli.stdout.waitFor("Approve write?");
    cli.writeLine("n not now");
    await cli.stdout.waitFor("write: ");
    await cli.stdout.waitFor("> ");
    assert.equal(existsSync(todo), false);
    cli.writeLine(call("write", { path: "todo.txt", content: "call mom" }));
    await cli.stdout.waitFor("Approve write?");
    assert.equal(existsSync(todo), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("write: Successfully wrote");
    cli.writeLine(call("bash", { command: "echo approved > bash.txt" }));
    await cli.stdout.waitFor("Approve bash?");
    assert.equal(existsSync(join(home.workspace, "bash.txt")), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("bash: ");
    cli.writeLine(call("edit", { path: "todo.txt", edits: [{ oldText: "mom", newText: "dad" }] }));
    await cli.stdout.waitFor("edit: Successfully replaced");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(cli.stdout.all.match(/Approve write\?/g)?.length, 2);
    assert.doesNotMatch(cli.stdout.all, /Approve edit/);
    assert.equal(readFileSync(todo, "utf8"), "call dad");
    assert.equal(readFileSync(join(home.workspace, "bash.txt"), "utf8"), "approved\n");
  });

  test("the file tools reach files outside the workspace, as in Pi", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "shared.txt"), "outside note\n");
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: join(outside, "shared.txt") }));
    await cli.stdout.waitFor("read: outside note");
    cli.writeLine(call("write", { path: join(outside, "created.txt"), content: "written by the agent" }));
    await cli.stdout.waitFor("write: Successfully wrote");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(join(outside, "created.txt"), "utf8"), "written by the agent");
  });

  test("an edit that fails before writing records a failure the model sees", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("edit", { path: "notes.txt", edits: [{ oldText: "bread", newText: "cake" }] }));
    await cli.stdout.waitFor("edit: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine(call("edit", { path: "missing.txt", edits: [{ oldText: "a", newText: "b" }] }));
    await cli.stdout.waitFor("edit: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResults = JSON.stringify(api.modelRequests().at(-1)?.["messages"]);
    assert.match(toolResults, /Could not find/);
    assert.match(toolResults, /Could not edit file: missing\.txt/);
    assert.doesNotMatch(toolResults, /outcome is unknown/);
    assert.equal(readFileSync(join(home.workspace, "notes.txt"), "utf8"), "buy milk\n");
  });

  test("a bash command that exits with a non-zero code records a failure with its output", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("bash", { command: "echo ran > ran.txt; echo bad; exit 3" }));
    await cli.stdout.waitFor("bash: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResult = lastToolResults();
    assert.match(toolResult, /bad/);
    assert.match(toolResult, /Command exited with code 3/);
    assert.doesNotMatch(toolResult, /outcome is unknown/);
    assert.equal(readFileSync(join(home.workspace, "ran.txt"), "utf8"), "ran\n");
  });

  test("a cut bash output names Pi's file with the full output", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("bash", { command: "seq 1 200000" }));
    await cli.stdout.waitFor("Full output: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const fullOutputPath = /Full output: ([^\]\\"]+)\]/.exec(lastToolResults())?.[1];
    assert.ok(fullOutputPath !== undefined, lastToolResults());
    try {
      assert.match(readFileSync(fullOutputPath, "utf8"), /^1\n2\n[\s\S]*\n200000\n$/);
    } finally {
      rmSync(fullOutputPath, { force: true });
    }
  });

  test("bash output reaches the session as tool progress while the command runs", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine(call("bash", { command: "echo first; sleep 1; echo second" }));
    await cli.stdout.waitFor("bash: first\nsecond");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const { environment, sessions } = await openSessionClient(engine, home.sessionFlowType());
    let progress: string[];
    try {
      progress = (await sessions.listAgentEvents(sessionId))
        .filter((event) => event.type === "pi.tool_progress")
        .map((event) => String((JSON.parse(event.payload) as Record<string, unknown>)["text"]));
    } finally {
      await environment.close();
    }
    assert.ok(progress.some((text) => text.includes("first")), JSON.stringify(progress));
  });

  test("a bash command without a timeout is killed after pi.bash_timeout_seconds, and the model sees it", async () => {
    const home = newHome({ ...noConnectors, pi: { bash_timeout_seconds: 1 } });
    const cli = start(home);
    await cli.waitForSession();
    const startedAt = Date.now();
    cli.writeLine(call("bash", { command: "echo started; sleep 30; echo finished" }));
    await cli.stdout.waitFor("bash: ");
    await cli.stdout.waitFor("> ");
    const elapsedMs = Date.now() - startedAt;
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.ok(elapsedMs < 20_000, `the command was killed early, after ${elapsedMs} ms`);
    assert.match(JSON.stringify(offeredTool("bash")), /Timeout in seconds; defaults to 1/);
    const toolResult = lastToolResults();
    assert.match(toolResult, /started/);
    assert.match(toolResult, /Command timed out after 1 seconds/);
    assert.doesNotMatch(toolResult, /finished|outcome is unknown/);
  });

  test("a bash call with an invalid timeout records a failure and never runs", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("bash", { command: "echo ran > ran.txt", timeout: -1 }));
    await cli.stdout.waitFor("bash: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResult = lastToolResults();
    assert.match(toolResult, /Invalid timeout/);
    assert.doesNotMatch(toolResult, /outcome is unknown/);
    assert.equal(existsSync(join(home.workspace, "ran.txt")), false);
  });

  test("grep and find run Pi's rg and fd in the workspace", async () => {
    const home = newHome(noConnectors);
    home.provideSearchTools();
    mkdirSync(join(home.workspace, "src"), { recursive: true });
    mkdirSync(join(home.workspace, ".git"), { recursive: true });
    writeFileSync(join(home.workspace, ".gitignore"), "ignored.ts\n");
    writeFileSync(join(home.workspace, "src", "a.ts"), "const Alpha = 1;\nconst beta = 2;\n");
    writeFileSync(join(home.workspace, "ignored.ts"), "const alpha = 3;\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("grep", { pattern: "alpha", ignoreCase: true }));
    await cli.stdout.waitFor("grep: src/a.ts:1: const Alpha = 1;");
    await cli.stdout.waitFor("> ");
    cli.writeLine(call("find", { pattern: "*.ts" }));
    await cli.stdout.waitFor("find: src/a.ts");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /ignored\.ts|Approve/);
  });

  test("without rg and fd, offline grep and find fail with Pi's message, and the session goes on", async () => {
    const home = newHome(noConnectors);
    home.hideSearchTools();
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("grep", { pattern: "milk" }));
    await cli.stdout.waitFor("ripgrep (rg) is not available and could not be downloaded");
    cli.writeLine(call("find", { pattern: "*.txt" }));
    await cli.stdout.waitFor("fd is not available and could not be downloaded");
    cli.writeLine("say still here");
    await cli.stdout.waitFor("said: still here");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(existsSync(home.piBinDirectory), false);
  });

  test("a group switched off in the file is not offered", async () => {
    const home = newHome({ ...noConnectors, local_tools: { browser: false, files: false } });
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: "notes.txt" }));
    await cli.stdout.waitFor("read: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const offered = offeredToolNames();
    assert.ok(offered.includes("bash"));
    for (const toolName of ["read", "grep", "find", "ls", "write", "edit", "read_file"]) {
      assert.ok(!offered.includes(toolName), `${toolName} is not offered`);
    }
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /buy milk/);
  });

  test("session settings that switch the shell off remove bash", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    const { environment, sessions } = await openSessionClient(engine, home.sessionFlowType());
    try {
      await sessions.updateSessionSettings(sessionId, { local_tools: { shell: { enabled: false } } });
    } finally {
      await environment.close();
    }
    cli.writeLine(call("bash", { command: "echo ran > bash.txt" }));
    await cli.stdout.waitFor("bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.ok(!offeredToolNames().includes("bash"));
    assert.ok(offeredToolNames().includes("read"));
    assert.equal(existsSync(join(home.workspace, "bash.txt")), false);
  });

  test("an approved call is checked again by the Worker that runs it", async () => {
    const approval = { always: ["write"] };
    const home = newHome({ ...noConnectors, approval });
    const first = start(home);
    const sessionId = await first.waitForSession();
    const todo = join(home.workspace, "todo.txt");
    first.writeLine(call("write", { path: "todo.txt", content: "call mom" }));
    await first.stdout.waitFor("Approve write?");
    // Killed while it waits: the approval stays pending in the session.
    await first.kill();

    // The file now switches the file tools off; the Worker that runs the approved call applies it.
    home.writeConfig({ ...noConnectors, approval, local_tools: { browser: false, files: false } });
    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor("Approve write?");
    second.writeLine("y");
    await second.stdout.waitFor("write is switched off in the configuration of the Worker that ran it; it did not run");
    await second.stdout.waitFor("> ");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(existsSync(todo), false);
  });

  test("a bash command whose Worker dies after it started records outcome_unknown and never runs again", async () => {
    const command = "echo started >> runs.txt; sleep 20";
    const home = newHome(noConnectors);
    const first = start(home);
    const sessionId = await first.waitForSession();
    first.writeLine(call("bash", { command }));
    const runs = join(home.workspace, "runs.txt");
    const deadline = Date.now() + 60_000;
    while (!existsSync(runs) && Date.now() < deadline) {
      await delay(100);
    }
    assert.ok(existsSync(runs), "the command started");
    await first.kill();

    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor("bash: The tool call's outcome is unknown", 240_000);
    second.writeLine("recall");
    await second.stdout.waitFor("history: ");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(readFileSync(runs, "utf8"), "started\n");
  });

  test("a connector read runs at once, and every connector write runs once after approval", async () => {
    const cli = start(newHome({ connectors: { mcp: connectorsUrl }, local_tools: { browser: false } }));
    await cli.waitForSession();
    cli.writeLine(call("hackernews_get_top_stories", {}));
    await cli.stdout.waitFor("hackernews_get_top_stories: 1. Durable agents; 2. Local first");
    assert.equal(connectors.runCount("hackernews_get_top_stories"), 1);
    assert.doesNotMatch(cli.stdout.all, /Approve hackernews_get_top_stories/);

    cli.writeLine(call("slack_post_thread_reply", { thread: "T1", text: "on it" }));
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    assert.equal(connectors.runCount("slack_post_thread_reply"), 0);
    cli.writeLine("y");
    await cli.stdout.waitFor("slack_post_thread_reply: posted on it in T1");

    // An idempotent connector write waits too.
    cli.writeLine(call("notion_update_page_title", { page: "P1", title: "Plan" }));
    await cli.stdout.waitFor("Approve notion_update_page_title?");
    assert.equal(connectors.runCount("notion_update_page_title"), 0);
    cli.writeLine("n not now");
    await cli.stdout.waitFor("notion_update_page_title: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(connectors.runCount("slack_post_thread_reply"), 1);
    assert.equal(connectors.runCount("notion_update_page_title"), 0);
  });

  test("a connector tool named like a Pi tool gets the connector_ prefix", async () => {
    const enabled = ["hackernews", "files/default", "archive/default"];
    const home = newHome({
      connectors: { mcp: connectorsUrl, enabled },
      approval: { always: ["connector_connector_read"] },
      local_tools: { browser: false },
    });
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: "notes.txt" }));
    await cli.stdout.waitFor("read: buy milk");
    cli.writeLine(call("connector_read", { path: "a.txt" }));
    await cli.stdout.waitFor("connector_read: read through the archive connector: a.txt");
    cli.writeLine(call("connector_connector_read", { path: "b.txt" }));
    await cli.stdout.waitFor("Approve connector_connector_read?");
    assert.equal(connectors.runCount("read"), 0);
    cli.writeLine("y");
    await cli.stdout.waitFor("connector_connector_read: read through the files connector: b.txt");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const offered = offeredToolNames();
    assert.equal(offered.filter((name) => name === "read").length, 1);
    assert.ok(offered.includes("connector_read"));
    assert.ok(offered.includes("connector_connector_read"));
    assert.ok(offered.includes("hackernews_get_top_stories"));
    assert.equal(connectors.runCount("read"), 1);
    assert.equal(connectors.runCount("connector_read"), 1);
  });

  test("--yes writes no workspace, and the workspace is the directory the command starts in", async () => {
    const home = new TestHome(enginePorts(engine), api, proxy.url);
    const first = start(home, ["--yes"]);
    const sessionId = await first.waitForSession();
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    const written = readFileSync(home.configPath, "utf8");
    assert.doesNotMatch(written, /workspace/);
    const listing = start(home, ["sessions"]);
    assert.equal(await listing.waitForExit(), 0);
    assert.match(listing.stdout.all, new RegExp(`${sessionId}\\t`));
  });

  test("each model call adds the workspace's AGENTS.md, unless pi.load_project_context is false", async () => {
    const marker = "Answer with the project's house style";
    const loading = newHome(noConnectors);
    mkdirSync(loading.workspace, { recursive: true });
    writeFileSync(join(loading.workspace, "AGENTS.md"), `${marker}.\n`);
    const first = start(loading);
    await first.waitForSession();
    first.writeLine("say hi");
    await first.stdout.waitFor("said: hi");
    assert.ok(lastSystemPrompt().includes(marker), "the system prompt has AGENTS.md");
    assert.ok(lastSystemPrompt().includes(`<project_instructions path=\\"${join(realpathSync(loading.workspace), "AGENTS.md")}\\">`));
    // The next model call reads the file again.
    writeFileSync(join(loading.workspace, "AGENTS.md"), "Changed instructions.\n");
    first.writeLine("say again");
    await first.stdout.waitFor("said: again");
    assert.ok(lastSystemPrompt().includes("Changed instructions."));
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const skipping = newHome({ ...noConnectors, pi: { load_project_context: false } });
    mkdirSync(skipping.workspace, { recursive: true });
    writeFileSync(join(skipping.workspace, "AGENTS.md"), `${marker}.\n`);
    const second = start(skipping);
    await second.waitForSession();
    second.writeLine("say hi");
    await second.stdout.waitFor("said: hi");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.ok(!lastSystemPrompt().includes(marker), "the system prompt has no AGENTS.md");
  });

  test("a project context file linked out of its directory is skipped, also with the file tools off", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    for (const scope of ["workspace", "ancestor", "global"]) {
      writeFileSync(join(outside, `${scope}.md`), `Outside ${scope} instructions.\n`);
    }
    const home = newHome({ ...noConnectors, local_tools: { browser: false, files: false } });
    mkdirSync(join(home.workspace, "docs"), { recursive: true });
    writeFileSync(join(home.workspace, "rules.md"), "Inside workspace rules.\n");
    symlinkSync(join(home.workspace, "rules.md"), join(home.workspace, "AGENTS.md"));
    symlinkSync(join(outside, "ancestor.md"), join(home.directory, "AGENTS.md"));
    mkdirSync(join(home.directory, ".pi", "agent"), { recursive: true });
    symlinkSync(join(outside, "global.md"), join(home.directory, ".pi", "agent", "AGENTS.md"));
    const first = start(home);
    await first.waitForSession();
    first.writeLine("say hi");
    await first.stdout.waitFor("said: hi");
    assert.ok(lastSystemPrompt().includes("Inside workspace rules."), "a link to a file in the same directory loads");
    assert.doesNotMatch(lastSystemPrompt(), /Outside (ancestor|global) instructions/);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    assert.match(first.stderr.all, /skipped the project context file .*\.pi\/agent\/AGENTS\.md: it resolves to .*outside its directory/);
    assert.match(first.stderr.all, new RegExp(`skipped the project context file ${home.directory}/AGENTS\\.md`));

    unlinkSync(join(home.workspace, "AGENTS.md"));
    symlinkSync(join(outside, "workspace.md"), join(home.workspace, "AGENTS.md"));
    const second = start(home);
    await second.waitForSession();
    second.writeLine("say again");
    await second.stdout.waitFor("said: again");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /Outside (workspace|ancestor|global) instructions/);
  });

  test("a workspace swapped for a link after startup never authorizes the new place for project context", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "AGENTS.md"), "Outside swapped instructions.\n");
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "AGENTS.md"), "Inside instructions.\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("say hi");
    await cli.stdout.waitFor("said: hi");
    assert.ok(lastSystemPrompt().includes("Inside instructions."));
    renameSync(home.workspace, `${home.workspace}-old`);
    symlinkSync(outside, home.workspace);
    cli.writeLine("say again");
    await cli.stdout.waitFor("said: again");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /Outside swapped instructions/);
    assert.match(cli.stderr.all, /skipped the project context file .*AGENTS\.md: it resolves to .*outside its directory/);
  });

  test("without an API key the command stops and names the variable, even with an OAuth token", async () => {
    const home = newHome(noConnectors);
    delete home.providerEnvironment["ANTHROPIC_API_KEY"];
    home.providerEnvironment["ANTHROPIC_OAUTH_TOKEN"] = "sk-ant-oat-mock-token";
    const cli = start(home);
    assert.equal(await cli.waitForExit(), 1);
    assert.match(cli.stderr.all, /no API key for the model's provider: set ANTHROPIC_API_KEY/);
    assert.deepEqual(api.modelRequests(), []);
  });

  test("an unknown key in the pi section is a configuration error", async () => {
    const cli = start(newHome({ ...noConnectors, pi: { max_turns: 3 } }));
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /schema at "\/pi\/max_turns"/);
    assert.deepEqual(api.modelRequests(), []);
  });
});
