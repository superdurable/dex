// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { fileURLToPath } from "node:url";

import { LocalDirectoryMediaStore, startAgentSession } from "@superdurable/dex-agents";
import { PrivateDexEngine, findFreeLoopbackPorts } from "@superdurable/dex-agents/testing";

import { candidateSessionIds } from "../../src/terminal-ui/request-keys.js";
import { terminalUiTerminalVariables } from "../../src/terminal-ui/terminal-ui-environment.js";
import { readProcessIdentity } from "../../src/worker-host/process-identity.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { plainOnlyScriptedHarness } from "../fixtures/scripted-harness.js";
import {
  FakeTerminalUi,
  PseudoTerminalCli,
  chatTranscript,
  cliEntryPath,
  isProcessAlive,
  isProcessGroupAlive,
  newUuidV7,
  contractHostPath,
  pythonExecutable,
  readTranscript,
  runRecords,
  terminalUiEndMarker,
  transcriptDirectory,
  terminalUiStartMarker,
  waitForRunRecord,
  waitUntil,
  type RunRecordForTest,
  type Transcript,
  type TranscriptStep,
} from "../fixtures/terminal-ui-test-support.js";

const simulatedWindows = fileURLToPath(new URL("../fixtures/simulated-windows-platform.js", import.meta.url));
// The fake's shell adds PWD, SHLVL, and _, and macOS adds __CF_USER_TEXT_ENCODING to every process.
const addedByTheShell = new Set(["PWD", "OLDPWD", "SHLVL", "_", "__CF_USER_TEXT_ENCODING"]);
const quietConfig = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
const shellCommand = "sleep 617";
const shellConfig = `connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: [${JSON.stringify(shellCommand)}]}}\n`;
/** A 1x1 PNG. */
const png = Buffer.from(
  "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c63600000000200015" +
    "4a24f5d0000000049454e44ae426082",
  "hex",
);

const tuiRequest = (id: number, method: string, params: object, key?: string): TranscriptStep => ({
  from: "tui",
  message: { type: "request", id, method, params, ...(key === undefined ? {} : { idempotency_key: key }) },
});
const launcherResponse = (id: number, outcome: object): TranscriptStep => ({
  from: "launcher",
  message: { type: "response", id, ...outcome },
});
const tuiPause = (pauseMs: number): TranscriptStep => ({ pause_ms: pauseMs, role: "tui" });

