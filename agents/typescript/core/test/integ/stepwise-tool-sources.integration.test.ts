// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0


import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { after, before, describe, test } from "node:test";

import {
  ToolExecutionError,
  resolveToolPolicy,
  type AgentToolCall,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type ToolExecutionResult,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "../../src/index.js";
import {
  McpToolSource,
  defaultMcpSourceName,
  mcpGetPromptToolName,
  mcpListResourcesAndPromptsToolName,
  mcpReadResourceToolName,
} from "../../src/mcp/index.js";
import {
  ScriptedModelClient,
  BuiltinLoopAgentRunner,
  type ModelClient,
  type ModelRequest,
  type SourceToolDefinition,
  type ToolSource,
} from "../../src/builtin-loop/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  InvocationLog,
  PrivateDexEngine,
  ScriptedModelClient as ScriptedModelClientOfTesting,
  executeCompletedEvent,
  findFreeLoopbackPort,
  readStepHistory,
} from "../../src/testing/index.js";
import { ChildProcessGroup } from "../../src/testing/child-process-group.js";
import {
  fakeMcpInvocationLogName,
  fakeMcpReadyLine,
  fakeMcpServerModulePath,
  fakeMcpStateDirectoryVariable,
} from "../fixtures/fake-mcp-server-names.js";
import { createMcpStepwiseDefinition, createMcpStepwiseFlow, takeNote } from "../fixtures/mcp-stepwise-agent.js";

