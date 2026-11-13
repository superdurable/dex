// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The commands against a remote Dex server that requires a credential: TLS with a private CA, an API key, a bearer
// token, the startup errors, and what the terminal UI receives.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { describeRefusedDexCredential } from "../../src/dex-connection.js";
import { assertNoDexSecret, secretSentinels } from "../fixtures/dex-secret-sentinels.js";
import { AuthenticatedDexServer, acceptedApiKey, acceptedServerId } from "../fixtures/authenticated-dex-server.js";
import { CliProcess, TestHome } from "../fixtures/cli-test-support.js";
import {
  FakeTerminalUi,
  PseudoTerminalCli,
  readTranscript,
  type Transcript,
} from "../fixtures/terminal-ui-test-support.js";

// dex.server names a server, so the dexcli dev ports are never used.
const unusedPorts = { dexPort: 1, webPort: 2 };
const quietSections = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
const shellEnvironmentCommand = "env > shell-environment.txt";

describe("a remote Dex server that requires a credential", () => {
  let server: AuthenticatedDexServer;
  const processes: CliProcess[] = [];
  const terminals: PseudoTerminalCli[] = [];

  before(async () => {
    server = await AuthenticatedDexServer.start();
  });

  afterEach(async () => {
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
    for (const terminal of terminals.splice(0)) {
      await terminal.kill();
    }
  });

  after(async () => {
    await server?.stop();
  });

  function newHome(dexSection: string): TestHome {
    const home = new TestHome(unusedPorts);
    home.writeConfig(`${dexSection}${quietSections}`);
    return home;
  }

  function apiKeyOverTls(): string {
    return [
      "dex:",
      `  server: ${server.tlsUrl}`,
      "  auth:",
      "    mode: api_key",
      `    token_url: ${server.tokenUrl}`,
      `    server_id: ${acceptedServerId}`,
      "  tls:",
      `    ca_file: ${server.caFile}`,
      "",
    ].join("\n");
  }

  function bearerEnv(): string {
    return `dex:\n  server: ${server.plaintextUrl}\n  auth: {mode: bearer, source: env}\n`;
  }

  function start(home: TestHome, commandArguments: readonly string[], environment: NodeJS.ProcessEnv): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
  }

  async function chatOneTurn(home: TestHome, environment: NodeJS.ProcessEnv): Promise<string> {
    const cli = start(home, [], environment);
    const sessionId = await cli.waitForSession();
    cli.writeLine("hello there");
    await cli.stdout.waitFor("echo: hello there");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    return sessionId;
  }

  async function listSessions(home: TestHome, environment: NodeJS.ProcessEnv): Promise<string> {
    const cli = start(home, ["sessions"], environment);
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    return cli.stdout.all;
  }

  test("an API key over TLS with a private CA: a chat turn completes, and sessions lists the session", async () => {
    const home = newHome(apiKeyOverTls());
    const exchangesBefore = server.exchangeCount;
    const environment = { DEX_API_KEY: ` ${acceptedApiKey}\n` };
    const sessionId = await chatOneTurn(home, environment);
    assert.match(await listSessions(home, environment), new RegExp(`^${sessionId}\\trunning\\t`, "m"));
    assert.ok(server.exchangeCount > exchangesBefore, "the command exchanged its API key");
  });

  test("a bearer token from DEX_CREDENTIAL over plaintext loopback", async () => {
    const home = newHome(bearerEnv());
    const sessionId = await chatOneTurn(home, { DEX_CREDENTIAL: server.token });
    assert.match(await listSessions(home, { DEX_CREDENTIAL: server.token }), new RegExp(`^${sessionId}\\t`, "m"));
  });

  test("a missing DEX_API_KEY stops the command at startup with exit code 2", async () => {
    const cli = start(newHome(apiKeyOverTls()), [], {});
    assert.equal(await cli.waitForExit(), 2);
    assert.match(cli.stderr.all, /: DEX_API_KEY is not set; dex\.auth mode api_key needs it\.\n/);
  });

  test("a key the token endpoint refuses names DEX_API_KEY and never shows the key", async () => {
    const wrongKey = "dex_wrong_key_for_agents_cli";
    const cli = start(newHome(apiKeyOverTls()), [], { DEX_API_KEY: wrongKey });
    assert.equal(await cli.waitForExit(), 1);
    assert.match(
      cli.stderr.all,
      new RegExp(
        `could not exchange DEX_API_KEY for an access token at ${server.tokenUrl.replaceAll(".", "\\.")} \\(HTTP status 401\\)\\. ` +
          "Check DEX_API_KEY, dex\\.auth\\.token_url, and dex\\.auth\\.server_id\\.",
      ),
    );
    assert.ok(!cli.stderr.all.includes(wrongKey) && !cli.stdout.all.includes(wrongKey));
  });

  test("a bearer token the server refuses names DEX_CREDENTIAL", async () => {
    const wrongToken = "not-the-server-token-123";
    const cli = start(newHome(bearerEnv()), [], { DEX_CREDENTIAL: wrongToken });
    assert.equal(await cli.waitForExit(), 1);
    assert.match(
      cli.stderr.all,
      new RegExp(
        `the Dex server at 127\\.0\\.0\\.1:\\d+ rejected the bearer token in DEX_CREDENTIAL \\(UNAUTHENTICATED\\)\\. Check DEX_CREDENTIAL\\.\n`,
      ),
    );
    assert.ok(!cli.stderr.all.includes(wrongToken));
  });

  test("a shell command never sees the credential the command uses, or other Dex credential variables", async () => {
    const home = new TestHome(unusedPorts);
    home.writeConfig(
      `${bearerEnv()}connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {allow: ["${shellEnvironmentCommand}"]}}\n`,
    );
    const cli = start(home, [], { ...secretSentinels, DEX_CREDENTIAL: server.token });
    await cli.waitForSession();
    cli.writeLine(`call run_shell_command ${JSON.stringify({ command: shellEnvironmentCommand })}`);
    await cli.stdout.waitFor(/result: exit code 0/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    const shellEnvironment = readFileSync(join(home.workspace, "shell-environment.txt"), "utf8");
    assertNoDexSecret(shellEnvironment);
    assert.ok(!shellEnvironment.includes(server.token));
  });

  test("the terminal UI gets dex.tls, dex.auth, and only the one auth variable", async () => {
    const cases = [
      {
        name: "api_key",
        dexSection: apiKeyOverTls(),
        environment: { DEX_API_KEY: acceptedApiKey },
        tls: { enabled: true, ca_file: server.caFile, server_name: null },
        auth: { mode: "api_key", token_url: server.tokenUrl, server_id: acceptedServerId },
        variable: "DEX_API_KEY",
      },
      {
        name: "bearer file",
        dexSection: `dex:\n  server: ${server.plaintextUrl}\n  auth: {mode: bearer, source: file, file: ${server.tokenFile}}\n`,
        environment: {},
        tls: { enabled: false, ca_file: null, server_name: null },
        auth: { mode: "bearer", source: "file" },
        variable: "DEX_CREDENTIAL_FILE",
      },
    ];
    for (const testCase of cases) {
      const fake = new FakeTerminalUi([launchComparing(testCase.tls, testCase.auth)]);
      const terminal = PseudoTerminalCli.start(newHome(testCase.dexSection), [], {
        DEXCLI: fake.executable,
        OPENAI_API_KEY: "sentinel-provider-key",
        DEX_FLOW_SERVICE_ADDRESS: "dex.other.example:8801",
        DEX_TLS: "false",
        DEX_CREDENTIAL: "sentinel-other-credential",
        ...testCase.environment,
      });
      terminals.push(terminal);
      assert.equal(await terminal.waitForExit(), 0, `${testCase.name}: ${terminal.terminal.all}`);
      const record = fake.record();
      assert.deepEqual(
        record.filter((event) => event.event === "mismatch"),
        [],
        testCase.name,
      );
      const start = record.find((event) => event.event === "start");
      const dexNames = (start?.environment_names as string[]).filter(
        (name) => name.startsWith("DEX_") && name !== "DEX_AGENTS_TUI_DEBUG",
      );
      assert.deepEqual(dexNames, [testCase.variable], testCase.name);
      assert.ok(!(start?.environment_names as string[]).includes("OPENAI_API_KEY"), testCase.name);
    }
  });
});

