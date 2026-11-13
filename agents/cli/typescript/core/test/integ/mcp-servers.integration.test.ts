// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { promisify } from "node:util";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeRemoteMcpServer } from "../fixtures/fake-remote-mcp-server.js";

const apiKeySentinel = "remote-mcp-api-key-sentinel-7";

describe("remote MCP servers from mcp_servers", () => {
  let engine: PrivateDexEngine;
  let remote: FakeRemoteMcpServer;
  let remoteUrl: string;
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    remote = new FakeRemoteMcpServer(apiKeySentinel);
    remoteUrl = await remote.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    await remote?.stop();
    await engine?.stop();
  });

  function newHome(serverFields: readonly string[], extra: readonly string[] = []): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(
      [
        "connectors: {enabled: []}",
        "local_tools: {browser: false}",
        "mcp_servers:",
        "  studio:",
        `    url: ${remoteUrl}`,
        "    headers:",
        '      Authorization: "Bearer ${STUDIO_API_KEY}"',
        ...serverFields.map((field) => `    ${field}`),
        ...extra,
        "",
      ].join("\n"),
    );
    return home;
  }

  function start(home: TestHome, environment: NodeJS.ProcessEnv = { STUDIO_API_KEY: apiKeySentinel }): CliProcess {
    const cli = CliProcess.start(home, [], environment);
    processes.push(cli);
    return cli;
  }

  function logFileOf(home: TestHome): string {
    const path = join(home.directory, ".dex", "agents", `${home.harness.name}.log`);
    return existsSync(path) ? readFileSync(path, "utf8") : "";
  }

  async function sessionHistory(home: TestHome, sessionId: string): Promise<string> {
    const { stdout } = await promisify(execFile)(home.environment().DEXCLI ?? "dexcli", [
      "flow",
      "history",
      sessionId,
      "--server",
      engine.serverAddress,
      "--output",
      "json",
    ]);
    return stdout;
  }

  test("a stateless server's tools are listed and called with the header from the environment, which is never stored", async () => {
    const home = newHome([]);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("tools");
    await cli.stdout.waitFor(/tools: .*studio__publish_asset,studio__search_assets/);
    // Without trust_tool_annotations, even the read-only tool waits for approval.
    cli.writeLine('call studio__search_assets {"query":"logo"}');
    await cli.stdout.waitFor("Approve studio__search_assets?");
    cli.writeLine("y");
    await cli.stdout.waitFor("result: assets for logo");
    cli.writeLine('call studio__publish_asset {"name":"banner"}');
    await cli.stdout.waitFor("Approve studio__publish_asset?");
    cli.writeLine("n not now");
    await cli.stdout.waitFor(/result: .*not now/);
    const history = await sessionHistory(home, sessionId);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);

    assert.equal(remote.runsOf("search_assets").filter((run) => run.arguments.query === "logo").length, 1);
    assert.equal(remote.runsOf("publish_asset").filter((run) => run.arguments.name === "banner").length, 0);
    assert.ok(remote.authorizedRequests > 0);
    assert.equal(remote.hasSeenSessionId, false, "the server is stateless");
    assert.match(history, /studio__search_assets/);
    for (const [where, text] of [
      ["stdout", cli.stdout.all],
      ["stderr", cli.stderr.all],
      ["log file", logFileOf(home)],
      ["session history", history],
    ] as const) {
      assert.ok(!text.includes(apiKeySentinel), `the API key reached the ${where}`);
    }
  });

  test("trusted annotations run reads and idempotent writes at once, other writes wait, and approval.always gates a read", async () => {
    const trusted = start(newHome(["trust_tool_annotations: true"]));
    await trusted.waitForSession();
    trusted.writeLine('call studio__search_assets {"query":"trusted"}');
    await trusted.stdout.waitFor("result: assets for trusted");
    trusted.writeLine('call studio__tag_asset {"name":"logo","tag":"brand"}');
    await trusted.stdout.waitFor("result: tagged logo brand");
    assert.doesNotMatch(trusted.stdout.all, /Approve studio__(search|tag)_asset/);
    trusted.writeLine('call studio__publish_asset {"name":"poster"}');
    await trusted.stdout.waitFor("Approve studio__publish_asset?");
    trusted.writeLine("y");
    await trusted.stdout.waitFor("result: published poster");
    trusted.writeLine("/exit");
    assert.equal(await trusted.waitForExit(), 0);

    const gated = start(newHome(["trust_tool_annotations: true"], ["approval: {always: [studio__search_assets]}"]));
    await gated.waitForSession();
    gated.writeLine('call studio__search_assets {"query":"gated"}');
    await gated.stdout.waitFor("Approve studio__search_assets?");
    gated.writeLine("y");
    await gated.stdout.waitFor("result: assets for gated");
    gated.writeLine("/exit");
    assert.equal(await gated.waitForExit(), 0);
  });

  test("an unset header variable stops the command with exit code 2 and names the variable", async () => {
    const home = newHome([]);
    const cli = start(home, {});
    assert.equal(await cli.waitForExit(), 2);
    assert.match(
      cli.stderr.all,
      /mcp_servers\.studio\.headers\.Authorization needs the environment variable STUDIO_API_KEY, which is not set or is empty\./,
    );
    assert.doesNotMatch(cli.stderr.all, /Starting dexcli dev/);
  });

  test("a server that rejects the header offers no tools and says so once", async () => {
    const home = newHome([]);
    const cli = start(home, { STUDIO_API_KEY: "wrong-key" });
    await cli.waitForSession();
    await cli.stderr.waitFor("MCP server tools are unavailable for now; each turn tries again.");
    cli.writeLine("tools");
    await cli.stdout.waitFor(/tools: /);
    assert.doesNotMatch(cli.stdout.all, /studio__/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(cli.stderr.all.split("MCP server tools are unavailable for now").length - 1, 1);
    assert.match(logFileOf(home), /could not list the tools of MCP server studio/);
    assert.ok(!(cli.stdout.all + cli.stderr.all + logFileOf(home)).includes("wrong-key"));
  });
});
