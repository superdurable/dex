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
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, before, describe, test } from "node:test";

import { SpanKind, SpanStatusCode, trace, type AttributeValue } from "@opentelemetry/api";
import {
  BasicTracerProvider,
  InMemorySpanExporter,
  ParentBasedSampler,
  SamplingDecision,
  SimpleSpanProcessor,
  type ReadableSpan,
  type Sampler,
  type SamplingResult,
  type SpanProcessor,
} from "@opentelemetry/sdk-trace-base";
import { Registry, Worker, openBlobCache, type BlobCache } from "@superdurable/dex";

import type {
  AgentFrameworkRunner,
  AgentSessionFlow,
  AgentTracingOptions,
  DeferredToolOutcome,
  AgentTurnContext,
  AgentTurnOutcome,
  AgentUsage,
} from "../../src/index.js";
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
import { blockedImportLogVariable } from "../fixtures/blocked-imports.js";
import {
  ScriptedAgentRunner,
  createScriptedAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
} from "../fixtures/scripted-agent-runner.js";

const latestSchemaUrl = "https://opentelemetry.io/schemas/gen-ai-dev/1.42.0-dev";
const tracedUsage: AgentUsage = {
  modelCalls: 1,
  inputTokens: 10,
  outputTokens: 5,
  cachedInputTokens: 4,
  reasoningTokens: 2,
};

class ScriptedAgentRunnerWithModelMetadata extends ScriptedAgentRunner {
  public constructor(
    markerDirectory: string,
    private readonly usage: AgentUsage = tracedUsage,
    private readonly finishReasons: readonly string[] | null = ["stop"],
  ) {
    super(markerDirectory);
  }

  public override async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const outcome = await super.runModelStep(turn);
    return {
      ...outcome,
      usage: this.usage,
      modelCallMetadata: {
        requestModel: "scripted-model",
        responseModel: "scripted-model-2026-11",
        providerName: "scripted",
        responseId: `${turn.turnId}-attempt-${turn.attempt}`,
        // A JavaScript runner can return null despite the type; tracing must tolerate it.
        finishReasons: this.finishReasons as readonly string[],
      },
    };
  }
}

/** Samples every other root span: a deterministic 50% rate. */
class AlternatingRootSampler implements Sampler {
  private rootCount = 0;

  public shouldSample(): SamplingResult {
    this.rootCount += 1;
    return {
      decision: this.rootCount % 2 === 1 ? SamplingDecision.RECORD_AND_SAMPLED : SamplingDecision.NOT_RECORD,
    };
  }

  public toString(): string {
    return "AlternatingRootSampler";
  }
}

/** A broken processor: it throws when any span starts or ends. */
class ThrowingSpanProcessor implements SpanProcessor {
  public onStart(): void {
    throw new Error("span processor failed on start");
  }

  public onEnd(): void {
    throw new Error("span processor failed on end");
  }

  public async forceFlush(): Promise<void> {}

  public async shutdown(): Promise<void> {}
}

/** Counts spans that started and have not ended. */
class OpenSpanCounter implements SpanProcessor {
  public openSpanCount = 0;

  public onStart(): void {
    this.openSpanCount += 1;
  }

  public onEnd(): void {
    this.openSpanCount -= 1;
  }

  public async forceFlush(): Promise<void> {}

  public async shutdown(): Promise<void> {}
}

