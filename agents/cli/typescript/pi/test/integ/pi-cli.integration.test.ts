// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  existsSync,
  linkSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  realpathSync,
  renameSync,
  symlinkSync,
  unlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, RefusingProxy, TestHome, enginePorts, mockApiKey, openSessionClient } from "../fixtures/cli-test-support.js";
import { MockMessagesApi } from "../fixtures/mock-messages-api.js";

const failAfterRenameHook = fileURLToPath(new URL("../fixtures/fail-after-rename-hook.js", import.meta.url));
const noConnectors = { connectors: { enabled: [] }, local_tools: { browser: false } };
const piToolNames = ["read", "grep", "find", "ls", "write", "edit", "bash"];

function call(toolName: string, toolInput: Readonly<Record<string, unknown>>): string {
  return `call ${toolName} ${JSON.stringify(toolInput)}`;
}

describe("dex-agents-pi with pi-ai against a scripted Messages API", () => {
  let engine: PrivateDexEngine;
  let api: MockMessagesApi;
  let proxy: RefusingProxy;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    api = await MockMessagesApi.start();
    proxy = await RefusingProxy.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
    api.requests.splice(0);
  });

  after(async () => {
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

  test("read and ls run at once, and the Pi tools replace the runtime's file and shell tools", async () => {
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
    for (const toolName of [...piToolNames, "fetch_web_page"]) {
      assert.ok(offered.includes(toolName), `${toolName} is offered`);
    }
    for (const toolName of ["read_file", "write_file", "edit_file", "list_directory", "search_files", "run_shell_command"]) {
      assert.ok(!offered.includes(toolName), `${toolName} is not offered`);
    }
  });

  test("write and edit wait for approval and run once", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    const notes = join(home.workspace, "notes.txt");
    writeFileSync(notes, "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();

    const todo = join(home.workspace, "todo.txt");
    cli.writeLine(call("write", { path: "todo.txt", content: "call mom" }));
    await cli.stdout.waitFor("Approve write?");
    assert.equal(existsSync(todo), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("write: Successfully wrote");
    assert.equal(readFileSync(todo, "utf8"), "call mom");

    cli.writeLine(call("edit", { path: "notes.txt", edits: [{ oldText: "milk", newText: "milk and eggs" }] }));
    await cli.stdout.waitFor("Approve edit?");
    assert.equal(readFileSync(notes, "utf8"), "buy milk\n");
    cli.writeLine("y");
    await cli.stdout.waitFor("edit: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(notes, "utf8"), "buy milk and eggs\n");
  });

  test("an edit that fails before writing records a failure the model sees", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("edit", { path: "notes.txt", edits: [{ oldText: "bread", newText: "cake" }] }));
    await cli.stdout.waitFor("Approve edit?");
    cli.writeLine("y");
    await cli.stdout.waitFor("edit: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResults = JSON.stringify(api.modelRequests().at(-1)?.["messages"]);
    assert.match(toolResults, /Could not find/);
    assert.doesNotMatch(toolResults, /outcome is unknown/);
    assert.equal(readFileSync(join(home.workspace, "notes.txt"), "utf8"), "buy milk\n");
  });

  test("an allow-listed bash command runs at once in the workspace, and others wait", async () => {
    const home = newHome({ ...noConnectors, local_tools: { browser: false, shell: { enabled: true, allow: ["pwd -P"] } } });
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("bash", { command: "pwd -P" }));
    await cli.stdout.waitFor(`bash: ${realpathSync(home.workspace)}`);
    assert.doesNotMatch(cli.stdout.all, /Approve bash/);

    cli.writeLine(call("bash", { command: "echo ran > bash.txt" }));
    await cli.stdout.waitFor("Approve bash?");
    assert.equal(existsSync(join(home.workspace, "bash.txt")), false);
    cli.writeLine("n not now");
    await cli.stdout.waitFor("bash: ");

    cli.writeLine(call("bash", { command: "echo approved > bash.txt" }));
    await cli.stdout.waitFor("Approve bash?");
    cli.writeLine("y");
    await cli.stdout.waitFor("bash: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(join(home.workspace, "bash.txt"), "utf8"), "approved\n");
  });

  test("a bash command without a timeout is killed after pi.bash_timeout_seconds, and the model sees it", async () => {
    const home = newHome({
      ...noConnectors,
      local_tools: { browser: false, shell: { enabled: true, allow: ["echo started; sleep 30; echo finished"] } },
      pi: { bash_timeout_seconds: 1 },
    });
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
    const bashTool = ((api.modelRequests().at(-1)?.["tools"] as Record<string, unknown>[] | undefined) ?? []).find(
      (tool) => tool["name"] === "bash",
    );
    assert.match(JSON.stringify(bashTool), /defaults to 1/);
    const toolResult = lastToolResults();
    assert.match(toolResult, /The tool call failed: started/);
    assert.match(toolResult, /Command timed out after 1 seconds/);
    assert.doesNotMatch(toolResult, /finished|outcome is unknown/);
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
    const home = newHome(noConnectors);
    const first = start(home);
    const sessionId = await first.waitForSession();
    const todo = join(home.workspace, "todo.txt");
    first.writeLine(call("write", { path: "todo.txt", content: "call mom" }));
    await first.stdout.waitFor("Approve write?");
    // Killed while it waits: the approval stays pending in the session.
    await first.kill();

    // The file now switches the file tools off; the Worker that runs the approved call applies it.
    home.writeConfig({ ...noConnectors, local_tools: { browser: false, files: false } });
    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor("Approve write?");
    second.writeLine("y");
    await second.stdout.waitFor("write is switched off in the configuration of the Worker that ran it; it did not run");
    await second.stdout.waitFor("> ");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(existsSync(todo), false);
  });

  test("file tools refuse paths outside the workspace", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "secret.txt"), "do not read");
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    symlinkSync(join(outside, "created.txt"), join(home.workspace, "link.txt"));
    const cli = start(home);
    await cli.waitForSession();
    for (const path of [join(outside, "secret.txt"), `../../${basename(outside)}/secret.txt`, `@${outside}/secret.txt`]) {
      cli.writeLine(call("read", { path }));
      await cli.stdout.waitFor("is outside the workspace");
    }
    cli.writeLine(call("ls", { path: outside }));
    await cli.stdout.waitFor("is outside the workspace");
    cli.writeLine(call("find", { pattern: "*.txt", path: outside }));
    await cli.stdout.waitFor("is outside the workspace");
    cli.writeLine(call("write", { path: "link.txt", content: "x" }));
    await cli.stdout.waitFor("Approve write?");
    cli.writeLine("y");
    await cli.stdout.waitFor("is a symbolic link");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve (read|ls|find)/);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
    assert.equal(existsSync(join(outside, "created.txt")), false);
  });

  test("read's file name fallbacks never reach a link out of the workspace", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "secret.txt"), "do not read");
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    const variants: readonly (readonly [string, string])[] = [
      ["a'b.txt", "a’b.txt"],
      ["Shot 10.00.00 AM.txt", "Shot 10.00.00 AM.txt"],
      ["café.txt", "café.txt"],
    ];
    for (const [, linkName] of variants) {
      symlinkSync(join(outside, "secret.txt"), join(home.workspace, linkName));
    }
    const cli = start(home);
    await cli.waitForSession();
    for (const [requested] of variants) {
      cli.writeLine(call("read", { path: requested }));
      await cli.stdout.waitFor("read: ");
      await cli.stdout.waitFor("> ");
    }
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve read/);
    assert.equal(cli.stdout.all.match(/is outside the workspace/g)?.length, variants.length);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
  });

  test("a path swapped for a link out of the workspace after the call was requested is refused", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "a.txt"), "do not read");
    const home = newHome({ ...noConnectors, approval: { always: ["read", "ls", "grep", "find"] } });
    const notes = join(home.workspace, "notes");
    mkdirSync(notes, { recursive: true });
    writeFileSync(join(notes, "a.txt"), "inside note");
    const cli = start(home);
    await cli.waitForSession();
    const calls: readonly (readonly [string, Readonly<Record<string, unknown>>])[] = [
      ["read", { path: "notes/a.txt" }],
      ["ls", { path: "notes" }],
      ["grep", { pattern: "read", path: "notes" }],
      ["find", { pattern: "*.txt", path: "notes" }],
    ];
    for (const [toolName, toolInput] of calls) {
      cli.writeLine(call(toolName, toolInput));
      await cli.stdout.waitFor(`Approve ${toolName}?`);
      renameSync(notes, `${notes}-real`);
      symlinkSync(outside, notes);
      cli.writeLine("y");
      await cli.stdout.waitFor(`${toolName}: `);
      await cli.stdout.waitFor("> ");
      unlinkSync(notes);
      renameSync(`${notes}-real`, notes);
    }
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(cli.stdout.all.match(/is outside the workspace/g)?.length, calls.length);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
  });

  test("grep matches with a glob, ignoring case, a literal, context lines, and a limit", async () => {
    const home = newHome(noConnectors);
    mkdirSync(join(home.workspace, "src"), { recursive: true });
    writeFileSync(join(home.workspace, "src", "a.ts"), "const Alpha = 1;\nconst beta = 2;\nconst gamma = 3;\n");
    writeFileSync(join(home.workspace, "src", "b.md"), "alpha in the docs\n");
    writeFileSync(join(home.workspace, "notes.txt"), "a.b is literal\naxb is not\n");
    mkdirSync(join(home.workspace, "lib", "src"), { recursive: true });
    writeFileSync(join(home.workspace, "lib", "src", "c.ts"), "alpha nested\n");
    assert.equal(spawnSync("mkfifo", [join(home.workspace, "pipe")]).status, 0);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("grep", { pattern: "alpha", ignoreCase: true, glob: "*.ts" }));
    await cli.stdout.waitFor("grep: lib/src/c.ts:1: alpha nested\nsrc/a.ts:1: const Alpha = 1;");
    await cli.stdout.waitFor("> ");
    cli.writeLine(call("grep", { pattern: "alpha", ignoreCase: true, glob: "/src/*.ts" }));
    await cli.stdout.waitFor("grep: src/a.ts:1: const Alpha = 1;\n");
    await cli.stdout.waitFor("> ");
    cli.writeLine(call("grep", { pattern: "alpha", ignoreCase: true, glob: "!src" }));
    await cli.stdout.waitFor("grep: No matches found");
    cli.writeLine(call("read", { path: "pipe" }));
    await cli.stdout.waitFor("is not a regular file");
    cli.writeLine(call("grep", { pattern: "alpha", ignoreCase: true, limit: 1 }));
    await cli.stdout.waitFor("1 matches limit reached");
    cli.writeLine(call("grep", { pattern: "a.b", literal: true }));
    await cli.stdout.waitFor("grep: notes.txt:1: a.b is literal");
    await cli.stdout.waitFor("> ");
    cli.writeLine(call("grep", { pattern: "beta", path: "src", context: 1 }));
    await cli.stdout.waitFor("grep: a.ts-1- const Alpha = 1;\na.ts:2: const beta = 2;\na.ts-3- const gamma = 3;");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /b\.md|axb|Approve grep/);
  });

  test("a grep pattern that backtracks without end fails the call instead of blocking the Worker", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "slow.txt"), `${"a!".repeat(32)}\n${"a".repeat(32)}!\n`);
    const cli = start(home);
    await cli.waitForSession();
    const startedAt = Date.now();
    cli.writeLine(call("grep", { pattern: "(a+)+$" }));
    await cli.stdout.waitFor("the pattern took too long to match", 60_000);
    await cli.stdout.waitFor("> ");
    assert.ok(Date.now() - startedAt < 40_000, "the call failed within its budget");
    cli.writeLine("say still here");
    await cli.stdout.waitFor("said: still here");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a hard link to an outside file is refused for read, write, and edit, and grep never reads it", async () => {
    const outside = realpathSync(mkdtempSync(join(tmpdir(), "dex-agents-pi-outside-")));
    writeFileSync(join(outside, "secret.txt"), "do not read");
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    linkSync(join(outside, "secret.txt"), join(home.workspace, "linked.txt"));
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read", { path: "linked.txt" }));
    await cli.stdout.waitFor("has 2 hard links");
    cli.writeLine(call("grep", { pattern: "read" }));
    await cli.stdout.waitFor("grep: No matches found");
    cli.writeLine(call("write", { path: "linked.txt", content: "overwritten" }));
    await cli.stdout.waitFor("Approve write?");
    cli.writeLine("y");
    await cli.stdout.waitFor("has 2 hard links");
    cli.writeLine(call("edit", { path: "linked.txt", edits: [{ oldText: "read", newText: "write" }] }));
    await cli.stdout.waitFor("Approve edit?");
    cli.writeLine("y");
    await cli.stdout.waitFor("has 2 hard links");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(join(outside, "secret.txt"), "utf8"), "do not read");
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
  });

  test("a write that fails after its rename records outcome_unknown", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "draft.txt"), "before");
    const cli = start(home, [], ["--import", failAfterRenameHook]);
    await cli.waitForSession();
    cli.writeLine(call("write", { path: "draft.txt", content: "after" }));
    await cli.stdout.waitFor("Approve write?");
    cli.writeLine("y");
    await cli.stdout.waitFor("write: The tool call's outcome is unknown");
    cli.writeLine(call("write", { path: "../outside.txt", content: "after" }));
    await cli.stdout.waitFor("Approve write?");
    cli.writeLine("y");
    await cli.stdout.waitFor("write: The tool call failed: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.match(lastToolResults(), /is outside the workspace/);
  });

  test("a bash call with an invalid timeout records a failure and never runs", async () => {
    const home = newHome(noConnectors);
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("bash", { command: "echo ran > ran.txt", timeout: -1 }));
    await cli.stdout.waitFor("Approve bash?");
    cli.writeLine("y");
    await cli.stdout.waitFor("bash: ");
    await cli.stdout.waitFor("> ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const toolResult = lastToolResults();
    assert.match(toolResult, /The tool call failed: timeout must be a number of seconds/);
    assert.doesNotMatch(toolResult, /outcome is unknown/);
    assert.equal(existsSync(join(home.workspace, "ran.txt")), false);
  });

  test("a bash command whose Worker dies after it started records outcome_unknown and never runs again", async () => {
    const command = "echo started >> runs.txt; sleep 20";
    const home = newHome({ ...noConnectors, local_tools: { browser: false, shell: { enabled: true, allow: [command] } } });
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
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
    assert.equal(readFileSync(runs, "utf8"), "started\n");
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
    writeFileSync(join(home.workspace, "docs", "rules.md"), "Inside workspace rules.\n");
    symlinkSync(join(home.workspace, "docs", "rules.md"), join(home.workspace, "AGENTS.md"));
    symlinkSync(join(outside, "ancestor.md"), join(home.directory, "AGENTS.md"));
    mkdirSync(join(home.directory, ".pi", "agent"), { recursive: true });
    symlinkSync(join(outside, "global.md"), join(home.directory, ".pi", "agent", "AGENTS.md"));
    const first = start(home);
    await first.waitForSession();
    first.writeLine("say hi");
    await first.stdout.waitFor("said: hi");
    assert.ok(lastSystemPrompt().includes("Inside workspace rules."), "a link inside its directory loads");
    assert.doesNotMatch(lastSystemPrompt(), /Outside (ancestor|global) instructions/);
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);
    assert.match(first.stderr.all, /skipped the project context file .*\.pi\/agent\/AGENTS\.md: .*outside the workspace/);
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
    assert.match(cli.stderr.all, /skipped the project context file .*AGENTS\.md: .*outside the workspace/);
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
