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
import { after, afterEach, before, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, TestHome, enginePorts, mockApiKey, openSessionClient } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { MockResponsesApi } from "../fixtures/mock-responses-api.js";

const noConnectors = { connectors: { enabled: [] }, local_tools: { browser: false } };

function call(toolName: string, toolInput: Readonly<Record<string, unknown>>): string {
  return `call ${toolName} ${JSON.stringify(toolInput)}`;
}

describe("dex-agents-mastra with the Mastra model router against a scripted Responses API", () => {
  let engine: PrivateDexEngine;
  let api: MockResponsesApi;
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    api = await MockResponsesApi.start();
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
    await api?.stop();
    await engine?.stop();
  });

  function newHome(config: Readonly<Record<string, unknown>>): TestHome {
    const home = new TestHome(enginePorts(engine), api);
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = []): CliProcess {
    const cli = CliProcess.start(home, commandArguments);
    processes.push(cli);
    return cli;
  }

  test("a turn streams the answer, --resume continues the session, and the key is never written down", async () => {
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

    assert.equal(api.modelRequests()[0]?.["model"], "gpt-5.2");
    assert.deepEqual([...new Set(api.requests.map((request) => request.authorization))], [`Bearer ${mockApiKey}`]);
    const output = first.stdout.all + first.stderr.all + second.stdout.all + second.stderr.all;
    assert.doesNotMatch(output, new RegExp(mockApiKey));
    assert.doesNotMatch(home.stateText(), new RegExp(mockApiKey));
  });

  test("read_file runs at once, and write_file runs once after approval", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine(call("read_file", { path: "notes.txt" }));
    await cli.stdout.waitFor("read_file: buy milk");
    assert.doesNotMatch(cli.stdout.all, /Approve read_file/);
    assert.ok(api.lastOfferedToolNames().includes("write_file"));

    const todoPath = join(home.workspace, "todo.txt");
    cli.writeLine(call("write_file", { path: "todo.txt", content: "call mom" }));
    await cli.stdout.waitFor("Approve write_file?");
    assert.equal(existsSync(todoPath), false);
    cli.writeLine("y");
    await cli.stdout.waitFor("write_file: ");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(readFileSync(todoPath, "utf8"), "call mom");
    assert.equal(cli.stdout.all.match(/Approve write_file\?/g)?.length, 1);
  });

  test("a connector read runs at once and a write runs once after approval", async () => {
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
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(connectors.runCount("slack_post_thread_reply"), 1);
  });

  test("session settings change the model and instructions of the next turn and its call after a tool", async () => {
    const home = newHome(noConnectors);
    mkdirSync(home.workspace, { recursive: true });
    writeFileSync(join(home.workspace, "notes.txt"), "buy milk\n");
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine(call("read_file", { path: "notes.txt" }));
    await cli.stdout.waitFor("read_file: buy milk");
    const firstTurnRequests = api.modelRequests();
    assert.ok(firstTurnRequests.length >= 2);
    for (const request of firstTurnRequests) {
      assert.equal(request["model"], "gpt-5.2");
      assert.doesNotMatch(JSON.stringify(request), /Answer like a pirate\./);
    }

    const { environment, sessions } = await openSessionClient(engine, home.sessionFlowType());
    try {
      await sessions.updateSessionSettings(sessionId, {
        model: "openai/gpt-5-nano",
        system_prompt: "Answer like a pirate.",
      });
    } finally {
      await environment.close();
    }
    cli.writeLine(call("read_file", { path: "notes.txt" }));
    await cli.stdout.waitFor("read_file: buy milk");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const nextTurnRequests = api.modelRequests().slice(firstTurnRequests.length);
    // The first request calls the tool; the last one carries its result.
    assert.ok(nextTurnRequests.length >= 2);
    for (const request of nextTurnRequests) {
      assert.equal(request["model"], "gpt-5-nano");
      assert.match(JSON.stringify(request), /Answer like a pirate\./);
    }
    assert.match(JSON.stringify(nextTurnRequests.at(-1)), /buy milk/);
  });

  test("a key in the mastra section is a configuration error", async () => {
    const cli = start(newHome({ ...noConnectors, mastra: { max_turns: 3 } }));
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /schema at "\/mastra\/max_turns"/);
    assert.deepEqual(api.modelRequests(), []);
  });
});
