// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import { SpanKind } from "@opentelemetry/api";
import { BasicTracerProvider, InMemorySpanExporter, SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";

import { contextSummaryCodec, turnFailureRouteInputCodec } from "../../src/agent-session-codecs.js";
import {
  createAgentSessionFlow,
  outcomeUnknownToolResult,
  type AgentEvent,
  type AgentEventWriter,
  type ContextSummary,
} from "../../src/index.js";
import {
  StepwiseAgentRunner,
  buildContextSummaryOmissionNote,
  contextSummaryInstructions,
  contextSummaryNoteHeader,
  contextSummaryRequest,
  type ModelClient,
  type ModelInputItem,
  type ModelOutputItem,
  type ModelRequest,
} from "../../src/stepwise/index.js";
import { toStepwiseFrameworkMessage } from "../../src/stepwise/stepwise-input-items.js";
import {
  convertAgentSnapshotToJson,
  streamAgentUIMessageChunks,
  type AgentUIMessageChunk,
} from "../../src/ui-stream/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "../../src/testing/index.js";
import {
  compactionWindowTokensVariable,
  createCompactionTestAgentFlow,
  failSummaryFileName,
  readLoggedCompactionModelCalls,
  summaryGateName,
} from "../fixtures/compaction-test-agent.js";
import {
  connectCompactionTestClient,
  listAllMessages,
  newMarkerDirectory,
  newToken,
  startCompactionTestWorker,
} from "../fixtures/compaction-test-session.js";
import { ScriptedAgentRunner } from "../fixtures/scripted-agent-runner.js";

// Each "pad" turn stores about 115 tokens, so the sixth model call crosses 85% of 600.
const padTurnCommands = [1, 2, 3, 4, 5, 6].map((turn) => `pad 400 t${turn}`);
const summarizedThroughSequence = 8;

describe("context compaction", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    ({ environment, sessions } = await connectCompactionTestClient(engine));
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  async function startWorker(
    markerDirectory: string,
    extraEnvironment: Readonly<Record<string, string>> = {},
  ): Promise<KillableWorkerProcess> {
    const worker = await startCompactionTestWorker(engine, markerDirectory, extraEnvironment);
    workers.push(worker);
    return worker;
  }

  async function runTurns(sessionId: string, commands: readonly string[]): Promise<void> {
    await sessions.startSession(sessionId, commands[0] ?? "");
    for (const command of commands.slice(1)) {
      assert.equal(await sessions.sendUserMessage(sessionId, command), true);
    }
    await sessions.waitForCompletedTurns(sessionId, commands.length, { timeoutMs: 120_000 });
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function eventPayloads(sessionId: string, eventType: string): Promise<Record<string, unknown>[]> {
    return (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === eventType)
      .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
  }

  test("crossing the threshold compacts once before the model call and keeps every message", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `compaction-threshold-${newToken()}`;
    await runTurns(sessionId, padTurnCommands);

    // One summary, of the first four turns, ran in the sixth turn before its model call.
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((stepType) => stepType === "CompactContext").length, 1);
    // The first RunModel of the turn makes no model call; it moves to CompactContext.
    assert.deepEqual(steps.slice(-4), ["AwaitUserMessage", "RunModel", "CompactContext", "RunModel"]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const summary = snapshot.contextSummary;
    assert.ok(summary !== null && summary !== undefined);
    assert.deepEqual([summary.generation, summary.firstSequence, summary.lastSequence], [1, 1, summarizedThroughSequence]);
    assert.ok(summary.text.startsWith("commands: pad 400 t1 || pad 400 t2 || pad 400 t3 || pad 400 t4"));

    // The model input of that call starts with the summary and leaves the summarized messages out.
    const calls = readLoggedCompactionModelCalls(markerDirectory);
    assert.deepEqual(
      calls.filter((call) => call.kind === "summary").map((call) => call.turnId),
      ["turn-6"],
    );
    const sixthTurnCalls = calls.filter((call) => call.kind === "model" && call.turnId === "turn-6");
    assert.equal(sixthTurnCalls.length, 1);
    const items = sixthTurnCalls[0]?.items ?? [];
    assert.deepEqual(items[0], ["user", `${contextSummaryNoteHeader}\n${summary.text}`]);
    const shownTexts = items.slice(1).map((item) => String(item[1]));
    assert.equal(shownTexts[0], "pad 400 t5");
    assert.equal(shownTexts.at(-1), "pad 400 t6");
    assert.ok(!shownTexts.some((text) => ["t1", "t2", "t3", "t4"].some((tag) => text.includes(tag))));

    // Every message stays stored and listable.
    const messages = await listAllMessages(sessions, sessionId);
    assert.deepEqual(
      messages.map((message) => message.sequence),
      Array.from({ length: 12 }, (_value, index) => index + 1),
    );
    assert.deepEqual(
      messages.filter((message) => message.role === "user").map((message) => message.text),
      padTurnCommands,
    );
    assert.equal(snapshot.messages.at(-1)?.text, `t6:${"x".repeat(400)}`);
    assert.equal(snapshot.state?.status, "waiting_for_user_message");
    assert.equal(snapshot.usage.modelCalls, 7);

    assert.deepEqual(await eventPayloads(sessionId, "compaction_started"), [
      { through_sequence: summarizedThroughSequence },
    ]);
    assert.deepEqual(await eventPayloads(sessionId, "compaction_finished"), [
      { generation: 1, through_sequence: summarizedThroughSequence },
    ]);
    const compactionEvents = (await sessions.listAgentEvents(sessionId)).filter((event: AgentEvent) =>
      event.stepExecutionId.startsWith("CompactContext-"),
    );
    assert.deepEqual([...new Set(compactionEvents.map((event) => event.turnId))], ["turn-6"]);
    assert.deepEqual(
      compactionEvents.map((event) => event.type),
      ["compaction_started", "usage_recorded", "compaction_finished"],
    );
  });

  test("the UI message stream carries compaction as event data inside the turn's one model step", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `compaction-ui-${newToken()}`;
    // The summary waits at its gate, so the stream opens while the sixth turn is active.
    await runTurns(sessionId, ["pad 400 gate-summary", ...padTurnCommands.slice(1, 5)]);
    assert.equal(await sessions.sendUserMessage(sessionId, padTurnCommands[5] ?? ""), true);
    const gate = new FileGate(markerDirectory, summaryGateName);
    await gate.waitUntilReached();
    const streamedChunks = (async () => {
      const chunks: AgentUIMessageChunk[] = [];
      for await (const chunk of streamAgentUIMessageChunks({
        client: environment.client,
        flow: sessions.flow,
        flowId: sessionId,
        pollTimeoutMs: 1_000,
      })) {
        chunks.push(chunk);
      }
      return chunks;
    })();
    await pollUntil(
      async () =>
        (await sessions.listAgentEvents(sessionId)).some((event) => event.type === "compaction_started")
          ? true
          : undefined,
      { description: "the compaction_started event" },
    );
    gate.release();
    const chunks = await streamedChunks;

    // The summary call's usage opens no step, and the stream neither pauses nor ends before the answer.
    const protocolTypes = chunks
      .map((chunk) => String((chunk as { type?: unknown }).type))
      .filter((type) => !type.startsWith("data-"));
    assert.deepEqual(protocolTypes, ["start", "start-step", "text-start", "text-delta", "text-end", "finish-step", "finish"]);
    const eventTypes = chunks.flatMap((chunk) => {
      const data = (chunk as { type?: unknown; data?: { type?: unknown } }).data;
      return (chunk as { type?: unknown }).type === "data-agent-event" ? [String(data?.type)] : [];
    });
    assert.ok(eventTypes.indexOf("compaction_started") < eventTypes.indexOf("compaction_finished"));
    assert.ok(eventTypes.includes("compaction_finished"));
    const snapshotJson = convertAgentSnapshotToJson(await sessions.getAgentSnapshot(sessionId));
    assert.deepEqual(
      (snapshotJson["context_summary"] as Record<string, unknown> | null)?.["last_sequence"],
      summarizedThroughSequence,
    );
  });

  test("the plan note follows the summary and is never summarized", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `compaction-plan-${newToken()}`;
    await runTurns(sessionId, ["plan Write the report", ...padTurnCommands.slice(1)]);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.ok(snapshot.contextSummary !== null && snapshot.contextSummary !== undefined);
    assert.ok(snapshot.plan !== null && snapshot.plan !== undefined);
    const planNotePrefix = "[Dex] The current plan, revision 1, is active:";
    const calls = readLoggedCompactionModelCalls(markerDirectory);
    const afterSummary = calls.filter(
      (call) => call.kind === "model" && String(call.items[0]?.[1]).startsWith(contextSummaryNoteHeader),
    );
    assert.ok(afterSummary.length > 0);
    for (const call of afterSummary) {
      const planNoteIndexes = call.items.flatMap((item, index) =>
        item[0] === "user" && String(item[1]).startsWith(planNotePrefix) ? [index] : [],
      );
      assert.equal(planNoteIndexes.length, 1);
      assert.ok((planNoteIndexes[0] ?? 0) > 0);
    }
    for (const call of calls.filter((entry) => entry.kind === "summary")) {
      assert.ok(!call.items.some((item) => String(item[1]).startsWith(planNotePrefix)));
    }
  });

  test("a smaller window on a long session summarizes a request that fits", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const roomyWorker = await startWorker(markerDirectory, { [compactionWindowTokensVariable]: "100000" });
    const sessionId = `compaction-shrink-${newToken()}`;
    await runTurns(
      sessionId,
      [1, 2, 3, 4, 5, 6, 7, 8].map((turn) => `pad 400 t${turn}`),
    );
    assert.equal(readLoggedCompactionModelCalls(markerDirectory).filter((call) => call.kind === "summary").length, 0);

    // A deploy shrinks the window: the range to summarize no longer fits in one request.
    await roomyWorker.kill();
    await startWorker(markerDirectory);
    assert.equal(await sessions.sendUserMessage(sessionId, "pad 400 t9"), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 9, { timeoutMs: 120_000 });

    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `t9:${"x".repeat(400)}`);
    const summaryCalls = readLoggedCompactionModelCalls(markerDirectory).filter((call) => call.kind === "summary");
    assert.equal(summaryCalls.length, 1);
    const omissionNotes = (summaryCalls[0]?.items ?? []).filter(
      (item) => item[0] === "user" && String(item[1]).endsWith("did not fit in this request and are left out."),
    );
    assert.equal(omissionNotes.length, 1);
    assert.ok(!(snapshot.contextSummary?.text ?? "").includes("pad 400 t1 ||"));
  });

  test("each CompactContext attempt emits a chat span with its usage", async () => {
    const exporter = new InMemorySpanExporter();
    const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
    const markerDirectory = newMarkerDirectory(engine);
    const tracedFlow = createCompactionTestAgentFlow(markerDirectory, 600, 0, {
      flowType: "TracedCompactionAgent",
      tracing: { tracerProvider, shouldCaptureMessageContent: true },
      summaryModelCallMetadata: {
        requestModel: "summary-model",
        responseModel: "summary-model-2026-11",
        providerName: "scripted",
      },
    });
    const tracedEnvironment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [tracedFlow],
      isWorkerInProcess: true,
    });
    const sessionId = `compaction-traced-${newToken()}`;
    let runModelExecutions = 0;
    try {
      const tracedSessions = new AgentSessionTestClient(tracedEnvironment.client, tracedFlow);
      await tracedSessions.startSession(sessionId, padTurnCommands[0] ?? "");
      for (const command of padTurnCommands.slice(1)) {
        assert.equal(await tracedSessions.sendUserMessage(sessionId, command), true);
      }
      await tracedSessions.waitForCompletedTurns(sessionId, padTurnCommands.length, { timeoutMs: 120_000 });
      runModelExecutions = (await executedStepTypes(sessionId)).filter((stepType) => stepType === "RunModel").length;
    } finally {
      await tracedEnvironment.close();
    }

    // Shutting the provider down empties the in-memory exporter, so read the spans first.
    const spans = exporter
      .getFinishedSpans()
      .filter((span) => span.attributes["gen_ai.conversation.id"] === sessionId);
    await tracerProvider.shutdown();
    const summarySpans = spans.filter((span) => span.attributes["dex.step.type"] === "CompactContext");
    assert.equal(summarySpans.length, 1);
    const summarySpan = summarySpans[0];
    assert.ok(summarySpan !== undefined);
    assert.equal(summarySpan.name, "chat summary-model");
    assert.equal(summarySpan.kind, SpanKind.CLIENT);
    assert.equal(summarySpan.attributes["gen_ai.operation.name"], "chat");
    assert.equal(summarySpan.attributes["gen_ai.request.model"], "summary-model");
    assert.equal(summarySpan.attributes["gen_ai.response.model"], "summary-model-2026-11");
    assert.equal(summarySpan.attributes["gen_ai.provider.name"], "scripted");
    assert.equal(summarySpan.attributes["gen_ai.usage.input_tokens"], 10);
    assert.equal(summarySpan.attributes["gen_ai.usage.output_tokens"], 5);
    assert.equal(summarySpan.attributes["dex.agent.turn.id"], "turn-6");
    assert.equal(summarySpan.attributes["dex.agent.input_messages.last_key"], "000000000008");
    assert.equal(
      (JSON.parse(String(summarySpan.attributes["gen_ai.input.messages"])) as unknown[]).length,
      summarizedThroughSequence,
    );
    const outputMessages = JSON.parse(String(summarySpan.attributes["gen_ai.output.messages"])) as {
      readonly parts: readonly { readonly content: string }[];
    }[];
    assert.ok(outputMessages[0]?.parts[0]?.content.startsWith("commands: pad 400 t1"));
    const root = spans.find(
      (span) => span.name.startsWith("invoke_agent") && span.attributes["dex.agent.turn.id"] === "turn-6",
    );
    assert.ok(root !== undefined);
    assert.equal(summarySpan.parentSpanContext?.spanId, root.spanContext().spanId);
    // The RunModel attempt that moved to CompactContext made no model call, so it has no span.
    const modelSpans = spans.filter((span) => span.attributes["dex.step.type"] === "RunModel");
    assert.deepEqual([runModelExecutions, modelSpans.length], [7, 6]);
  });

  test("a turn with 60 tool calls compacts mid-turn and never separates a call from its result", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory, { [compactionWindowTokensVariable]: "2000" });
    const sessionId = `compaction-chain-${newToken()}`;
    await sessions.startSession(sessionId, "chain 60");
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 180_000 });

    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "chain done: 60 lookups");
    const invocations = new InvocationLog(join(markerDirectory, "invocations.log"));
    for (let step = 0; step < 60; step += 1) {
      assert.equal(invocations.count(`lookup_order:${step}`), 1);
    }
    const finished = await eventPayloads(sessionId, "compaction_finished");
    assert.ok(finished.length >= 2);
    assert.equal(snapshot.contextSummary?.generation, finished.length);

    // The stored transcript holds exactly one result after every tool call.
    const messages = await listAllMessages(sessions, sessionId);
    assert.deepEqual(
      messages.map((message) => message.sequence),
      Array.from({ length: messages.length }, (_value, index) => index + 1),
    );
    const callSequences = new Map<string, number>();
    const resultSequences = new Map<string, number[]>();
    for (const message of messages) {
      if (message.role === "assistant" && message.toolCallId !== null) {
        callSequences.set(message.toolCallId, message.sequence);
      }
      if (message.role === "tool" && message.toolCallId !== null) {
        resultSequences.set(message.toolCallId, [...(resultSequences.get(message.toolCallId) ?? []), message.sequence]);
      }
    }
    assert.equal(callSequences.size, 60);
    // turn_completed counts every message the turn appended, summarized ones included.
    assert.deepEqual(await eventPayloads(sessionId, "turn_completed"), [{ message_count: messages.length - 1 }]);
    assert.deepEqual([...resultSequences.keys()].sort(), [...callSequences.keys()].sort());
    for (const [toolCallId, sequence] of callSequences) {
      const results = resultSequences.get(toolCallId) ?? [];
      assert.equal(results.length, 1);
      assert.ok((results[0] ?? 0) > sequence);
    }

    // Each summary ends at a boundary: no call before it has its result after it.
    for (const payload of finished) {
      const through = Number(payload["through_sequence"]);
      assert.notEqual(messages[through]?.role, "tool");
      for (const [toolCallId, sequence] of callSequences) {
        if (sequence <= through) {
          assert.ok((resultSequences.get(toolCallId)?.[0] ?? Infinity) <= through);
        }
      }
    }

    // Every model call saw each shown call answered, never with a made-up result.
    for (const call of readLoggedCompactionModelCalls(markerDirectory).filter((entry) => entry.kind === "model")) {
      const openCalls = new Set<string>();
      for (const item of call.items) {
        if (item[0] === "output") {
          for (const toolCallId of item[2] as string[]) {
            openCalls.add(toolCallId);
          }
        } else if (item[0] === "result") {
          assert.notEqual(item[2], outcomeUnknownToolResult);
          assert.equal(openCalls.delete(String(item[1])), true);
        }
      }
      assert.equal(openCalls.size, 0);
    }
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((stepType) => stepType === "CompactContext").length, finished.length);
  });

  test("a steer during a mid-turn compaction counts the summarized messages of the turn", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory, { [compactionWindowTokensVariable]: "2000" });
    const sessionId = `compaction-steer-${newToken()}`;
    await sessions.startSession(sessionId, "chain 60 gate-summary");
    const gate = new FileGate(markerDirectory, summaryGateName);
    await gate.waitUntilReached();
    assert.equal(await sessions.steerUserMessage(sessionId, "pad 10 steered"), true);
    gate.release();
    const state = await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });

    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `steered:${"x".repeat(10)}`);
    const [finished] = await eventPayloads(sessionId, "compaction_finished");
    // The summary covers the turn's user message and some of its tool calls.
    assert.ok(Number(finished?.["through_sequence"]) > 2);
    // The turn-control check comes first, so no model call follows the summary in the turn.
    const calls = readLoggedCompactionModelCalls(markerDirectory).map((call) => `${call.kind} ${call.turnId}`);
    const firstSummary = calls.indexOf("summary turn-1");
    assert.ok(firstSummary >= 0);
    assert.equal(calls.slice(firstSummary).includes("model turn-1"), false);
    const messages = await listAllMessages(sessions, sessionId);
    const firstTurnMessages = messages.filter((message) => message.turnId === "turn-1");
    // turn_steered counts every message the turn appended, summarized ones included.
    assert.deepEqual(await eventPayloads(sessionId, "turn_steered"), [
      { steered_message_count: 1, message_count: firstTurnMessages.length - 1 },
    ]);
  });

  test("a Worker killed during CompactContext reruns only the summary call and commits one generation", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `compaction-kill-${newToken()}`;
    const commands = ["pad 400 gate-summary", ...padTurnCommands.slice(1)];
    await sessions.startSession(sessionId, commands[0] ?? "");
    for (const command of commands.slice(1)) {
      assert.equal(await sessions.sendUserMessage(sessionId, command), true);
    }
    const gate = new FileGate(markerDirectory, summaryGateName);
    await gate.waitUntilReached();
    const compacting = await sessions.getAgentSnapshot(sessionId);
    assert.equal(compacting.state?.status, "compacting_context");
    assert.equal(compacting.contextSummary, null);
    // CompactContext holds no lock, so a message sent meanwhile queues for the next turn.
    assert.equal(await sessions.sendUserMessage(sessionId, "pad 10 t7"), true);

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, commands.length + 1, { timeoutMs: 120_000 });

    assert.equal(gate.reachedCount(), 2);
    const calls = readLoggedCompactionModelCalls(markerDirectory);
    assert.equal(calls.filter((call) => call.kind === "summary").length, 2);
    assert.equal(calls.filter((call) => call.kind === "model" && call.turnId === "turn-6").length, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.contextSummary?.generation, 1);
    assert.deepEqual(
      snapshot.messages.slice(-4).map((message) => message.text),
      ["pad 400 t6", `t6:${"x".repeat(400)}`, "pad 10 t7", `t7:${"x".repeat(10)}`],
    );
    assert.equal((await eventPayloads(sessionId, "compaction_finished")).length, 1);
    assert.equal((await executedStepTypes(sessionId)).filter((stepType) => stepType === "CompactContext").length, 1);
    // The usage counts seven model calls and the one committed summary call.
    assert.equal(snapshot.usage.modelCalls, 8);
  });

  test("a failing summary fails the turn through RecordTurnFailure and the next message compacts again", async () => {
    const markerDirectory = newMarkerDirectory(engine);
    await startWorker(markerDirectory);
    const sessionId = `compaction-failure-${newToken()}`;
    const failSummaryPath = join(markerDirectory, failSummaryFileName);
    writeFileSync(failSummaryPath, "");
    await runTurns(sessionId, padTurnCommands);

    const failed = await sessions.getAgentSnapshot(sessionId);
    assert.match(failed.state?.lastTurnError ?? "", /scripted summary failure/);
    assert.equal(failed.contextSummary, null);
    const calls = readLoggedCompactionModelCalls(markerDirectory);
    assert.equal(calls.filter((call) => call.kind === "summary").length, 3);
    assert.equal(calls.filter((call) => call.kind === "model" && call.turnId === "turn-6").length, 0);
    const steps = await executedStepTypes(sessionId);
    assert.deepEqual(steps.slice(-3), ["AwaitUserMessage", "RunModel", "RecordTurnFailure"]);
    assert.ok(!steps.includes("CompactContext"));
    assert.equal(failed.messages.at(-1)?.text, padTurnCommands.at(-1));

    rmSync(failSummaryPath);
    assert.equal(await sessions.sendUserMessage(sessionId, "pad 400 t7"), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 7, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.contextSummary?.generation, 1);
    assert.equal(snapshot.messages.at(-1)?.text, `t7:${"x".repeat(400)}`);
    assert.deepEqual((await executedStepTypes(sessionId)).slice(-4), [
      "AwaitUserMessage",
      "RunModel",
      "CompactContext",
      "RunModel",
    ]);
  });

  test("the summary cap leaves out whole model responses", async () => {
    // The OpenAI Responses API stores a reasoning item and its function call as two messages.
    const reasoning: ModelOutputItem = {
      type: "model_output",
      provider: "openai",
      api: "responses",
      item: '{"id":"rs_1"}',
      text: null,
      toolCalls: [],
    };
    const functionCall: ModelOutputItem = {
      ...reasoning,
      item: '{"id":"fc_1"}',
      toolCalls: [{ toolCallId: "c1", toolName: "lookup_order", arguments: "{}" }],
    };
    const answer: ModelOutputItem = { ...reasoning, item: '{"id":"msg_1"}', text: "Order 1 shipped." };
    const rangeItems: ModelInputItem[] = [
      { type: "user_text", text: "Look up order 1" },
      reasoning,
      functionCall,
      { type: "tool_result", toolCallId: "c1", toolName: "lookup_order", output: "shipped" },
      answer,
    ];
    const messages = rangeItems.map((item, index) => ({
      ...toStepwiseFrameworkMessage(item),
      sequence: index + 1,
      turnId: "turn-1",
      createdAt: "2026-11-01T12:00:00.000Z",
    }));
    const requests: ModelRequest[] = [];
    // Counts 10 tokens per input item and records each request.
    const model: ModelClient = {
      callModel: async (request) => {
        requests.push(request);
        return {
          outputItems: [],
          text: "summary",
          toolCalls: [],
          usage: { modelCalls: 1, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, reasoningTokens: 0 },
          responseModel: "",
          finishReason: "",
        };
      },
      estimateInputTokens: (request) => 10 * request.inputItems.length,
    };
    // 20 tokens for the note and the request text, then 10 per item: 70 in all.
    for (const [inputTokenBudget, maxOutputTokens] of [
      [50, undefined],
      [60, undefined],
      [70, 10],
    ] as const) {
      const runner = new StepwiseAgentRunner({
        name: "agent",
        instructions: "Help.",
        model,
        ...(maxOutputTokens === undefined ? {} : { modelSettings: { maxOutputTokens } }),
      });
      await runner.summarizeContext({
        flowId: "session-1",
        turnId: "turn-1",
        attempt: 1,
        stepExecutionId: "CompactContext-1",
        previousSummary: null,
        messages,
        throughSequence: 5,
        events: { turnId: "turn-1", attempt: 1 } as unknown as AgentEventWriter,
        cancellationSignal: new AbortController().signal,
        deadline: undefined,
        inputTokenBudget,
        heartbeat: async () => {},
      });
    }
    const [at50, at60, at70Less10Output] = requests;
    // Leaving out the user message is not enough, so the whole response and its result go too.
    assert.deepEqual(at50?.inputItems, [
      { type: "user_text", text: "[Dex] 4 earlier messages did not fit in this request and are left out." },
      answer,
      { type: "user_text", text: contextSummaryRequest },
    ]);
    assert.deepEqual(at60?.inputItems, [
      { type: "user_text", text: "[Dex] 1 earlier message did not fit in this request and is left out." },
      ...rangeItems.slice(1),
      { type: "user_text", text: contextSummaryRequest },
    ]);
    // The window also holds the summary, so maxOutputTokens comes off the budget.
    assert.deepEqual(at70Less10Output?.inputItems, at60?.inputItems);
  });

  test("compaction needs a runner with the compaction methods and a policy in range", () => {
    assert.throws(
      () =>
        createAgentSessionFlow(new ScriptedAgentRunner(newMarkerDirectory(engine)), "NoCompaction", {
          compaction: { contextWindowTokens: 1_000 },
        }),
      /estimateInputTokens, selectCompactionCutoff, and summarizeContext/,
    );
    for (const compaction of [
      { contextWindowTokens: 0 },
      { contextWindowTokens: 1_000, retainedRatio: 0.9 },
      { contextWindowTokens: 1_000, triggerRatio: 1.5 },
    ]) {
      assert.throws(() => createCompactionFlowWithPolicy(compaction), RangeError);
    }
  });

  test("compaction values have the shared wire format", () => {
    const summary: ContextSummary = {
      generation: 2,
      firstSequence: 1,
      lastSequence: 41,
      text: "the user wants a refund",
      createdAt: "2026-11-01T12:00:00.000Z",
    };
    // The Python core's test encodes the same values; its SDK sorts the keys, so compare the JSON as data.
    const encodedSummary = contextSummaryCodec.encode(summary);
    assert.equal(encodedSummary.kind, "json");
    assert.deepEqual(
      JSON.parse(String(encodedSummary.data)),
      JSON.parse(
        '{"created_at":"2026-11-01T12:00:00.000Z","first_sequence":1,"generation":2,' +
          '"last_sequence":41,"text":"the user wants a refund"}',
      ),
    );
    assert.deepEqual(contextSummaryCodec.decode(contextSummaryCodec.encode(summary)), summary);
    assert.deepEqual(turnFailureRouteInputCodec.encode({ throughSequence: 41 }), {
      kind: "json",
      data: '{"through_sequence":41}',
    });
    assert.deepEqual(turnFailureRouteInputCodec.encode(undefined), { kind: "json", data: "null" });
    assert.equal(turnFailureRouteInputCodec.decode({ kind: "json", data: "null" }), undefined);
    // Model-visible text, the same literals as in the Python core's test.
    assert.equal(contextSummaryNoteHeader, "[Dex] Summary of the earlier conversation:");
    assert.equal(
      buildContextSummaryOmissionNote(3),
      "[Dex] 3 earlier messages did not fit in this request and are left out.",
    );
    assert.equal(
      buildContextSummaryOmissionNote(1),
      "[Dex] 1 earlier message did not fit in this request and is left out.",
    );
    assert.equal(contextSummaryRequest, "[Dex] Summarize the conversation above. Reply with the summary only.");
    assert.equal(
      contextSummaryInstructions,
      "You summarize the earlier part of an agent conversation. The agent continues from " +
        "your summary and the messages after it, so keep every fact, decision, user request " +
        "and preference, open task, and tool result that it still needs, including what an " +
        "earlier summary says. Write plain text.",
    );
  });
});

function createCompactionFlowWithPolicy(compaction: {
  readonly contextWindowTokens: number;
  readonly triggerRatio?: number;
  readonly retainedRatio?: number;
}): void {
  const flow = createCompactionTestAgentFlow("unused");
  createAgentSessionFlow(flow.runner, "BadCompaction", { compaction });
}
