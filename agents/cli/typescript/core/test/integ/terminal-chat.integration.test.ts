// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, realpathSync, statSync, writeFileSync } from "node:fs";
import { hostname, userInfo } from "node:os";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";
import { promisify } from "node:util";

import { pollUntil, PrivateDexEngine } from "@superdurable/dex-agents/testing";

import {
  renderSessionFlowGraph,
  sessionFlowDefinitionDocument,
  type AgentTurnContext,
  type DeferredToolExecution,
  type ToolEffect,
} from "@superdurable/dex-agents";

import {
  ConnectorsMcpToolSource,
  TurnConfigResolver,
  probeConnectorsMcp,
  resolveConfig,
  sessionActionPermission,
  type ConnectorsMcpUnavailability,
} from "../../src/index.js";
import { CliProcess, SessionClients, TestHome, enginePorts } from "../fixtures/cli-test-support.js";
import { connectorsUnavailableLine } from "../../src/connector-tool-source.js";
import { FakeConnectorsMcpServer } from "../fixtures/fake-connectors-mcp-server.js";
import { shellReplacingScriptedHarness } from "../fixtures/scripted-harness.js";

/** One definition in the protobuf JSON that `dexcli api call GetFlowDefinitions` prints. */
interface StoredFlowDefinition {
  readonly info: { readonly flowType: string; readonly generatedAt: string };
  readonly content: string;
}