describe("a Stepwise agent with an MCP tool source and the scripted demo model", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let model: ScriptedModelClient;
  let stateDirectory: string;
  let httpServer: ChildProcessGroup | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
    stateDirectory = mkdtempSync(join(engine.stateDirectory, "stepwise-mcp-state-"));
    const httpPort = await findFreeLoopbackPort();
    httpServer = await ChildProcessGroup.start({
      description: "fake MCP server",
      command: process.execPath,
      commandArguments: [fakeMcpServerModulePath, "http", String(httpPort)],
      environment: { ...process.env, [fakeMcpStateDirectoryVariable]: stateDirectory },
      logPath: join(stateDirectory, "fake-mcp-server.log"),
      readyLine: fakeMcpReadyLine,
      readyTimeoutMs: 30_000,
    });
    const definition = createMcpStepwiseDefinition(stateDirectory, `http://127.0.0.1:${httpPort}/mcp`);
    model = definition.model as ScriptedModelClient;
    const flow = createMcpStepwiseFlow(definition);
    environment = await AgentTestEnvironment.start({ serverAddress: engine.serverAddress, flows: [flow], isWorkerInProcess: true });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await httpServer?.kill();
    await engine?.stop();
  });

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

  async function runTurns(sessionId: string, commands: readonly string[]): Promise<void> {
    await sessions.startSession(sessionId, commands[0] ?? null);
    for (const [index, command] of commands.slice(1).entries()) {
      await sessions.waitForCompletedTurns(sessionId, index + 1);
      assert.equal(await sessions.sendUserMessage(sessionId, command), true);
    }
    await sessions.waitForCompletedTurns(sessionId, commands.length);
  }

  test("offers MCP tools after function tools and waits for approval of an MCP write", async () => {
    const sessionId = `stepwise-mcp-approval-${randomUUID()}`;
    await sessions.startSession(sessionId, 'call remote__send_message {"text": "hello"}');
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "remote__send_message");
    assert.deepEqual(toolRuns("send_message", sessionId), []);
    const [record] = (await sessions.getAgentSnapshot(sessionId)).toolCalls;
    assert.equal(record?.effect, "unclassified");
    const envelope = JSON.parse(record?.toolSourceReference ?? "{}") as { stepwise_tool_source: string; reference: string };
    assert.equal(envelope.stepwise_tool_source, "mcp");
    assert.deepEqual(JSON.parse(envelope.reference), {
      mcp_server: "remote",
      mcp_tool: "send_message",
      task_support: "forbidden",
    });
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "remote__send_message: sent hello");
    assert.deepEqual(toolRuns("send_message", sessionId), [`${sessionId}/turn-1/${record?.toolCallId}`]);
    assert.deepEqual((await executedStepTypes(sessionId)).slice(2, 4), ["AwaitToolApproval", "CallSingleAttemptTool"]);
    const offered = model.requests[0]?.tools.map((tool) => tool.name) ?? [];
    // Function tools, then source tools in source order, then built-in tools.
    assert.equal(offered[0], "take_note");
    assert.deepEqual(offered.slice(1, 3), ["local__lookup", "local__audit_lookup"]);
    assert.deepEqual(offered.slice(-4), [
      mcpListResourcesAndPromptsToolName,
      mcpReadResourceToolName,
      mcpGetPromptToolName,
      "ask_user",
    ]);
    assert.ok(offered.includes("remote__send_message"));
    const getPrompt = model.requests[0]?.tools.find((tool) => tool.name === mcpGetPromptToolName);
    // The same model-visible definition as the Python core.
    assert.deepEqual(getPrompt, {
      name: "mcp_get_prompt",
      description: "Get one prompt of a configured MCP server, rendered with its arguments.",
      parameters: {
        type: "object",
        properties: {
          server: { type: "string", enum: ["local", "remote"], description: "Name of a configured MCP server." },
          name: { type: "string", description: "Name of the prompt, as the server lists it." },
          arguments: {
            type: "object",
            additionalProperties: { type: "string" },
            description: "Prompt arguments by name. Defaults to none.",
          },
        },
        required: ["server", "name"],
        additionalProperties: false,
      },
    });
  });

  test("polls an MCP tool task with PollToolTask and still runs function tools", async () => {
    const sessionId = `stepwise-mcp-task-${randomUUID()}`;
    await runTurns(sessionId, ['call remote__long_report {"topic": "q3"}', 'call take_note {"text": "q3 done"}']);

    const texts = (await sessions.getAgentSnapshot(sessionId)).messages
      .filter((message) => message.role === "assistant" && (message.text ?? "") !== "")
      .map((message) => message.text);
    assert.deepEqual(texts.slice(-2), ["remote__long_report: report on q3", "take_note: kept q3 done"]);
    assert.equal(toolRuns("long_report", sessionId).length, 1);
    const steps = await executedStepTypes(sessionId);
    assert.deepEqual(steps.slice(0, 4), ["StartAgentSession", "RunModel", "CallTool", "PollToolTask"]);
  });

  test("reads MCP resources and prompts through read tools", async () => {
    const sessionId = `stepwise-mcp-resources-${randomUUID()}`;
    const commands = [
      'call mcp_list_resources_and_prompts {"server": "remote"}',
      'call mcp_read_resource {"server": "local", "uri": "memo://greeting"}',
      'call mcp_get_prompt {"server": "remote", "name": "summarize", "arguments": {"topic": "q3"}}',
      'call mcp_read_resource {"server": "nowhere", "uri": "memo://greeting"}',
    ];
    await runTurns(sessionId, commands);

    const outputs = (await sessions.getAgentSnapshot(sessionId)).messages
      .filter((message) => message.role === "tool")
      .map((message) => message.toolOutput);
    const listing = JSON.parse(outputs[0] ?? "") as Record<string, Record<string, unknown>[]>;
    assert.deepEqual(listing["resources"], [
      {
        annotations: { priority: 1 },
        description: "A greeting memo.",
        mimeType: "text/plain",
        name: "greeting",
        uri: "memo://greeting",
      },
    ]);
    // The same text as the Python core, which writes a whole-number float as JavaScript does.
    assert.ok((outputs[0] ?? "").includes('"annotations": {"priority": 1}, "description": "A greeting memo."'));
    assert.deepEqual(listing["resource_templates"], []);
    assert.deepEqual(listing["prompts"]?.map((prompt) => prompt["name"]), ["summarize"]);
    // The same text as the Python core.
    assert.equal(
      outputs[1],
      '{"contents": [{"mimeType": "text/plain", "text": "hello from the memo", "uri": "memo://greeting"}]}',
    );
    assert.equal(
      outputs[2],
      '{"description": "Summarize a topic.", "messages": [{"content": {"text": "Summarize q3 in one line.", ' +
        '"type": "text"}, "role": "user"}]}',
    );
    assert.equal(outputs[3], "The tool call failed: MCP server nowhere is not configured");
    const steps = await executedStepTypes(sessionId);
    // Read tools run in CallTool with no approval.
    assert.equal(steps.filter((step) => step === "CallTool").length, commands.length);
    assert.ok(!steps.includes("AwaitToolApproval"));
  });
});

