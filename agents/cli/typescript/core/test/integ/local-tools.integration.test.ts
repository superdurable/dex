// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { outcomeUnknownToolResult } from "@superdurable/dex-agents";
import { pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { assertNoDexSecret, secretSentinels } from "../fixtures/dex-secret-sentinels.js";

const formPage = `<!doctype html>
<html><head><title>Signup</title></head>
<body>
  <h1>Join the list</h1>
  <form id="signup" method="post" action="/submit">
    <label>Email <input id="email" name="email" type="email"></label>
    <button type="submit">Join</button>
  </form>
</body></html>`;

describe("local tools", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  let pageServer: Server;
  let pageUrl: string;
  const submissions: string[] = [];
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
    pageServer = createServer((request, response) => {
      if (request.method === "POST" && request.url === "/submit") {
        let body = "";
        request.on("data", (chunk: Buffer) => {
          body += chunk.toString("utf8");
        });
        request.on("end", () => {
          submissions.push(body);
          response.writeHead(200, { "content-type": "text/html" }).end("<html><body><h1>Thanks for joining</h1></body></html>");
        });
        return;
      }
      response.writeHead(200, { "content-type": "text/html" }).end(formPage);
    });
    await new Promise<void>((resolve) => pageServer.listen(0, "127.0.0.1", resolve));
    const address = pageServer.address();
    pageUrl = `http://127.0.0.1:${typeof address === "object" && address !== null ? address.port : 0}/`;
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    pageServer?.closeAllConnections();
    pageServer?.close();
    await sessionClients?.close();
    await engine?.stop();
  });

  function startWith(
    config: string,
    commandArguments: readonly string[] = [],
    home?: TestHome,
    environment: NodeJS.ProcessEnv = {},
  ): { cli: CliProcess; home: TestHome } {
    const testHome = home ?? new TestHome(enginePorts(engine));
    if (home === undefined) {
      testHome.writeConfig(config);
    }
    const cli = CliProcess.start(testHome, commandArguments, environment);
    processes.push(cli);
    return { cli, home: testHome };
  }

  test("file tools stay inside the workspace, symbolic links included, and a write waits for approval once", async () => {
    const { cli, home } = startWith("connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: false}}\n");
    await cli.waitForSession();
    writeFileSync(join(home.directory, "secret.txt"), "top secret");
    mkdirSync(join(home.directory, "outside"), { recursive: true });
    symlinkSync(join(home.directory, "secret.txt"), join(home.workspace, "link-to-secret.txt"));
    symlinkSync(join(home.directory, "outside"), join(home.workspace, "outside-dir"));

    cli.writeLine('call read_file {"path":"../secret.txt"}');
    await cli.stdout.waitFor(/result: .*outside the workspace/);
    cli.writeLine(`call read_file {"path":"${join(home.directory, "secret.txt")}"}`);
    await cli.stdout.waitFor(/result: .*outside the workspace/);
    cli.writeLine('call read_file {"path":"link-to-secret.txt"}');
    await cli.stdout.waitFor(/result: .*outside the workspace/);
    cli.writeLine('call write_file {"path":"outside-dir/planted.txt","content":"x"}');
    await cli.stdout.waitFor("Approve write_file?");
    cli.writeLine("y");
    await cli.stdout.waitFor(/result: .*outside the workspace/);
    assert.equal(existsSync(join(home.directory, "outside", "planted.txt")), false);
    assert.doesNotMatch(cli.stdout.all, /top secret/);

    cli.writeLine('call write_file {"path":"notes/today.txt","content":"buy milk"}');
    await cli.stdout.waitFor("Approve write_file?");
    cli.writeLine("y");
    await cli.stdout.waitFor("result: wrote 8 bytes to notes/today.txt");
    assert.equal(readFileSync(join(home.workspace, "notes", "today.txt"), "utf8"), "buy milk");
    cli.writeLine('call read_file {"path":"notes/today.txt"}');
    await cli.stdout.waitFor("result: buy milk");
    cli.writeLine('call search_files {"query":"milk"}');
    await cli.stdout.waitFor("result: notes/today.txt:1: buy milk");
    assert.equal(cli.stdout.all.match(/Approve write_file\?/g)?.length, 2);
    cli.writeLine('call edit_file {"path":"notes/today.txt","old_text":"milk","new_text":"oat milk"}');
    await cli.stdout.waitFor("Approve edit_file?");
    cli.writeLine("y");
    await cli.stdout.waitFor("result: edited notes/today.txt: replaced 1 occurrence");
    assert.equal(readFileSync(join(home.workspace, "notes", "today.txt"), "utf8"), "buy oat milk");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a shell command on the allow list runs at once; any other waits for approval", async () => {
    const { cli } = startWith(
      'connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: ["echo allowed"]}}\n',
    );
    await cli.waitForSession();
    cli.writeLine('call run_shell_command {"command":"echo allowed"}');
    await cli.stdout.waitFor(/result: exit code 0\n--- stdout ---\nallowed/);
    assert.doesNotMatch(cli.stdout.all, /Approve run_shell_command/);
    cli.writeLine('call run_shell_command {"command":"echo allowed && echo more"}');
    await cli.stdout.waitFor("Approve run_shell_command?");
    cli.writeLine("y");
    await cli.stdout.waitFor(/result: exit code 0\n--- stdout ---\nallowed\nmore/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a shell command never sees Dex credential variables that the shell exported, without dex.auth", async () => {
    const command = "env > shell-environment.txt";
    const testHome = new TestHome(enginePorts(engine));
    testHome.writeConfig(
      `connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: ["${command}"]}}\n`,
    );
    const cli = CliProcess.start(testHome, [], secretSentinels);
    processes.push(cli);
    await cli.waitForSession();
    cli.writeLine(`call run_shell_command ${JSON.stringify({ command })}`);
    await cli.stdout.waitFor(/result: exit code 0/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    assertNoDexSecret(readFileSync(join(testHome.workspace, "shell-environment.txt"), "utf8"));
  });

  test("the browser reads pages at once and fills and submits only after approval", async () => {
    const { cli } = startWith("connectors: {enabled: []}\nlocal_tools: {shell: {enabled: false}}\n");
    await cli.waitForSession();
    cli.writeLine(`call browser_navigate {"url":"${pageUrl}"}`);
    await cli.stdout.waitFor(/result: Page: Signup[\s\S]*Join the list/, 180_000);
    assert.doesNotMatch(cli.stdout.all, /Approve browser_navigate/);
    cli.writeLine('call browser_fill {"selector":"#email","value":"ada@example.com"}');
    await cli.stdout.waitFor("Approve browser_fill?");
    cli.writeLine("y");
    await cli.stdout.waitFor(/result: Page: Signup/);
    cli.writeLine('call browser_submit_form {"selector":"#email"}');
    await cli.stdout.waitFor("Approve browser_submit_form?");
    assert.deepEqual(submissions, []);
    cli.writeLine("y");
    await cli.stdout.waitFor(/result: [\s\S]*Thanks for joining/);
    assert.deepEqual(submissions, ["email=ada%40example.com"]);
    cli.writeLine("call browser_snapshot {}");
    await cli.stdout.waitFor(/result: [\s\S]*Thanks for joining/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a shell command interrupted by a Worker kill is never run again", async () => {
    const command = "echo started >> starts.log && while [ ! -f release ]; do sleep 0.1; done && echo finished";
    const config = `connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: [${JSON.stringify(command)}]}}\n`;
    // A short lease, so the resumed chat does not wait 600 seconds for the killed command's lease.
    const first = startWith(config, [], undefined, { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "6" });
    const sessionId = await first.cli.waitForSession();
    first.cli.writeLine(`call run_shell_command ${JSON.stringify({ command })}`);
    const startsLog = join(first.home.workspace, "starts.log");
    await pollUntil(() => existsSync(startsLog) || undefined, { description: "the shell command to start", timeoutMs: 60_000 });
    await first.cli.kill();

    try {
      const second = startWith(config, ["--resume", sessionId], first.home);
      // The resumed chat waits for the killed command's lease to expire, so the turn may end before it shows.
      await second.cli.stdout.waitFor(`Resumed session ${sessionId}`);
      const sessions = await sessionClients.of(first.home);
      const toolMessage = await pollUntil(
        async () => {
          const snapshot = await sessions.getAgentSnapshot(sessionId);
          return snapshot.state?.activeTurnId === null ? snapshot.messages.find((message) => message.role === "tool") : undefined;
        },
        { description: "the interrupted turn to end", timeoutMs: 180_000 },
      );
      assert.equal(toolMessage.toolOutput, outcomeUnknownToolResult);
      assert.equal(readFileSync(startsLog, "utf8"), "started\n");
      second.cli.writeLine("/exit");
      assert.equal(await second.cli.waitForExit(), 0);
    } finally {
      writeFileSync(join(first.home.workspace, "release"), "");
    }
    assert.equal(readFileSync(startsLog, "utf8"), "started\n");
  });
});
