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
import { after, before, describe, test } from "node:test";

import { readUIMessageStream, type UIMessage, type UIMessageChunk } from "ai";

import { turnFailureNoteHeader, type AgentMessage, type AgentSessionFlow } from "../../src/index.js";
import {
  ScriptedModelClient,
  contextSummaryInstructions,
  createBuiltinAgentSessionFlow,
  functionTool,
  maxConsecutiveTurnContinuations,
  type ModelRequest,
  type ModelToolResultItem,
  type ScriptedModelReply,
  type BuiltinLoopAgentRunner,
  type TurnContinuation,
  type TurnContinuationContext,
} from "../../src/builtin-loop/index.js";
import { buildTurnContinuationLimitError } from "../../src/builtin-loop/builtin-loop-agent-runner.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import { convertAgentSnapshotToUIMessages, streamAgentUIMessageChunks } from "../../src/ui-stream/index.js";
import { describeUIMessageParts } from "../fixtures/agent-ui-stream-http.js";

type HookMode = "decide" | "always-default" | "none";

/** What the hook saw after tools, by session: each model output item's text, or its tool names. */
interface RecordedHookContext {
  readonly modelOutput: readonly string[];
  readonly toolResultCount: number;
}

const compactionWindowTokens = 1_000;
const longDraftCount = 4;

