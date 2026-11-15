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

import type { MastraDBMessage } from "@mastra/core/agent/message-list";
import {
  outcomeUnknownToolResult,
  toolCallCancelledBySteeringResult,
  turnCancelledNoteHeader,
  turnSteeredNoteHeader,
  type AgentMessage,
  type AgentSessionFlow,
  type AgentSnapshot,
  type AgentToolCall,
  type AgentTurnContext,
} from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  InvocationLog,
  PrivateDexEngine,
  pollUntil,
} from "@superdurable/dex-agents/testing";

import { readRecordedModelPrompts } from "../fixtures/scripted-language-model.js";
import { createScriptedMastraAgentFlow } from "../fixtures/scripted-mastra-agent.js";
import type { MastraAgentRunner } from "../../src/index.js";

const weatherOutput = '{"forecast":"sunny in Paris"}';

describe("Mastra agent turns that end early store their messages", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let flow: AgentSessionFlow;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    flow = createScriptedMastraAgentFlow(markerDirectory, { flowType: "ScriptedMastraAgentUnfinishedTurns" });
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

  test("a turn steered at an approval stores every call's result instead of a note", async () => {
    const sessionId = newSessionId("steered-approval");
    await sessions.startSession(sessionId, "call get_weather then send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await sessions.steerUserMessage(sessionId, "steered: never mind"), true);
    const snapshot = await waitForTurns(sessionId, 2);

    assert.deepEqual(
      snapshot.messages.map((message) => [message.sequence, message.turnId, message.role, message.text]),
      [
        [1, "turn-1", "user", "call get_weather then send_email"],
        [2, "turn-1", "assistant", null],
        [3, "turn-2", "user", "steered: never mind"],
        [4, "turn-2", "assistant", "echo: steered: never mind"],
      ],
    );
    assert.deepEqual(toolInvocations(snapshot.messages[1]), [
      ["call-1-get_weather", "get_weather", "result", weatherOutput],
      ["call-1-send_email", "send_email", "result", toolCallCancelledBySteeringResult],
    ]);
    assertNoNote(snapshot.messages);
    assert.deepEqual(toolLog("send_email", sessionId), []);
    assert.deepEqual(toolResultsOfPrompt("steered: never mind"), [
      ["call-1-get_weather", weatherOutput],
      ["call-1-send_email", toolCallCancelledBySteeringResult],
    ]);
  });

  test("a turn cancelled during its resumed model call stores the tool results, and the next turn resumes", async () => {
    const sessionId = newSessionId("cancelled-model-call");
    await sessions.startSession(sessionId, "call get_weather then pause");
    await pollUntil(
      () => (new InvocationLog(join(markerDirectory, "turn.log")).names().length > 0 ? true : undefined),
      { description: `the resumed model call of ${sessionId} waiting at the turn gate` },
    );

    assert.equal(await sessions.cancelTurn(sessionId), true);
    const cancelled = await waitForTurns(sessionId, 1);
    assert.deepEqual(
      cancelled.messages.map((message) => [message.sequence, message.role, message.toolName, message.toolOutput]),
      [
        [1, "user", null, null],
        [2, "assistant", "get_weather", weatherOutput],
      ],
    );
    assert.deepEqual(toolInvocations(cancelled.messages[1]), [
      ["call-1-get_weather", "get_weather", "result", weatherOutput],
    ]);
    assertNoNote(cancelled.messages);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const next = await waitForTurns(sessionId, 2);
    assert.equal(next.state?.lastTurnError, null);
    assert.equal(next.messages.at(-1)?.text, "history | user: call get_weather then pause");
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call-1-get_weather`]);
  });

  test("the same committed state gives the same messages, and a call without a final record gets the unknown outcome", async () => {
    const sessionId = newSessionId("deterministic");
    await sessions.startSession(sessionId, "call get_weather then send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    const [frameworkRunState] = await environment.client.getAttributes(sessionId, flow.frameworkRunState);
    assert.ok(frameworkRunState !== undefined && frameworkRunState !== null);
    const records = waiting.toolCalls as unknown as readonly AgentToolCall[];
    const finalRecords = records.filter((record) => record.status === "succeeded");
    const runner = flow.runner as MastraAgentRunner;

    const endedTurn = (): AgentTurnContext => endedTurnContext(waiting.messages, frameworkRunState, finalRecords);
    const messages = runner.buildUnfinishedTurnMessages(endedTurn());
    assert.deepEqual(runner.buildUnfinishedTurnMessages(endedTurn()), messages);
    assert.equal(messages.length, 1);
    const stored = JSON.parse(messages[0]?.frameworkItem ?? "null") as MastraDBMessage;
    assert.deepEqual(
      stored.content.parts.flatMap((part) =>
        part.type === "tool-invocation"
          ? [[part.toolInvocation.toolCallId, part.toolInvocation.state, readResult(part.toolInvocation)]]
          : [],
      ),
      [
        ["call-1-get_weather", "result", weatherOutput],
        ["call-1-send_email", "result", outcomeUnknownToolResult],
      ],
    );
    // Mastra stamps its merge time on a part it answers; the part this method answers carries none.
    const sendEmailPart = stored.content.parts.find(
      (part) => part.type === "tool-invocation" && part.toolInvocation.toolCallId === "call-1-send_email",
    );
    assert.equal(sendEmailPart !== undefined && "updatedAt" in sendEmailPart, false);
  });

  async function waitForTurns(sessionId: string, turnCount: number): Promise<AgentSnapshot> {
    await sessions.waitForCompletedTurns(sessionId, turnCount, { timeoutMs: 120_000 });
    return sessions.getAgentSnapshot(sessionId);
  }

  function toolLog(logName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${logName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }

  /** The tool results of the model call whose last user text is `userText`, in prompt order. */
  function toolResultsOfPrompt(userText: string): string[][] {
    const prompts = readRecordedModelPrompts(markerDirectory)
      .map((recorded) => recorded.prompt as PromptMessage[])
      .filter((prompt) => {
        const lastUser = prompt.filter((message) => message.role === "user").at(-1);
        return lastUser?.content.some((part) => part.text === userText) === true;
      });
    const prompt = prompts.at(-1);
    assert.ok(prompt !== undefined, `no model call for ${userText}`);
    return prompt.flatMap((message) =>
      message.role === "tool"
        ? message.content.map((part) => [part.toolCallId ?? "", String(part.output?.value ?? "")])
        : [],
    );
  }
});

interface PromptMessage {
  readonly role: string;
  readonly content: readonly {
    readonly text?: string;
    readonly toolCallId?: string;
    readonly output?: { readonly type: string; readonly value?: unknown };
  }[];
}

function endedTurnContext(
  messages: readonly AgentMessage[],
  frameworkRunState: string,
  toolCalls: readonly AgentToolCall[],
): AgentTurnContext {
  return {
    turnId: "turn-1",
    messages,
    frameworkRunState,
    toolCalls,
    recordedToolCall: (toolCallId: string) => toolCalls.find((toolCall) => toolCall.toolCallId === toolCallId),
  } as unknown as AgentTurnContext;
}

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function readResult(toolInvocation: unknown): unknown {
  return (toolInvocation as { readonly result?: unknown }).result;
}

function toolInvocations(message: AgentMessage | undefined): unknown[][] {
  assert.ok(message !== undefined, "message is missing");
  const item = JSON.parse(message.frameworkItem) as MastraDBMessage;
  return item.content.parts.flatMap((part) =>
    part.type === "tool-invocation"
      ? [
          [
            part.toolInvocation.toolCallId,
            part.toolInvocation.toolName,
            part.toolInvocation.state,
            readResult(part.toolInvocation),
          ],
        ]
      : [],
  );
}

function assertNoNote(messages: readonly AgentMessage[]): void {
  const notes = messages.filter(
    (message) =>
      message.text?.startsWith(turnSteeredNoteHeader) === true ||
      message.text?.startsWith(turnCancelledNoteHeader) === true,
  );
  assert.deepEqual(notes, []);
}
