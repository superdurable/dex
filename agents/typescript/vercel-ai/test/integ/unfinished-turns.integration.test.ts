// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, test } from "node:test";

import { tool, type ModelMessage } from "ai";
import { z } from "zod";

import {
  outcomeUnknownToolResult,
  toolCallCancelledBySteeringResult,
  turnCancelledNoteHeader,
  turnSteeredNoteHeader,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentMessage,
  type AgentSnapshot,
  type AgentToolCall,
  type AgentTurnContext,
  type FrameworkMessage,
} from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  pollUntil,
} from "@superdurable/dex-agents/testing";

import { createScriptedLanguageModel, readRecordedModelPrompts } from "../fixtures/scripted-language-model.js";
import { createScriptedVercelAiAgentFlow, scriptedPrivateFieldValue } from "../fixtures/scripted-vercel-ai-agent.js";
import { createVercelAiAgentSessionFlow, toolCallOutputOmittedResult, type AgentSessionFlow } from "../../src/index.js";

// The built-package smoke test imports only the package entry, which does not export the runner class.
type VercelAiAgentRunner = Required<Pick<AgentFrameworkRunner, "runModelStep" | "buildUnfinishedTurnMessages">>;

describe("Vercel AI SDK agent turns store their messages as they run and when they end early", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let flow: AgentSessionFlow;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    flow = createScriptedVercelAiAgentFlow(markerDirectory, { flowType: "ScriptedVercelAiAgentUnfinishedTurns" });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a deferred call stores the turn so far, and the completed turn stores no message twice", async () => {
    const sessionId = newSessionId("stored-at-deferral");
    await sessions.startSession(sessionId, "call get_weather then send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");

    const waiting = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(waiting.messages.map(describeMessage), turnOneUntilApproval);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const completed = await waitForTurns(sessionId, 1);
    assert.deepEqual(completed.messages.map(describeMessage), [
      ...turnOneUntilApproval,
      "5 turn-1 tool tool-result call-1-send_email send_email " +
        `{"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}`,
      "6 turn-1 assistant get_weather -> sunny in Paris | send_email -> " +
        `{"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}`,
    ]);
    assertNoMessageStoredTwice(completed.messages);
    assert.equal(completed.state?.lastTurnError, null);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email attempt 1`]);
  });

  test("an invalid call's error waits for the valid call's result, so the response keeps one tool message", async () => {
    const approvedId = newSessionId("mixed-approved");
    const steeredId = newSessionId("mixed-steered");
    for (const sessionId of [approvedId, steeredId]) {
      await sessions.startSession(sessionId, "call send_email and no_such_tool");
      await sessions.waitForPendingInterrupt(sessionId, "send_email");
      const waiting = await sessions.getAgentSnapshot(sessionId);
      assert.deepEqual(waiting.messages.map(describeMessage), [
        "1 turn-1 user call send_email and no_such_tool",
        "2 turn-1 assistant tool-call,tool-call",
      ]);
      const [frameworkRunState] = await environment.client.getAttributes(sessionId, flow.frameworkRunState);
      const runState = JSON.parse(frameworkRunState ?? "null") as { turnMessages: ModelMessage[] };
      assert.deepEqual(runState.turnMessages.map(toolResultsOf), [[["no_such_tool", "error-text"]]]);
    }

    const interrupt = await sessions.waitForPendingInterrupt(approvedId, "send_email");
    assert.equal(await sessions.resolveInterrupt(approvedId, interrupt.interruptId, true), true);
    const approved = await waitForTurns(approvedId, 1);
    assert.deepEqual(
      approved.messages.map((message) => message.role),
      ["user", "assistant", "tool", "assistant"],
    );
    assert.deepEqual(toolResultsOf(readFrameworkItem(approved.messages[2])), [
      ["send_email", "json"],
      ["no_such_tool", "error-text"],
    ]);
    assertNoMessageStoredTwice(approved.messages);

    assert.equal(await sessions.steerUserMessage(steeredId, "never mind"), true);
    const steered = await waitForTurns(steeredId, 2);
    assert.deepEqual(
      steered.messages.map((message) => [message.turnId, message.role]),
      [
        ["turn-1", "user"],
        ["turn-1", "assistant"],
        ["turn-1", "tool"],
        ["turn-2", "user"],
        ["turn-2", "assistant"],
      ],
    );
    assert.deepEqual(toolResultsOf(readFrameworkItem(steered.messages[2])), [
      ["send_email", "execution-denied"],
      ["no_such_tool", "error-text"],
    ]);
    assertNoNote(steered.messages);
    assertNoMessageStoredTwice(steered.messages);
    const prompt = readLastPrompt(steeredId, "turn-2");
    assert.deepEqual(
      prompt.map((message) => message.role),
      ["system", "user", "assistant", "tool", "user"],
    );
    assert.deepEqual(
      readToolResultsOfLastPrompt(steeredId, "turn-2").map(([toolCallId, outputType]) => [toolCallId, outputType]),
      [
        ["call-1-send_email", "execution-denied"],
        ["call-1-no_such_tool", "error-text"],
      ],
    );
    assert.deepEqual(toolLog("send_email", approvedId), [`${approvedId} call-1-send_email attempt 1`]);
    assert.deepEqual(toolLog("send_email", steeredId), []);
  });

  test("a turn steered at an approval stores the call's steering result instead of a note", async () => {
    const sessionId = newSessionId("steered-approval");
    await sessions.startSession(sessionId, "call get_weather then send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await sessions.steerUserMessage(sessionId, "never mind"), true);
    const snapshot = await waitForTurns(sessionId, 2);

    assert.deepEqual(snapshot.messages.map(describeMessage), [
      ...turnOneUntilApproval,
      `5 turn-1 tool tool-result call-1-send_email send_email ${toolCallCancelledBySteeringResult}`,
      "6 turn-2 user never mind",
      "7 turn-2 assistant echo: never mind",
    ]);
    assert.deepEqual(readFrameworkItem(snapshot.messages[4]).content, [
      {
        type: "tool-result",
        toolCallId: "call-1-send_email",
        toolName: "send_email",
        output: { type: "execution-denied", reason: toolCallCancelledBySteeringResult },
      },
    ]);
    assertNoNote(snapshot.messages);
    assertNoMessageStoredTwice(snapshot.messages);
    assert.deepEqual(toolLog("send_email", sessionId), []);
    assert.deepEqual(readToolResultsOfLastPrompt(sessionId, "turn-2"), [
      ["call-1-get_weather", "text", "sunny in Paris"],
      ["call-1-send_email", "execution-denied", toolCallCancelledBySteeringResult],
    ]);
  });

  test("a turn steered while a write runs stores the write's result, and the write runs once", async () => {
    const sessionId = newSessionId("steered-write");
    await sessions.startSession(sessionId, "call get_weather then charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const gate = new FileGate(markerDirectory, "charge_card");
    await gate.waitUntilReached();

    assert.equal(await sessions.steerUserMessage(sessionId, "use the other card"), true);
    gate.release();
    const snapshot = await waitForTurns(sessionId, 2);

    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather then charge_card",
      "2 turn-1 assistant tool-call call-1-get_weather get_weather",
      "3 turn-1 tool tool-result call-1-get_weather get_weather sunny in Paris",
      "4 turn-1 assistant tool-call call-1-charge_card charge_card",
      "5 turn-1 tool tool-result call-1-charge_card charge_card card charged",
      "6 turn-2 user use the other card",
      "7 turn-2 assistant echo: use the other card",
    ]);
    assertNoNote(snapshot.messages);
    assertNoMessageStoredTwice(snapshot.messages);
    assert.deepEqual(toolLog("charge_card", sessionId), [`${sessionId} call-1-charge_card attempt 1`]);
  });

  test("a turn cancelled during its resumed model call stores the tool results, and the next turn resumes", async () => {
    const sessionId = newSessionId("cancelled-model-call");
    await sessions.startSession(sessionId, "call lookup_inventory then pause");
    await pollUntil(() => (toolLog("turn", sessionId).length > 0 ? true : undefined), {
      description: `the resumed model call of ${sessionId} waiting at the turn gate`,
    });

    assert.equal(await sessions.cancelTurn(sessionId), true);
    const cancelled = await waitForTurns(sessionId, 1);
    assert.deepEqual(cancelled.messages.map(describeMessage), [
      "1 turn-1 user call lookup_inventory then pause",
      "2 turn-1 assistant tool-call call-1-lookup_inventory lookup_inventory",
      '3 turn-1 tool tool-result call-1-lookup_inventory lookup_inventory {"in_stock":3}',
    ]);
    // The tool has no toModelOutput, so the stored result is what the resumed model call would have sent.
    assert.deepEqual(readFrameworkItem(cancelled.messages[2]).content, [
      {
        type: "tool-result",
        toolCallId: "call-1-lookup_inventory",
        toolName: "lookup_inventory",
        output: { type: "json", value: { in_stock: 3 } },
      },
    ]);
    assertNoNote(cancelled.messages);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const next = await waitForTurns(sessionId, 2);
    assert.equal(next.state?.lastTurnError, null);
    assert.equal(next.messages.at(-1)?.text, "history | user: call lookup_inventory then pause");
    assertNoMessageStoredTwice(next.messages);
    assert.deepEqual(readToolResultsOfLastPrompt(sessionId, "turn-2"), [
      ["call-1-lookup_inventory", "json", '{"in_stock":3}'],
    ]);
    assert.deepEqual(toolLog("lookup_inventory", sessionId), [`${sessionId} call-1-lookup_inventory attempt 1`]);
  });

  test("the next model call answers a call that a Worker without buildUnfinishedTurnMessages left open", async () => {
    const sessionId = newSessionId("older-worker-end");
    await sessions.startSession(sessionId, "call send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");

    const ended = await cancelTurnWithoutUnfinishedTurnMessages(sessionId, 1);
    // Such a Worker stores only the core's note, so the stored call keeps no result.
    assert.deepEqual(
      ended.messages.map((message) => [message.sequence, message.role, message.toolName]),
      [
        [1, "user", null],
        [2, "assistant", "send_email"],
        [3, "user", null],
      ],
    );
    assert.ok(ended.messages[2]?.text?.startsWith(turnCancelledNoteHeader));

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const next = await waitForTurns(sessionId, 2);
    assert.equal(next.state?.lastTurnError, null);
    assert.equal(next.messages.length, 5);
    const prompt = readLastPrompt(sessionId, "turn-2");
    assert.deepEqual(
      prompt.map((message) => message.role),
      ["system", "user", "assistant", "tool", "user", "user"],
    );
    assert.deepEqual(readToolResultsOfLastPrompt(sessionId, "turn-2"), [
      ["call-1-send_email", "error-text", outcomeUnknownToolResult],
    ]);
    assert.deepEqual(toolLog("send_email", sessionId), []);
  });

  test("a run state of an earlier adapter resumes and ends the turn without storing a message twice", async () => {
    const sessionId = newSessionId("legacy-run-state");
    await sessions.startSession(sessionId, "call get_weather then send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    const sendEmailRecord = waiting.toolCalls.find((toolCall) => toolCall.toolName === "send_email");
    assert.ok(sendEmailRecord !== undefined);
    const rejected: AgentToolCall = { ...sendEmailRecord, status: "rejected", rejectionMessage: "not now" };
    const runner = flow.runner as VercelAiAgentRunner;
    const [userMessage, ...storedTurnMessages] = waiting.messages;
    assert.ok(userMessage !== undefined);
    const [committedRunState] = await environment.client.getAttributes(sessionId, flow.frameworkRunState);
    // Version 2 makes an adapter before 0.6.12 refuse the run state instead of misreading it.
    assert.equal((JSON.parse(committedRunState ?? "null") as { version: number }).version, 2);
    const runState = (version: number, turnMessages: readonly AgentMessage[]): string =>
      JSON.stringify({
        version,
        turnMessages: turnMessages.map((message) => JSON.parse(message.frameworkItem) as unknown),
        requestedToolCallIds: ["call-1-send_email"],
        modelSteps: [],
      });
    // This adapter stored the turn so far; an earlier one kept the same messages in a version 1 run state.
    const storedTurn = (flowId: string): AgentTurnContext =>
      resumedTurnContext(flowId, waiting.messages, runState(2, []), [rejected]);
    const legacyTurn = (flowId: string): AgentTurnContext =>
      resumedTurnContext(flowId, [userMessage], runState(1, storedTurnMessages), [rejected]);

    const storedEnd = runner.buildUnfinishedTurnMessages(storedTurn(sessionId));
    const legacyEnd = runner.buildUnfinishedTurnMessages(legacyTurn(sessionId));
    assert.deepEqual(storedEnd.map(describeFrameworkMessage), [
      "tool tool-result call-1-send_email send_email not now",
    ]);
    assert.deepEqual(frameworkItems(legacyEnd), frameworkItems([...storedTurnMessages, ...storedEnd]));
    // The same committed state always gives the same messages.
    assert.deepEqual(runner.buildUnfinishedTurnMessages(legacyTurn(sessionId)), legacyEnd);

    const storedResume = await runner.runModelStep(storedTurn(`${sessionId}-stored`));
    const legacyResume = await runner.runModelStep(legacyTurn(`${sessionId}-legacy`));
    assert.equal(storedResume.kind, "completed");
    assert.equal(legacyResume.kind, "completed");
    assert.deepEqual(storedResume.newMessages.map(describeFrameworkMessage), [
      "tool tool-result call-1-send_email send_email not now",
      "assistant get_weather -> sunny in Paris | send_email -> denied: not now",
    ]);
    assert.deepEqual(
      frameworkItems(legacyResume.newMessages),
      frameworkItems([...storedTurnMessages, ...storedResume.newMessages]),
    );
  });

  test("a steered turn keeps the converted result that the model read, and the hook gives the same messages twice", async () => {
    const sessionId = newSessionId("model-output-steered");
    await sessions.startSession(sessionId, "call read_profile then send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    // The model call that read the result stored it with its converted output.
    const modelOutput = readFrameworkItemText(waiting.messages[2]);
    assert.match(String(modelOutput), /^visible [0-9a-f-]{36}$/);
    const [frameworkRunState] = await environment.client.getAttributes(sessionId, flow.frameworkRunState);
    const readProfileRecord = await sessions.readToolCall(sessionId, "call-1-read_profile");
    const sendEmailRecord = await sessions.readToolCall(sessionId, "call-1-send_email");
    assert.ok(frameworkRunState !== undefined && readProfileRecord !== undefined && sendEmailRecord !== undefined);
    const records = [
      readProfileRecord,
      { ...sendEmailRecord, status: "rejected", rejectionMessage: toolCallCancelledBySteeringResult },
    ] as unknown as AgentToolCall[];
    const runner = flow.runner as VercelAiAgentRunner;
    const endedTurn = (): AgentTurnContext =>
      resumedTurnContext(sessionId, waiting.messages, frameworkRunState, records);
    const conversions = toolLog("read_profile-model-output", sessionId).length;
    // The hook runs no application code, so the same committed state gives the same messages.
    assert.deepEqual(runner.buildUnfinishedTurnMessages(endedTurn()), runner.buildUnfinishedTurnMessages(endedTurn()));
    assert.equal(toolLog("read_profile-model-output", sessionId).length, conversions);
    // Only a call without a succeeded final record gets the unknown outcome.
    const withoutFinalRecord = runner.buildUnfinishedTurnMessages(
      resumedTurnContext(sessionId, waiting.messages, frameworkRunState, [readProfileRecord as unknown as AgentToolCall]),
    );
    assert.deepEqual(withoutFinalRecord.map(describeFrameworkMessage), [
      `tool tool-result call-1-send_email send_email ${outcomeUnknownToolResult}`,
    ]);

    assert.equal(await sessions.steerUserMessage(sessionId, "never mind"), true);
    const snapshot = await waitForTurns(sessionId, 2);
    assert.equal(readFrameworkItemText(snapshot.messages[2]), modelOutput);
    assertNoPrivateField(sessionId, snapshot.messages);
  });

  test("a turn cancelled before the model read a toModelOutput result stores the omitted-output success, never the raw output", async () => {
    const sessionId = newSessionId("model-output-cancelled");
    await sessions.startSession(sessionId, "call read_profile then pause");
    await pollUntil(() => (toolLog("turn", sessionId).length > 0 ? true : undefined), {
      description: `the resumed model call of ${sessionId} waiting at the turn gate`,
    });

    assert.equal(await sessions.cancelTurn(sessionId), true);
    const cancelled = await waitForTurns(sessionId, 1);
    assert.deepEqual(cancelled.messages.map(describeMessage), [
      "1 turn-1 user call read_profile then pause",
      "2 turn-1 assistant tool-call call-1-read_profile read_profile",
      `3 turn-1 tool tool-result call-1-read_profile read_profile ${toolCallOutputOmittedResult}`,
    ]);
    assert.deepEqual(readFrameworkItem(cancelled.messages[2]).content, [
      {
        type: "tool-result",
        toolCallId: "call-1-read_profile",
        toolName: "read_profile",
        output: { type: "text", value: toolCallOutputOmittedResult },
      },
    ]);
    assertNoNote(cancelled.messages);
    assertNoPrivateField(sessionId, cancelled.messages);
  });

  test("a failed turn stores the omitted-output success for an unread toModelOutput result before the core's note", async () => {
    const sessionId = newSessionId("model-output-failed");
    await sessions.startSession(sessionId, "call read_profile then fail");

    const failed = await waitForTurns(sessionId, 1);
    assert.match(failed.state?.lastTurnError ?? "", /scripted model failure/);
    assert.deepEqual(
      failed.messages.map((message) => message.role),
      ["user", "assistant", "tool", "user"],
    );
    assert.equal(failed.messages[2]?.toolOutput, toolCallOutputOmittedResult);
    // The core's failure note after it still lists the raw output; that is tracked separately.
    assert.equal(failed.messages[2]?.frameworkItem.includes(scriptedPrivateFieldValue), false);
  });

  function assertNoPrivateField(sessionId: string, messages: readonly AgentMessage[]): void {
    assert.equal(JSON.stringify(messages).includes(scriptedPrivateFieldValue), false);
    const prompts = readRecordedModelPrompts(markerDirectory, sessionId);
    assert.ok(prompts.length > 0);
    assert.equal(JSON.stringify(prompts).includes(scriptedPrivateFieldValue), false);
  }

  async function waitForTurns(sessionId: string, turnCount: number): Promise<AgentSnapshot> {
    await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 120_000 });
    return sessions.getAgentSnapshot(sessionId);
  }

  /** Cancels the turn while the runner lacks the method, as adapter 0.6.11 does; needs the in-process Worker. */
  async function cancelTurnWithoutUnfinishedTurnMessages(sessionId: string, turnCount: number): Promise<AgentSnapshot> {
    const runner = flow.runner as { buildUnfinishedTurnMessages?: unknown };
    runner.buildUnfinishedTurnMessages = undefined;
    try {
      assert.equal(await sessions.cancelTurn(sessionId), true);
      return await waitForTurns(sessionId, turnCount);
    } finally {
      delete runner.buildUnfinishedTurnMessages;
    }
  }

  function toolLog(logName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${logName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }

  function readLastPrompt(sessionId: string, turnId: string): PromptMessage[] {
    const prompts = readRecordedModelPrompts(markerDirectory, sessionId).filter((prompt) => prompt.turnId === turnId);
    const prompt = prompts.at(-1)?.prompt;
    assert.ok(Array.isArray(prompt), `no model call of ${turnId} in ${sessionId}`);
    return prompt as PromptMessage[];
  }

  function readToolResultsOfLastPrompt(sessionId: string, turnId: string): string[][] {
    return readLastPrompt(sessionId, turnId).flatMap((message) =>
      message.role === "tool"
        ? message.content.map((part) => [
            part.toolCallId ?? "",
            part.output?.type ?? "",
            describePromptOutputValue(part.output?.value ?? part.output?.reason ?? ""),
          ])
        : [],
    );
  }
});

describe("a Vercel AI SDK tool result near the server's value limit", () => {
  const maxValueBytes = 65_536;
  const rawBody = "b".repeat(32_768);
  const summary = "s".repeat(20_480);
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const stateDirectory = mkdtempSync(join(tmpdir(), "vercel-ai-value-limit-"));

  before(async () => {
    const configPath = join(stateDirectory, "zd-overrides.yaml");
    writeFileSync(configPath, `dev:\n  durabilityMode: processCrashSafe\nvalues:\n  maxValueBytes: ${maxValueBytes}\n`);
    engine = await PrivateDexEngine.start({ stateDirectory, serverZdConfigPath: configPath });
    const markerDirectory = mkdtempSync(join(stateDirectory, "markers-"));
    const flow = createVercelAiAgentSessionFlow(
      {
        model: createScriptedLanguageModel(markerDirectory),
        tools: {
          read_report: tool({
            description: "Read a large report",
            inputSchema: z.object({ requested_by: z.string() }),
            execute: async () => ({ body: rawBody, summary }),
            toModelOutput: ({ output }) => ({ type: "text", value: output.summary }),
          }),
        },
        toolPolicies: { read_report: { effect: "read" } },
      },
      "ScriptedVercelAiAgentNearValueLimit",
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
    // A supplied state directory outlives the engine.
    rmSync(stateDirectory, { recursive: true, force: true });
  });

  test("a raw output and its converted result that each fit, but not together, keep the call succeeded", async () => {
    const sessionId = newSessionId("near-value-limit");
    await sessions.startSession(sessionId, "call read_report");
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    const rawOutputBytes = Buffer.byteLength(JSON.stringify({ body: rawBody, summary }));
    assert.ok(rawOutputBytes < maxValueBytes && rawOutputBytes + summary.length > maxValueBytes);
    assert.deepEqual(
      snapshot.messages.map((message) => message.role),
      ["user", "assistant", "tool", "assistant"],
    );
    assert.equal(readFrameworkItemText(snapshot.messages[2]), summary);
    assert.equal(snapshot.messages[3]?.text, `read_report -> ${summary}`);
  });
});

const turnOneUntilApproval = [
  "1 turn-1 user call get_weather then send_email",
  "2 turn-1 assistant tool-call call-1-get_weather get_weather",
  "3 turn-1 tool tool-result call-1-get_weather get_weather sunny in Paris",
  "4 turn-1 assistant tool-call call-1-send_email send_email",
];

interface PromptMessage {
  readonly role: string;
  readonly content: readonly {
    readonly toolCallId?: string;
    readonly output?: { readonly type: string; readonly value?: unknown; readonly reason?: string };
  }[];
}

function resumedTurnContext(
  flowId: string,
  messages: readonly AgentMessage[],
  frameworkRunState: string,
  toolCalls: readonly AgentToolCall[],
): AgentTurnContext {
  const events: AgentEventWriter = {
    writeEvent: () => undefined,
    writeTextDelta: () => undefined,
    writeReasoningDelta: () => undefined,
  } as unknown as AgentEventWriter;
  return {
    flowId,
    turnId: "turn-1",
    attempt: 1,
    messages,
    frameworkRunState,
    isResuming: true,
    toolCalls,
    events,
    cancellationSignal: new AbortController().signal,
    deadline: new Date(Date.now() + 60_000),
    resolveToolPolicy: () => ({ effect: "read", requiresApproval: false }),
    recordedToolCall: (toolCallId: string) => toolCalls.find((toolCall) => toolCall.toolCallId === toolCallId),
    heartbeat: () => Promise.resolve(),
  } as unknown as AgentTurnContext;
}

function describePromptOutputValue(value: unknown): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function readFrameworkItem(message: AgentMessage | undefined): ModelMessage {
  assert.ok(message !== undefined, "message is missing");
  return JSON.parse(message.frameworkItem) as ModelMessage;
}

/** The text of a stored tool message's only result. */
function readFrameworkItemText(message: AgentMessage | undefined): unknown {
  const item = readFrameworkItem(message);
  const [part] = item.role === "tool" ? item.content : [];
  return part?.type === "tool-result" && part.output.type === "text" ? part.output.value : undefined;
}

function toolResultsOf(message: ModelMessage): string[][] {
  return message.role === "tool"
    ? message.content.flatMap((part) => (part.type === "tool-result" ? [[part.toolName, part.output.type]] : []))
    : [];
}

function frameworkItems(messages: readonly (AgentMessage | FrameworkMessage)[]): string[] {
  return messages.map((message) => message.frameworkItem);
}

function assertNoNote(messages: readonly AgentMessage[]): void {
  const notes = messages.filter(
    (message) =>
      message.text?.startsWith(turnSteeredNoteHeader) === true ||
      message.text?.startsWith(turnCancelledNoteHeader) === true,
  );
  assert.deepEqual(notes, []);
}

function assertNoMessageStoredTwice(messages: readonly AgentMessage[]): void {
  const toolCallIds = messages.flatMap((message) => {
    const item = readFrameworkItem(message);
    return Array.isArray(item.content)
      ? item.content.flatMap((part) =>
          part.type === "tool-call" || part.type === "tool-result" ? [`${part.type} ${part.toolCallId}`] : [],
        )
      : [];
  });
  assert.deepEqual(toolCallIds, [...new Set(toolCallIds)]);
}

function describeMessage(message: AgentMessage): string {
  return `${message.sequence} ${message.turnId} ${describeFrameworkMessage(message)}`;
}

function describeFrameworkMessage(message: AgentMessage | FrameworkMessage): string {
  const item = JSON.parse(message.frameworkItem) as ModelMessage;
  const partType =
    Array.isArray(item.content) && message.text === null ? item.content.map((part) => part.type).join(",") : null;
  const detail =
    message.text ??
    [partType, message.toolCallId, message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.role} ${detail}`;
}
