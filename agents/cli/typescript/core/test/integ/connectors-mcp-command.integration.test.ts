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

import { findFreeLoopbackPorts, pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { CliProcess, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";

// dex.server names a server, so the dexcli dev ports are never used; listing connectors makes no Dex call.
const remoteServerConfig = "dex: {server: 'http://127.0.0.1:1'}\nconnectors: {mcp: auto}\n";
const autoServerConfig = "connectors: {mcp: auto}\n";

/**
 * Serves `connectors mcp` from the test's variables and passes every other command to the real dexcli. Each mode
 * other than unsupported and error waits for stdin to close, then records whether the Dex Web of dexcli dev still
 * answered.
 */
const connectorsMcpDexcliScript = `#!/bin/sh
if [ "$1" = "connectors" ] && [ "$2" = "mcp" ]; then
  markers="$DEX_TEST_MARKER_DIRECTORY"
  echo "$@" > "$markers/connectors-mcp.args"
  echo $$ > "$markers/connectors-mcp.pid"
  case "$DEX_TEST_CONNECTORS_MCP_MODE" in
    unsupported) echo '{"error":{"kind":"usage","message":"unknown command \\"connectors\\""}}' >&2; exit 2 ;;
    error) echo '{"type":"error","message":"fake startup failure"}'; exit 1 ;;
    hang) ;;
    empty-line) echo "" ;;
    close-stdout) exec 1>&- ;;
    null) echo null ;;
    bad-url) echo '{"type":"ready","url":"http://["}' ;;
    huge) head -c 1100000 /dev/zero | tr '\\0' a ;;
    orphan)
      sh -c 'trap "" TERM; echo $$ > "$0/descendant.pid"; while :; do sleep 1; done' "$markers" &
      while [ ! -s "$markers/descendant.pid" ]; do sleep 0.05; done
      printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$" ;;
    *) printf '{"type":"ready","url":"%s","pid":%s}\\n' "$DEX_TEST_CONNECTORS_MCP_URL" "$$" ;;
  esac
  cat > /dev/null
  curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$DEX_AGENTS_WEB_PORT/readyz" > "$markers/dev-at-stop" 2>/dev/null
  touch "$markers/connectors-mcp.stopped"
  exit 0
fi
exec "$DEX_TEST_REAL_DEXCLI" "$@"
`;

type ConnectorsMcpMode =
  | "ready"
  | "unsupported"
  | "error"
  | "hang"
  | "empty-line"
  | "close-stdout"
  | "null"
  | "bad-url"
  | "huge"
  | "orphan";

/** A test home whose `DEXCLI` is {@link connectorsMcpDexcliScript}. */
class ConnectorsMcpDexcliHome extends TestHome {
  private readonly wrapperPath = join(this.directory, "dexcli-wrapper.sh");

  public constructor(
    ports: { readonly dexPort: number; readonly webPort: number },
    private readonly connectorsMcpUrl: string,
    private readonly mode: ConnectorsMcpMode = "ready",
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

  /** Reads a pid the wrapper recorded, or `undefined` before it did. */
  public recordedPid(name: "connectors-mcp.pid" | "descendant.pid"): number | undefined {
    const path = join(this.markerDirectory, name);
    const text = existsSync(path) ? readFileSync(path, "utf8").trim() : "";
    return text === "" ? undefined : Number(text);
  }

  /** The HTTP status Dex Web of dexcli dev answered when `dexcli connectors mcp` stopped. */
  public devStatusAtStop(): string {
    return readFileSync(join(this.markerDirectory, "dev-at-stop"), "utf8").trim();
  }
}

function isProcessAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
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

  test("a remote dex.server with a dexcli that may lack the command names the version it needs", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, "unsupported");
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 1);
    assert.match(
      listed.stdout,
      /^Connectors are off: \S+dexcli-wrapper\.sh may not support dexcli connectors mcp, which needs dexcli 1\.47\.7 or later; see \S+dexcli-connectors-mcp\.log\. Update it, or set connectors\.mcp to a URL\.$/m,
    );
  });

  for (const [mode, reason] of [
    ["empty-line", /dexcli connectors mcp printed an empty line/],
    ["close-stdout", /dexcli connectors mcp closed its output without a line/],
    ["null", /unexpected output: null/],
    ["bad-url", /unexpected output: \{"type":"ready","url":"http:\/\/\["\}/],
    ["huge", /dexcli connectors mcp printed more than 1048576 bytes without a newline/],
  ] as const) {
    test(`a remote dex.server whose dexcli connectors mcp prints ${mode} stops it and keeps Connector tools off`, async () => {
      const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, mode);
      home.writeConfig(remoteServerConfig);
      home.writeConnections([slackConnection]);

      const listed = await listConnectors(home);
      assert.equal(listed.exitCode, 1, listed.stdout);
      const offLine = listed.stdout.split("\n")[0] ?? "";
      assert.match(offLine, /^Connectors are off: dexcli connectors mcp failed: /);
      assert.match(offLine, reason);
      assert.ok(offLine.length < 1_000, "the line carries a bounded reason");
      assert.ok(home.isConnectorsMcpStopped(), "the command stopped the child before it went on");
    });
  }

  test("stopping kills a descendant that ignores SIGTERM after the leader exits", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, "orphan");
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);

    const listed = await listConnectors(home);
    assert.equal(listed.exitCode, 0, listed.stdout);
    const descendant = home.recordedPid("descendant.pid");
    assert.ok(descendant !== undefined);
    // The killed orphan may stay a zombie until init reaps it.
    await pollUntil(() => (isProcessAlive(descendant) ? undefined : true), {
      description: "the descendant that ignores SIGTERM to be gone",
      timeoutMs: 5_000,
    });
  });

  test("a SIGTERM while it waits for the ready line with a remote dex.server stops it and exits 143", async () => {
    const home = new ConnectorsMcpDexcliHome({ dexPort: 1, webPort: 2 }, connectorsUrl, "hang");
    home.writeConfig(remoteServerConfig);
    home.writeConnections([slackConnection]);
    const cli = CliProcess.start(home, ["connectors"]);
    processes.push(cli);

    await pollUntil(() => home.recordedPid("connectors-mcp.pid"), {
      description: "dexcli connectors mcp to start",
    });
    cli.signal("SIGTERM");
    assert.equal(await cli.waitForExit(), 143, cli.stderr.all);
    assert.ok(home.isConnectorsMcpStopped(), "its stdin closed while the command ran");
  });

  test("a SIGTERM while it waits for the ready line stops it before the dexcli dev it started", async () => {
    const [dexPort, webPort] = await findFreeLoopbackPorts(2);
    assert.ok(dexPort !== undefined && webPort !== undefined);
    const home = new ConnectorsMcpDexcliHome({ dexPort, webPort }, connectorsUrl, "hang");
    home.writeConfig(autoServerConfig);
    const cli = CliProcess.start(home, ["connectors"]);
    processes.push(cli);

    await pollUntil(() => home.recordedPid("connectors-mcp.pid"), {
      description: "dexcli connectors mcp to start after dexcli dev",
      timeoutMs: 120_000,
    });
    cli.signal("SIGTERM");
    assert.equal(await cli.waitForExit(), 143, cli.stderr.all);
    assert.ok(home.isConnectorsMcpStopped(), "its stdin closed while the command ran");
    assert.equal(home.devStatusAtStop(), "200", "dexcli dev still ran when it stopped");
    assert.match(cli.stderr.all, /Stopped the dexcli dev this command started\./);
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