describe("Built-in loop decideTurnContinuation with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let markerDirectory: string;
  const failedHookKeys = new Set<string>();
  const hookContexts = new Map<string, RecordedHookContext[]>();
  const compactingRequestTokens = new Map<string, number[]>();
  let compactingModel: ScriptedModelClient;
  let compactingFlow: AgentSessionFlow<BuiltinLoopAgentRunner>;
  let compactingSessions: AgentSessionTestClient;
  const models = new Map<HookMode, ScriptedModelClient>();
  const flows = new Map<HookMode, AgentSessionFlow<BuiltinLoopAgentRunner>>();
  const sessions = new Map<HookMode, AgentSessionTestClient>();

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    for (const mode of ["decide", "always-default", "none"] as const) {
      const model = new ScriptedModelClient((request) => respond(markerDirectory, request));
      models.set(mode, model);
      flows.set(mode, createContinuationAgentFlow(mode, model));
    }
    compactingModel = new ScriptedModelClient((request) => respondWithLongDrafts(request));
    compactingFlow = createBuiltinAgentSessionFlow(
      {
        name: "continuation-compacting",
        instructions: "Draft.",
        model: compactingModel,
        compaction: { contextWindowTokens: compactionWindowTokens },
        decideTurnContinuation: ({ modelOutput }): TurnContinuation => {
          const answer = modelOutput.map((item) => item.text ?? "").join("");
          return answer.startsWith("draft") ? { action: "continue", message: continuationMessageAfter(answer) } : "default";
        },
      },
      "BuiltinLoopContinuation_compacting",
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [...flows.values(), compactingFlow],
      isWorkerInProcess: true,
    });
    for (const [mode, flow] of flows) {
      sessions.set(mode, new AgentSessionTestClient(environment.client, flow));
    }
    compactingSessions = new AgentSessionTestClient(environment.client, compactingFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  function createContinuationAgentFlow(
    mode: HookMode,
    model: ScriptedModelClient,
  ): AgentSessionFlow<BuiltinLoopAgentRunner> {
    const idParameters = {
      type: "object",
      properties: { id: { type: "string" }, gate: { type: "string" } },
      required: ["id"],
    };
    return createBuiltinAgentSessionFlow(
      {
        name: `continuation-${mode}`,
        instructions: "Decide.",
        model,
        tools: [
          functionTool<{ id: string; gate?: string }>({
            name: "submit_decision",
            description: "Submit a decision.",
            parameters: idParameters,
            effect: "read",
            execute: async ({ id, gate }, context) => {
              if (gate !== undefined) {
                const fileGate = new FileGate(markerDirectory, gate);
                fileGate.markReached();
                await fileGate.waitForRelease(context.cancellationSignal);
              }
              return `decision ${id} submitted`;
            },
          }),
          functionTool<{ id: string }>({
            name: "lookup_order",
            description: "Look up an order.",
            parameters: idParameters,
            effect: "read",
            execute: ({ id }) => `order ${id} shipped`,
          }),
        ],
        ...(mode === "none"
          ? {}
          : {
              decideTurnContinuation:
                mode === "always-default"
                  ? (): TurnContinuation => "default"
                  : async (context: TurnContinuationContext) => {
                      if (context.toolResults.length > 0) {
                        const recorded = hookContexts.get(context.flowId) ?? [];
                        recorded.push({
                          modelOutput: context.modelOutput.map(
                            (item) => item.text ?? item.toolCalls.map((toolCall) => toolCall.toolName).join(","),
                          ),
                          toolResultCount: context.toolResults.length,
                        });
                        hookContexts.set(context.flowId, recorded);
                      }
                      return decideTestContinuation(context, failedHookKeys);
                    },
            }),
      },
      `BuiltinLoopContinuation_${mode.replace("-", "_")}`,
    );
  }

  function sessionsOf(mode: HookMode): AgentSessionTestClient {
    return sessions.get(mode)!;
  }

  function requestsOf(mode: HookMode, sessionId: string): ModelRequest[] {
    return models.get(mode)!.requests.filter((request) => request.metadata.flowId === sessionId);
  }

  /**
   * Drafts of about 600 tokens until {@link longDraftCount}, then `final`. A request over the window fails, as a
   * provider would. The next draft number comes from the newest continuation message, which compaction never
   * summarizes.
   */
  function respondWithLongDrafts(request: ModelRequest): ScriptedModelReply {
    if (request.instructions === contextSummaryInstructions) {
      return { text: "summary of the earlier drafts" };
    }
    const tokens = compactingModel.estimateInputTokens(request);
    const recorded = compactingRequestTokens.get(request.metadata.flowId) ?? [];
    recorded.push(tokens);
    compactingRequestTokens.set(request.metadata.flowId, recorded);
    if (tokens > compactionWindowTokens) {
      throw new Error(`simulated context window exceeded: ${tokens} tokens`);
    }
    const lastUserText = ScriptedModelClient.userTexts(request).at(-1) ?? "";
    const draftNumber = Number(/after draft (\d+)/.exec(lastUserText)?.[1] ?? "0") + 1;
    return draftNumber > longDraftCount ? { text: "final" } : { text: `draft ${draftNumber} ${"x".repeat(2_400)}` };
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  test("end after a tool completes the turn without a model call, and the next turn sees the tool result", async () => {
    const token = newToken();
    const sessionId = `continuation-end-${token}`;
    const gate = `submit-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `submit ${token} ${gate}`);
    const submitGate = new FileGate(markerDirectory, gate);
    await submitGate.waitUntilReached();
    const streaming = collectChunks(
      streamAgentUIMessageChunks({
        client: environment.client,
        flow: flows.get("decide")!,
        flowId: sessionId,
        pollTimeoutMs: 1_000,
      }),
    );
    submitGate.release();
    const state = await client.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);

    assert.equal(requestsOf("decide", sessionId).length, 1);
    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user submit ${token} ${gate}`,
      "2 turn-1 assistant submit_decision",
      `3 turn-1 tool submit_decision decision ${token} submitted`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 1);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
    const events = await client.listAgentEvents(sessionId);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_completed").map((event) => JSON.parse(event.payload)),
      [{ message_count: 2 }],
    );
    assert.equal(events.some((event) => event.type === "turn_failed"), false);
    const lastUsage = events.filter((event) => event.type === "usage_recorded").at(-1);
    assert.equal(JSON.parse(lastUsage?.payload ?? "null").recorded.model_calls, 0);

    const chunks = await streaming;
    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "stop" }],
    );
    const toolPart = `tool submit_decision output-available {"id":"${token}","gate":"${gate}"} -> decision ${token} submitted`;
    // The stream starts a step per RunModel Step, so the ending RunModel sends one more step with no visible part.
    assert.equal(chunks.filter((chunk) => chunk.type === "start-step").length, 2);
    // The AI SDK message has no part for it, so it matches the snapshot, which has a step per model call.
    assert.deepEqual(describeUIMessageParts(await readFinalMessage(chunks as UIMessageChunk[])), [
      "step-start",
      toolPart,
    ]);
    assert.deepEqual(describeUIMessageParts(convertAgentSnapshotToUIMessages(snapshot)[1]), ["step-start", toolPart]);

    await client.sendUserMessage(sessionId, "lookup 7");
    const secondTurn = await client.waitForCompletedTurns(sessionId, 2);
    assert.equal(secondTurn.lastTurnError, null);
    const secondRequest = requestsOf("decide", sessionId)[1];
    assert.ok(
      secondRequest?.inputItems.some(
        (item) => item.type === "tool_result" && item.output === `decision ${token} submitted`,
      ),
      "the next turn's model input holds the tool result that ended the previous turn",
    );
    assert.equal((await client.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "lookup: order 7 shipped");
  });

  test("a hook that returns default behaves exactly like no hook", async () => {
    const outcomes: unknown[] = [];
    for (const mode of ["always-default", "none"] as const) {
      const token = newToken();
      const sessionId = `continuation-default-${mode}-${token}`;
      const client = sessionsOf(mode);
      await client.startSession(sessionId, "submit 9");
      await client.waitForCompletedTurns(sessionId, 1);
      await client.sendUserMessage(sessionId, "revise 9");
      const state = await client.waitForCompletedTurns(sessionId, 2);
      const snapshot = await client.getAgentSnapshot(sessionId);
      outcomes.push({
        lastTurnError: state.lastTurnError,
        messages: snapshot.messages.map(describeMessage),
        modelCalls: snapshot.usage.modelCalls,
        requests: requestsOf(mode, sessionId).length,
        steps: await executedStepTypes(sessionId),
      });
    }
    assert.deepEqual(outcomes[0], outcomes[1]);
    assert.deepEqual(outcomes[0], {
      lastTurnError: null,
      messages: [
        "1 turn-1 user submit 9",
        "2 turn-1 assistant submit_decision",
        "3 turn-1 tool submit_decision decision 9 submitted",
        "4 turn-1 assistant submitted",
        "5 turn-2 user revise 9",
        "6 turn-2 assistant draft for 9",
      ],
      modelCalls: 3,
      requests: 3,
      steps: ["StartAgentSession", "RunModel", "CallTool", "ResumeTurn", "RunModel", "AwaitUserMessage", "RunModel"],
    });
  });

  test("a hook that throws fails the RunModel attempt, which the retry policy runs again", async () => {
    const token = newToken();
    const sessionId = `continuation-throw-${token}`;
    const client = sessionsOf("decide");
    // After the tool, the throw comes before any model call.
    await client.startSession(sessionId, `submit hook-fail-once-${token}`);
    const firstTurn = await client.waitForCompletedTurns(sessionId, 1);
    assert.equal(firstTurn.lastTurnError, null);
    assert.equal(requestsOf("decide", sessionId).length, 1);
    // After an answer, the throw comes after the model call, so the retry repeats the call.
    await client.sendUserMessage(sessionId, `say hook-fail-once-${token}`);
    const secondTurn = await client.waitForCompletedTurns(sessionId, 2);
    assert.equal(secondTurn.lastTurnError, null);
    assert.equal(requestsOf("decide", sessionId).length, 3);

    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user submit hook-fail-once-${token}`,
      "2 turn-1 assistant submit_decision",
      `3 turn-1 tool submit_decision decision hook-fail-once-${token} submitted`,
      `4 turn-2 user say hook-fail-once-${token}`,
      `5 turn-2 assistant said hook-fail-once-${token}`,
    ]);
    const runModelAttempts = (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent && entry.stepType === "RunModel")
      .map((entry) => entry.finalAttempt);
    assert.deepEqual(runModelAttempts, [1, 2, 2]);
  });

  test("continue after an answer stores its message and runs RunModel again in the same turn", async () => {
    const token = newToken();
    const sessionId = `continuation-continue-${token}`;
    const gate = `revise-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `revise ${token} ${gate}`);
    const modelGate = new FileGate(markerDirectory, gate);
    await modelGate.waitUntilReached();
    const streaming = collectChunks(
      streamAgentUIMessageChunks({
        client: environment.client,
        flow: flows.get("decide")!,
        flowId: sessionId,
        pollTimeoutMs: 1_000,
      }),
    );
    modelGate.release();
    const state = await client.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);

    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user revise ${token} ${gate}`,
      `2 turn-1 assistant draft for ${token}`,
      "3 turn-1 user [continue] after draft for",
      `4 turn-1 assistant final for ${token}`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.equal(snapshot.state?.turnCount, 1);
    const requests = requestsOf("decide", sessionId);
    assert.equal(requests.length, 2);
    // The continued request ends with the message as user text.
    assert.deepEqual(requests[1]?.inputItems.at(-1), { type: "user_text", text: "[continue] after draft for" });
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel", "RunModel"]);
    const events = await client.listAgentEvents(sessionId);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_completed").map((event) => JSON.parse(event.payload)),
      [{ message_count: 3 }],
    );
    assert.equal(events.filter((event) => event.type === "usage_recorded").length, 2);

    const chunks = await streaming;
    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "stop" }],
    );
    const liveParts = describeUIMessageParts(await readFinalMessage(chunks as UIMessageChunk[]));
    assert.deepEqual(liveParts, ["step-start", `text draft for ${token}`, "step-start", `text final for ${token}`]);
    // A refreshed page shows the same steps: the message stays out of the chat.
    const storedMessages = convertAgentSnapshotToUIMessages(snapshot);
    assert.deepEqual(
      storedMessages.map((message) => message.role),
      ["user", "assistant"],
    );
    assert.deepEqual(describeUIMessageParts(storedMessages[1]), liveParts);
  });

  test("continue after tools adds its message after the tool results in the same model call", async () => {
    const token = newToken();
    const sessionId = `continuation-after-tools-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `lookup continue-after-tools-${token}`);
    const state = await client.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);

    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user lookup continue-after-tools-${token}`,
      "2 turn-1 assistant lookup_order",
      `3 turn-1 tool lookup_order order continue-after-tools-${token} shipped`,
      "4 turn-1 user [continue] summarize the lookup",
      `5 turn-1 assistant lookup: order continue-after-tools-${token} shipped`,
    ]);
    const requests = requestsOf("decide", sessionId);
    assert.equal(requests.length, 2);
    assert.deepEqual(
      requests[1]?.inputItems.slice(-2).map((item) => item.type),
      ["tool_result", "user_text"],
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
    // A step per model call; the message adds none, before or after a refresh.
    assert.deepEqual(describeUIMessageParts(convertAgentSnapshotToUIMessages(snapshot)[1]), [
      "step-start",
      `tool lookup_order output-available {"id":"continue-after-tools-${token}"} -> order continue-after-tools-${token} shipped`,
      "step-start",
      `text lookup: order continue-after-tools-${token} shipped`,
    ]);
  });

  for (const hasEarlierTool of [false, true]) {
    test(`a snapshot taken while a continued call runs keeps one assistant message${
      hasEarlierTool ? " after a tool call" : ""
    }`, async () => {
      const token = newToken();
      const sessionId = `continuation-running-${hasEarlierTool ? "tool" : "answer"}-${token}`;
      const gate = `running-${token}`;
      const client = sessionsOf("decide");
      await client.startSession(
        sessionId,
        hasEarlierTool ? `lookup-then-draft ${token} ${gate}` : `draft-then-stall ${token} ${gate}`,
      );
      const modelGate = new FileGate(markerDirectory, gate);
      await modelGate.waitUntilReached();

      const running = await client.getAgentSnapshot(sessionId);
      assert.equal(running.state?.status, "running_model");
      assert.equal(running.messages.at(-1)?.text, "[continue] after draft for");
      const toolParts = hasEarlierTool
        ? [`tool lookup_order output-available {"id":"${token}"} -> order ${token} shipped`, "step-start"]
        : [];
      const runningMessages = convertAgentSnapshotToUIMessages(running);
      assert.deepEqual(
        runningMessages.map((message) => message.role),
        ["user", "assistant"],
      );
      assert.equal(new Set(runningMessages.map((message) => message.id)).size, runningMessages.length);
      assert.deepEqual(describeUIMessageParts(runningMessages[1]), [
        "step-start",
        ...toolParts,
        `text draft for ${token}`,
        "step-start",
      ]);

      modelGate.release();
      await client.waitForCompletedTurns(sessionId, 1);
      const finished = convertAgentSnapshotToUIMessages(await client.getAgentSnapshot(sessionId));
      assert.deepEqual(describeUIMessageParts(finished[1]), [
        "step-start",
        ...toolParts,
        `text draft for ${token}`,
        "step-start",
        `text final for ${token}`,
      ]);
    });
  }

  test("a turn that fails after a continuation still shows its failure note", async () => {
    const token = newToken();
    const sessionId = `continuation-failure-note-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `lookup-then-draft-fail ${token}`);
    const state = await client.waitForCompletedTurns(sessionId, 1);
    assert.match(state.lastTurnError ?? "", /scripted model failure/);

    const messages = convertAgentSnapshotToUIMessages(await client.getAgentSnapshot(sessionId));
    assert.deepEqual(
      messages.map((message) => message.role),
      ["user", "assistant", "user"],
    );
    assert.equal(new Set(messages.map((message) => message.id)).size, messages.length);
    const note = messages[2]?.parts[0];
    assert.ok(note?.type === "text" && note.text.startsWith(turnFailureNoteHeader), "the failure note is shown");
    assert.deepEqual(describeUIMessageParts(messages[1]), [
      "step-start",
      `tool lookup_order output-available {"id":"${token}"} -> order ${token} shipped`,
      "step-start",
      `text draft for ${token}`,
      "step-start",
    ]);
  });

  test("after a continued answer and a tool call, the hook sees only the tool-calling response", async () => {
    const token = newToken();
    const sessionId = `continuation-boundary-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `draft-then-lookup ${token}`);
    const state = await client.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);

    assert.deepEqual(hookContexts.get(sessionId), [{ modelOutput: ["lookup_order"], toolResultCount: 1 }]);
    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user draft-then-lookup ${token}`,
      `2 turn-1 assistant draft for ${token}`,
      "3 turn-1 user [continue] after draft for",
      "4 turn-1 assistant lookup_order",
      `5 turn-1 tool lookup_order order ${token} shipped`,
      `6 turn-1 assistant lookup: order ${token} shipped`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 3);
  });

  test("continued answers that cross the compaction threshold compact the earlier responses", async () => {
    const token = newToken();
    const sessionId = `continuation-compaction-${token}`;
    await compactingSessions.startSession(sessionId, `long drafts ${token}`);
    const state = await compactingSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);

    const snapshot = await compactingSessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "final");
    assert.equal(snapshot.usage.modelCalls - (snapshot.contextSummary?.generation ?? 0), longDraftCount + 1);
    const requestTokens = compactingRequestTokens.get(sessionId) ?? [];
    assert.equal(requestTokens.length, longDraftCount + 1);
    assert.ok(
      requestTokens.every((tokens) => tokens <= compactionWindowTokens),
      `every model request fits the window: ${requestTokens.join(", ")}`,
    );
    const events = await compactingSessions.listAgentEvents(sessionId);
    const compactions = events.filter((event) => event.type === "compaction_finished").length;
    assert.ok(compactions >= 2, `the earlier responses were compacted, ${compactions} time(s)`);
    assert.equal(snapshot.contextSummary?.generation, compactions);
  });

  for (const invalid of ["bare-continue", "blank-continue"]) {
    test(`a ${invalid} result fails the RunModel attempt like a hook error`, async () => {
      const token = newToken();
      const sessionId = `continuation-${invalid}-${token}`;
      const client = sessionsOf("decide");
      await client.startSession(sessionId, `say ${invalid}-${token}`);
      const state = await client.waitForCompletedTurns(sessionId, 1);

      assert.match(state.lastTurnError ?? "", /decideTurnContinuation of agent continuation-decide must return/);
      // Each attempt repeats the model call, and nothing of the failed attempts is stored.
      assert.equal(requestsOf("decide", sessionId).length, 3);
      assert.deepEqual(
        (await client.getAgentSnapshot(sessionId)).messages.map(describeMessage),
        [`1 turn-1 user say ${invalid}-${token}`],
      );
    });
  }

  test(`more than ${maxConsecutiveTurnContinuations} continues in a row end the turn with an error`, async () => {
    const token = newToken();
    const sessionId = `continuation-cap-${token}`;
    const client = sessionsOf("decide");
    await client.startSession(sessionId, `stubborn-draft ${token}`);
    const state = await client.waitForCompletedTurns(sessionId, 1);
    const limitError = buildTurnContinuationLimitError(maxConsecutiveTurnContinuations);
    assert.equal(state.lastTurnError, limitError);

    const modelCalls = maxConsecutiveTurnContinuations + 1;
    const snapshot = await client.getAgentSnapshot(sessionId);
    assert.equal(snapshot.usage.modelCalls, modelCalls);
    assert.equal(requestsOf("decide", sessionId).length, modelCalls);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.role} ${message.text}`),
      [
        `user stubborn-draft ${token}`,
        ...Array.from({ length: modelCalls }, (_unused, index) => [
          `assistant draft ${index + 1} for ${token}`,
          // The answer that crosses the limit gets no continuation message.
          ...(index + 1 < modelCalls ? ["user [continue] after draft " + String(index + 1)] : []),
        ]).flat(),
      ],
    );
    const events = await client.listAgentEvents(sessionId);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_failed").map((event) => JSON.parse(event.payload)),
      [{ error: limitError }],
    );
    assert.equal(events.some((event) => event.type === "turn_completed"), false);

    // The count belongs to one turn, so the next turn continues again.
    await client.sendUserMessage(sessionId, `revise again-${token}`);
    const secondTurn = await client.waitForCompletedTurns(sessionId, 2);
    assert.equal(secondTurn.lastTurnError, null);
    assert.equal((await client.getAgentSnapshot(sessionId)).messages.at(-1)?.text, `final for again-${token}`);
  });
});

/**
 * `end` after `submit_decision`, a continuation after a draft or a `continue-after-tools` lookup, `default` otherwise.
 * A model output or tool result that mentions `hook-fail-once-<token>` throws on its first evaluation.
 */
function decideTestContinuation(context: TurnContinuationContext, failedHookKeys: Set<string>): TurnContinuation {
  const answer = context.modelOutput.map((item) => item.text ?? "").join("");
  const seen = [answer, ...context.toolResults.map((result) => result.output)].join(" ");
  const failOnce = /hook-fail-once-\w+/.exec(seen)?.[0];
  if (failOnce !== undefined) {
    const key = `${context.flowId}/${context.turnId}/${failOnce}`;
    if (!failedHookKeys.has(key)) {
      failedHookKeys.add(key);
      throw new Error(`scripted hook failure for ${failOnce}`);
    }
  }
  if (context.toolResults.some((result) => result.toolName === "submit_decision")) {
    return "end";
  }
  if (answer.includes("bare-continue")) {
    // The string form a release candidate accepted; a continuation now needs a message.
    return "continue" as unknown as TurnContinuation;
  }
  if (answer.includes("blank-continue")) {
    return { action: "continue", message: " " };
  }
  if (context.toolResults.some((result) => result.output.includes("continue-after-tools"))) {
    return { action: "continue", message: "[continue] summarize the lookup" };
  }
  if (context.toolResults.length === 0 && answer.startsWith("draft")) {
    return { action: "continue", message: continuationMessageAfter(answer) };
  }
  return "default";
}

/** `[continue] after <first two words of the answer>`, such as `[continue] after draft 3`. */
function continuationMessageAfter(answer: string): string {
  return `[continue] after ${answer.split(" ").slice(0, 2).join(" ")}`;
}

/**
 * The scripted model reads the last user text that is not a `[continue]` message, and counts the continuation
 * messages after it:
 * - `submit <id> [gate]`: a `submit_decision` call, then `submitted`.
 * - `lookup <id>`: a `lookup_order` call, then `lookup: <result>`.
 * - `revise <id> [gate]`: `draft for <id>`, after waiting at the gate; once continued, `final for <id>`.
 * - `draft-then-lookup <id>`: `draft for <id>`; once continued, a `lookup_order` call, then `lookup: <result>`.
 * - `draft-then-stall <id> <gate>`: `draft for <id>`; once continued, `final for <id>` after waiting at the gate.
 * - `lookup-then-draft <id> <gate>`: a `lookup_order` call, then `draft for <id>`; once continued, `final for <id>`
 *   after waiting at the gate. `lookup-then-draft-fail <id>` throws instead of the final answer.
 * - `stubborn-draft <id>`: `draft <n> for <id>` on every call.
 * - `say <text>`: `said <text>`.
 */
async function respond(markerDirectory: string, request: ModelRequest): Promise<ScriptedModelReply> {
  const userTexts = ScriptedModelClient.userTexts(request);
  let commandIndex = userTexts.length - 1;
  while (commandIndex > 0 && userTexts[commandIndex]?.startsWith("[continue]") === true) {
    commandIndex -= 1;
  }
  const userText = userTexts[commandIndex] ?? "";
  const continuations = userTexts.length - 1 - commandIndex;
  const toolResults = readToolResultsSinceCommand(request);
  const [command, id = "", gate] = userText.split(" ");
  switch (command) {
    case "submit":
      return toolResults.length > 0
        ? { text: "submitted" }
        : { toolCalls: [{ toolName: "submit_decision", arguments: gate === undefined ? { id } : { id, gate } }] };
    case "lookup":
      return toolResults.length > 0
        ? { text: `lookup: ${toolResults[0]?.output}` }
        : { toolCalls: [{ toolName: "lookup_order", arguments: { id } }] };
    case "revise":
      if (continuations > 0) {
        return { text: `final for ${id}` };
      }
      if (gate !== undefined) {
        await waitAtGate(markerDirectory, gate, request.cancellationSignal);
      }
      return { text: `draft for ${id}` };
    case "draft-then-lookup":
      if (toolResults.length > 0) {
        return { text: `lookup: ${toolResults[0]?.output}` };
      }
      return continuations === 0
        ? { text: `draft for ${id}` }
        : { toolCalls: [{ toolName: "lookup_order", arguments: { id } }] };
    case "draft-then-stall":
    case "lookup-then-draft":
    case "lookup-then-draft-fail":
      if (continuations > 0) {
        if (command === "lookup-then-draft-fail") {
          throw new Error(`scripted model failure for ${id}`);
        }
        await waitAtGate(markerDirectory, gate ?? "", request.cancellationSignal);
        return { text: `final for ${id}` };
      }
      if (command !== "draft-then-stall" && toolResults.length === 0) {
        return { toolCalls: [{ toolName: "lookup_order", arguments: { id } }] };
      }
      return { text: `draft for ${id}` };
    case "stubborn-draft":
      return { text: `draft ${continuations + 1} for ${id}` };
    case "say":
      return { text: `said ${id}` };
    default:
      return { text: `echo: ${userText}` };
  }
}

/** The tool results after the last user text that is not a `[continue]` message. */
function readToolResultsSinceCommand(request: ModelRequest): ModelToolResultItem[] {
  let results: ModelToolResultItem[] = [];
  for (const item of request.inputItems) {
    if (item.type === "user_text" && !item.text.startsWith("[continue]")) {
      results = [];
    } else if (item.type === "tool_result") {
      results.push(item);
    }
  }
  return results;
}

async function waitAtGate(markerDirectory: string, gate: string, cancellationSignal: AbortSignal): Promise<void> {
  const fileGate = new FileGate(markerDirectory, gate);
  fileGate.markReached();
  await fileGate.waitForRelease(cancellationSignal);
}

function describeMessage(message: AgentMessage): string {
  const detail = message.text ?? [message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}

async function collectChunks<T>(chunks: AsyncIterable<T>): Promise<T[]> {
  const collected: T[] = [];
  for await (const chunk of chunks) {
    collected.push(chunk);
  }
  return collected;
}

async function readFinalMessage(chunks: readonly UIMessageChunk[]): Promise<UIMessage | undefined> {
  let message: UIMessage | undefined;
  for await (const update of readUIMessageStream({ stream: ReadableStream.from(chunks) })) {
    message = update;
  }
  return message;
}

function newToken(): string {
  return randomUUID().slice(0, 8);
}