describe("tool source routing, policies, listing failures, and name collisions", () => {
  test("an unlisted source tool fails as unknown without an approval and never runs", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-unlisted-"));
    const filesCommand = join(directory, "files-server");
    const source = new McpToolSource(
      [
        {
          transport: "stdio",
          name: "other",
          command: process.execPath,
          args: [fakeMcpServerModulePath, "stdio"],
          env: { [fakeMcpStateDirectoryVariable]: directory },
        },
        { transport: "stdio", name: "files", command: filesCommand, env: { [fakeMcpStateDirectoryVariable]: directory } },
      ],
      { connectionAttempts: 1 },
    );
    const model = new ScriptedModelClient(() => ({
      toolCalls: [{ toolName: "files__send_message", arguments: { text: "wire $1M" }, toolCallId: "call-1" }],
    }));
    const runner = new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model, toolSources: [source] });

    const outcome = await runner.runModelStep(turnContext());

    assert.equal(outcome.kind, "tool_calls_requested");
    const offered = new Set(model.requests[0]?.tools.map((tool) => tool.name));
    assert.ok(offered.has("other__send_message"));
    assert.ok(!offered.has("files__send_message"));
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;
    // It can never run, so nobody is asked to approve it.
    assert.deepEqual(call?.policy, { effect: "unclassified", requiresApproval: false });
    assert.equal(call?.toolSourceReference, undefined);
    // The server comes back before the approved tool Step runs.
    writeLaunchScript(filesCommand, join(directory, "files-launches.log"), `exec "${process.execPath}" "${fakeMcpServerModulePath}" stdio`);
    await assert.rejects(runner.executeDeferredToolCall(deferredExecution(call!)), (error: Error) => {
      assert.ok(error instanceof ToolExecutionError);
      assert.equal(error.message, "unknown tool files__send_message");
      return true;
    });
    const log = join(directory, fakeMcpInvocationLogName);
    assert.ok(!(existsSync(log) ? readFileSync(log, "utf8") : "").includes("send_message"));
  });

  test("a call reaches the source that built it without a listing", async () => {
    const search = new InMemoryToolSource("search");
    const fetch = new InMemoryToolSource("fetch");
    const definition = {
      name: "agent",
      instructions: "Help.",
      model: new ScriptedModelClient(() => ({ toolCalls: [{ toolName: "fetch", toolCallId: "call-1" }] })),
      toolSources: [search, fetch],
    };
    const outcome = await new BuiltinLoopAgentRunner(definition).runModelStep(turnContext());
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;
    assert.deepEqual(JSON.parse(call?.toolSourceReference ?? ""), {
      stepwise_tool_source: "memory:fetch",
      reference: "own-reference-of-fetch",
    });

    // A new Worker runs the tool Step while neither source can list.
    search.isListingBroken = fetch.isListingBroken = true;
    const freshRunner = new BuiltinLoopAgentRunner(definition);
    assert.deepEqual(await freshRunner.executeDeferredToolCall(deferredExecution(call!)), { output: "ran fetch" });
    assert.deepEqual(await freshRunner.pollToolTask(toolTaskPoll(call!)), { output: "polled fetch" });
    assert.deepEqual(
      fetch.reached.map((record) => record.toolSourceReference),
      ["own-reference-of-fetch", "own-reference-of-fetch"],
    );
    assert.deepEqual(search.reached, []);
  });

  test("a call whose source cannot be told is retried, not failed", async () => {
    const one = new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model: answers(),
      toolSources: [new InMemoryToolSource("search")],
    });
    const moved = {
      toolCallId: "call-1",
      toolName: "fetch",
      arguments: "{}",
      policy: { effect: "unclassified" as const, requiresApproval: true },
      toolSourceReference: JSON.stringify({ stepwise_tool_source: "memory:gone", reference: "x" }),
    };
    // A RangeError is not a ToolExecutionError, so the effect decides the outcome.
    await assert.rejects(
      one.executeDeferredToolCall(deferredExecution(moved)),
      (error: Error) => error instanceof RangeError && /memory:gone, .* is not configured on this Worker/.test(error.message),
    );
    const several = new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model: answers(),
      toolSources: [new InMemoryToolSource("search"), new InMemoryToolSource("fetch")],
    });
    const foreign = { ...moved, toolSourceReference: JSON.stringify({ mcp_server: "files", mcp_tool: "fetch" }) };
    await assert.rejects(several.executeDeferredToolCall(deferredExecution(foreign)), (error: Error) => {
      assert.ok(error instanceof RangeError);
      assert.match(error.message, /cannot tell which tool source/);
      return true;
    });
    await assert.rejects(several.pollToolTask(toolTaskPoll(foreign)), RangeError);
  });

  test("down servers offer no tools without failing the turn or being retried", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-down-"));
    const refusingLog = join(directory, "refusing.log");
    const hangingLog = join(directory, "hanging.log");
    const source = new McpToolSource(
      [
        { transport: "stdio", name: "refusing", command: writeLaunchScript(join(directory, "refusing"), refusingLog, "exit 1") },
        { transport: "stdio", name: "hanging", command: writeLaunchScript(join(directory, "hanging"), hangingLog, "exec sleep 60") },
      ],
      { connectionTimeoutMs: 3_000, connectionAttempts: 1, listingFailureBackoffMs: 300_000 },
    );
    const model = answers();
    const runner = new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model,
      tools: [takeNote],
      toolSources: [source],
      builtInTools: ["ask_user"],
      toolSourceListingTimeoutMs: 1_000,
    });

    for (let call = 0; call < 3; call += 1) {
      const started = Date.now();
      assert.equal((await runner.runModelStep(turnContext())).kind, "completed");
      assert.ok(Date.now() - started < 2_500);
    }
    // Wait until the hanging server's one listing timed out.
    const deadline = Date.now() + 20_000;
    while (launches(hangingLog) === 1 && Date.now() < deadline && !(await hasListingFailed(source, "hanging"))) {
      await delay(100);
    }
    const started = Date.now();
    assert.equal((await runner.runModelStep(turnContext())).kind, "completed");
    assert.ok(Date.now() - started < 500);

    assert.deepEqual(
      model.requests.map((request) => request.tools.map((tool) => tool.name)),
      Array.from({ length: 4 }, () => ["take_note", "ask_user"]),
    );
    assert.equal(launches(refusingLog), 1);
    assert.equal(launches(hangingLog), 1);
  });

  test("a hanging source is bounded per model call", async () => {
    const runner = new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model: answers(),
      toolSources: [new HangingToolSource()],
      toolSourceListingTimeoutMs: 300,
    });
    const started = Date.now();
    assert.equal((await runner.runModelStep(turnContext())).kind, "completed");
    assert.ok(Date.now() - started < 2_000);
  });

  test("two sources with resource and prompt tools are refused when the runner is built", () => {
    const source = (serverName: string) =>
      new McpToolSource([{ transport: "stdio", name: serverName, command: "mcp-files" }], {
        resourceAndPromptToolsEnabled: true,
        sourceName: serverName,
      });
    assert.throws(
      () =>
        new BuiltinLoopAgentRunner({
          name: "agent",
          instructions: "Help.",
          model: answers(),
          toolSources: [source("files"), source("tickets")],
        }),
      (error: Error) => error instanceof TypeError && error.message === "two tool sources list a tool named mcp_get_prompt",
    );
    assert.throws(
      () =>
        new BuiltinLoopAgentRunner({
          name: "agent",
          instructions: "Help.",
          model: answers(),
          toolSources: [new InMemoryToolSource().declaring("ask_user")],
          builtInTools: ["ask_user"],
        }),
      (error: Error) => error.message === "tool ask_user uses the name of an enabled built-in tool",
    );
    new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model: answers(), toolSources: [source("files")] });
  });

  test("a source tool name collision fails the model step before any model call", async () => {
    const cases: [readonly ToolSource[], string][] = [
      [[new InMemoryToolSource("ask_user")], "tool ask_user uses the name of an enabled built-in tool"],
      [[new InMemoryToolSource("take_note")], "tool take_note of a tool source uses the name of a function tool"],
      [
        [new InMemoryToolSource("search"), new InMemoryToolSource("search").named("mirror")],
        "two tool sources list a tool named search",
      ],
    ];
    for (const [toolSources, message] of cases) {
      const model = answers();
      const runner = new BuiltinLoopAgentRunner({
        name: "agent",
        instructions: "Help.",
        model,
        tools: [takeNote],
        toolSources,
        builtInTools: ["ask_user"],
      });
      await assert.rejects(runner.runModelStep(turnContext()), (error: Error) => {
        assert.ok(error instanceof TypeError);
        assert.equal(error.message, message);
        return true;
      });
      assert.deepEqual(model.requests, []);
    }
    assert.throws(
      () => new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model: answers(), toolSources: [{} as ToolSource] }),
      /has no listTools method/,
    );
  });

  test("resource and prompt tools retry the connection and say why they failed", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-reader-"));
    const log = join(directory, "launches.log");
    const source = new McpToolSource(
      [{ transport: "stdio", name: "down", command: writeLaunchScript(join(directory, "down"), log, "exit 1") }],
      { connectionAttempts: 2, resourceAndPromptToolsEnabled: true },
    );
    assert.deepEqual(
      [...source.declaredToolNames()].sort(),
      ["mcp_get_prompt", "mcp_list_resources_and_prompts", "mcp_read_resource"],
    );
    await assert.rejects(source.callTool("mcp_read_resource", { server: "down", uri: "memo://greeting" }), (error: Error) => {
      assert.ok(error instanceof ToolExecutionError);
      assert.ok(error.message.startsWith("could not read from MCP server down, so mcp_read_resource did not finish: "));
      return true;
    });
    assert.equal(launches(log), 2);
  });

  test("the estimate counts a source listing cached before any model call", async () => {
    const estimated: ModelRequest[] = [];
    const model: ModelClient = {
      callModel: () => Promise.reject(new Error("no model call")),
      estimateInputTokens: (request) => {
        estimated.push(request);
        return 1;
      },
    };
    const runner = new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model,
      tools: [takeNote],
      toolSources: [new CachingToolSource("search")],
      builtInTools: ["ask_user"],
    });
    await runner.estimateInputTokens(turnContext());
    assert.deepEqual(estimated[0]?.tools.map((tool) => tool.name), ["take_note", "search", "ask_user"]);
  });

  test("the demo model keeps only its latest requests and is the testing export", async () => {
    const output = { writeTextDelta: () => undefined, writeReasoningDelta: () => undefined };
    const model = new ScriptedModelClient(() => ({ text: "hi" }));
    for (let index = 0; index < 101; index += 1) {
      await model.callModel(modelRequest(`request ${index}`), output);
    }
    assert.equal(model.requests.length, 100);
    assert.equal(model.requests[0]?.instructions, "request 1");
    const silent = new ScriptedModelClient(() => ({ text: "hi" }), { maxRecordedRequests: 0 });
    await silent.callModel(modelRequest("request"), output);
    assert.deepEqual(silent.requests, []);
    assert.equal(ScriptedModelClient, ScriptedModelClientOfTesting);
  });

  test("a stored call reaches its source after sources are reordered or added", async () => {
    const search = new InMemoryToolSource("search");
    const fetch = new InMemoryToolSource("fetch");
    const outcome = await new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model: new ScriptedModelClient(() => ({ toolCalls: [{ toolName: "fetch", toolCallId: "call-1" }] })),
      toolSources: [search, fetch],
    }).runModelStep(turnContext());
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;
    for (const toolSources of [[fetch, search], [new InMemoryToolSource("crm"), search, fetch]]) {
      const deployed = new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model: answers(), toolSources });
      await deployed.executeDeferredToolCall(deferredExecution(call!));
      await deployed.pollToolTask(toolTaskPoll(call!));
    }
    assert.deepEqual(search.reached, []);
    assert.equal(fetch.reached.length, 4);
  });

  test("swapped MCP sources still run the call on its server", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-swap-"));
    const server = (name: string) => ({
      transport: "stdio" as const,
      name,
      command: process.execPath,
      args: [fakeMcpServerModulePath, "stdio"],
      env: { [fakeMcpStateDirectoryVariable]: join(directory, name) },
    });
    mkdirSync(join(directory, "files"));
    mkdirSync(join(directory, "other"));
    const files = new McpToolSource([server("files")], { connectionAttempts: 1, sourceName: "files" });
    const other = new McpToolSource([server("other")], { connectionAttempts: 1, sourceName: "other" });
    const model = new ScriptedModelClient(() => ({
      toolCalls: [{ toolName: "files__send_message", arguments: { text: "x" }, toolCallId: "c1" }],
    }));
    const outcome = await new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model,
      toolSources: [files, other],
    }).runModelStep(turnContext());
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;
    const swapped = new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model, toolSources: [other, files] });
    assert.deepEqual(await swapped.executeDeferredToolCall(deferredExecution(call!)), { output: "sent x" });
    assert.ok(readFileSync(join(directory, "files", fakeMcpInvocationLogName), "utf8").includes("send_message"));
  });

  test("a stored call still routes after a server is added to its source", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-added-server-"));
    const server = (name: string) => ({
      transport: "stdio" as const,
      name,
      command: process.execPath,
      args: [fakeMcpServerModulePath, "stdio"],
      env: { [fakeMcpStateDirectoryVariable]: join(directory, name) },
    });
    mkdirSync(join(directory, "files"));
    mkdirSync(join(directory, "crm"));
    const model = new ScriptedModelClient(() => ({
      toolCalls: [{ toolName: "files__send_message", arguments: { text: "x" }, toolCallId: "c1" }],
    }));
    const before = new McpToolSource([server("files")], { connectionAttempts: 1 });
    assert.equal(before.sourceName, defaultMcpSourceName);
    assert.equal(defaultMcpSourceName, "mcp");
    const outcome = await new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model,
      toolSources: [before],
    }).runModelStep(turnContext());
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;

    // A deploy adds a server to the same source; the files server and tool are unchanged.
    const after = new McpToolSource([server("files"), server("crm")], { connectionAttempts: 1 });
    assert.equal(after.sourceName, before.sourceName);
    const deployed = new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model, toolSources: [after] });
    assert.deepEqual(await deployed.executeDeferredToolCall(deferredExecution(call!)), { output: "sent x" });
    assert.ok(readFileSync(join(directory, "files", fakeMcpInvocationLogName), "utf8").includes("send_message"));
  });

  test("a refresh during the model call does not fail the model step", async () => {
    const directory = mkdtempSync(join(tmpdir(), "stepwise-refresh-"));
    const source = new McpToolSource(
      [
        {
          transport: "stdio",
          name: "files",
          command: process.execPath,
          args: [fakeMcpServerModulePath, "stdio"],
          env: { [fakeMcpStateDirectoryVariable]: directory },
        },
      ],
      { connectionAttempts: 1 },
    );
    const model = new ScriptedModelClient(() => {
      // A background refresh finishes while the model runs and no longer offers the tool.
      (source as unknown as { tools: ReadonlyMap<string, unknown> }).tools = new Map();
      return { toolCalls: [{ toolName: "files__lookup", arguments: { query: "q" }, toolCallId: "c1" }] };
    });
    const outcome = await new BuiltinLoopAgentRunner({
      name: "agent",
      instructions: "Help.",
      model,
      toolSources: [source],
    }).runModelStep(turnContext());
    const call = outcome.kind === "tool_calls_requested" ? outcome.toolCalls[0] : undefined;
    assert.equal(call?.policy.effect, "read");
    const envelope = JSON.parse(call?.toolSourceReference ?? "") as { reference: string };
    assert.deepEqual(JSON.parse(envelope.reference), { mcp_server: "files", mcp_tool: "lookup", task_support: "forbidden" });
  });

  test("tool source names must be unique and present", () => {
    assert.throws(
      () =>
        new BuiltinLoopAgentRunner({
          name: "agent",
          instructions: "Help.",
          model: answers(),
          toolSources: [
            new McpToolSource([{ transport: "stdio", name: "files", command: "a" }]),
            new McpToolSource([{ transport: "stdio", name: "tickets", command: "b" }]),
          ],
        }),
      (error: Error) => error instanceof TypeError && error.message === "two tool sources are named mcp",
    );
    assert.equal(
      new McpToolSource([{ transport: "stdio", name: "files", command: "b" }], { sourceName: "archive" }).sourceName,
      "archive",
    );
    assert.throws(
      () =>
        new BuiltinLoopAgentRunner({
          name: "agent",
          instructions: "Help.",
          model: answers(),
          toolSources: [new InMemoryToolSource("search").named(" ")],
        }),
      /needs a non-blank sourceName/,
    );
  });
});