describe("terminal chat with connectors and session settings", () => {
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

  function newHome(config: string): TestHome {
    const home = new TestHome(enginePorts(engine));
    home.writeConfig(config);
    return home;
  }

  function start(home: TestHome, commandArguments: readonly string[] = [], environment: NodeJS.ProcessEnv = {}): CliProcess {
    const cli = CliProcess.start(home, commandArguments, environment);
    processes.push(cli);
    return cli;
  }

  const connectorConfig = (extra = ""): string =>
    [
      "connectors:",
      `  mcp: ${connectorsUrl}`,
      "  enabled: [slack/support-slack, hackernews]",
      "local_tools: {browser: false}",
      extra,
    ].join("\n");

  test("a new session records where the command runs in its start input", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const spawnedAt = Math.floor(Date.now() / 1000) * 1000;
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    assertSessionIdNamesTheHarnessAndUtcStart(sessionId, spawnedAt);
    const { stdout } = await promisify(execFile)(home.environment().DEXCLI ?? "dexcli", [
      "flow",
      "history",
      sessionId,
      "--server",
      engine.serverAddress,
      "--output",
      "json",
    ]);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    const [firstEvent] = (JSON.parse(stdout) as { events: { payload: StartedPayload }[] }).events;
    assert.ok(firstEvent !== undefined, "the history has the start event");
    const started = firstEvent.payload;
    assert.equal(started.flowType, home.sessionFlowType());
    assert.deepEqual(started.initialStart.stepInput.start_environment, {
      host_name: hostname(),
      user_id: String(process.getuid?.() ?? userInfo().username),
      config_identity: realpathSync(home.configPath),
      workspace: realpathSync.native(home.workspace),
      user_name: userInfo().username,
    });
  });

  test("the command uploads the session graph for Dex Web and logs the outcome only to its log file", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const cli = start(home);
    await cli.waitForSession();
    const flowType = home.sessionFlowType();
    const definition = await pollUntil(
      async () => {
        const { stdout } = await promisify(execFile)(home.environment().DEXCLI ?? "dexcli", [
          "api",
          "call",
          "GetFlowDefinitions",
          "--server",
          engine.serverAddress,
          "--data",
          JSON.stringify({ flowTypes: [flowType] }),
        ]);
        return (JSON.parse(stdout) as { definitions?: StoredFlowDefinition[] }).definitions?.[0];
      },
      { description: `the uploaded definition of ${flowType}`, timeoutMs: 10_000 },
    );
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.equal(definition.info.flowType, flowType);
    assert.equal(
      Buffer.from(definition.content, "base64").toString("utf8"),
      renderSessionFlowGraph({ flowType, actionPermission: sessionActionPermission }),
    );
    const { generatedAt } = sessionFlowDefinitionDocument({ flowType, actionPermission: sessionActionPermission });
    assert.equal(new Date(definition.info.generatedAt).toISOString(), generatedAt.toISOString());
    // The upload outcome goes to the log file, never into the terminal chat.
    assert.doesNotMatch(cli.stdout.all + cli.stderr.all, /Flow definition|dex info/);
    const logFile = readFileSync(join(home.directory, ".dex", "agents", `${home.harness.name}.log`), "utf8");
    assert.match(logFile, new RegExp(`session Flow definition (created|unchanged): flowType=${flowType} `));
  });

  test("an unwritable log file neither stops the chat nor reaches the terminal", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const logPath = join(home.directory, ".dex", "agents", `${home.harness.name}.log`);
    mkdirSync(logPath, { recursive: true });
    const cli = start(home);
    await cli.waitForSession();
    cli.writeLine("hello there");
    await cli.stdout.waitFor("echo: hello there");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.ok(statSync(logPath).isDirectory());
    assert.doesNotMatch(cli.stdout.all + cli.stderr.all, /Flow definition|dex info|EISDIR/);
  });

  test("one chat turn streams the reply, reusing the running dexcli dev", async () => {
    const cli = start(newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n"));
    await cli.waitForSession();
    cli.writeLine("hello there");
    await cli.stdout.waitFor("echo: hello there");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stdout.all, /keeps running\. Resume it with: dex-agents-scripted --resume /);
    assert.doesNotMatch(cli.stderr.all, /Starting dexcli dev/);
  });

  test("an approved connector write runs once with an idempotency key, and a rejected one never runs", async () => {
    const cli = start(newHome(connectorConfig()));
    await cli.waitForSession();
    cli.writeLine('call slack_post_thread_reply {"text":"approved reply"}');
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    cli.writeLine("y");
    await cli.stdout.waitFor("result: posted approved reply");
    const runs = connectors.runsOf("slack_post_thread_reply").filter((run) => run.arguments.text === "approved reply");
    assert.equal(runs.length, 1);
    assert.notEqual(runs[0]?.idempotencyKey, undefined);
    assert.equal(runs[0]?.meta["io.superdurable.connectors/expectedConnector"], "slack");
    assert.equal(runs[0]?.meta["io.superdurable.connectors/expectedConnection"], "support-slack");
    assert.equal(runs[0]?.meta["io.superdurable.connectors/expectedOperation"], "postThreadReply");

    cli.writeLine('call slack_post_thread_reply {"text":"rejected reply"}');
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    cli.writeLine("n not today");
    await cli.stdout.waitFor(/result: .*not today/);
    assert.equal(connectors.runsOf("slack_post_thread_reply").filter((run) => run.arguments.text === "rejected reply").length, 0);

    cli.writeLine('call hackernews_get_top_stories {}');
    await cli.stdout.waitFor("result: top stories: dex-1, dex-2");
    assert.doesNotMatch(cli.stdout.all, /Approve hackernews_get_top_stories/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a connector tool name rebound to another connection while waiting for approval is refused unsent", async () => {
    const cli = start(
      newHome(["connectors:", `  mcp: ${connectorsUrl}`, "  enabled: [slack]", "local_tools: {browser: false}"].join("\n")),
    );
    await cli.waitForSession();
    cli.writeLine('call slack_post_thread_reply {"text":"rebound reply"}');
    await cli.stdout.waitFor("Approve slack_post_thread_reply?");
    connectors.slackConnection = "other-slack";
    try {
      cli.writeLine("y");
      await cli.stdout.waitFor(
        /result: .*requested for slack\/support-slack postThreadReply but now names slack\/other-slack postThreadReply; it did not run/,
      );
    } finally {
      connectors.slackConnection = "support-slack";
    }
    assert.equal(connectors.runsOf("slack_post_thread_reply").filter((run) => run.arguments.text === "rebound reply").length, 0);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("a retried attempt resumes a session-polled connector task without a listing; other retries are checked", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl, enabled: ["reports"] } }, "builtin", "scripted/default");
    const turns = new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined));
    const source = new ConnectorsMcpToolSource(connectorsUrl, turns, () => undefined);
    const listed = (await source.listTools()).find((tool) => tool.name === "reports_long_report");
    assert.ok(listed !== undefined);
    const turn = {
      sessionSettings: {},
      toolPolicies: {},
      resolveToolPolicy: (_toolName: string, effect?: ToolEffect) => ({ effect: effect ?? "unclassified", requiresApproval: false }),
    } as unknown as AgentTurnContext;
    const deferred = source.deferredToolCall(turn, {
      toolCallId: "call-1",
      toolName: "reports_long_report",
      arguments: '{"topic": "q1"}',
      listedTool: listed,
    });
    const checkpoints: string[] = [];
    const execution = (heartbeatCheckpoint?: string, supportsToolTasks = true): DeferredToolExecution =>
      ({
        flowId: "resume-session",
        toolCall: {
          toolCallId: "call-1",
          turnId: "turn-1",
          toolName: "reports_long_report",
          arguments: deferred.arguments,
          effect: deferred.policy.effect,
          requiresApproval: deferred.policy.requiresApproval,
          status: "approved",
          output: null,
          error: null,
          rejectionMessage: null,
          deferralOrder: 0,
          toolSourceReference: deferred.toolSourceReference,
        },
        idempotencyKey: "resume-session/turn-1/call-1",
        attempt: heartbeatCheckpoint === undefined ? 1 : 2,
        events: {
          turnId: "turn-1",
          attempt: 1,
          writeEvent: () => undefined,
          writeTextDelta: () => undefined,
          writeReasoningDelta: () => undefined,
          writeToolCallStarted: () => undefined,
          writeToolCallFinished: () => undefined,
        },
        cancellationSignal: new AbortController().signal,
        deadline: undefined,
        heartbeat: () => Promise.resolve(),
        supportsToolTasks,
        ...(heartbeatCheckpoint === undefined
          ? {
              saveHeartbeatCheckpoint: async (checkpoint: string) => {
                checkpoints.push(checkpoint);
              },
            }
          : { heartbeatCheckpoint }),
      }) as unknown as DeferredToolExecution;

    const started = await source.executeDeferredToolCall(execution());
    assert.equal("kind" in started ? started.kind : undefined, "tool_task_started");
    assert.equal(checkpoints.length, 1);
    assert.equal(connectors.runsOf("reports_long_report").length, 1);

    // The Worker died before the tool Step committed; the name is rebound, then the server is unavailable.
    connectors.reportsConnection = "other-reports";
    try {
      const afterRebind = await source.executeDeferredToolCall(execution(checkpoints[0]));
      assert.deepEqual(afterRebind, { kind: "tool_task_started", taskId: "taskId" in started ? started.taskId : undefined, pollIntervalSeconds: 1 });
      connectors.unavailableReason = "the connectors MCP server is restarting";
      const whileUnavailable = await source.executeDeferredToolCall(execution(checkpoints[0]));
      assert.equal("kind" in whileUnavailable ? whileUnavailable.kind : undefined, "tool_task_started");
      connectors.unavailableReason = undefined;
      assert.equal(connectors.runsOf("reports_long_report").length, 1);

      // A new send after the rebind is still refused.
      await assert.rejects(source.executeDeferredToolCall(execution()), /now names reports\/other-reports longReport; it did not run/);
      // A task polled inside the tool Step, such as a session-bound one, may be called again when lost, so it is checked.
      const polledInStep = JSON.stringify({ ...JSON.parse(checkpoints[0] ?? "{}"), mcp_task_polled_by_session: false });
      await assert.rejects(source.executeDeferredToolCall(execution(polledInStep)), /now names reports\/other-reports/);
      await assert.rejects(source.executeDeferredToolCall(execution(checkpoints[0], false)), /now names reports\/other-reports/);
      connectors.unavailableReason = "the connectors MCP server is restarting";
      await assert.rejects(source.executeDeferredToolCall(execution(polledInStep)), /its connection cannot be checked/);
      connectors.unavailableReason = undefined;
      assert.equal(connectors.runsOf("reports_long_report").length, 1);

      // A Worker whose configuration does not enable the connector refuses even a resume, without a listing.
      const otherEffective = resolveConfig({ connectors: { mcp: connectorsUrl, enabled: ["gmail"] } }, "builtin", "scripted/default");
      const otherWorker = new ConnectorsMcpToolSource(
        connectorsUrl,
        new TurnConfigResolver(otherEffective, null, () => undefined, () => Promise.resolve(undefined)),
        () => undefined,
      );
      connectors.unavailableReason = "the connectors MCP server is restarting";
      await assert.rejects(
        otherWorker.executeDeferredToolCall(execution(checkpoints[0])),
        /is not enabled in the configuration of the Worker that ran it; it did not run/,
      );
      connectors.unavailableReason = undefined;
      assert.equal(connectors.runsOf("reports_long_report").length, 1);
    } finally {
      connectors.reportsConnection = "default";
      connectors.unavailableReason = undefined;
    }
  });

  test("approval.always makes a read tool wait for approval", async () => {
    const cli = start(newHome(connectorConfig("approval: {always: [hackernews_get_top_stories]}")));
    await cli.waitForSession();
    cli.writeLine("call hackernews_get_top_stories {}");
    await cli.stdout.waitFor("Approve hackernews_get_top_stories?");
    cli.writeLine("yes");
    await cli.stdout.waitFor("result: top stories");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("Ctrl-C cancels the running turn, the session stays, and a second Ctrl-C exits", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("wait slow-model");
    await pollUntil(() => existsSync(join(home.markerDirectory, "slow-model.reached")) || undefined, {
      description: "the model call to start",
    });
    cli.interrupt();
    await cli.stdout.waitFor("Cancelling the turn");
    await cli.stdout.waitFor("Turn cancelled.");
    cli.writeLine("still here");
    await cli.stdout.waitFor("echo: still here\n\n> ");
    cli.interrupt();
    assert.equal(await cli.waitForExit(), 0);
    assert.match(cli.stdout.all, new RegExp(`--resume ${sessionId}`));
    writeFileSync(join(home.markerDirectory, "slow-model.release"), "");
    const state = await (await sessionClients.of(home)).readAgentState(sessionId);
    assert.equal(state?.status, "waiting_for_user_message");
  });

  test("--resume continues the conversation after the command exited", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const first = start(home);
    const sessionId = await first.waitForSession();
    first.writeLine("remember teal");
    await first.stdout.waitFor("echo: remember teal");
    first.writeLine("/exit");
    assert.equal(await first.waitForExit(), 0);

    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor(`Resumed session ${sessionId}`);
    second.writeLine("history");
    await second.stdout.waitFor("history: remember teal | history");
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);

    const missing = start(home, ["--resume", "scripted-no-such-session"]);
    assert.equal(await missing.waitForExit(), 2);
    assert.match(missing.stderr.all, /does not exist/);
  });

  test("session settings narrow tools and change the model; widening fails the turn and the exit code", async () => {
    const home = newHome(connectorConfig());
    const sessions = await sessionClients.of(home);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("tools");
    await cli.stdout.waitFor(/tools: .*hackernews_get_top_stories.*slack_post_thread_reply/);

    assert.equal(
      await sessions.updateSessionSettings(sessionId, {
        model: "scripted/other",
        connectors: { enabled: ["hackernews"] },
        local_tools: { shell: { enabled: false } },
        persona: "kept for another reader",
      }),
      true,
    );
    cli.writeLine("tools");
    const narrowed = await cli.stdout.waitFor(/tools: [^\n]*/);
    assert.match(narrowed, /hackernews_get_top_stories/);
    assert.doesNotMatch(narrowed, /slack_post_thread_reply|gmail_get_message|run_shell_command/);
    await cli.stderr.waitFor("Session settings keys ignored: /persona");
    cli.writeLine("model");
    await cli.stdout.waitFor("model: scripted/other");

    assert.equal(await sessions.updateSessionSettings(sessionId, { connectors: { enabled: ["gmail/personal"] } }), true);
    cli.writeLine("tools");
    await cli.stdout.waitFor('Turn failed: widens_connectors at "/connectors/enabled/0"', 120_000);
    assert.equal(connectors.runsOf("gmail_get_message").length, 0);

    assert.equal(await sessions.updateSessionSettings(sessionId, {}), true);
    cli.writeLine("model");
    await cli.stdout.waitFor("model: scripted/default");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 1);
  });

  test("a session's reasoning_effort reaches the next turn's model call; a value the harness does not declare fails the turn", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    const sessions = await sessionClients.of(home);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("effort");
    await cli.stdout.waitFor("effort: default");

    assert.equal(await sessions.updateSessionSettings(sessionId, { reasoning_effort: "high" }), true);
    cli.writeLine("effort");
    await cli.stdout.waitFor("effort: high");
    assert.doesNotMatch(cli.stderr.all, /Session settings keys ignored/);

    assert.equal(await sessions.updateSessionSettings(sessionId, { reasoning_effort: "xhigh" }), true);
    cli.writeLine("effort");
    await cli.stdout.waitFor('Turn failed: unsupported_reasoning_effort at "/reasoning_effort"', 120_000);

    assert.equal(await sessions.updateSessionSettings(sessionId, {}), true);
    cli.writeLine("effort");
    await cli.stdout.waitFor("effort: default");
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 1);
  });

  test("an operation limit needs the tool's operation keys in _meta", async () => {
    const cli = start(
      newHome(
        [
          "connectors:",
          `  mcp: ${connectorsUrl}`,
          "  enabled:",
          "    - gmail/personal: {tools: [get_message, send_message]}",
          "local_tools: {browser: false}",
        ].join("\n"),
      ),
    );
    await cli.waitForSession();
    cli.writeLine("tools");
    const tools = await cli.stdout.waitFor(/tools: [^\n]*/);
    assert.match(tools, /gmail_get_message/);
    assert.doesNotMatch(tools, /gmail_send_message|hackernews|slack/);
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("an unavailable connectors server offers no tools, and its tools return in a later listing", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    const logLines: string[] = [];
    const source = new ConnectorsMcpToolSource(
      connectorsUrl,
      new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined)),
      (line) => lines.push(line),
      undefined,
      { writeLogLine: (line) => logLines.push(line), describeUnavailable: describeWithLogHint },
    );
    connectors.unavailableReason = "the connectors MCP server is starting";
    try {
      // Two turns' listings fail: the terminal gets one short line, and the log file gets each error.
      assert.deepEqual(await source.listTools(), []);
      assert.deepEqual(await source.listTools(), []);
      assert.deepEqual(lines, ["Connector tools are unavailable for now; each turn tries again. See the log file."]);
      assert.equal(source.isAvailable, false);
      const failures = logLines.filter((line) => line.startsWith(`connectors MCP server ${connectorsUrl} is unavailable: `));
      assert.equal(failures.length, 2, logLines.join("\n"));
      assert.match(logLines.join("\n"), /the connectors MCP server is starting/);
    } finally {
      connectors.unavailableReason = undefined;
    }
    await pollUntil(async () => ((await source.listTools()).length > 0 ? true : undefined), {
      description: "the connector tools to return",
      timeoutMs: 60_000,
    });
    const unavailable = "Connector tools are unavailable for now; each turn tries again. See the log file.";
    assert.deepEqual(lines, [unavailable, "Connector tools are available again."]);
    assert.equal(source.isAvailable, true);

    // A second outage and recovery print one line each way again. The cached identities expire first.
    connectors.unavailableReason = "the connectors MCP server restarts";
    try {
      await pollUntil(async () => ((await source.listTools()).length === 0 ? true : undefined), {
        description: "the connector tools to go",
        timeoutMs: 90_000,
      });
      assert.deepEqual(await source.listTools(), []);
    } finally {
      connectors.unavailableReason = undefined;
    }
    await pollUntil(async () => ((await source.listTools()).length > 0 ? true : undefined), {
      description: "the connector tools to return again",
      timeoutMs: 60_000,
    });
    assert.ok((await source.listTools()).length > 0);
    assert.deepEqual(lines, [unavailable, "Connector tools are available again.", unavailable, "Connector tools are available again."]);
  });

  test("a server unavailable at startup prints only that its tools are available again", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    connectors.unavailableReason = "the connectors MCP server is starting";
    let source: ConnectorsMcpToolSource;
    try {
      const startupAvailability = await probeConnectorsMcp(connectorsUrl);
      assert.equal(startupAvailability.isAvailable, false);
      // The command already printed the startup line for this failure.
      source = new ConnectorsMcpToolSource(
        connectorsUrl,
        new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined)),
        (line) => lines.push(line),
        undefined,
        { startupAvailability, writeLogLine: () => undefined, describeUnavailable: describeWithLogHint },
      );
      assert.deepEqual(await source.listTools(), []);
      assert.deepEqual(lines, []);
    } finally {
      connectors.unavailableReason = undefined;
    }
    await pollUntil(async () => ((await source.listTools()).length > 0 ? true : undefined), {
      description: "the connector tools to return",
      timeoutMs: 60_000,
    });
    assert.ok((await source.listTools()).length > 0);
    assert.deepEqual(lines, ["Connector tools are available again."]);
  });

  test("a server that answers initialize but denies tools/list prints one access-denied line", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    const source = new ConnectorsMcpToolSource(
      connectorsUrl,
      new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined)),
      (line) => lines.push(line),
      undefined,
      { writeLogLine: () => undefined, describeUnavailable: describeWithLogHint },
    );
    connectors.listingAccessDeniedStatus = 403;
    try {
      assert.deepEqual(await source.listTools(), []);
      assert.deepEqual(await source.listTools(), []);
    } finally {
      connectors.listingAccessDeniedStatus = undefined;
    }
    assert.deepEqual(lines, [
      "Connector tools are unavailable. The connectors MCP server denied access (HTTP 403). " +
        "Check its access settings and that connectors.mcp names the right server. See the log file.",
    ]);
  });

  test("a fast listing failure prints its line while the identity listing hangs, and closes that request", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    const source = new ConnectorsMcpToolSource(
      connectorsUrl,
      new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined)),
      (line) => lines.push(line),
      undefined,
      { writeLogLine: () => undefined, describeUnavailable: describeWithLogHint },
    );
    connectors.listingAccessDeniedStatus = 403;
    connectors.isIdentityListingHanging = true;
    const closedBefore = connectors.closedHangingRequests;
    try {
      const started = Date.now();
      assert.deepEqual(await source.listTools(), []);
      assert.ok(Date.now() - started < 5_000, `the listing took ${Date.now() - started} ms`);
      assert.deepEqual(lines, [
        "Connector tools are unavailable. The connectors MCP server denied access (HTTP 403). " +
          "Check its access settings and that connectors.mcp names the right server. See the log file.",
      ]);
      await pollUntil(() => (connectors.closedHangingRequests > closedBefore ? true : undefined), {
        description: "the hanging identity listing request to close",
        timeoutMs: 5_000,
      });
    } finally {
      connectors.listingAccessDeniedStatus = undefined;
      connectors.isIdentityListingHanging = false;
    }
  });

  test("a server that denies access prints one line that says so", async () => {
    const effective = resolveConfig({ connectors: { mcp: connectorsUrl } }, "builtin", "scripted/default");
    const lines: string[] = [];
    const source = new ConnectorsMcpToolSource(
      connectorsUrl,
      new TurnConfigResolver(effective, null, () => undefined, () => Promise.resolve(undefined)),
      (line) => lines.push(line),
      undefined,
      { writeLogLine: () => undefined, describeUnavailable: describeWithLogHint },
    );
    connectors.accessDeniedStatus = 401;
    try {
      assert.deepEqual(await source.listTools(), []);
      assert.deepEqual(await source.listTools(), []);
    } finally {
      connectors.accessDeniedStatus = undefined;
    }
    assert.deepEqual(lines, [
      "Connector tools are unavailable. The connectors MCP server denied access (HTTP 401). " +
        "Check its access settings and that connectors.mcp names the right server. See the log file.",
    ]);
  });

  test("a Worker whose file now switches shell off refuses a shell Step that another process requested", async () => {
    const home = newHome("connectors: {enabled: []}\nlocal_tools: {browser: false}\n");
    // A short lease, so the resumed chat does not wait 600 seconds for the killed command's lease.
    const first = start(home, [], { DEX_AGENTS_SESSION_LEASE_TTL_SECONDS: "6" });
    const sessionId = await first.waitForSession();
    first.writeLine('call run_shell_command {"command":"echo ran > ran.txt"}');
    await first.stdout.waitFor("Approve run_shell_command?");
    await first.kill();

    home.writeConfig("connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: false}}\n");
    const second = start(home, ["--resume", sessionId]);
    await second.stdout.waitFor("Approve run_shell_command?");
    second.writeLine("y");
    await second.stdout.waitFor(/result: .*switched off in the configuration of the Worker that ran it; it did not run/);
    assert.equal(existsSync(join(home.workspace, "ran.txt")), false);
    second.writeLine("/exit");
    assert.equal(await second.waitForExit(), 0);
  });

  test("a replacement shell tool reads the turn's narrowed permissions in its tool Step", async () => {
    const home = new TestHome(enginePorts(engine), shellReplacingScriptedHarness);
    home.writeConfig('connectors: {enabled: []}\nlocal_tools: {browser: false, shell: {enabled: true, allow: ["ls", "pwd"]}}\n');
    const sessions = await sessionClients.of(home);
    const cli = start(home);
    const sessionId = await cli.waitForSession();
    cli.writeLine("tools");
    const tools = await cli.stdout.waitFor(/tools: [^\n]*/);
    assert.match(tools, /replacement_shell/);
    assert.doesNotMatch(tools, /run_shell_command/);
    cli.writeLine("call replacement_shell {}");
    await cli.stdout.waitFor('result: {"enabled":true,"allow":["ls","pwd"]}');
    assert.equal(await sessions.updateSessionSettings(sessionId, { local_tools: { shell: { allow: ["ls"] } } }), true);
    cli.writeLine("call replacement_shell {}");
    await cli.stdout.waitFor('result: {"enabled":true,"allow":["ls"]}');
    assert.equal(await sessions.updateSessionSettings(sessionId, { local_tools: { shell: { enabled: false } } }), true);
    cli.writeLine("call replacement_shell {}");
    await cli.stdout.waitFor('result: {"enabled":false,"allow":["ls","pwd"]}');
    cli.writeLine("/exit");
    assert.equal(await cli.waitForExit(), 0);
  });

  test("commands with different configurations never run each other's open sessions' Steps", async () => {
    const config = "connectors: {enabled: []}\nlocal_tools: {browser: false}\n";
    const homeA = newHome(config);
    const homeB = newHome(config);
    assert.notEqual(homeA.legacySessionFlowType(), homeB.legacySessionFlowType());
    const cliA = start(homeA);
    const cliB = start(homeB);
    const sessionA = await cliA.waitForSession();
    await cliB.waitForSession();
    for (let index = 0; index < 4; index += 1) {
      cliA.writeLine(`call write_file {"path":"from-a-${index}.txt","content":"a"}`);
      await cliA.stdout.waitFor("Approve write_file?");
      cliA.writeLine("y");
      await cliA.stdout.waitFor(`result: wrote 1 bytes to from-a-${index}.txt`);
      assert.equal(existsSync(join(homeA.workspace, `from-a-${index}.txt`)), true);
      assert.equal(existsSync(join(homeB.workspace, `from-a-${index}.txt`)), false);
    }
    cliA.writeLine("/exit");
    assert.equal(await cliA.waitForExit(), 0);
    cliB.writeLine("/exit");
    assert.equal(await cliB.waitForExit(), 0);

    // Since 0.6.8 the other environment takes the session over, and its tool Steps then run there.
    const resumeInB = start(homeB, ["--resume", sessionA]);
    await resumeInB.stdout.waitFor(`Resumed session ${sessionA}.`);
    resumeInB.writeLine('call write_file {"path":"from-b.txt","content":"b"}');
    await resumeInB.stdout.waitFor("Approve write_file?");
    resumeInB.writeLine("y");
    await resumeInB.stdout.waitFor("result: wrote 1 bytes to from-b.txt");
    assert.equal(existsSync(join(homeB.workspace, "from-b.txt")), true);
    assert.equal(existsSync(join(homeA.workspace, "from-b.txt")), false);
    resumeInB.writeLine("/exit");
    assert.equal(await resumeInB.waitForExit(), 0);
  });
});

/** The payload of the history event that starts a Flow, as dexcli flow history prints it. */
interface StartedPayload {
  readonly flowType: string;
  readonly initialStart: { readonly stepInput: { readonly start_environment: Record<string, string> } };
}

function describeWithLogHint(unavailability: ConnectorsMcpUnavailability): string {
  return connectorsUnavailableLine(unavailability, "See the log file.");
}

// Same shape as the Python CLI core: <harness>-<UTC yyyymmdd-HHMMSS>-<6 hex>.
function assertSessionIdNamesTheHarnessAndUtcStart(sessionId: string, spawnedAt: number): void {
  const match = /^scripted-(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})-[0-9a-f]{6}$/.exec(sessionId);
  assert.ok(match !== null, sessionId);
  const [year, month, day, hour, minute, second] = match.slice(1).map(Number);
  const startedAt = Date.UTC(year!, month! - 1, day!, hour!, minute!, second!);
  assert.ok(spawnedAt <= startedAt && startedAt <= Date.now(), `${sessionId} started before ${new Date(spawnedAt).toISOString()}`);
}