describe("the terminal UI launcher", () => {
  let engine: PrivateDexEngine;
  let sessionClients: SessionClients;
  const terminals: PseudoTerminalCli[] = [];
  const processes: CliProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    sessionClients = new SessionClients(engine);
  });

  afterEach(async () => {
    for (const terminal of terminals.splice(0)) {
      await terminal.kill();
    }
    for (const cli of processes.splice(0)) {
      await cli.kill();
    }
  });

  after(async () => {
    await sessionClients?.close();
    await engine?.stop();
  });

  function newHome(config = quietConfig, home = new TestHome(enginePorts(engine))): TestHome {
    home.writeConfig(config);
    return home;
  }

  function startOnTerminal(
    home: TestHome,
    fake: FakeTerminalUi | undefined,
    commandArguments: readonly string[] = [],
    environment: NodeJS.ProcessEnv = {},
    nodeArguments: readonly string[] = [],
    entryPath: string = cliEntryPath,
  ): PseudoTerminalCli {
    const terminal = PseudoTerminalCli.start(
      home,
      commandArguments,
      { ...(fake === undefined ? {} : { DEXCLI: fake.executable }), ...environment },
      nodeArguments,
      entryPath,
    );
    terminals.push(terminal);
    return terminal;
  }

  async function sessionsOf(home: TestHome): Promise<string[]> {
    const sessions = await sessionClients.of(home);
    const page = await sessions.client.searchFlows(`FlowType = "${home.sessionFlowType()}"`, 50);
    return page.flows.map((flow) => flow.flowId).sort();
  }

  async function onlySession(home: TestHome): Promise<string> {
    let sessions: string[] = [];
    await waitUntilAsync(async () => {
      sessions = await sessionsOf(home);
      return sessions.length === 1;
    }, "the command started its session");
    return sessions[0] ?? "";
  }

  async function waitForReady(fake: FakeTerminalUi, launch = 1): Promise<void> {
    await fake.waitForRecord(
      (event) => event.event === "sent" && (event.message as { type?: string }).type === "ready",
      launch,
    );
  }

  function logFile(home: TestHome): string {
    return readFileSync(join(home.directory, ".dex", "agents", `${home.harness.name}.log`), "utf8");
  }

  test("hands the chat to the terminal UI and back, with only the allow-listed environment", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")], { hasMarkers: true });
    const terminal = startOnTerminal(home, fake, [], {
      OPENAI_API_KEY: "sentinel-provider-key",
      ANTHROPIC_API_KEY: "sentinel-provider-key",
      AWS_SECRET_ACCESS_KEY: "sentinel-cloud-key",
      DEX_TOKEN_URL: "https://auth.example.com/token",
      COLORTERM: "truecolor",
    });
    assert.equal(await terminal.waitForExit(), 0);
    const output = terminal.terminal.all;
    assert.equal((await sessionsOf(home)).length, 1);
    // After ready, the terminal UI's exit summary is the only resume line.
    assert.doesNotMatch(output, /keeps running\. Resume it with/);
    const start = fake.record().find((event) => event.event === "start");
    assert.ok(start !== undefined);
    const names = (start.environment_names as string[]).filter((name) => !addedByTheShell.has(name));
    assert.deepEqual(names, names.filter((name) => terminalUiTerminalVariables.includes(name)));
    assert.ok(names.includes("TERM") && names.includes("COLORTERM") && names.includes("HOME"));
    assert.equal((start.terminal as { is_foreground?: boolean } | null)?.is_foreground, true);
    assert.deepEqual((start.argv as string[]).slice(-3), ["agents-tui", "--channel-fd", "3"]);
    assert.equal(textBetweenMarkers(output), "");
    await waitUntil(() => runRecords(home).length === 0, "the Worker host removed its run record");
  });

  test("a bin run through a symlink, as a global install or npx runs it, starts its Worker host", async () => {
    const home = newHome();
    const linkDirectory = mkdtempSync(join(tmpdir(), "dex-agents-bin-"));
    const link = join(linkDirectory, "dex-agents-scripted");
    symlinkSync(cliEntryPath, link);
    const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")]);
    const terminal = startOnTerminal(home, fake, [], {}, [], link);
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    assert.equal(fake.launchCount, 1);
    assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
  });

  test("replays every chat transcript of the contract that a real launcher plays", async () => {
    const connectors = new FakeConnectorsMcpServer();
    const connectorsUrl = await connectors.start();
    try {
      const names = readdirSync(transcriptDirectory)
        .filter((name) => name.endsWith(".json"))
        .map((name) => name.replace(/\.json$/, ""))
        .filter((name) => readTranscript(name).real_roles.includes("launcher"));
      // The sessions and connectors modes come with the pickers; this launcher hands off only the chat.
      const replayed = names.filter((name) => name.startsWith("chat_") || name === "ready_timeout");
      assert.ok(replayed.length >= 9, replayed.join(", "));
      for (const name of replayed) {
        const home = newHome(`connectors: {mcp: ${connectorsUrl}}\nlocal_tools: {browser: false}\n`);
        const pictures = join(home.directory, "pictures");
        mkdirSync(pictures, { recursive: true });
        writeFileSync(join(pictures, "screenshot.png"), png);
        writeFileSync(join(home.directory, "notes.txt"), "not an image");
        const environment: NodeJS.ProcessEnv = {};
        let openedFile: string | undefined;
        if (name === "chat_open_url") {
          const openerDirectory = mkdtempSync(join(tmpdir(), "dex-agents-opener-"));
          openedFile = join(openerDirectory, "opened");
          for (const opener of ["open", "xdg-open"]) {
            writeFileSync(join(openerDirectory, opener), `#!/bin/sh\necho "$1" >> '${openedFile}'\n`);
            chmodSync(join(openerDirectory, opener), 0o755);
          }
          Object.assign(environment, { DEX_AGENTS_OPEN_BROWSER: "1", PATH: `${openerDirectory}:${process.env.PATH ?? ""}` });
        }
        const fake = new FakeTerminalUi([readTranscript(name)], {
          aliases: {
            "/home/user/pictures/screenshot.png": join(pictures, "screenshot.png"),
            "/home/user/notes.txt": join(home.directory, "notes.txt"),
          },
        });
        const terminal = startOnTerminal(home, fake, [], environment);
        let expectedExit = 0;
        if (name === "chat_worker_stopped") {
          await waitForReady(fake);
          process.kill((await waitForRunRecord(home)).hostPid, "SIGKILL");
          expectedExit = 1;
        }
        if (name === "ready_timeout") {
          await terminal.terminal.waitFor("Type a message");
          terminal.type("/exit\r");
        }
        assert.equal(await terminal.waitForExit(), expectedExit, `${name}: ${terminal.terminal.all}`);
        assert.deepEqual(
          fake.record().filter((event) => event.event === "mismatch"),
          [],
          name,
        );
        if (openedFile !== undefined) {
          assert.match(readFileSync(openedFile, "utf8"), new RegExp(`^${engine.webUrl}/v2/flows/`), name);
        }
      }
    } finally {
      await connectors.stop();
    }
  });

  test("relays put_image and start_session, and recovers a lost response with get_request_result", async () => {
    const home = newHome();
    const picture = join(home.directory, "picture.png");
    writeFileSync(picture, png);
    const notes = join(home.directory, "notes.txt");
    writeFileSync(notes, "not an image");
    const image = await new LocalDirectoryMediaStore(home.mediaDirectory).putImage(png);
    const imageResult = {
      result: {
        image: { type: "image", uri: image.uri, mime_type: image.mimeType, size_bytes: image.sizeBytes, sha256: image.sha256 },
        name: "picture.png",
      },
    };
    const [imageKey, sessionKey, duplicateKey] = [newUuidV7(), newUuidV7(), newUuidV7()];
    const [newSessionId] = candidateSessionIds("scripted", sessionKey);
    const fake = new FakeTerminalUi([
      chatTranscript("relay", [
        tuiRequest(1, "put_image", { path: picture }, imageKey),
        launcherResponse(1, imageResult),
        // A cancel that races the completion and loses changes nothing: request 1 had its one response.
        { from: "tui", message: { type: "cancel", id: 1 } },
        tuiRequest(2, "put_image", { path: notes }, newUuidV7()),
        launcherResponse(2, { error: { code: "not_an_image", message: "notes.txt is not a PNG, JPEG, GIF, or WebP image." } }),
        // The response is lost: the terminal UI cancels its wait, then asks for the outcome.
        tuiRequest(3, "start_session", {}, sessionKey),
        { from: "tui", message: { type: "cancel", id: 3 } },
        launcherResponse(3, { error: { code: "cancelled", message: "Stopped waiting for start_session." } }),
        tuiPause(3_000),
        tuiRequest(4, "get_request_result", { idempotency_key: sessionKey }),
        launcherResponse(4, { result: { state: "done", result: { session_id: newSessionId } } }),
        // A resent key gets the first outcome and starts nothing new.
        tuiRequest(5, "start_session", {}, sessionKey),
        launcherResponse(5, { result: { session_id: newSessionId } }),
        // The same key twice while the first still runs: both get the first outcome.
        tuiRequest(6, "put_image", { path: picture }, duplicateKey),
        tuiRequest(7, "put_image", { path: picture }, duplicateKey),
        launcherResponse(6, imageResult),
        launcherResponse(7, imageResult),
        tuiRequest(8, "start_session", { reused: true }, sessionKey),
        launcherResponse(8, { error: { code: "invalid_params", message: "This idempotency key came with another request." } }),
        tuiRequest(9, "export_transcript", {}, newUuidV7()),
        launcherResponse(9, { error: { code: "unknown_method", message: "This command does not serve export_transcript." } }),
        tuiRequest(10, "get_request_result", { idempotency_key: newUuidV7() }),
        launcherResponse(10, { result: { state: "unknown" } }),
        { from: "tui", message: { type: "progress_report", percent: 40 } },
        tuiRequest(11, "open_url", { url: `${engine.webUrl}/v2/flows/${newSessionId}` }, newUuidV7()),
        launcherResponse(11, { error: { code: "browser_off", message: "Opening a browser is switched off for this command." } }),
        tuiRequest(12, "open_url", { url: `${engine.webUrl}.evil.example/` }, newUuidV7()),
        launcherResponse(12, { error: { code: "refused", message: "open_url only opens Dex Web pages." } }),
      ]),
    ]);
    const terminal = startOnTerminal(home, fake);
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    const sessions = await sessionsOf(home);
    assert.equal(sessions.length, 2);
    assert.ok(sessions.includes(newSessionId ?? ""));
  });

  test("with a slow Worker host, get_request_result reports running, then done", async () => {
    const home = newHome();
    const pauseFile = join(home.directory, "pause");
    const key = newUuidV7();
    const [sessionId] = candidateSessionIds("scripted", key);
    const resultRequest = (id: number): TranscriptStep => tuiRequest(id, "get_request_result", { idempotency_key: key });
    const fake = new FakeTerminalUi([
      chatTranscript("running_then_done", [
        tuiPause(1_000),
        tuiRequest(1, "start_session", {}, key),
        { from: "tui", message: { type: "cancel", id: 1 } },
        launcherResponse(1, { error: { code: "cancelled", message: "Stopped waiting for start_session." } }),
        resultRequest(2),
        launcherResponse(2, { result: { state: "running" } }),
        tuiPause(1_000),
        resultRequest(3),
        launcherResponse(3, { result: { state: "running" } }),
        tuiPause(4_000),
        resultRequest(4),
        launcherResponse(4, { result: { state: "done", result: { session_id: sessionId } } }),
      ]),
    ]);
    const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_PAUSE_FILE: pauseFile });
    await waitForReady(fake);
    writeFileSync(pauseFile, "");
    await fake.waitForRecord((event) => event.event === "received" && (event.message as { id?: number }).id === 3);
    rmSync(pauseFile);
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    assert.ok((await sessionsOf(home)).includes(sessionId ?? ""));
  });

  test("with a slow Worker host, a request that timed out and is resent with the same key runs once", async () => {
    for (const method of ["put_image", "start_session"] as const) {
      const home = newHome();
      const pauseFile = join(home.directory, "pause");
      const picture = join(home.directory, "picture.png");
      writeFileSync(picture, png);
      const image = await new LocalDirectoryMediaStore(join(home.directory, "expected-media")).putImage(png);
      const key = newUuidV7();
      const [sessionId] = candidateSessionIds("scripted", key);
      const params = method === "put_image" ? { path: picture } : {};
      const outcome: TranscriptStep =
        method === "put_image"
          ? {
              from: "launcher",
              message: {
                type: "response",
                id: 2,
                result: {
                  image: { type: "image", uri: "", mime_type: image.mimeType, size_bytes: image.sizeBytes, sha256: image.sha256 },
                  name: "picture.png",
                },
              },
              ignore: ["result.image.uri"],
            }
          : launcherResponse(2, { result: { session_id: sessionId } });
      const fake = new FakeTerminalUi([
        chatTranscript("timed_out_then_resent", [
          tuiPause(1_000),
          tuiRequest(1, method, params, key),
          { from: "tui", message: { type: "cancel", id: 1 } },
          launcherResponse(1, { error: { code: "cancelled", message: `Stopped waiting for ${method}.` } }),
          // The terminal UI resends once with the same key while the first run is still under way.
          tuiRequest(2, method, params, key),
          outcome,
        ]),
      ]);
      const before = await sessionsOf(home);
      const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_PAUSE_FILE: pauseFile });
      await waitForReady(fake);
      writeFileSync(pauseFile, "");
      await fake.waitForRecord((event) => event.event === "sent" && (event.message as { id?: number }).id === 2);
      rmSync(pauseFile);
      assert.equal(await terminal.waitForExit(), 0, `${method}: ${terminal.terminal.all}`);
      const after = await sessionsOf(home);
      if (method === "put_image") {
        assert.deepEqual(readdirSync(join(home.mediaDirectory, "sha256")), [image.sha256]);
        assert.equal(after.length, before.length + 1);
      } else {
        // The launcher's own session and the one start_session started: nothing twice.
        assert.equal(after.length, before.length + 2);
        assert.ok(after.includes(sessionId ?? ""));
      }
    }
  });

  test("start_session finds the session its key already started, takes the long ID on a collision, and refuses when both are taken", async () => {
    const home = newHome();
    const sessions = await sessionClients.of(home);
    const [resentKey, collidingKey, conflictKey] = [newUuidV7(), newUuidV7(), newUuidV7()];
    // A Worker host that started the Flow and died before it answered: the Flow exists with this key.
    const [resentId] = candidateSessionIds("scripted", resentKey);
    await startAgentSession(sessions.client, sessions.flow, resentId ?? "", {}, { requestId: resentKey });
    // Another request holds the short ID of the colliding key.
    const [collidingShortId, collidingLongId] = candidateSessionIds("scripted", collidingKey);
    await startAgentSession(sessions.client, sessions.flow, collidingShortId ?? "", {}, { requestId: newUuidV7() });
    for (const takenId of candidateSessionIds("scripted", conflictKey)) {
      await startAgentSession(sessions.client, sessions.flow, takenId, {}, { requestId: newUuidV7() });
    }
    const before = await sessionsOf(home);
    const fake = new FakeTerminalUi([
      chatTranscript("start_session_keys", [
        tuiRequest(1, "start_session", {}, resentKey),
        launcherResponse(1, { result: { session_id: resentId } }),
        tuiRequest(2, "start_session", {}, collidingKey),
        launcherResponse(2, { result: { session_id: collidingLongId } }),
        tuiRequest(3, "start_session", {}, conflictKey),
        launcherResponse(3, {
          error: { code: "id_conflict", message: "Other requests already started both session IDs this key derives." },
        }),
      ]),
    ]);
    const terminal = startOnTerminal(home, fake);
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    const after = await sessionsOf(home);
    // Only the launcher's own session and the long ID are new.
    assert.equal(after.length, before.length + 2);
    assert.ok(after.includes(collidingLongId ?? ""));
  });

  test("a Worker host killed in the middle of a request answers host_stopped and reports the Worker stopped", async () => {
    const home = newHome();
    const spinFile = join(home.directory, "spin");
    const base = readTranscript("chat_host_stopped");
    const [launch, ready, ...rest] = base.steps;
    const transcript: Transcript = { ...base, steps: [launch ?? {}, ready ?? {}, tuiPause(2_000), ...rest] };
    const fake = new FakeTerminalUi([transcript]);
    const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_SPIN_FILE: spinFile });
    await waitForReady(fake);
    const record = await waitForRunRecord(home);
    // The host's main loop blocks for good, so the put_image request stays unanswered until the host dies.
    writeFileSync(spinFile, "");
    await fake.waitForRecord(
      (event) => event.event === "sent" && (event.message as { method?: string }).method === "put_image",
    );
    process.kill(record.hostPid, "SIGKILL");
    assert.equal(await terminal.waitForExit(), 1, terminal.terminal.all);
    assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
  });

  test("open_url opens the page with the platform's opener, and says when no browser opened it", async () => {
    for (const [openerExit, expected] of [
      [0, { result: {} }],
      [1, { error: { code: "open_failed", message: "No browser opened the page." } }],
    ] as const) {
      const home = newHome();
      const openerDirectory = mkdtempSync(join(tmpdir(), "dex-agents-opener-"));
      const openedFile = join(openerDirectory, "opened");
      for (const name of ["open", "xdg-open"]) {
        writeFileSync(join(openerDirectory, name), `#!/bin/sh\necho "$1" >> '${openedFile}'\nexit ${openerExit}\n`);
        chmodSync(join(openerDirectory, name), 0o755);
      }
      const url = `${engine.webUrl}/v2/flows`;
      const fake = new FakeTerminalUi([
        chatTranscript("open_url", [tuiRequest(1, "open_url", { url }, newUuidV7()), launcherResponse(1, expected)]),
      ]);
      const terminal = startOnTerminal(home, fake, [], {
        DEX_AGENTS_OPEN_BROWSER: "1",
        PATH: `${openerDirectory}:${process.env.PATH ?? ""}`,
      });
      assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
      assert.equal(readFileSync(openedFile, "utf8").trim(), url);
    }
  });

  test("a dexcli that cannot start after --describe falls back to the plain chat on the same session", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")], { isUnrunnableAfterDescribe: true });
    const terminal = startOnTerminal(home, fake);
    await terminal.terminal.waitFor(/Plain chat: dexcli agents-tui could not start: [^\n]*EACCES/);
    const sessionId = await onlySession(home);
    await terminal.terminal.waitFor(`Resumed session ${sessionId}.`);
    await terminal.terminal.waitFor("Type a message");
    terminal.type("/exit\r");
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    await waitUntil(() => runRecords(home).length === 0, "the Worker host stopped");
  });

  test("a terminal UI that closes its channel or rejects the launch and keeps running is stopped at once, then the plain chat goes on", async () => {
    const [launch] = readTranscript("chat_ready_then_exit").steps;
    const rejection = { type: "launch_rejected", code: "auth_missing", message: "DEX_API_KEY is not set; dex.auth mode api_key needs it." };
    for (const [steps, reason] of [
      [[{ close: "tui" }, tuiPause(60_000)], "Plain chat: the terminal UI closed its channel."],
      [[{ from: "tui", message: rejection }, tuiPause(60_000)], "Plain chat: DEX_API_KEY is not set; dex.auth mode api_key needs it."],
    ] as const) {
      const home = newHome();
      const fake = new FakeTerminalUi([{ ...readTranscript("chat_ready_then_exit"), steps: [launch ?? {}, ...steps] }]);
      const terminal = startOnTerminal(home, fake);
      await fake.waitForRecord((event) => event.event === "start");
      const startedAt = Date.now();
      await terminal.terminal.waitFor(reason);
      // Well inside the 5 s ready timeout: the launcher did not wait for it.
      assert.ok(Date.now() - startedAt < 4_500, `fell back after ${Date.now() - startedAt} ms`);
      await terminal.terminal.waitFor("Type a message");
      terminal.type("/exit\r");
      assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    }
  });

  test("after ready, a closed channel or a malformed line ends the chat: the terminal UI stops and the command exits 1", async () => {
    const malformed = "the terminal UI sent a line the contract does not allow: a line is not JSON";
    for (const [steps, reason] of [
      [[{ close: "tui" }, tuiPause(60_000)], "the terminal UI closed its channel"],
      [[{ from: "tui", raw_line: "not json" }, tuiPause(60_000)], malformed],
      // A protocol error stays a failure even when the terminal UI then exits 0.
      [[{ from: "tui", raw_line: "not json" }], malformed],
    ] as const) {
      const home = newHome();
      const fake = new FakeTerminalUi([chatTranscript("contract_end", steps)]);
      const terminal = startOnTerminal(home, fake);
      assert.equal(await terminal.waitForExit(30_000), 1, terminal.terminal.all);
      assert.ok(terminal.terminal.all.includes(`The chat closed unexpectedly (${reason}).`), terminal.terminal.all);
      assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
      // The Worker host removes its run record when it stops.
      await waitUntil(() => runRecords(home).length === 0, "the Worker host stopped");
    }
  });

  test("a failure before ready continues in the plain chat on the same session", async () => {
    const rejected: Transcript = {
      ...readTranscript("chat_ready_then_exit"),
      steps: [
        readTranscript("chat_ready_then_exit").steps[0] ?? {},
        {
          from: "tui",
          message: { type: "launch_rejected", code: "auth_missing", message: "DEX_API_KEY is not set; dex.auth mode api_key needs it." },
        },
      ],
      tui_exit: 2,
    };
    for (const [transcript, reason] of [
      [rejected, "Plain chat: DEX_API_KEY is not set; dex.auth mode api_key needs it."],
      [readTranscript("ready_timeout"), "Plain chat: the terminal UI was not ready within 5 seconds."],
    ] as const) {
      const home = newHome();
      const fake = new FakeTerminalUi([transcript]);
      const terminal = startOnTerminal(home, fake);
      await terminal.terminal.waitFor(reason);
      const sessionId = await onlySession(home);
      await terminal.terminal.waitFor(`Resumed session ${sessionId}.`);
      await terminal.terminal.waitFor("Type a message");
      terminal.type("hello\r");
      await terminal.terminal.waitFor("echo: hello");
      terminal.type("/exit\r");
      assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
      assert.deepEqual(await sessionsOf(home), [sessionId]);
      await waitUntil(() => runRecords(home).length === 0, "the Worker host stopped");
    }
  });

  test("a dexcli without the terminal UI keeps the plain chat and starts no Worker host", async () => {
    const home = newHome();
    const terminal = startOnTerminal(home, undefined);
    await terminal.terminal.waitFor("Plain chat: dexcli agents-tui --describe exited with 2.");
    await terminal.terminal.waitFor("Type a message");
    assert.deepEqual(runRecords(home), []);
    terminal.type("/exit\r");
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
  });

  test("on Windows the command never probes dexcli or starts a Worker host", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")]);
    const terminal = startOnTerminal(home, fake, [], {}, ["--import", simulatedWindows]);
    // The local media store refuses Windows paths in this POSIX test, which ends the run before the chat.
    assert.notEqual(await terminal.waitForExit(), 0);
    assert.match(terminal.terminal.all, /needs a POSIX file system/);
    assert.equal(fake.describeCount + fake.launchCount, 0, terminal.terminal.all);
    assert.deepEqual(runRecords(home), []);
    assert.doesNotMatch(terminal.terminal.all, /Plain chat:/);
  });

  test("--plain, TERM=dumb, the switch off, no terminal, and a plain-only harness never probe dexcli", async () => {
    const cases: readonly {
      readonly name: string;
      readonly home: TestHome;
      readonly arguments?: readonly string[];
      readonly environment?: NodeJS.ProcessEnv;
      readonly nodeArguments?: readonly string[];
    }[] = [
      { name: "--plain", home: newHome(), arguments: ["--plain"] },
      { name: "TERM=dumb", home: newHome(), environment: { TERM: "dumb" } },
      { name: "switch off", home: newHome(), environment: { DEX_AGENTS_TERMINAL_UI: "" } },
      { name: "plain-only harness", home: newHome(quietConfig, new TestHome(enginePorts(engine), plainOnlyScriptedHarness)) },
    ];
    for (const testCase of cases) {
      const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")]);
      const terminal = startOnTerminal(
        testCase.home,
        fake,
        testCase.arguments,
        testCase.environment,
        testCase.nodeArguments,
      );
      await terminal.terminal.waitFor("Type a message");
      terminal.type("/exit\r");
      assert.equal(await terminal.waitForExit(), 0, `${testCase.name}: ${terminal.terminal.all}`);
      assert.equal(fake.describeCount + fake.launchCount, 0, testCase.name);
      assert.doesNotMatch(terminal.terminal.all, /Plain chat:/, testCase.name);
    }
    const home = newHome();
    const fake = new FakeTerminalUi([readTranscript("chat_ready_then_exit")]);
    const cli = CliProcess.start(home, [], { DEXCLI: fake.executable, DEX_AGENTS_TERMINAL_UI: "1" });
    processes.push(cli);
    await cli.waitForSession();
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(fake.describeCount + fake.launchCount, 0, "no terminal");
  });

  test("nothing the Worker host, its children, or the launcher's late writes print reaches the terminal UI's screen", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([chatTranscript("leaks", [tuiPause(15_000)])], { hasMarkers: true });
    const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_IMPORT_NOISE: "1" });
    await waitForReady(fake);
    const sessionId = await onlySession(home);
    const sessions = await sessionClients.of(home);
    assert.equal(await sessions.sendUserMessage(sessionId, "leak"), true);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 12_000 });
    assert.equal(await terminal.waitForExit(), 0, terminal.terminal.all);
    assert.equal(textBetweenMarkers(terminal.terminal.all), "");
    const log = logFile(home);
    for (const text of ["host wrote to fd 1", "host wrote to fd 2", "grandchild wrote to stdout", "grandchild wrote to stderr", "import noise later"]) {
      assert.match(log, new RegExp(text), text);
    }
  });

  async function startShellAndKillLauncher(home: TestHome, isHostStuck: boolean): Promise<{
    readonly record: RunRecordForTest;
    readonly shellGroup: number;
    readonly killedAt: number;
    readonly terminal: PseudoTerminalCli;
  }> {
    const spinFile = join(home.directory, "spin");
    const fake = new FakeTerminalUi([chatTranscript("launcher_killed", [tuiPause(120_000)])]);
    const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_SPIN_FILE: spinFile });
    await waitForReady(fake);
    const sessionId = await onlySession(home);
    const sessions = await sessionClients.of(home);
    await sessions.sendUserMessage(sessionId, `call run_shell_command ${JSON.stringify({ command: shellCommand })}`);
    const record = await waitForRunRecord(home, (candidate) => candidate.tool_groups.length === 1);
    const shellGroup = record.tool_groups[0]?.pgid ?? 0;
    assert.ok(isProcessGroupAlive(shellGroup));
    // The command holds stdin, stdout, and stderr only: no channel of the launcher, the terminal UI, or its
    // gatekeeper, whose fds 3 and 4 talk to the Worker host.
    const commandPids = execFileSync("pgrep", ["-g", String(shellGroup), "-f", shellCommand], { encoding: "utf8" })
      .trim()
      .split("\n")
      .map(Number)
      .filter((pid) => pid !== shellGroup);
    assert.ok(commandPids.length > 0);
    for (const pid of commandPids) {
      assert.deepEqual(openFileDescriptors(pid).filter((fd) => fd > 2), [], `pid ${pid}`);
    }
    assert.deepEqual(openFileDescriptors(shellGroup).filter((fd) => fd > 4), []);
    if (isHostStuck) {
      writeFileSync(spinFile, "");
      await new Promise((resolve) => setTimeout(resolve, 500));
    }
    process.kill(record.launcher.pid, "SIGKILL");
    return { record, shellGroup, killedAt: Date.now(), terminal };
  }

  test("the Worker host stops a running shell command and exits after the launcher is killed", async () => {
    const home = newHome(shellConfig);
    const { record, shellGroup, killedAt, terminal } = await startShellAndKillLauncher(home, false);
    await waitUntil(
      () => !isProcessAlive(record.hostPid) && !isProcessGroupAlive(record.host.pgid) && !isProcessGroupAlive(shellGroup),
      "the Worker host and the shell command stopped",
      30_000,
    );
    // 1 s to notice, then the orderly shutdown: SIGTERM to the shell's group and SIGKILL 2 s later at most.
    assert.ok(Date.now() - killedAt <= 10_000, `stopped after ${Date.now() - killedAt} ms`);
    assert.deepEqual(runRecords(home), []);
    await terminal.waitForExit();
  });

  test("with its main loop stuck, the Worker host still ends within its bound, and the next start stops the shell command", async () => {
    const home = newHome(shellConfig);
    const { record, shellGroup, killedAt, terminal } = await startShellAndKillLauncher(home, true);
    await waitUntil(
      () => !isProcessAlive(record.hostPid) && !isProcessGroupAlive(record.host.pgid),
      "the Worker host stopped",
      30_000,
    );
    // 1 s to notice the launcher is gone, then the 15 s bound, plus slack.
    assert.ok(Date.now() - killedAt <= 22_000, `stopped after ${Date.now() - killedAt} ms`);
    // The watchdog thread never signals tool groups: the shell runs on, and its record stays.
    assert.ok(isProcessGroupAlive(shellGroup), "the watchdog left the shell's group alone");
    assert.equal(runRecords(home).length, 1);
    await terminal.waitForExit();
    const cli = CliProcess.start(home, ["sessions"], { DEX_AGENTS_TERMINAL_UI: "1" });
    processes.push(cli);
    assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
    assert.match(cli.stderr.all, /Stopped 1 tool process group that a leftover worker host left behind\./);
    await waitUntil(() => !isProcessGroupAlive(shellGroup), "the leftover shell stopped", 10_000);
    assert.deepEqual(runRecords(home), []);
  });

  test("a shell command's background job ends with the command, in the plain chat and in the Worker host", async () => {
    const command = "sleep 30 & echo $!";
    const config = `connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: [${JSON.stringify(command)}]}}\n`;
    const call = `call run_shell_command ${JSON.stringify({ command })}`;
    const backgroundPid = (text: string): number => Number(/result: exit code 0\n--- stdout ---\n(\d+)/.exec(text)?.[1] ?? 0);
    // The plain chat runs the shell tool in the command's own process.
    const plainHome = newHome(config);
    const plain = CliProcess.start(plainHome);
    processes.push(plain);
    await plain.waitForSession();
    plain.writeLine(call);
    const plainPid = backgroundPid(await plain.stdout.waitFor(/result: exit code 0\n--- stdout ---\n\d+/));
    assert.ok(plainPid > 0);
    await waitUntil(() => !isProcessAlive(plainPid), "the plain chat's background sleep ended", 5_000);
    plain.writeLine("/exit");
    assert.equal(await plain.waitForExit(), 0);
    // The terminal UI's Worker host runs it there.
    const home = newHome(config);
    const fake = new FakeTerminalUi([chatTranscript("background_job", [tuiPause(20_000)])]);
    const terminal = startOnTerminal(home, fake);
    await waitForReady(fake);
    const sessionId = await onlySession(home);
    const sessions = await sessionClients.of(home);
    await sessions.sendUserMessage(sessionId, call);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 15_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const hostPid = backgroundPid(snapshot.messages.at(-1)?.text ?? "");
    assert.ok(hostPid > 0, JSON.stringify(snapshot.messages.at(-1)));
    await waitUntil(() => !isProcessAlive(hostPid), "the Worker host's background sleep ended", 5_000);
    assert.deepEqual((await waitForRunRecord(home)).tool_groups, []);
    await terminal.waitForExit();
  });

  test("SIGTERM and SIGHUP during the handoff, and SIGTERM in the plain chat after a fallback, stop the dexcli dev the command started", async () => {
    const [launch] = readTranscript("chat_ready_then_exit").steps;
    const rejected: Transcript = {
      ...readTranscript("chat_ready_then_exit"),
      steps: [launch ?? {}, { from: "tui", message: { type: "launch_rejected", code: "auth_missing", message: "DEX_API_KEY is not set." } }],
      tui_exit: 2,
    };
    for (const [signal, isFallback, exitCode] of [
      ["SIGTERM", false, 143],
      ["SIGHUP", false, 129],
      ["SIGTERM", true, 143],
    ] as const) {
      const [dexPort, webPort] = await findFreeLoopbackPorts(2);
      const home = newHome(quietConfig, new TestHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 }));
      const fake = new FakeTerminalUi([isFallback ? rejected : chatTranscript("own_dexcli_dev", [tuiPause(60_000)])]);
      const terminal = startOnTerminal(home, fake);
      const startedLine = await terminal.terminal.waitFor(/dexcli dev is ready \(pid \d+\)/, 120_000);
      const devPid = Number(/pid (\d+)/.exec(startedLine)?.[1]);
      const record = await waitForRunRecord(home);
      if (isFallback) {
        await terminal.terminal.waitFor("Type a message");
      } else {
        await waitForReady(fake);
      }
      process.kill(record.launcher.pid, signal);
      assert.equal(await terminal.waitForExit(), exitCode, `${signal}: ${terminal.terminal.all}`);
      // The terminal UI and the Worker host stop first; then the command's close stops the dexcli dev.
      await waitUntil(() => !isProcessGroupAlive(devPid), `the started dexcli dev stopped after ${signal}`, 30_000);
      await waitUntil(() => !isProcessAlive(record.hostPid), "the Worker host stopped");
    }
  });

  test("a signal after the terminal UI exited, while the Worker host still stops, waits for that stop", async () => {
    for (const [signal, isOwnDexcliDev, exitCode] of [
      ["SIGTERM", false, 143],
      ["SIGHUP", false, 129],
      ["SIGTERM", true, 143],
    ] as const) {
      const [dexPort, webPort] = await findFreeLoopbackPorts(2);
      const home = isOwnDexcliDev
        ? newHome(quietConfig, new TestHome({ dexPort: dexPort ?? 0, webPort: webPort ?? 0 }))
        : newHome();
      const pauseFile = join(home.directory, "pause");
      const fake = new FakeTerminalUi([chatTranscript("slow_host_stop", [tuiPause(1_500)])]);
      const terminal = startOnTerminal(home, fake, [], { DEX_AGENTS_CLI_TEST_PAUSE_FILE: pauseFile });
      await waitForReady(fake);
      const record = await waitForRunRecord(home);
      // The Worker host's main loop blocks, so its orderly stop runs into the 10 s bound and then SIGTERM and SIGKILL.
      writeFileSync(pauseFile, "");
      await new Promise((resolve) => setTimeout(resolve, 3_000));
      const signaledAt = Date.now();
      process.kill(record.launcher.pid, signal);
      assert.equal(await terminal.waitForExit(), exitCode, `${signal}: ${terminal.terminal.all}`);
      // A launcher that the signal ended at once would leave the blocked Worker host running.
      assert.ok(!isProcessAlive(record.hostPid), `${signal}: the launcher waited for the Worker host to stop`);
      assert.ok(Date.now() - signaledAt >= 5_000, `${signal}: exited after ${Date.now() - signaledAt} ms`);
      rmSync(pauseFile, { force: true });
    }
  });

  test("SIGTERM to the Worker host reports it stopping and stopped, and the chat then exits 1", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([
      chatTranscript("host_terminated", [
        { from: "launcher", message: { type: "worker", state: "stopping" } },
        { from: "launcher", message: { type: "worker", state: "stopped", exit_code: 0 } },
      ]),
    ]);
    const terminal = startOnTerminal(home, fake);
    await waitForReady(fake);
    const record = await waitForRunRecord(home);
    process.kill(record.hostPid, "SIGTERM");
    assert.equal(await terminal.waitForExit(), 1, terminal.terminal.all);
    assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
  });

  test("a command start stops what a killed launcher's Worker host left, but only processes that still match their record", async () => {
    const home = newHome();
    const sleepers = [spawnSleeper(), spawnSleeper(), spawnSleeper()];
    try {
      const [host, tool, reused] = sleepers.map((child) => readProcessIdentity(child.pid ?? 0));
      assert.ok(host !== undefined && tool !== undefined && reused !== undefined);
      const goneLauncher = { pid: 999_999, pgid: 999_999, start_time: "Thu Jan  1 00:00:00 2026", command_sha256: "0".repeat(64) };
      const recordDirectory = join(home.directory, ".dex", "agents", "run");
      mkdirSync(recordDirectory, { recursive: true });
      // The third group's leader runs the recorded command line but started at another time: a reused pid.
      // A tool's shell may exec its command, so a tool group's command line is not part of its identity.
      const record = {
        launcher: goneLauncher,
        host,
        tool_groups: [{ ...tool, command_sha256: "0".repeat(64) }, { ...reused, start_time: "Thu Jan  1 00:00:00 2026" }],
      };
      writeFileSync(join(recordDirectory, `scripted-${host.pid}.json`), JSON.stringify(record));
      const cli = CliProcess.start(home, ["sessions"], { DEX_AGENTS_TERMINAL_UI: "1" });
      processes.push(cli);
      assert.equal(await cli.waitForExit(), 0, cli.stderr.all);
      assert.match(cli.stderr.all, new RegExp(`Stopped a leftover worker host \\(pid ${host.pid}\\) and 1 tool process group\\.`));
      await waitUntil(() => !isProcessGroupAlive(host.pgid) && !isProcessGroupAlive(tool.pgid), "the leftover groups stopped");
      assert.ok(isProcessGroupAlive(reused.pgid), "a process that does not match its record keeps running");
      assert.deepEqual(runRecords(home), []);
    } finally {
      for (const child of sleepers) {
        child.kill("SIGKILL");
      }
    }
  });

  test("SIGTERM to the launcher exits 143; SIGINT to the launcher alone or to the foreground group exits 130", async () => {
    for (const [signal, target, exitCode] of [
      ["SIGTERM", "launcher", 143],
      ["SIGINT", "launcher", 130],
      ["SIGINT", "group", 130],
    ] as const) {
      const home = newHome();
      const fake = new FakeTerminalUi([chatTranscript("signal", [tuiPause(60_000)])]);
      const terminal = startOnTerminal(home, fake);
      await waitForReady(fake);
      const record = await waitForRunRecord(home);
      // A terminal sends SIGINT to the foreground process group; kill -INT reaches the launcher alone.
      process.kill(target === "launcher" ? record.launcher.pid : -record.launcher.pid, signal);
      assert.equal(await terminal.waitForExit(), exitCode, terminal.terminal.all);
      assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
      await waitUntil(() => !isProcessAlive(record.hostPid), "the Worker host stopped");
    }
  });

  test("once SIGINT reached the launcher, it exits 130 even when the terminal UI catches SIGINT and exits 0", async () => {
    const home = newHome();
    const directory = mkdtempSync(join(tmpdir(), "dex-agents-sigint-tui-"));
    const script = join(directory, "sigint_tui.py");
    writeFileSync(
      script,
      [
        "import os, signal, socket, sys, time",
        "channel = socket.socket(fileno=int(sys.argv[sys.argv.index('--channel-fd') + 1]))",
        "channel.makefile('rb').readline()",
        "channel.sendall(b'{\"type\":\"ready\"}\\n')",
        "signal.signal(signal.SIGINT, lambda *_: os._exit(0))",
        `open('${join(directory, "ready")}', 'w').close()`,
        "time.sleep(60)",
        "",
      ].join("\n"),
    );
    const dexcli = join(directory, "dexcli");
    writeFileSync(
      dexcli,
      [
        "#!/bin/sh",
        `if [ "$2" = "--describe" ]; then exec '${pythonExecutable()}' '${contractHostPath}' tui "$@"; fi`,
        `exec '${pythonExecutable()}' '${script}' "$@"`,
        "",
      ].join("\n"),
    );
    chmodSync(dexcli, 0o755);
    const terminal = startOnTerminal(home, undefined, [], { DEXCLI: dexcli });
    const record = await waitForRunRecord(home);
    await waitUntil(() => existsSync(join(directory, "ready")), "the terminal UI is ready");
    process.kill(record.launcher.pid, "SIGINT");
    assert.equal(await terminal.waitForExit(), 130, terminal.terminal.all);
    assert.doesNotMatch(terminal.terminal.all, /keeps running\. Resume it with/);
    await waitUntil(() => !isProcessAlive(record.hostPid), "the Worker host stopped");
  });

  test("a terminal UI killed after ready restores the terminal and exits 1", async () => {
    const home = newHome();
    const fake = new FakeTerminalUi([chatTranscript("killed", [tuiPause(60_000)])]);
    const terminal = startOnTerminal(home, fake);
    await waitForReady(fake);
    const record = await waitForRunRecord(home);
    const fakePids = execFileSync("pgrep", ["-f", fake.executable.replace(/dexcli$/, "launch-1.json")], { encoding: "utf8" })
      .trim()
      .split("\n")
      .map(Number);
    for (const pid of fakePids) {
      process.kill(pid, "SIGKILL");
    }
    assert.equal(await terminal.waitForExit(), 1, terminal.terminal.all);
    assert.match(terminal.terminal.all, /The chat closed unexpectedly \(dexcli exit 137\)\./);
    assert.match(terminal.terminal.all, /\u001b\[\?1049l\u001b\[\?2004l\u001b\[\?25h/);
    await waitUntil(() => !isProcessAlive(record.hostPid), "the Worker host stopped");
  });
});

/** Starts a `sleep` that leads its own process group, as a Worker host or a tool group does. */
function spawnSleeper(): ChildProcess {
  const child = spawn("sleep", ["600"], { detached: true, stdio: "ignore" });
  child.unref();
  return child;
}

/** Returns the numeric file descriptors a process has open, from /proc on Linux, else from lsof. */
function openFileDescriptors(pid: number): number[] {
  const procDirectory = `/proc/${pid}/fd`;
  if (existsSync(procDirectory)) {
    return readdirSync(procDirectory).map(Number);
  }
  const output = execFileSync("lsof", ["-a", "-p", String(pid), "-Ff"], { encoding: "utf8" });
  return output
    .split("\n")
    .filter((line) => /^f\d+$/.test(line))
    .map((line) => Number(line.slice(1)));
}

/** Returns what reached the terminal between the fake terminal UI's start and end markers. */
function textBetweenMarkers(output: string): string {
  const start = output.indexOf(terminalUiStartMarker);
  const end = output.indexOf(terminalUiEndMarker);
  assert.ok(start >= 0 && end > start, `the markers are missing:\n${output}`);
  return output.slice(start + terminalUiStartMarker.length, end);
}

async function waitUntilAsync(condition: () => Promise<boolean>, description: string, timeoutMs = 30_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!(await condition())) {
    if (Date.now() >= deadline) {
      throw new Error(`timed out waiting until ${description}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}