/** A tool source that lists fixed names and records what reaches it. */
class InMemoryToolSource implements ToolSource {
  public isListingBroken = false;
  public readonly reached: { readonly toolSourceReference?: string }[] = [];
  protected readonly tools: SourceToolDefinition[];
  public sourceName: string;
  private declared: ReadonlySet<string> = new Set();

  public constructor(...names: string[]) {
    this.tools = names.map((name) => ({ name, description: `The ${name} tool.`, inputSchema: { type: "object" } }));
    this.sourceName = `memory:${names.join(",")}`;
  }

  public declaring(...names: string[]): this {
    this.declared = new Set(names);
    this.sourceName = `memory:${names.join(",")}`;
    return this;
  }

  public named(sourceName: string): this {
    this.sourceName = sourceName;
    return this;
  }

  public listTools(): Promise<readonly SourceToolDefinition[]> {
    return this.isListingBroken ? Promise.reject(new Error("server unreachable")) : Promise.resolve(this.tools);
  }

  public declaredToolNames(): ReadonlySet<string> {
    return this.declared;
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall {
    assert.equal(call.listedTool.name, call.toolName);
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: turn.resolveToolPolicy(call.toolName, "read"),
      toolSourceReference: `own-reference-of-${call.toolName}`,
    };
  }