/** The shortest chat transcript, comparing the launch document's `dex.tls` and `dex.auth`. */
function launchComparing(tlsSettings: object, auth: object): Transcript {
  const transcript = readTranscript("chat_ready_then_exit");
  const steps = transcript.steps.map((step) => {
    const message = step.message as { type?: string; dex?: Record<string, unknown> } | undefined;
    if (message?.type !== "launch") {
      return step;
    }
    return { ...step, message: { ...message, dex: { ...message.dex, tls: tlsSettings, auth } } };
  });
  return { ...transcript, name: "chat_remote_server_launch", steps };
}

describe("a refused credential", () => {
  for (const code of [16, 7]) {
    test(`never shows the server's details of gRPC status ${code}`, () => {
      const secret = "bearer-token-echoed-by-a-proxy-9876";
      const failure = new Error("cannot start the Worker", {
        cause: Object.assign(new Error(`${code} status: token ${secret}`), { code, details: `token ${secret} is bad` }),
      });
      const line = describeRefusedDexCredential(failure, { mode: "bearer", source: "env" }, "dex.example.com:443");
      assert.ok(line !== undefined);
      assert.ok(!line.includes(secret), line);
      assert.match(line, code === 16 ? /\(UNAUTHENTICATED\)/ : /\(PERMISSION_DENIED\)/);
    });
  }

  test("never shows a token exchange text that a server sends as details", () => {
    const secret = "dex-access-token-echoed-1234";
    const failure = Object.assign(new Error("16 UNAUTHENTICATED"), {
      code: 16,
      details: `dex: token exchange https://auth.example.com/t HTTP status 401: ${secret}`,
    });
    const line = describeRefusedDexCredential(
      failure,
      { mode: "api_key", token_url: "https://auth.example.com/t", server_id: "dex" },
      "dex.example.com:443",
    );
    assert.equal(
      line,
      "could not exchange DEX_API_KEY for an access token at https://auth.example.com/t (HTTP status 401). " +
        "Check DEX_API_KEY, dex.auth.token_url, and dex.auth.server_id.",
    );
  });
});
