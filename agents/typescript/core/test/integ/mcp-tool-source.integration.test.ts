// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { BasicTracerProvider, InMemorySpanExporter, SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";

import {
  ToolExecutionError,
  createAgentSessionFlow,
  outcomeUnknownToolResult,
  resolveToolPolicy,
} from "../../src/index.js";
import {
  McpToolSource,
  defaultMcpTaskWaitTimeoutMs,
  mcpTaskStatusEventType,
  mcpToolProgressEventType,
} from "../../src/mcp/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  findFreeLoopbackPort,
  readStepHistory,
} from "../../src/testing/index.js";
import { ChildProcessGroup } from "../../src/testing/child-process-group.js";
import {
  fakeMcpGatewayModulePath,
  fakeMcpInvocationLogName,
  fakeMcpReadyLine,
  fakeMcpServerModulePath,
  fakeMcpStateDirectoryVariable,
} from "../fixtures/fake-mcp-server-names.js";
import {
  McpScriptedAgentRunner,
  createMcpScriptedAgentFlow,
  createMcpToolSource,
  mcpAgentGatewayUrlVariable,
  mcpAgentHttpUrlVariable,
  mcpAgentSessionsUrlVariable,
  mcpAgentToolPolicies,
} from "../fixtures/mcp-scripted-agent-runner.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/mcp-scripted-agent-worker.js", import.meta.url));

