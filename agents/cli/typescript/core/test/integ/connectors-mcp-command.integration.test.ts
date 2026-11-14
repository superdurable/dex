// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// connectors.mcp: auto runs dexcli connectors mcp for this machine's connections.json, with a remote dex.server and
// with dex.server: auto, and stops it when the command exits.

import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";

// dex.server names a server, so the dexcli dev ports are never used; listing connectors makes no Dex call.
const remoteServerConfig = "dex: {server: 'http://127.0.0.1:1'}\nconnectors: {mcp: auto}\n";
const autoServerConfig = "connectors: {mcp: auto}\n";

/** Serves `connectors mcp` from the test's variables and passes every other command to the real dexcli. */
const connectorsMcpDexcliScript = `#!/bin/sh
if [ "$1" = "connectors" ] && [ "$2" = "mcp" ]; then
  echo "$@" > "$DEX_TEST_MARKER_DIRECTORY/connectors-mcp.args"
  case "$DEX_TEST_CONNECTORS_MCP_MODE" in
    unsupported) echo '{"error":{"kind":"usage","message":"unknown command \\"connectors\\""}}' >&2; exit 2 ;;
    error) echo '{"type":"error","message":"fake startup failure"}'; exit 1 ;;
  esac
  printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$"
  cat > /dev/null
  touch "$DEX_TEST_MARKER_DIRECTORY/connectors-mcp.stopped"
  exit 0
fi
exec "$DEX_TEST_REAL_DEXCLI" "$@"
`;

/** A test home whose `DEXCLI` is {@link connectorsMcpDexcliScript}. */
class ConnectorsMcpDexcliHome extends TestHome {
  private readonly wrapperPath = join(this.directory, "dexcli-wrapper.sh");

  public constructor(
    ports: { readonly dexPort: number; readonly webPort: number },
    private readonly connectorsMcpUrl: string,
    private readonly mode: "ready" | "unsupported" | "error" = "ready",
  ) {
    super(ports);
    writeFileSync(this.wrapperPath, connectorsMcpDexcliScript, { mode: 0o755 });
  }

  public override environment(): NodeJS.ProcessEnv {
    const environment = super.environment();
    return {
      ...environment,
      DEXCLI: this.wrapperPath,
      DEX_TEST_REAL_DEXCLI: environment.DEXCLI,
      DEX_TEST_MARKER_DIRECTORY: this.markerDirectory,
      DEX_TEST_CONNECTORS_MCP_URL: this.connectorsMcpUrl,
      DEX_TEST_CONNECTORS_MCP_MODE: this.mode,
    };
  }

  /** Writes `~/.dex/connectors/connections.json` with the given connections. */
  public writeConnections(connections: readonly object[]): void {
    const directory = join(this.directory, ".dex", "connectors");
    mkdirSync(directory, { recursive: true });
    writeFileSync(
      join(directory, "connections.json"),
      JSON.stringify({ schemaVersion: "connectors.dex.dev/local-connections/v1alpha1", connections }),
    );
  }

  /** The arguments the command passed to `dexcli connectors mcp`, or `undefined` when it did not run it. */
  public connectorsMcpArguments(): string | undefined {
    const path = join(this.markerDirectory, "connectors-mcp.args");
    return existsSync(path) ? readFileSync(path, "utf8").trim() : undefined;
  }

  /** Whether `dexcli connectors mcp` saw its stdin close and stopped. */
  public isConnectorsMcpStopped(): boolean {
    return existsSync(join(this.markerDirectory, "connectors-mcp.stopped"));
  }
}

const slackConnection = { connectorId: "slack", connectionName: "support-slack" };

describe("connectors.mcp: auto runs dexcli connectors mcp", () => {
  let connectors: FakeConnectorsMcpServer;
  let connectorsUrl: string;
  let engine: PrivateDexEngine;
  const processes: CliProcess[] = [];

  before(async () => {
    connectors = new FakeConnectorsMcpServer();
    connectorsUrl = await connectors.start();
    engine = await PrivateDexEngine.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.terminate();
    }
  });

  after(async () => {
    await connectors?.stop();
    await engine?.stop();
  });

  async function listConnectors(home: ConnectorsMcpDexcliHome): Promise<{ exitCode: number | null; stdout: string }> {
    const cli = CliProcess.start(home, ["connectors"]);
    processes.push(cli);
    const exitCode = await cli.waitForExit();
    return { exitCode, stdout: cli.stdout.all };
  }

  test("a remote dex.server lists the tools of this machine's connections and stops the command at exit", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl);
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 0, listed.stdout);
    assert.match(listed.stdout, new RegExp(`^Connectors MCP server: ${connectorsUrl}$`, "m"));
    assert.match(listed.stdout, /^ {2}slack\/support-slack: /m);
    assert.equal(home.connectorsMcpArguments(), "connectors mcp --exit-on-stdin-close");
    await pollUntil(() => (home.isConnectorsMcpStopped() ? true : undefined), {
      description: "dexcli connectors mcp to stop when its stdin closes",
    });
  });

  test("a remote dex.server without a local connection keeps Connector tools off with a hint", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl);
    home.writeConfig(remoteServerConfig);
    home.writeConnections([]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 1);
    assert.equal(
      listed.stdout,
      "Connectors are off: this machine has no authorized Connector connection. Authorize one on the Connectors " +
        "page of Dex Web in a local dexcli dev, then run this command again.\n",
    );
    assert.equal(home.connectorsMcpArguments(), undefined, "no dexcli runs without a connection");
  });

  test("a remote dex.server with a dexcli older than 1.47.7 names the version it needs", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, "unsupported");
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 1);
    assert.match(
      listed.stdout,
      /^Connectors are off: connectors\.mcp is auto, which needs dexcli 1\.47\.7 or later to serve this machine's Connectors, and \S+dexcli-wrapper\.sh is older\. Update it, or set connectors\.mcp to a URL\.$/m,
    );
  });

  test("a remote dex.server whose dexcli connectors mcp fails says why", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, "error");
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 1);
    assert.match(listed.stdout, /^Connectors are off: dexcli connectors mcp failed: fake startup failure; see \S+dexcli-connectors-mcp\.log\.$/m);
  });

  test("dex.server: auto uses dexcli connectors mcp after it finds dexcli dev, and leaves dexcli dev running", async () => {
    const home = new ConnectorsMcpDexcliHome(enginePorts(engine), connectorsUrl);
    home.writeConfig(autoServerConfig);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 0, listed.stdout);
    assert.match(listed.stdout, new RegExp(`^Connectors MCP server: ${connectorsUrl}$`, "m"));
    await pollUntil(() => (home.isConnectorsMcpStopped() ? true : undefined), {
      description: "dexcli connectors mcp to stop when its stdin closes",
    });
    const readiness = await fetch(`${engine.webUrl}/readyz`);
    assert.equal(readiness.ok, true, "the reused dexcli dev keeps running");
  });

  test("dex.server: auto falls back to the dexcli dev URL when dexcli connectors mcp fails", async () => {
    const home = new ConnectorsMcpDexcliHome(enginePorts(engine), connectorsUrl, "error");
    home.writeConfig(autoServerConfig);

    const listed = await listConnectors(home);
    assert.match(listed.stdout, new RegExp(`Connectors MCP server ${engine.webUrl}/mcp is unavailable: `));
  });
});
