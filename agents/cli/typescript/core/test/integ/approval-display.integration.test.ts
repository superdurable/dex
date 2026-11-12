// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdirSync, realpathSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { maxApprovalSummaryBytes, type AgentInterrupt, type AgentInterruptDisplay } from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";

describe("approval displays of local and connector tools", () => {
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

  function start(config: string): { cli: CliProcess; home: TestHome } {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(config);
    const cli = CliProcess.start(home);
    processes.push(cli);
    return { cli, home };
  }

  /** Sends one tool call, waits for its approval, and returns the pending interrupt, then rejects it. */
  async function approvalOf(
    cli: CliProcess,
    home: TestHome,
    sessionId: string,
    toolName: string,
    toolArguments: Readonly<Record<string, unknown>>,
  ): Promise<AgentInterrupt> {
    cli.writeLine(`call ${toolName} ${JSON.stringify(toolArguments)}`);
    await cli.stdout.waitFor(`Approve ${toolName}?`);
    const client = await sessionClients.of(home);
    const pending = await client.readPendingInterrupts(sessionId);
    assert.equal(pending.length, 1);
    const events = await client.listAgentEvents(sessionId);
    const raised = events.filter((event) => event.type === "interrupt_raised").at(-1);
    assert.deepEqual(JSON.parse(raised?.payload ?? "{}").display, {
      target: pending[0]!.display?.target,
      summary: pending[0]!.display?.summary,
      preview: pending[0]!.display?.preview ?? null,
    });
    cli.writeLine("n not now");
    await cli.stdout.waitFor(/result: .*not now/);
    return pending[0]!;
  }

  function displayOf(interrupt: AgentInterrupt): AgentInterruptDisplay {
    assert.ok(interrupt.display !== undefined && interrupt.display !== null, "the approval has a display");
    return interrupt.display;
  }

  test("a file write carries its path and a diff against the file now; a shell command its command and directory", async () => {
    const { cli, home } = start("connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true}}\n");
    const sessionId = await cli.waitForSession();
    mkdirSync(join(home.workspace, "notes"), { recursive: true });
    writeFileSync(join(home.workspace, "notes", "today.txt"), "one\ntwo\nthree\n");

    const write = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", { path: "notes/today.txt", content: "one\nTWO\nthree\n" }),
    );
    assert.deepEqual(write, {
      target: "notes/today.txt",
      summary: "Writes 14 bytes to notes/today.txt.",
      preview: [
        "--- notes/today.txt (the file now)",
        "+++ notes/today.txt (after this write)",
        "@@ -1,3 +1,3 @@",
        " one",
        "-two",
        "+TWO",
        " three",
        "",
      ].join("\n"),
    });

    const created = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", { path: "drafts/new.md", content: "# Title\n" }),
    );
    assert.deepEqual(created, {
      target: "drafts/new.md",
      summary: "Writes 8 bytes to drafts/new.md.",
      preview: "--- /dev/null (no file now)\n+++ drafts/new.md (after this write)\n@@ -0,0 +1 @@\n+# Title\n",
    });

    const edit = displayOf(
      await approvalOf(cli, home, sessionId, "edit_file", { path: "notes/today.txt", old_text: "three", new_text: "3" }),
    );
    assert.equal(edit.summary, "Replaces 5 bytes with 1 byte in notes/today.txt.");
    assert.equal(
      edit.preview,
      "--- notes/today.txt (the file now)\n+++ notes/today.txt (after this write)\n@@ -1,3 +1,3 @@\n one\n two\n-three\n+3\n",
    );

    const missingEdit = displayOf(
      await approvalOf(cli, home, sessionId, "edit_file", { path: "notes/today.txt", old_text: "absent", new_text: "x" }),
    );
    assert.equal(missingEdit.preview, null);

    const shell = displayOf(await approvalOf(cli, home, sessionId, "run_shell_command", { command: "npm test -- --watch=false" }));
    const workspace = realpathSync.native(home.workspace);
    assert.deepEqual(shell, {
      target: workspace,
      summary: `Runs a shell command in ${workspace}: npm test -- --watch=false`,
      preview: null,
    });

    // Inline credentials are redacted in the display; the arguments keep the full command.
    const secretCommand = 'curl -H "Authorization: Bearer abc123456" https://ci:hunter2@api.example.com?token=t1';
    const secretShell = await approvalOf(cli, home, sessionId, "run_shell_command", { command: secretCommand });
    assert.equal(
      displayOf(secretShell).summary,
      `Runs a shell command in ${workspace}: curl -H "Authorization: Bearer <redacted>" https://ci:<redacted>@api.example.com?token=<redacted>`,
    );
    assert.equal(JSON.parse(secretShell.arguments).command, secretCommand);

    // A long command is cut to maxApprovalSummaryBytes in the display.
    const longCommand = `echo ${"x".repeat(20 * 1024)}`;
    const longShell = await approvalOf(cli, home, sessionId, "run_shell_command", { command: longCommand });
    assert.equal(Buffer.byteLength(displayOf(longShell).summary, "utf8"), maxApprovalSummaryBytes);
    assert.ok(displayOf(longShell).summary.endsWith("…"));
    assert.equal(JSON.parse(longShell.arguments).command, longCommand);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a preview is left out when it is too large, for a credential file, and for a path outside the workspace", async () => {
    const { cli, home } = start("connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: false}}\n");
    const sessionId = await cli.waitForSession();
    writeFileSync(join(home.directory, "outside.txt"), "outside secret\n");
    symlinkSync(join(home.directory, "outside.txt"), join(home.workspace, "linked.txt"));
    writeFileSync(join(home.workspace, ".env"), "API_KEY=old-value\n");

    const huge = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", { path: "huge.txt", content: "x".repeat(70 * 1024) }),
    );
    assert.deepEqual(huge, { target: "huge.txt", summary: `Writes ${70 * 1024} bytes to huge.txt.`, preview: null });

    const manyLines = Array.from({ length: 300 }, (_, index) => `line ${index}`).join("\n");
    const long = displayOf(await approvalOf(cli, home, sessionId, "write_file", { path: "long.txt", content: manyLines }));
    assert.equal(long.preview, null);

    const credentials = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", { path: ".env", content: "API_KEY=new-value\n" }),
    );
    assert.deepEqual(credentials, { target: ".env", summary: "Writes 18 bytes to .env.", preview: null });

    const linked = displayOf(await approvalOf(cli, home, sessionId, "write_file", { path: "linked.txt", content: "x" }));
    assert.deepEqual(linked, { target: "linked.txt", summary: "Writes 1 byte to linked.txt.", preview: null });
    const withSecret = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", {
        path: "notes/settings.json",
        content: '{"credentials": "topsecret"}\n',
      }),
    );
    assert.equal(withSecret.preview, null);
    const secretsFile = displayOf(
      await approvalOf(cli, home, sessionId, "write_file", { path: "config/secrets.yaml", content: "a: 1\n" }),
    );
    assert.equal(secretsFile.preview, null);
    const pendingText = JSON.stringify(await (await sessionClients.of(home)).listAgentEvents(sessionId));
    assert.doesNotMatch(pendingText, /outside secret|old-value|topsecret/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a connector write carries its operation, connection, and short arguments, with credentials redacted", async () => {
    const { cli, home } = start(
      ["connectors:", `  mcp: ${connectorsUrl}`, "  enabled: [slack/support-slack]", "local_tools: {browser: false}"].join("\n"),
    );
    const sessionId = await cli.waitForSession();
    const display = displayOf(
      await approvalOf(cli, home, sessionId, "slack_post_thread_reply", {
        channel: "#eng",
        text: "Found 3 open issues",
        token: "xoxb-secret",
        body: "line one\nline two",
      }),
    );
    assert.deepEqual(display, {
      target: "slack/support-slack",
      summary:
        'Calls slack postThreadReply with connection support-slack; channel: "#eng", text: "Found 3 open issues", token: <redacted>.',
      preview: null,
    });
    assert.doesNotMatch(JSON.stringify(display), /xoxb-secret|line one/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });
});
