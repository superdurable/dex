// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { format, inspect } from "node:util";
import { after, afterEach, before, describe, test } from "node:test";

import { McpError, type CallToolResult, type Tool } from "@modelcontextprotocol/sdk/types.js";
import { BasicTracerProvider, InMemorySpanExporter, SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";

import {
  ToolExecutionError,
  createAgentSessionFlow,
  outcomeUnknownToolResult,
  resolveToolPolicy,
  type AgentTurnContext,
  type DeferredToolExecution,
  type ToolEffect,
  type ToolPolicy,
} from "../../src/index.js";
import {
  McpToolSource,
  defaultMcpTaskTtlMs,
  defaultMcpTaskWaitTimeoutMs,
  mcpIdempotencyKeyMetaKey,
  mcpTaskStatusEventType,
  mcpToolApprovalMetaKey,
  mcpToolApprovalRecommended,
  mcpToolEffectMetaKey,
  mcpToolOutcomeMetaKey,
  mcpToolOutcomeRejected,
  mcpToolOutcomeUnknown,
  mcpToolProgressEventType,
  type McpStdioServerConfig,
  type McpStreamableHttpServerConfig,
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
  mcpToolFromListing,
  outcomeFromMcpError,
  outcomeFromToolResult,
  type ToolCallOutcome,
} from "../../src/mcp/mcp-tool-source.js";
import { conformanceSuiteDirectory } from "../fixtures/conformance-suite.js";
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

  function requestedTaskTtls(): number[] {
    return new InvocationLog(join(stateDirectory, fakeMcpInvocationLogName))
      .names()
      .filter((line) => line.startsWith("long_report-ttl "))
      .map((line) => Number(line.split(" ")[1]));
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

  test("a task-required tool of a stateless server is polled by PollToolTask, and a task needing input fails clearly", async () => {
    await startWorker();
    const sessionId = `mcp-task-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__long_report {"topic": "q3"} && call remote__ask_account {}');
    await sessions.waitForCompletedTurns(sessionId, 1);

    // The 2025-11-25 tasks deliver input requests on a live connection, which no durable question can hold.
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
    assert.deepEqual(
      (await eventPayloads(sessionId, "tool_task_started")).map((payload) => payload["tool_name"]).sort(),
      ["remote__ask_account", "remote__long_report"],
    );
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((step) => step === "CallTool").length, 2);
    assert.ok(steps.filter((step) => step === "PollToolTask").length >= 2);
  });

  test("cancel_turn cancels a running task of a stateless server and ends the turn at once", async () => {
    await startWorker();
    const sessionId = `mcp-task-cancel-${randomUUID()}`;
    const gateName = `report-${randomUUID()}`;
    await sessions.startSession(sessionId, `call remote__long_report {"topic": "q5", "gate": "${gateName}"}`);
    await new FileGate(stateDirectory, gateName).waitUntilReached();

    const cancelledAt = Date.now();
    assert.equal(await sessions.cancelTurn(sessionId), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.ok(Date.now() - cancelledAt < 15_000);

    const [taskId] = (await eventPayloads(sessionId, "tool_task_started")).map((payload) => payload["task_id"]);
    assert.equal(typeof taskId, "string");
    assert.equal(
      new InvocationLog(join(stateDirectory, fakeMcpInvocationLogName)).names().filter((line) => line === `cancel ${taskId}`)
        .length,
      1,
    );
    assert.ok((await lastMessageText(sessionId))?.includes(outcomeUnknownToolResult));
    assert.deepEqual((await executedStepTypes(sessionId)).slice(-3), ["CancelToolTask", "ResumeTurn", "ApplyTurnControl"]);
  });

  test("a stdio server's task is polled inside its tool Step", async () => {
    await startWorker();
    const sessionId = `mcp-stdio-task-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call local__long_report {"topic": "q6"}');
    await sessions.waitForCompletedTurns(sessionId, 1);

    // The stdio server's task lives in the process that the tool Step holds.
    assert.equal(await lastMessageText(sessionId), "local__long_report: report on q6");
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((step) => step === "CallTool").length, 1);
    assert.ok(!steps.includes("PollToolTask"));
  });

  test("a retried attempt resumes its task without calling the tool again", async () => {
    const tools = createMcpToolSource(stateDirectory, httpUrl);
    const savedCheckpoints: string[] = [];
    const first = await tools.executeDeferredToolCall(
      deferredExecution('{"topic": "q8"}', undefined, async (checkpoint) => {
        savedCheckpoints.push(checkpoint);
      }),
    );
    assert.equal("kind" in first ? first.kind : undefined, "tool_task_started");
    assert.equal(savedCheckpoints.length, 1);
    const retried = await tools.executeDeferredToolCall(deferredExecution('{"topic": "q8"}', savedCheckpoints[0]));

    assert.deepEqual(retried, {
      kind: "tool_task_started",
      taskId: "kind" in first ? first.taskId : undefined,
      pollIntervalSeconds: 1,
    });
    assert.deepEqual(toolRuns("long_report", "direct-session"), ["direct-session/turn-1/call-1"]);
  });

  test("without a poller the tool Step polls the task to its end", async () => {
    const tools = createMcpToolSource(stateDirectory, httpUrl);

    const result = await tools.executeDeferredToolCall(
      deferredExecution('{"topic": "q14"}', undefined, undefined, false, "direct-session-no-poller"),
    );

    assert.deepEqual(result, { output: "report on q14" });
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

    const ttlLinesBefore = requestedTaskTtls().length;
    await firstWorker.kill();
    await startWorker();
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(await lastMessageText(sessionId), "remote__long_report: report on q4");
    assert.deepEqual(toolRuns("long_report", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    // The tool Step ended once it had the task; PollToolTask polled it across the Worker kill.
    assert.deepEqual(
      history.filter((entry) => entry.stepType === "CallTool").map((entry) => entry.finalAttempt),
      [1],
    );
    assert.ok(gate.reachedCount() >= 3);
    // Polls that saw the task still working wrote no event; only status changes do.
    assert.deepEqual(
      (await eventPayloads(sessionId, mcpTaskStatusEventType)).map((payload) => payload["status"]),
      ["working", "completed"],
    );
    // The durable poll may span Worker restarts, so the task's TTL is the long one, not a multiple of a wait.
    assert.equal(requestedTaskTtls().length, ttlLinesBefore);
    assert.equal(requestedTaskTtls().at(-1), defaultMcpTaskTtlMs);
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
    // The session-bound task is polled inside the tool Step, with a TTL that outlasts its bounded waits.
    assert.ok(!(await executedStepTypes(sessionId)).includes("PollToolTask"));
    assert.equal(requestedTaskTtls().at(-1), 4 * defaultMcpTaskWaitTimeoutMs);
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

  test("Dex _meta declares effect and approval, and a session policy only raises safety", async () => {
    const tools = createMcpToolSource(stateDirectory, httpUrl);
    const listed = new Map((await tools.listTools()).map((tool) => [tool.name, tool]));
    assert.equal(listed.get("remote__post_note")?.dexEffect, "non_idempotent_write");
    assert.deepEqual(listed.get("remote__post_note")?.annotations, { readOnlyHint: true });
    assert.equal(listed.get("remote__send_payment")?.isApprovalRecommended, true);
    assert.deepEqual(tools.toolPolicies()["remote__send_payment"], { effect: "idempotent_write", requiresApproval: true });
    assert.deepEqual(tools.toolPolicies()["remote__post_note"], { effect: "non_idempotent_write", requiresApproval: false });

    await startWorker();
    const sessionId = `mcp-dex-declared-${randomUUID()}`;
    await sessions.startSession(
      sessionId,
      'call remote__get_balance {"account": "a"} && call remote__post_note {"text": "hi"} && call remote__send_payment {"amount": 5}',
    );
    const first = await sessions.waitForPendingInterrupt(sessionId, "remote__get_balance");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    // A non-idempotent write policy raises the declared read; its default asks for approval.
    assert.deepEqual(
      Object.fromEntries(
        waiting.toolCalls.map((call) => [call.toolName, [call.effect, call.requiresApproval, call.orderedWrite]]),
      ),
      {
        remote__get_balance: ["non_idempotent_write", true, true],
        remote__post_note: ["non_idempotent_write", false, true],
        remote__send_payment: ["idempotent_write", true, true],
      },
    );
    // post_note waits for the earlier ordered write, get_balance.
    assert.deepEqual(toolRuns("post_note", sessionId), []);
    assert.equal(await sessions.resolveInterrupt(sessionId, first.interruptId, true), true);
    // post_note runs without approval; send_payment's approval is raised once its turn comes.
    const second = await sessions.waitForPendingInterrupt(sessionId, "remote__send_payment");
    assert.deepEqual(toolRuns("get_balance", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    assert.deepEqual(toolRuns("post_note", sessionId), [`${sessionId}/turn-1/turn-1-call-1`]);
    assert.equal(await sessions.resolveInterrupt(sessionId, second.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(
      await lastMessageText(sessionId),
      "remote__get_balance: balance of a is 5; remote__post_note: posted hi; remote__send_payment: paid 5",
    );
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((step) => step === "CallTool").length, 1);
    assert.equal(steps.filter((step) => step === "CallSingleAttemptTool").length, 2);
  });

  for (const outcome of ["unknown", "unknown_error"]) {
    test(`a Dex outcome unknown is recorded once and never retried (${outcome})`, async () => {
      await startWorker();
      const sessionId = `mcp-dex-unknown-${randomUUID()}`;
      await sessions.startSession(sessionId, `call remote__send_payment {"amount": 5, "outcome": "${outcome}"}`);
      const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_payment");
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await sessions.waitForCompletedTurns(sessionId, 1);

      // An idempotent write runs in CallTool, which would retry a Step failure.
      assert.equal(await lastMessageText(sessionId), `remote__send_payment: ${outcomeUnknownToolResult}`);
      assert.deepEqual(toolRuns("send_payment", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
      const history = await readStepHistory(engine.serverAddress, sessionId);
      assert.deepEqual(
        history.filter((entry) => entry.stepType === "CallTool").map((entry) => entry.finalAttempt),
        [1],
      );
      assert.ok(!(await executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
      assert.deepEqual(
        (await eventPayloads(sessionId, "tool_call_finished")).map((payload) => payload["status"]),
        ["outcome_unknown"],
      );
    });
  }

  for (const outcome of ["rejected", "rejected_error"]) {
    test(`a Dex outcome rejected is recorded failed and never retried (${outcome})`, async () => {
      await startWorker();
      const sessionId = `mcp-dex-rejected-${randomUUID()}`;
      await sessions.startSession(sessionId, `call remote__send_payment {"amount": 5, "outcome": "${outcome}"}`);
      const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_payment");
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      await sessions.waitForCompletedTurns(sessionId, 1);

      const message = outcome === "rejected" ? "the card was declined" : "MCP error -32603: the card was declined";
      assert.equal(await lastMessageText(sessionId), `remote__send_payment: The tool call failed: ${message}`);
      assert.deepEqual(toolRuns("send_payment", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
      const history = await readStepHistory(engine.serverAddress, sessionId);
      assert.deepEqual(
        history.filter((entry) => entry.stepType === "CallTool").map((entry) => entry.finalAttempt),
        [1],
      );
      assert.deepEqual(
        (await eventPayloads(sessionId, "tool_call_finished")).map((payload) => payload["status"]),
        ["failed"],
      );
    });
  }

  test("retries of a Dex write carry one idempotency key, so the server applies it once", async () => {
    await startWorker();
    const sessionId = `mcp-dex-retry-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call local__send_payment {"amount": 5, "crash_after_apply": true}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "local__send_payment");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), "local__send_payment: payment of 5 already applied");
    const key = `${sessionId}/turn-1/turn-1-call-0`;
    assert.deepEqual(toolRuns("send_payment", sessionId), [key, key]);
    assert.deepEqual(toolRuns("payment-applied", sessionId), [key]);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    assert.deepEqual(
      history.filter((entry) => entry.stepType === "CallTool").map((entry) => entry.finalAttempt),
      [2],
    );
  });

  test("a task result with the Dex outcome unknown is recorded by PollToolTask", async () => {
    await startWorker();
    const sessionId = `mcp-dex-task-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__long_report {"topic": "q3", "outcome": "unknown"}');
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await lastMessageText(sessionId), `remote__long_report: ${outcomeUnknownToolResult}`);
    assert.deepEqual(toolRuns("long_report", sessionId), [`${sessionId}/turn-1/turn-1-call-0`]);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    const polls = history.filter((entry) => entry.stepType === "PollToolTask").map((entry) => entry.finalAttempt);
    assert.ok(polls.length > 0 && polls.every((attempt) => attempt === 1), `PollToolTask attempts ${polls.join(", ")}`);
    assert.ok(!(await executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
  });
});

/** Builds the execution a tool Step passes for one approved `remote__long_report` call. */
function deferredExecution(
  toolArguments: string,
  heartbeatCheckpoint?: string,
  saveHeartbeatCheckpoint?: (checkpoint: string) => Promise<void>,
  supportsToolTasks = true,
  flowId = "direct-session",
): DeferredToolExecution {
  const events = {
    turnId: "turn-1",
    attempt: 1,
    writeEvent: () => undefined,
    writeTextDelta: () => undefined,
    writeReasoningDelta: () => undefined,
    writeToolCallStarted: () => undefined,
    writeToolCallFinished: () => undefined,
  };
  return {
    flowId,
    toolCall: {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "remote__long_report",
      arguments: toolArguments,
      effect: "read",
      requiresApproval: false,
      status: "approved",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      toolSourceReference: JSON.stringify({ mcp_server: "remote", mcp_tool: "long_report", task_support: "required" }),
    },
    idempotencyKey: `${flowId}/turn-1/call-1`,
    attempt: heartbeatCheckpoint === undefined ? 1 : 2,
    events,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: () => Promise.resolve(),
    supportsToolTasks,
    ...(heartbeatCheckpoint === undefined ? {} : { heartbeatCheckpoint }),
    ...(saveHeartbeatCheckpoint === undefined ? {} : { saveHeartbeatCheckpoint }),
  };
}

test("inspecting an MCP tool source redacts environment and header values but keeps their names", () => {
  const stdio: McpStdioServerConfig = {
    transport: "stdio",
    name: "files",
    command: "npx",
    env: { API_KEY: "stdio-secret", REGION: "eu-secret" },
  };
  const http: McpStreamableHttpServerConfig = {
    transport: "streamable_http",
    name: "tickets",
    url: "https://tickets.example.com/mcp",
    headers: { Authorization: "Bearer http-secret", "X-Api-Key": "key-secret" },
  };
  const source = new McpToolSource([stdio, http]);

  const printed = [inspect(source, { depth: null }), inspect({ nested: source }, { depth: null }), format("%o", source)];
  for (const text of printed) {
    assert.doesNotMatch(text, /secret/);
    assert.match(text, /^McpToolSource |McpToolSource \{/);
    assert.match(text, /API_KEY: '\*\*\*'/);
    assert.match(text, /REGION: '\*\*\*'/);
    assert.match(text, /Authorization: '\*\*\*'/);
    assert.match(text, /'X-Api-Key': '\*\*\*'/);
  }
  assert.deepEqual(stdio.env, { API_KEY: "stdio-secret", REGION: "eu-secret" });
  assert.deepEqual(http.headers, { Authorization: "Bearer http-secret", "X-Api-Key": "key-secret" });
});

interface DexConventionsSuite {
  readonly metaKeys: Readonly<Record<string, string>>;
  readonly values: Readonly<Record<string, unknown>>;
  readonly definitionCases: readonly {
    readonly name: string;
    readonly tool: Tool;
    readonly trustsToolAnnotations: boolean;
    readonly sessionPolicy: ToolPolicy | null;
    readonly expected: {
      readonly frameworkEffect: ToolEffect | null;
      readonly dexEffect: ToolEffect | null;
      readonly isApprovalRecommended: boolean;
      readonly policy: { readonly effect: ToolEffect; readonly requiresApproval: boolean };
      readonly ordered: boolean;
    };
  }[];
  readonly resultCases: readonly { readonly name: string; readonly result: CallToolResult; readonly expected: unknown }[];
  readonly errorCases: readonly {
    readonly name: string;
    readonly error: { readonly code: number; readonly message: string; readonly data?: unknown };
    readonly expected: unknown;
  }[];
}

const dexConventions = JSON.parse(
  readFileSync(join(conformanceSuiteDirectory, "mcp_dex_conventions.json"), "utf8"),
) as DexConventionsSuite;

describe("Dex MCP conventions match the conformance suite", () => {
  test("the meta keys and values", () => {
    assert.deepEqual(dexConventions.metaKeys, {
      idempotencyKey: mcpIdempotencyKeyMetaKey,
      toolEffect: mcpToolEffectMetaKey,
      toolApproval: mcpToolApprovalMetaKey,
      toolOutcome: mcpToolOutcomeMetaKey,
    });
    assert.deepEqual(dexConventions.values, {
      toolEffects: ["read", "idempotent_write", "non_idempotent_write"],
      toolApprovalRecommended: mcpToolApprovalRecommended,
      toolOutcomeUnknown: mcpToolOutcomeUnknown,
      toolOutcomeRejected: mcpToolOutcomeRejected,
    });
  });

  for (const testCase of dexConventions.definitionCases) {
    test(`definition: ${testCase.name}`, () => {
      const tool = mcpToolFromListing(
        { transport: "stdio", name: "srv", command: "unused", trustsToolAnnotations: testCase.trustsToolAnnotations },
        testCase.tool,
      );
      const toolPolicies: Record<string, ToolPolicy> =
        testCase.sessionPolicy === null ? {} : { [tool.name]: testCase.sessionPolicy };
      const turn = {
        toolPolicies,
        defaultToolEffect: "unclassified",
        resolveToolPolicy: (toolName: string, frameworkEffect?: ToolEffect) =>
          resolveToolPolicy(toolName, toolPolicies, "unclassified", frameworkEffect),
      } as unknown as AgentTurnContext;
      const call = new McpToolSource([]).deferredToolCall(turn, {
        toolCallId: "call-1",
        toolName: tool.name,
        arguments: "{}",
        listedTool: tool,
      });

      const { expected } = testCase;
      assert.equal(tool.frameworkEffect ?? null, expected.frameworkEffect);
      assert.equal(tool.dexEffect ?? null, expected.dexEffect);
      assert.equal(tool.isApprovalRecommended === true, expected.isApprovalRecommended);
      assert.deepEqual(call.policy, expected.policy);
      assert.equal(call.ordered === true, expected.ordered);
    });
  }

  for (const testCase of dexConventions.resultCases) {
    test(`result: ${testCase.name}`, () => {
      assert.deepEqual(describeDexOutcome(outcomeFromToolResult(testCase.result)), testCase.expected);
    });
  }

  for (const testCase of dexConventions.errorCases) {
    test(`error: ${testCase.name}`, () => {
      const { code, message, data } = testCase.error;
      assert.deepEqual(describeDexOutcome(outcomeFromMcpError(new McpError(code, message, data))), testCase.expected);
    });
  }
});

/** Maps an outcome to the suite's `{status, text}`; `undefined` leaves the outcome to the effect class. */
function describeDexOutcome(outcome: ToolCallOutcome | undefined): { status: string; text: string | null } {
  if (outcome === undefined) {
    return { status: "interrupted", text: null };
  }
  switch (outcome.kind) {
    case "succeeded":
      return { status: "succeeded", text: outcome.output };
    case "failed":
    case "outcome_unknown":
      return { status: outcome.kind, text: outcome.message };
    default:
      throw new Error(`unexpected outcome ${outcome.kind}`);
  }
}