  public executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    this.reached.push(call.toolCall);
    return Promise.resolve({ output: `ran ${call.toolCall.toolName}` });
  }

  public pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    this.reached.push(poll.toolCall);
    return Promise.resolve({ output: `polled ${poll.toolCall.toolName}` });
  }
}

class CachingToolSource extends InMemoryToolSource {
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.tools;
  }
}

class HangingToolSource extends InMemoryToolSource {
  public override listTools(): Promise<readonly SourceToolDefinition[]> {
    return delay(60_000, [], { ref: false });
  }
}

function answers(): ScriptedModelClient {
  return new ScriptedModelClient(() => ({ text: "answered" }));
}

function turnContext(): AgentTurnContext {
  return {
    flowId: "session-1",
    turnId: "turn-1",
    attempt: 1,
    messages: [],
    frameworkRunState: null,
    isResuming: false,
    toolCalls: [],
    events: noEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    enabledBuiltInTools: new Set(["ask_user"]),
    resolveToolPolicy: (toolName, frameworkEffect) => resolveToolPolicy(toolName, {}, "unclassified", frameworkEffect),
    recordedToolCall: () => undefined,
    heartbeat: () => Promise.resolve(),
  };
}

const noEvents = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};

function toolCallRecord(call: DeferredToolCall): AgentToolCall {
  return {
    toolCallId: call.toolCallId,
    turnId: "turn-1",
    toolName: call.toolName,
    arguments: call.arguments,
    effect: call.policy.effect,
    requiresApproval: call.policy.requiresApproval,
    status: "approved",
    output: null,
    error: null,
    rejectionMessage: null,
    deferralOrder: 0,
    ...(call.toolSourceReference === undefined ? {} : { toolSourceReference: call.toolSourceReference }),
  };
}