describe("MCP tools run as tool Steps against local stdio and Streamable HTTP servers", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let stateDirectory: string;
  let httpPort: number;
  let httpUrl: string;
  let httpServer: ChildProcessGroup | undefined;
  const workers: KillableWorkerProcess[] = [];
  const helperProcesses: ChildProcessGroup[] = [];

  async function startHttpServer(): Promise<void> {
    httpServer = await ChildProcessGroup.start({
      description: "fake MCP server",
      command: process.execPath,
      commandArguments: [fakeMcpServerModulePath, "http", String(httpPort)],
      environment: { ...process.env, [fakeMcpStateDirectoryVariable]: stateDirectory },
      logPath: join(stateDirectory, "fake-mcp-server.log"),
      readyLine: fakeMcpReadyLine,
      readyTimeoutMs: 30_000,
    });
  }

  before(async () => {
    engine = await PrivateDexEngine.start();
    stateDirectory = mkdtempSync(join(engine.stateDirectory, "mcp-state-"));
    httpPort = await findFreeLoopbackPort();
    httpUrl = `http://127.0.0.1:${httpPort}/mcp`;
    await startHttpServer();
    const flow = createMcpScriptedAgentFlow(stateDirectory, httpUrl);
    environment = await AgentTestEnvironment.start({ serverAddress: engine.serverAddress, flows: [flow], isWorkerInProcess: false });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
    for (const helperProcess of helperProcesses.splice(0)) {
      await helperProcess.kill();
    }
  });

  after(async () => {
    await httpServer?.kill();
    await environment?.close();
    await engine?.stop();
  });

  async function startWorker(extraEnvironment: Readonly<Record<string, string>> = {}): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(stateDirectory, "worker.log"),
      environment: {
        [fakeMcpStateDirectoryVariable]: stateDirectory,
        [mcpAgentHttpUrlVariable]: httpUrl,
        ...extraEnvironment,
      },
    });
    workers.push(worker);
    return worker;
  }

  /** Starts a child process that prints the fake MCP ready line and returns its URL. */
  async function startFakeMcpProcess(description: string, commandArguments: readonly string[], port: number) {
    const processGroup = await ChildProcessGroup.start({
      description,
      command: process.execPath,
      commandArguments,
      environment: { ...process.env, [fakeMcpStateDirectoryVariable]: stateDirectory },
      logPath: join(stateDirectory, `${description.replaceAll(" ", "-")}.log`),
      readyLine: fakeMcpReadyLine,
      readyTimeoutMs: 30_000,
    });
    helperProcesses.push(processGroup);
    return `http://127.0.0.1:${port}/mcp`;
  }

  /** Idempotency keys of every real run of `toolName` in the session, in run order. */
  function toolRuns(toolName: string, sessionId: string): string[] {
    return new InvocationLog(join(stateDirectory, fakeMcpInvocationLogName))
      .names()
      .filter((name) => name.startsWith(`${toolName} ${sessionId}/`))
      .map((name) => name.slice(toolName.length + 1));
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function eventPayloads(sessionId: string, type: string): Promise<Record<string, unknown>[]> {
    return (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === type)
      .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
  }

  async function lastMessageText(sessionId: string): Promise<string | null | undefined> {
    return (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text;
  }

  test("annotations choose each tool's effect and an explicit policy wins", async () => {
    const tools = createMcpToolSource(stateDirectory, httpUrl);
    const listed = new Map((await tools.listTools()).map((tool) => [tool.name, tool]));

    assert.equal(listed.get("local__lookup")?.frameworkEffect, "read");
    assert.deepEqual(listed.get("local__lookup")?.annotations, { readOnlyHint: true });
    assert.equal(listed.get("remote__record_note")?.frameworkEffect, "idempotent_write");
    // idempotentHint wins over destructiveHint, as in the Python core.
    assert.equal(listed.get("remote__purge_cache")?.frameworkEffect, "idempotent_write");
    assert.equal(listed.get("local__audit_lookup")?.mcpToolName, "audit_lookup");
    assert.equal(listed.get("remote__send_message")?.frameworkEffect, undefined);
    assert.equal(listed.get("remote__send_message")?.description, "Send a message to a person.");
    assert.equal(listed.get("remote__long_report")?.taskSupport, "required");
    assert.deepEqual(tools.toolPolicies()["remote__record_note"], { effect: "idempotent_write" });
    assert.equal(tools.toolPolicies()["remote__send_message"], undefined);
    const auditLookup = listed.get("local__audit_lookup");
    assert.deepEqual(resolveToolPolicy("local__audit_lookup", mcpAgentToolPolicies, "unclassified", auditLookup?.frameworkEffect), {
      effect: "non_idempotent_write",
      requiresApproval: true,
    });

    const untrusting = new McpToolSource([
      {
        transport: "stdio",
        name: "untrusted",
        command: process.execPath,
        args: [fakeMcpServerModulePath, "stdio"],
        env: { [fakeMcpStateDirectoryVariable]: stateDirectory },
        trustsToolAnnotations: false,
      },
    ]);
    const untrusted = new Map((await untrusting.listTools()).map((tool) => [tool.name, tool]));
    assert.equal(untrusted.get("untrusted__lookup")?.frameworkEffect, undefined);
    assert.deepEqual(untrusting.toolPolicies(), {});
  });

  test("MCP tool calls emit execute_tool spans in their activation's trace", async () => {
    const exporter = new InMemorySpanExporter();
    const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
    const tracedFlow = createAgentSessionFlow(
      new McpScriptedAgentRunner(createMcpToolSource(stateDirectory, httpUrl)),
      "TracedMcpScriptedAgent",
      { toolPolicies: mcpAgentToolPolicies, tracing: { tracerProvider } },
    );
    const tracedEnvironment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [tracedFlow],
      isWorkerInProcess: true,
    });
    try {
      const tracedSessions = new AgentSessionTestClient(tracedEnvironment.client, tracedFlow);
      const sessionId = `mcp-traced-${randomUUID()}`;
      await tracedSessions.startSession(
        sessionId,
        // record_note, not lookup: lookup's crash_first counts runs across this file's tests.
        'call remote__record_note {"note": "traced"} && call remote__send_message {"text": "hi"}',
      );
      const interrupt = await tracedSessions.waitForPendingInterrupt(sessionId, "remote__send_message");
      assert.equal(await tracedSessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await tracedSessions.waitForCompletedTurns(sessionId, 1);

      const spans = exporter
        .getFinishedSpans()
        .filter((span) => span.attributes["gen_ai.conversation.id"] === sessionId);
      const rootOf = (interruptId: string | undefined) =>
        spans.find(
          (span) =>
            span.name === "invoke_agent TracedMcpScriptedAgent" &&
            span.attributes["dex.agent.interrupt.id"] === interruptId,
        );
      const toolSpans = [
        ["execute_tool remote__record_note", "CallTool", "idempotent_write", rootOf(undefined)],
        ["execute_tool remote__send_message", "CallSingleAttemptTool", "unclassified", rootOf(interrupt.interruptId)],
      ] as const;
      for (const [name, stepType, effect, root] of toolSpans) {
        const span = spans.find((candidate) => candidate.name === name);
        assert.ok(span !== undefined && root !== undefined, name);
        assert.equal(span.parentSpanContext?.spanId, root.spanContext().spanId, name);
        assert.equal(span.attributes["dex.step.type"], stepType, name);
        assert.equal(span.attributes["dex.agent.tool.effect"], effect, name);
        assert.equal(span.attributes["dex.agent.tool_call.status"], "succeeded", name);
      }
    } finally {
      await tracedEnvironment.close();
      await tracerProvider.shutdown();
    }
  });

  test("a read tool re-issues a call whose stream broke, in CallTool", async () => {
    await startWorker();
    const sessionId = `mcp-read-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call local__lookup {"query": "q", "crash_first": true}');
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), "local__lookup: found q");
    const key = `${sessionId}/turn-1/turn-1-call-0`;
    assert.deepEqual(toolRuns("lookup", sessionId), [key, key]);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel"]);
    const callTool = (await readStepHistory(engine.serverAddress, sessionId)).filter((entry) => entry.stepType === "CallTool");
    assert.deepEqual(
      callTool.map((entry) => entry.finalAttempt),
      [2],
    );
  });

  test("an unclassified write waits for approval and runs once", async () => {
    await startWorker();
    const sessionId = `mcp-approval-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__send_message {"text": "hello"}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_message");
    assert.deepEqual(toolRuns("send_message", sessionId), []);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), "remote__send_message: sent hello");
    assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    assert.deepEqual((await executedStepTypes(sessionId)).slice(2, 4), ["AwaitToolApproval", "CallSingleAttemptTool"]);
    assert.deepEqual(await eventPayloads(sessionId, mcpToolProgressEventType), [
      { message: "sent hello", progress: 1, tool_call_id: "turn-1-call-0", tool_name: "remote__send_message", total: 1 },
    ]);
  });

  test("a Worker killed during an MCP write records outcome_unknown and never re-sends it", async () => {
    const firstWorker = await startWorker();
    const sessionId = `mcp-kill-${randomUUID()}`;
    const gateName = `send-${randomUUID()}`;
    await sessions.startSession(sessionId, `call remote__send_message {"text": "once", "gate": "${gateName}"}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_message");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const gate = new FileGate(stateDirectory, gateName);
    await gate.waitUntilReached();

    await firstWorker.kill();
    // The HTTP server finishes the write; a wrongful re-send would log a second run.
    gate.release();
    await startWorker();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(await lastMessageText(sessionId), `remote__send_message: ${outcomeUnknownToolResult}`);
    assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    assert.equal(gate.reachedCount(), 1);
    assert.ok((await executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
  });

  test("a broken stream during an unclassified write reports outcome_unknown instead of re-issuing", async () => {
    await startWorker();
    const sessionId = `mcp-broken-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call local__send_message {"text": "once", "crash": true}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "local__send_message");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), `local__send_message: ${outcomeUnknownToolResult}`);
    assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    const steps = await executedStepTypes(sessionId);
    assert.ok(steps.includes("RecordToolOutcomeUnknown"));
    assert.ok(!steps.includes("CallSingleAttemptTool"));
  });

  test("an MCP server restart between listing and the call is harmless", async () => {
    await startWorker();
    const sessionId = `mcp-restart-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__send_message {"text": "after restart"}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_message");

    await httpServer?.kill();
    await startHttpServer();
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), "remote__send_message: sent after restart");
    assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
  });

  test("a policy overrides annotations and annotations pick the tool Step", async () => {
    await startWorker();
    const sessionId = `mcp-policy-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call local__audit_lookup {"query": "q"} && call remote__record_note {"note": "n"}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "local__audit_lookup");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      Object.fromEntries(waiting.toolCalls.map((call) => [call.toolName, call.effect])),
      { local__audit_lookup: "non_idempotent_write", remote__record_note: "idempotent_write" },
    );

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not now"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), "local__audit_lookup: not now; remote__record_note: noted n");
    assert.deepEqual(toolRuns("audit_lookup", sessionId), []);
    assert.deepEqual(toolRuns("record_note", sessionId), [`${sessionId}/turn-1/turn-1-call-1`]);
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((step) => step === "CallTool").length, 1);
    assert.ok(!steps.includes("CallSingleAttemptTool"));
  });

  test("a task-required tool is polled inside its tool Step, and a task needing input fails clearly", async () => {
    await startWorker();
    const sessionId = `mcp-task-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__long_report {"topic": "q3"} && call remote__ask_account {}');
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(
      await lastMessageText(sessionId),
      "remote__long_report: report on q3; remote__ask_account: The tool call failed: the MCP server asked for " +
        "input that this agent session cannot provide yet (Which account?), so the tool did not finish",
    );
    assert.deepEqual(toolRuns("long_report", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    const statuses = (await eventPayloads(sessionId, mcpTaskStatusEventType))
      .filter((payload) => payload["tool_name"] === "remote__long_report")
      .map((payload) => payload["status"]);
    assert.equal(statuses[0], "working");
    assert.equal(statuses.at(-1), "completed");
    assert.equal((await executedStepTypes(sessionId)).filter((step) => step === "CallTool").length, 2);
  });
  for (const gatewayMode of ["502", "internal-error"] as const) {
    test(`a gateway ${gatewayMode} after the server ran a write records outcome_unknown`, async () => {
      const gatewayPort = await findFreeLoopbackPort();
      const gatewayUrl = await startFakeMcpProcess(
        `fake MCP gateway ${gatewayMode}`,
        [fakeMcpGatewayModulePath, String(gatewayPort), httpUrl, gatewayMode],
        gatewayPort,
      );
      await startWorker({ [mcpAgentGatewayUrlVariable]: gatewayUrl });
      const sessionId = `mcp-gateway-${randomUUID()}`;
      await sessions.startSession(sessionId, 'call gateway__send_message {"text": "through"}');
      const interrupt = await sessions.waitForPendingInterrupt(sessionId, "gateway__send_message");
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await sessions.waitForCompletedTurns(sessionId, 1);

      // The server ran the write, so a failed record would invite the model to repeat it.
      assert.equal(await lastMessageText(sessionId), `gateway__send_message: ${outcomeUnknownToolResult}`);
      assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
      assert.ok((await executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
    });
  }

  test("a Worker killed while polling a task resumes the same task", async () => {
    const firstWorker = await startWorker();
    const sessionId = `mcp-task-kill-${randomUUID()}`;
    const gateName = `report-${randomUUID()}`;
    await sessions.startSession(sessionId, `call remote__long_report {"topic": "q4", "gate": "${gateName}"}`);
    const gate = new FileGate(stateDirectory, gateName);
    // Two polls: the attempt saved the task ID as its heartbeat checkpoint before the first.
    await gate.waitUntilReached(2);

    await firstWorker.kill();
    await startWorker();
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(await lastMessageText(sessionId), "remote__long_report: report on q4");
    assert.deepEqual(toolRuns("long_report", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    const callTool = (await readStepHistory(engine.serverAddress, sessionId)).filter((entry) => entry.stepType === "CallTool");
    assert.deepEqual(
      callTool.map((entry) => entry.finalAttempt),
      [2],
    );
    // The requested TTL outlasts every attempt's bounded wait.
    const requestedTtls = new InvocationLog(join(stateDirectory, fakeMcpInvocationLogName))
      .names()
      .filter((line) => line.startsWith("long_report-ttl "))
      .map((line) => Number(line.split(" ")[1]));
    assert.ok(requestedTtls.length > 0);
    assert.ok(requestedTtls.every((ttl) => ttl >= 3 * defaultMcpTaskWaitTimeoutMs));
  });

  test("a server that binds tasks to its protocol session gets the call again after a Worker kill", async () => {
    const sessionsPort = await findFreeLoopbackPort();
    const sessionsUrl = await startFakeMcpProcess(
      "fake MCP sessions server for tasks",
      [fakeMcpServerModulePath, "http-sessions", String(sessionsPort)],
      sessionsPort,
    );
    const firstWorker = await startWorker({ [mcpAgentSessionsUrlVariable]: sessionsUrl });
    const sessionId = `mcp-task-sessions-${randomUUID()}`;
    const gateName = `report-${randomUUID()}`;
    await sessions.startSession(sessionId, `call sessions__long_report {"topic": "q5", "gate": "${gateName}"}`);
    const gate = new FileGate(stateDirectory, gateName);
    await gate.waitUntilReached(2);

    await firstWorker.kill();
    await startWorker({ [mcpAgentSessionsUrlVariable]: sessionsUrl });
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    // The retry's new session cannot see the task, so the read is called again, as documented.
    assert.equal(await lastMessageText(sessionId), "sessions__long_report: report on q5");
    const key = `${sessionId}/turn-1/turn-1-call-0`;
    assert.deepEqual(toolRuns("long_report", sessionId), [key, key]);
  });

  test("an unreachable server leaves the other servers' tools working", async () => {
    const downPort = await findFreeLoopbackPort();
    const tools = new McpToolSource(
      [
        { transport: "streamable_http", name: "down", url: `http://127.0.0.1:${downPort}/mcp` },
        { transport: "streamable_http", name: "remote", url: httpUrl },
      ],
      { connectionAttempts: 1 },
    );

    const listed = (await tools.listTools()).map((tool) => tool.name);
    assert.ok(listed.includes("remote__lookup"));
    assert.ok(!listed.some((name) => name.startsWith("down__")));
    assert.deepEqual(await tools.callTool("remote__lookup", { query: "q" }), { output: "found q" });
    await assert.rejects(tools.callTool("down__lookup", { query: "q" }), (error: unknown) => {
      assert.ok(error instanceof ToolExecutionError);
      assert.match(error.message, /no listed MCP server offers/);
      return true;
    });
  });

  test("a write that was never sent is recorded failed, not outcome_unknown", async () => {
    await startWorker();
    const sessionId = `mcp-never-sent-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__send_message {"text": "never sent"}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_message");

    await httpServer?.kill();
    try {
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await sessions.waitForCompletedTurns(sessionId, 1);
    } finally {
      await startHttpServer();
    }

    assert.match(
      (await lastMessageText(sessionId)) ?? "",
      /^remote__send_message: The tool call failed: could not connect to MCP server remote, so send_message did not run/,
    );
    assert.deepEqual(toolRuns("send_message", sessionId), []);
    const steps = await executedStepTypes(sessionId);
    assert.ok(steps.includes("CallSingleAttemptTool"));
    assert.ok(!steps.includes("RecordToolOutcomeUnknown"));
  });

  test("a server that never answers the handshake is bounded", async () => {
    const tools = new McpToolSource(
      [{ transport: "stdio", name: "silent", command: process.execPath, args: ["-e", "setTimeout(() => {}, 60_000)"] }],
      { connectionTimeoutMs: 1_000, connectionAttempts: 1 },
    );
    const startedAt = Date.now();

    await assert.rejects(tools.callTool("silent__anything", {}), (error: unknown) => {
      assert.ok(error instanceof ToolExecutionError);
      assert.match(error.message, /did not run/);
      return true;
    });
    assert.ok(Date.now() - startedAt < 15_000);
  });

  test("every protocol session a request opens is ended", async () => {
    const sessionsPort = await findFreeLoopbackPort();
    const sessionsUrl = await startFakeMcpProcess(
      "fake MCP sessions server",
      [fakeMcpServerModulePath, "http-sessions", String(sessionsPort)],
      sessionsPort,
    );
    const invocationLog = new InvocationLog(join(stateDirectory, fakeMcpInvocationLogName));
    const linesBefore = invocationLog.names().length;
    const tools = new McpToolSource([{ transport: "streamable_http", name: "stateful", url: sessionsUrl }]);
    await tools.listTools();
    for (let call = 0; call < 3; call += 1) {
      assert.deepEqual(await tools.callTool("stateful__lookup", { query: `q${call}` }), { output: `found q${call}` });
    }

    const lines = invocationLog.names().slice(linesBefore);
    const opened = lines.filter((line) => line.startsWith("session-open ")).map((line) => line.split(" ")[1]);
    const closed = new Set(lines.filter((line) => line.startsWith("session-closed ")).map((line) => line.split(" ")[1]));
    assert.equal(opened.length, 4);
    assert.deepEqual(
      opened.filter((sessionId) => !closed.has(sessionId)),
      [],
    );
  });
});