describe("agent sessions emit OpenTelemetry GenAI spans", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let tracerProvider: BasicTracerProvider;
  let globalTracerProvider: BasicTracerProvider;
  let throwingTracerProvider: BasicTracerProvider;
  const exporter = new InMemorySpanExporter();
  const globalExporter = new InMemorySpanExporter();
  const sampledExporter = new InMemorySpanExporter();
  const openSpans = new OpenSpanCounter();
  let markerDirectory: string;
  let tracedFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let contentFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let legacyFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let untracedFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let globalFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let throwingProcessorFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let zeroUsageFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let harnessFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let sampledFlow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    tracerProvider = new BasicTracerProvider({
      spanProcessors: [openSpans, new SimpleSpanProcessor(exporter)],
    });
    globalTracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(globalExporter)] });
    assert.equal(trace.setGlobalTracerProvider(globalTracerProvider), true);
    throwingTracerProvider = new BasicTracerProvider({ spanProcessors: [new ThrowingSpanProcessor()] });
    const createFlow = (
      flowType: string,
      tracing: AgentTracingOptions,
      runner = new ScriptedAgentRunnerWithModelMetadata(markerDirectory),
    ): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>> => createScriptedAgentFlow(markerDirectory, { flowType, runner, tracing });
    tracedFlow = createFlow("TracedAgent", { tracerProvider });
    contentFlow = createFlow("ContentTracedAgent", { tracerProvider, shouldCaptureMessageContent: true });
    legacyFlow = createFlow("LegacyTracedAgent", {
      tracerProvider,
      agentName: "Legacy agent",
      shouldCaptureMessageContent: true,
      semanticConventionsVersion: "1.36.0",
    });
    untracedFlow = createFlow("UntracedAgent", { tracerProvider, isEnabled: false });
    globalFlow = createFlow("GlobalTracedAgent", {});
    throwingProcessorFlow = createFlow("ThrowingProcessorAgent", {
      tracerProvider: throwingTracerProvider,
      shouldCaptureMessageContent: true,
    });
    zeroUsageFlow = createFlow(
      "ZeroUsageAgent",
      { tracerProvider },
      new ScriptedAgentRunnerWithModelMetadata(
        markerDirectory,
        { modelCalls: 1, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, reasoningTokens: 0 },
        null,
      ),
    );
    harnessFlow = createFlow(
      "HarnessAgent",
      { tracerProvider },
      new ScriptedAgentRunnerWithModelMetadata(markerDirectory, {
        modelCalls: 2,
        inputTokens: 30,
        outputTokens: 12,
        cachedInputTokens: 0,
        reasoningTokens: 0,
      }),
    );
    sampledFlow = createFlow("SampledAgent", {
      tracerProvider: new BasicTracerProvider({
        sampler: new ParentBasedSampler({ root: new AlternatingRootSampler() }),
        spanProcessors: [new SimpleSpanProcessor(sampledExporter)],
      }),
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [
        sampledFlow,
        tracedFlow,
        contentFlow,
        legacyFlow,
        untracedFlow,
        globalFlow,
        throwingProcessorFlow,
        zeroUsageFlow,
        harnessFlow,
      ],
      isWorkerInProcess: true,
    });
  });

  after(async () => {
    await environment?.close();
    await tracerProvider?.shutdown();
    await engine?.stop();
  });

  function sessionSpans(sessionId: string): ReadableSpan[] {
    return [...exporter.getFinishedSpans(), ...globalExporter.getFinishedSpans()].filter(
      (span) => span.attributes["gen_ai.conversation.id"] === sessionId,
    );
  }

  async function waitForSpans(sessionId: string, count: number): Promise<ReadableSpan[]> {
    const spans = await pollUntil(
      () => {
        const finished = sessionSpans(sessionId);
        return finished.length >= count ? finished : undefined;
      },
      { description: `${count} spans of ${sessionId}` },
    );
    assert.equal(spans.length, count, spans.map((span) => span.name).join(", "));
    return spans;
  }

  test("a turn with a tool call and an approval wait emits one trace per activation", async () => {
    const sessions = new AgentSessionTestClient(environment.client, tracedFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather and send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    await sessions.waitForToolCall(sessionId, "turn-1-get_weather", (toolCall) => toolCall.status === "succeeded");

    // The session parks on the approval: the first activation's trace is complete.
    const firstActivation = await waitForSpans(sessionId, 3);
    assert.equal(openSpans.openSpanCount, 0);
    const [firstRoot, firstChat, getWeather] = spansByName(
      firstActivation,
      "invoke_agent TracedAgent",
      "chat scripted-model",
      "execute_tool get_weather",
    );
    assertActivationRoot(firstRoot, null);
    assertChildrenOf(firstRoot, [firstChat, getWeather]);
    assertAttributes(firstRoot, {
      "dex.agent.activation.trigger": "user_message",
      "dex.agent.user_message.key": "000000000001",
      "dex.step.type": "StartAgentSession",
    });
    const [traceContext] = await environment.client.getAttributes(sessionId, tracedFlow.agentTraceContext);
    assert.equal(traceContext, traceparent(firstRoot));

    assert.equal(firstChat.kind, SpanKind.CLIENT);
    assertAttributes(firstChat, {
      "gen_ai.operation.name": "chat",
      "gen_ai.provider.name": "scripted",
      "gen_ai.request.model": "scripted-model",
      "gen_ai.response.model": "scripted-model-2026-11",
      "gen_ai.response.id": "turn-1-attempt-1",
      "gen_ai.response.finish_reasons": ["stop"],
      "gen_ai.usage.input_tokens": 10,
      "gen_ai.usage.output_tokens": 5,
      "gen_ai.usage.cache_read.input_tokens": 4,
      "gen_ai.usage.reasoning.output_tokens": 2,
      "dex.step.type": "RunModel",
      "dex.agent.input_messages.last_key": "000000000001",
      "dex.agent.requested_tool_call.ids": ["turn-1-get_weather", "turn-1-send_email"],
    });
    assert.equal(getWeather.kind, SpanKind.INTERNAL);
    assertAttributes(getWeather, {
      "gen_ai.operation.name": "execute_tool",
      "gen_ai.agent.name": "TracedAgent",
      "gen_ai.tool.name": "get_weather",
      "gen_ai.tool.call.id": "turn-1-get_weather",
      "dex.step.type": "CallTool",
      "dex.agent.tool.effect": "read",
      "dex.agent.tool_call.status": "succeeded",
    });

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const secondActivation = (await waitForSpans(sessionId, 6)).filter((span) => !firstActivation.includes(span));
    const [secondRoot, sendEmail, secondChat] = spansByName(
      secondActivation,
      "invoke_agent TracedAgent",
      "execute_tool send_email",
      "chat scripted-model",
    );
    assertActivationRoot(secondRoot, firstRoot);
    assertChildrenOf(secondRoot, [sendEmail, secondChat]);
    assertAttributes(secondRoot, {
      "dex.agent.activation.trigger": "interrupt_resolution",
      "dex.agent.interrupt.id": "turn-1-send_email",
      "dex.agent.interrupt.approved": true,
      "dex.step.type": "AwaitToolApproval",
    });
    assertAttributes(sendEmail, {
      "dex.step.type": "CallSingleAttemptTool",
      "dex.agent.tool.effect": "unclassified",
      "dex.agent.tool_call.status": "succeeded",
    });
    // The final model call produced only the answer; the earlier messages came from the tools.
    assertAttributes(secondChat, { "dex.agent.output_messages.keys": ["000000000006"] });

    // The next turn is a third activation, linked to the second.
    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const thirdActivation = (await waitForSpans(sessionId, 8)).filter(
      (span) => !firstActivation.includes(span) && !secondActivation.includes(span),
    );
    const [thirdRoot, thirdChat] = spansByName(thirdActivation, "invoke_agent TracedAgent", "chat scripted-model");
    assertActivationRoot(thirdRoot, secondRoot);
    assertChildrenOf(thirdRoot, [thirdChat]);
    assertAttributes(thirdRoot, { "dex.step.type": "AwaitUserMessage" });
    assert.equal(openSpans.openSpanCount, 0);

    const spans = sessionSpans(sessionId);
    assert.equal(new Set(spans.map((span) => span.spanContext().traceId)).size, 3);
    const executedSteps = new Set(
      (await readStepHistory(engine.serverAddress, sessionId))
        .filter((entry) => entry.eventType === executeCompletedEvent)
        .map((entry) => `${entry.stepType} ${entry.stepExecutionId}`),
    );
    for (const span of spans) {
      assertAttributes(span, { "session.id": sessionId, "dex.flow.id": sessionId, "dex.step.attempt": 1 });
      assert.notEqual(span.attributes["dex.run.id"], "");
      assert.ok(
        executedSteps.has(`${span.attributes["dex.step.type"]} ${span.attributes["dex.step.execution_id"]}`),
        span.name,
      );
      assert.equal(span.instrumentationScope.name, "@superdurable/dex-agents");
      assert.equal(span.instrumentationScope.schemaUrl, latestSchemaUrl);
      assert.ok(
        !Object.keys(span.attributes).some(
          (name) => name.startsWith("gen_ai.input") || name.startsWith("gen_ai.output.messages"),
        ),
        span.name,
      );
      assert.equal(span.attributes["gen_ai.tool.call.arguments"], undefined);
    }
  });

  test("retries emit one span per attempt", async () => {
    const sessions = new AgentSessionTestClient(environment.client, tracedFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call check_stock");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const spans = await waitForSpans(sessionId, 6);
    const [root] = spansByName(spans, "invoke_agent TracedAgent");
    const checkStockAttempts = byAttempt(spans.filter((span) => span.name === "execute_tool check_stock"));
    assert.deepEqual(
      checkStockAttempts.map((span) => span.attributes["dex.step.attempt"]),
      [1, 2, 3],
    );
    assert.equal(new Set(checkStockAttempts.map((span) => span.attributes["dex.step.execution_id"])).size, 1);
    assert.deepEqual(
      checkStockAttempts.map((span) => [span.status.code, span.attributes["error.type"]]),
      [
        [SpanStatusCode.ERROR, "Error"],
        [SpanStatusCode.ERROR, "Error"],
        [SpanStatusCode.ERROR, "Error"],
      ],
    );
    assertChildrenOf(root, checkStockAttempts);

    assert.equal(await sessions.sendUserMessage(sessionId, "call get_weather then fail"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const failedTurn = (await waitForSpans(sessionId, 12)).filter((span) => !spans.includes(span));
    const [failedRoot] = spansByName(failedTurn, "invoke_agent TracedAgent");
    // A failed model call reports no model, so its span keeps the name "chat".
    const failedModelCalls = byAttempt(failedTurn.filter((span) => span.name === "chat"));
    assert.deepEqual(
      failedModelCalls.map((span) => [span.attributes["dex.step.attempt"], span.status.code, span.attributes["error.type"]]),
      [
        [1, SpanStatusCode.ERROR, "Error"],
        [2, SpanStatusCode.ERROR, "Error"],
        [3, SpanStatusCode.ERROR, "Error"],
      ],
    );
    assert.equal(new Set(failedModelCalls.map((span) => span.attributes["dex.step.execution_id"])).size, 1);
    assertChildrenOf(failedRoot, failedModelCalls);
    assert.equal(openSpans.openSpanCount, 0);
  });

  test("message content is captured only when enabled", async () => {
    const sessions = new AgentSessionTestClient(environment.client, contentFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const [root, firstChat, getWeather, secondChat] = spansByName(
      await waitForSpans(sessionId, 4),
      "invoke_agent ContentTracedAgent",
      "chat scripted-model",
      "execute_tool get_weather",
      "chat scripted-model",
    );
    const userMessage = { role: "user", parts: [{ type: "text", content: "call get_weather" }] };
    const toolCall = {
      type: "tool_call",
      id: "turn-1-get_weather",
      name: "get_weather",
      arguments: { requested_by: "call get_weather" },
    };
    assert.deepEqual(jsonAttribute(root, "gen_ai.input.messages"), [userMessage]);
    assert.deepEqual(jsonAttribute(firstChat, "gen_ai.input.messages"), [userMessage]);
    assert.deepEqual(jsonAttribute(firstChat, "gen_ai.output.messages"), [{ role: "assistant", parts: [toolCall] }]);
    assertAttributes(getWeather, {
      "gen_ai.tool.call.arguments": '{"requested_by":"call get_weather"}',
      "gen_ai.tool.call.result": "sunny in Paris",
    });
    assert.deepEqual(jsonAttribute(secondChat, "gen_ai.input.messages"), [
      userMessage,
      { role: "assistant", parts: [toolCall] },
      { role: "tool", parts: [{ type: "tool_call_response", id: "turn-1-get_weather", response: "sunny in Paris" }] },
    ]);
    assert.deepEqual(jsonAttribute(secondChat, "gen_ai.output.messages"), [
      { role: "assistant", parts: [{ type: "text", content: "get_weather -> sunny in Paris" }] },
    ]);
  });

  test("semantic conventions 1.36.0 use gen_ai.system and capture no content", async () => {
    const sessions = new AgentSessionTestClient(environment.client, legacyFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const spans = await waitForSpans(sessionId, 4);
    const [chat] = spansByName(spans, "chat scripted-model");
    assertAttributes(chat, {
      "gen_ai.system": "scripted",
      "gen_ai.usage.input_tokens": 10,
      "gen_ai.usage.output_tokens": 5,
    });
    for (const name of [
      "gen_ai.provider.name",
      "gen_ai.usage.cache_read.input_tokens",
      "gen_ai.usage.reasoning.output_tokens",
    ]) {
      assert.equal(chat.attributes[name], undefined, name);
    }
    spansByName(spans, "invoke_agent Legacy agent", "execute_tool get_weather");
    for (const span of spans) {
      assert.equal(span.instrumentationScope.schemaUrl, "https://opentelemetry.io/schemas/1.36.0");
      assert.ok(
        !Object.keys(span.attributes).some((name) => name.includes(".messages") || name.includes(".call.result")),
        span.name,
      );
    }
  });

  test("disabled tracing emits no spans and stores no trace context", async () => {
    const sessions = new AgentSessionTestClient(environment.client, untracedFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.deepEqual(sessionSpans(sessionId), []);
    const [traceContext] = await environment.client.getAttributes(sessionId, untracedFlow.agentTraceContext);
    assert.equal(traceContext, undefined);
  });

  test("the global tracer provider receives spans by default", async () => {
    const sessions = new AgentSessionTestClient(environment.client, globalFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const [root, chat] = spansByName(
      await waitForSpans(sessionId, 2),
      "invoke_agent GlobalTracedAgent",
      "chat scripted-model",
    );
    assert.ok(globalExporter.getFinishedSpans().includes(root));
    assert.ok(globalExporter.getFinishedSpans().includes(chat));
    assertChildrenOf(root, [chat]);
  });

  test("a throwing span processor never fails or repeats a Step", async () => {
    const sessions = new AgentSessionTestClient(environment.client, throwingProcessorFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather and send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `get_weather -> sunny in Paris | send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email`,
    );
    for (const toolName of ["get_weather", "send_email"]) {
      assert.deepEqual(new InvocationLog(join(markerDirectory, `${toolName}.log`)).names().filter((entry) => entry.startsWith(sessionId)), [
        `${sessionId} turn-1-${toolName} attempt 1`,
      ]);
    }
    const executedSteps = (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
    assert.ok(!executedSteps.includes("RecordToolOutcomeUnknown"), executedSteps.join(", "));
    assert.ok(!executedSteps.includes("RecordTurnFailure"), executedSteps.join(", "));
  });

  test("a model call that reports no tokens sets no usage, and a null finishReasons is ignored", async () => {
    const sessions = new AgentSessionTestClient(environment.client, zeroUsageFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");
    const state = await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);
    const [chat] = spansByName(await waitForSpans(sessionId, 2), "chat scripted-model");
    assert.equal(chat.kind, SpanKind.CLIENT);
    assert.deepEqual(
      Object.keys(chat.attributes).filter((name) => name.startsWith("gen_ai.usage.") || name.includes("finish")),
      [],
    );
  });

  test("a RunModel with several model calls is an INTERNAL chat span without usage", async () => {
    const sessions = new AgentSessionTestClient(environment.client, harnessFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "hello");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const [chat] = spansByName(await waitForSpans(sessionId, 2), "chat scripted-model");
    assert.equal(chat.kind, SpanKind.INTERNAL);
    assertAttributes(chat, { "gen_ai.request.model": "scripted-model" });
    assert.deepEqual(Object.keys(chat.attributes).filter((name) => name.startsWith("gen_ai.usage.")), []);
  });

  test("spans follow the sampling decision of their activation root", async () => {
    const sessions = new AgentSessionTestClient(environment.client, sampledFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId);
    for (let turn = 1; turn <= 6; turn += 1) {
      assert.equal(await sessions.sendUserMessage(sessionId, "call get_weather"), true);
      await sessions.waitForCompletedTurns(sessionId, turn);
    }
    // The sixth root was sampled out, yet its context is stored, unsampled, for its children.
    const [traceContext] = await environment.client.getAttributes(sessionId, sampledFlow.agentTraceContext);
    assert.equal(typeof traceContext, "string");
    const traceFlags = /^00-[0-9a-f]{32}-[0-9a-f]{16}-([0-9a-f]{2})$/.exec(String(traceContext))?.[1];
    assert.ok(traceFlags !== undefined, `not a traceparent: ${String(traceContext)}`);
    assert.equal(Number.parseInt(traceFlags, 16) & 0x01, 0);

    const spans = sampledExporter.getFinishedSpans();
    const roots = spans.filter((span) => span.name === "invoke_agent SampledAgent");
    const children = spans.filter((span) => !roots.includes(span));
    assert.deepEqual(
      roots.map((root) => root.attributes["dex.agent.turn.id"]),
      ["turn-1", "turn-3", "turn-5"],
    );
    // ParentBased sampling drops every child of a dropped root: no child is a trace of its own.
    assert.deepEqual(children.filter((span) => span.parentSpanContext === undefined), []);
    assert.deepEqual(
      new Set(children.map((span) => span.spanContext().traceId)),
      new Set(roots.map((root) => root.spanContext().traceId)),
    );
    assert.deepEqual(
      children.map((span) => span.name).sort(),
      [...Array(6).fill("chat scripted-model"), ...Array(3).fill("execute_tool get_weather")],
    );
  });

  test("two approvals in one turn keep each tool span in its own activation", async () => {
    const sessions = new AgentSessionTestClient(environment.client, tracedFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call retry_payout and send_email");
    await sessions.waitForAgentSnapshot(
      sessionId,
      (snapshot) => snapshot.pendingInterrupts.length === 2,
      "both writes await approval",
    );
    const payoutGate = new FileGate(markerDirectory, "retry_payout");
    assert.equal(await sessions.resolveInterrupt(sessionId, "turn-1-retry_payout", true), true);
    await payoutGate.waitUntilReached();
    // The second answer starts a newer activation while the payout's first attempt still runs.
    assert.equal(await sessions.resolveInterrupt(sessionId, "turn-1-send_email", true), true);
    await sessions.waitForToolCall(sessionId, "turn-1-send_email", (toolCall) => toolCall.status === "succeeded");
    payoutGate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);

    const spans = await waitForSpans(sessionId, 8);
    const roots = new Map(
      spans
        .filter((span) => span.name === "invoke_agent TracedAgent")
        .map((span) => [span.attributes["dex.agent.interrupt.id"] ?? "user_message", span]),
    );
    const firstRoot = roots.get("user_message");
    const payoutRoot = roots.get("turn-1-retry_payout");
    const emailRoot = roots.get("turn-1-send_email");
    assert.ok(firstRoot !== undefined && payoutRoot !== undefined && emailRoot !== undefined);
    assertActivationRoot(payoutRoot, firstRoot);
    assertActivationRoot(emailRoot, payoutRoot);
    const payouts = byAttempt(spans.filter((span) => span.name === "execute_tool retry_payout"));
    assert.deepEqual(
      payouts.map((span) => span.status.code),
      [SpanStatusCode.ERROR, SpanStatusCode.UNSET],
    );
    // The retry started after the newer answer, yet stays in the payout approval's trace.
    assertChildrenOf(payoutRoot, payouts);
    assertChildrenOf(emailRoot, spansByName(spans, "execute_tool send_email"));
    const [firstChat, finalChat] = spansByName(spans, "chat scripted-model", "chat scripted-model");
    assertChildrenOf(firstRoot, [firstChat]);
    assertChildrenOf(emailRoot, [finalChat]);
    assert.equal(new Set(spans.map((span) => span.spanContext().traceId)).size, 3);
  });

  test("a tool that asks for input records awaiting_answer on its span, and its rerun records the result", async () => {
    const sessions = new AgentSessionTestClient(environment.client, tracedFlow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call confirm_return");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "confirm_return");
    const [root, firstChat, asking] = spansByName(
      await waitForSpans(sessionId, 3),
      "invoke_agent TracedAgent",
      "chat scripted-model",
      "execute_tool confirm_return",
    );
    assertChildrenOf(root, [firstChat, asking]);
    assertAttributes(asking, {
      "dex.step.type": "CallSingleAttemptTool",
      "dex.agent.tool_call.status": "awaiting_answer",
    });
    assert.equal(asking.status.code, SpanStatusCode.UNSET);

    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "yes"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const answered = (await waitForSpans(sessionId, 5)).filter((span) => ![root, firstChat, asking].includes(span));
    const [rerun] = spansByName(answered, "execute_tool confirm_return", "chat scripted-model");
    // A question answer starts no activation yet, so the rerun stays in the asking turn's trace.
    assertChildrenOf(root, [rerun]);
    assertAttributes(rerun, { "dex.agent.tool_call.status": "succeeded" });
    assert.equal(openSpans.openSpanCount, 0);
  });
});

describe("a root Step on a Worker without an OpenTelemetry SDK", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let untracedWorker: KillableWorkerProcess | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  after(async () => {
    await untracedWorker?.kill();
    await environment?.close();
    await engine?.stop();
  });

  test("clears the stored trace context, so the next spans start a new trace", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const exporter = new InMemorySpanExporter();
    const tracerProvider = new BasicTracerProvider({ spanProcessors: [new SimpleSpanProcessor(exporter)] });
    const flow = createScriptedAgentFlow(markerDirectory, { tracing: { tracerProvider } });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    const sessions = new AgentSessionTestClient(environment.client, flow);
    const sessionId = newSessionId();
    const firstTracedWorker = await InProcessWorker.start(engine.serverAddress, flow);
    try {
      await sessions.startSession(sessionId, "hello");
      await sessions.waitForCompletedTurns(sessionId, 1);
    } finally {
      await firstTracedWorker.close();
    }
    const [firstRoot] = spansByName(exporter.getFinishedSpans(), "invoke_agent ScriptedAgent");

    // The Worker child has @opentelemetry/api but no SDK, so its root span does not record.
    untracedWorker = await KillableWorkerProcess.start({
      entryModulePath: fileURLToPath(new URL("../fixtures/scripted-agent-worker.js", import.meta.url)),
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [scriptedAgentMarkerDirectoryVariable]: markerDirectory },
    });
    assert.equal(await sessions.sendUserMessage(sessionId, "slow"), true);
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();
    await untracedWorker.kill();
    turnGate.release();
    const secondTracedWorker = await InProcessWorker.start(engine.serverAddress, flow);
    try {
      await sessions.waitForCompletedTurns(sessionId, 2);
    } finally {
      await secondTracedWorker.close();
    }
    const [traceContext] = await environment.client.getAttributes(sessionId, flow.agentTraceContext);
    assert.equal(traceContext, undefined);
    const secondTurnChats = exporter
      .getFinishedSpans()
      .filter((span) => span.name.startsWith("chat") && span.attributes["dex.agent.turn.id"] === "turn-2");
    assert.equal(secondTurnChats.length, 1);
    assert.equal(secondTurnChats[0]?.parentSpanContext, undefined);
    assert.notEqual(secondTurnChats[0]?.spanContext().traceId, firstRoot.spanContext().traceId);
    await tracerProvider.shutdown();
  });
});

describe("a Worker without @opentelemetry/api", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let worker: KillableWorkerProcess | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  after(async () => {
    await worker?.kill();
    await environment?.close();
    await engine?.stop();
  });

  test("runs sessions and never loads OpenTelemetry", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const blockedImportLogPath = join(markerDirectory, "blocked-imports.log");
    const flow = createScriptedAgentFlow(markerDirectory);
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    worker = await KillableWorkerProcess.start({
      entryModulePath: fileURLToPath(new URL("../fixtures/agent-worker-without-opentelemetry.js", import.meta.url)),
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: {
        [scriptedAgentMarkerDirectoryVariable]: markerDirectory,
        [blockedImportLogVariable]: blockedImportLogPath,
      },
    });
    const sessions = new AgentSessionTestClient(environment.client, flow);
    const sessionId = newSessionId();
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "get_weather -> sunny in Paris");
    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const [traceContext] = await environment.client.getAttributes(sessionId, flow.agentTraceContext);
    assert.equal(traceContext, undefined);
    // The session tried the API once, at its first traced Step, and never again.
    assert.deepEqual(new InvocationLog(blockedImportLogPath).names(), ["@opentelemetry/api"]);
  });
});

/** A pull Worker in the test process that a test can close and replace. */
class InProcessWorker {
  private constructor(
    private readonly worker: Worker,
    private readonly blobCache: BlobCache,
  ) {}

  public static async start(serverAddress: string, flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>): Promise<InProcessWorker> {
    const blobCache = openBlobCache({
      directory: mkdtempSync(join(tmpdir(), "dex-agents-tracing-worker-")),
      maxBytes: 64 * 1024 * 1024,
    });
    const worker = new Worker(new Registry([flow]), blobCache, {
      serverAddress,
      pull: { appWorkerID: `dex-agents-tracing-worker-${randomUUID()}`, maxConcurrentTasks: 8 },
    });
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    return new InProcessWorker(worker, blobCache);
  }

  public async close(): Promise<void> {
    await this.worker.close();
    this.blobCache.close();
  }
}

function newSessionId(): string {
  return `session-${randomUUID()}`;
}

/** Returns one span per name, in start order, consuming each match once. */
function spansByName<const Names extends readonly string[]>(
  spans: readonly ReadableSpan[],
  ...names: Names
): { readonly [Index in keyof Names]: ReadableSpan } {
  const remaining = [...spans].sort((left, right) => hrTimeNanoseconds(left.startTime) - hrTimeNanoseconds(right.startTime));
  const matches = names.map((name) => {
    const index = remaining.findIndex((span) => span.name === name);
    assert.notEqual(index, -1, `no span ${name} in ${remaining.map((span) => span.name).join(", ")}`);
    return remaining.splice(index, 1)[0] as ReadableSpan;
  });
  return matches as unknown as { readonly [Index in keyof Names]: ReadableSpan };
}

function byAttempt(spans: readonly ReadableSpan[]): ReadableSpan[] {
  return [...spans].sort((left, right) => Number(left.attributes["dex.step.attempt"]) - Number(right.attributes["dex.step.attempt"]));
}

function hrTimeNanoseconds([seconds, nanoseconds]: readonly [number, number]): number {
  return seconds * 1e9 + nanoseconds;
}

function assertAttributes(span: ReadableSpan, expected: Readonly<Record<string, AttributeValue>>): void {
  for (const [name, value] of Object.entries(expected)) {
    assert.deepEqual(span.attributes[name], value, `${span.name} ${name}`);
  }
}

function assertActivationRoot(root: ReadableSpan, previousRoot: ReadableSpan | null): void {
  assert.equal(root.parentSpanContext, undefined);
  assert.equal(root.kind, SpanKind.INTERNAL);
  assert.equal(root.attributes["gen_ai.operation.name"], "invoke_agent");
  if (previousRoot === null) {
    assert.deepEqual(root.links, []);
    return;
  }
  assert.deepEqual(
    root.links.map((link) => [link.context.traceId, link.context.spanId]),
    [[previousRoot.spanContext().traceId, previousRoot.spanContext().spanId]],
  );
  assert.notEqual(root.spanContext().traceId, previousRoot.spanContext().traceId);
}

function assertChildrenOf(root: ReadableSpan, children: readonly ReadableSpan[]): void {
  for (const child of children) {
    assert.equal(child.spanContext().traceId, root.spanContext().traceId, child.name);
    assert.equal(child.parentSpanContext?.spanId, root.spanContext().spanId, child.name);
  }
}

function traceparent(span: ReadableSpan): string {
  const spanContext = span.spanContext();
  return `00-${spanContext.traceId}-${spanContext.spanId}-${spanContext.traceFlags.toString(16).padStart(2, "0")}`;
}

function jsonAttribute(span: ReadableSpan, name: string): unknown {
  return JSON.parse(String(span.attributes[name]));
}