function deferredExecution(call: DeferredToolCall): DeferredToolExecution {
  return {
    flowId: "session-1",
    toolCall: toolCallRecord(call),
    idempotencyKey: `session-1/turn-1/${call.toolCallId}`,
    attempt: 1,
    events: noEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: () => Promise.resolve(),
    supportsToolTasks: true,
  };
}

function toolTaskPoll(call: DeferredToolCall): ToolTaskPoll {
  return {
    flowId: "session-1",
    toolCall: {
      ...toolCallRecord(call),
      status: "waiting_for_task",
      waitUntil: null,
      inputContinuation: null,
      inputAnswers: [],
      inputQuestion: null,
      inputChoices: [],
      inputAllowsFreeText: true,
    },
    idempotencyKey: `session-1/turn-1/${call.toolCallId}`,
    attempt: 1,
    inputAnswers: [],
    pendingAnswer: null,
    events: noEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: () => Promise.resolve(),
  };
}

function modelRequest(instructions: string): ModelRequest {
  return {
    instructions,
    inputItems: [],
    tools: [],
    toolChoice: "auto",
    settings: {},
    metadata: { flowId: "f", turnId: "turn-1", stepExecutionId: "s", attempt: 1 },
    cancellationSignal: new AbortController().signal,
  };
}

/** Writes a server command that logs each launch, then runs `then`. */
function writeLaunchScript(path: string, log: string, then: string): string {
  writeFileSync(path, `#!/bin/sh\necho launched >> "${log}"\n${then}\n`);
  chmodSync(path, 0o755);
  return path;
}

function launches(log: string): number {
  return existsSync(log) ? readFileSync(log, "utf8").split("\n").filter((line) => line !== "").length : 0;
}

async function hasListingFailed(source: McpToolSource, serverName: string): Promise<boolean> {
  const listing = (source as unknown as { listings: Map<string, { failure: unknown }> }).listings.get(serverName);
  return Promise.resolve(listing?.failure !== undefined);
}
