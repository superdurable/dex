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
import { basename, join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, RefusingProxy, TestHome, enginePorts, mockApiKey, openSessionClient } from "../fixtures/cli-test-support.js";
import { MockMessagesApi } from "../fixtures/mock-messages-api.js";

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

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  function offeredToolNames(): string[] {
    const tools = (api.modelRequests().at(-1)?.["tools"] as Record<string, unknown>[] | undefined) ?? [];
    return tools.map((tool) => String(tool["name"]));
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
      await cli.stdout.waitFor("read may only use paths inside the workspace");
    }
    cli.writeLine(call("ls", { path: outside }));
    await cli.stdout.waitFor("ls may only use paths inside the workspace");
    cli.writeLine(call("write", { path: "link.txt", content: "x" }));
    await cli.stdout.waitFor("write may only use paths inside the workspace");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.doesNotMatch(cli.stdout.all, /Approve (read|ls|write)/);
    assert.doesNotMatch(JSON.stringify(api.modelRequests()), /do not read/);
    assert.equal(existsSync(join(outside, "created.txt")), false);
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

  test("without an API key the command stops and names the variable, even with an OAuth token", async () => {
    const home = newHome(noConnectors);
    delete home.providerEnvironment["ANTHROPIC_API_KEY"];
    home.providerEnvironment["ANTHROPIC_OAUTH_TOKEN"] = "sk-ant-oat-mock-token";
    const cli = start(home);
    assert.equal(await cli.waitForExit(), 1);
    assert.match(cli.stderr.all, /no API key for the pi-ai provider anthropic: set ANTHROPIC_API_KEY/);
    assert.deepEqual(api.modelRequests(), []);
  });

  test("an unknown key in the pi section is a configuration error", async () => {
    const cli = start(newHome({ ...noConnectors, pi: { max_turns: 3 } }));
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /schema at "\/pi\/max_turns"/);
    assert.deepEqual(api.modelRequests(), []);
  });
});
