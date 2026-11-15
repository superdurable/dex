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

import { toolCallCancelledBySteeringResult, type AgentMessage } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
} from "@superdurable/dex-agents/testing";

import { createScriptedOpenAIAgentFlow } from "../fixtures/scripted-openai-agent.js";

describe("OpenAI Agents SDK turns that end before their final answer", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedOpenAIAgentFlow(markerDirectory);
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

  test("steering while a write waits for approval stores the turn's items with the steering result", async () => {
    const sessionId = `steer-${randomUUID()}`;
    await sessions.startSession(sessionId, "call get_weather then call send_email");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await sessions.steerUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather then call send_email",
      "2 turn-1 assistant function_call get_weather",
      "3 turn-1 tool function_call_result get_weather sunny in Paris",
      "4 turn-1 assistant function_call send_email",
      `5 turn-1 tool function_call_result send_email ${toolCallCancelledBySteeringResult}`,
      "6 turn-2 user history",
      "7 turn-2 assistant history | user: call get_weather then call send_email",
    ]);
    assertEveryCallAnsweredOnce(snapshot.messages);
    assert.deepEqual(JSON.parse(snapshot.messages[4]?.frameworkItem ?? "null"), {
      type: "function_call_result",
      name: "send_email",
      callId: "call_1_send_email",
      status: "completed",
      output: { type: "text", text: toolCallCancelledBySteeringResult },
    });
    assert.deepEqual(toolLog("send_email", sessionId), []);
    const turnEvents = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "turn_steered")
      .map((event) => JSON.parse(event.payload));
    assert.deepEqual(turnEvents, [{ steered_message_count: 1, message_count: 4 }]);
  });

  test("cancelling a resumed model call stores the recorded result as the SDK would give it", async () => {
    const completedSessionId = `completed-${randomUUID()}`;
    await sessions.startSession(completedSessionId, "call get_weather");
    await sessions.waitForCompletedTurns(completedSessionId, 1);
    const completedMessages = (await sessions.getAgentSnapshot(completedSessionId)).messages;

    const sessionId = `cancel-${randomUUID()}`;
    await sessions.startSession(sessionId, "call get_weather then pause");
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();
    assert.equal(await sessions.cancelTurn(sessionId), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather then pause",
      "2 turn-1 assistant function_call get_weather",
      "3 turn-1 tool function_call_result get_weather sunny in Paris",
    ]);
    // The items match those of the same calls in a completed turn.
    assert.deepEqual(
      snapshot.messages.slice(1).map((message) => message.frameworkItem),
      completedMessages.slice(1, 3).map((message) => message.frameworkItem),
    );
    assert.equal(snapshot.messages.some((message) => message.origin !== undefined), false);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call_1_get_weather attempt 1`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const nextSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(nextSnapshot.state?.lastTurnError, null);
    assert.equal(nextSnapshot.messages.at(-1)?.text, "history | user: call get_weather then pause");
    assertEveryCallAnsweredOnce(nextSnapshot.messages);
  });

  function toolLog(toolName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${toolName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }
});

function assertEveryCallAnsweredOnce(messages: readonly AgentMessage[]): void {
  const callIds = messages.filter((message) => message.role === "assistant" && message.toolCallId !== null);
  const resultIds = messages.filter((message) => message.role === "tool");
  assert.deepEqual(
    callIds.map((message) => message.toolCallId).sort(),
    resultIds.map((message) => message.toolCallId).sort(),
  );
  assert.equal(new Set(callIds.map((message) => message.toolCallId)).size, callIds.length);
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as { type?: string };
  const detail =
    message.text ??
    [item.type, message.toolName, message.toolOutput].filter((part) => part !== null && part !== undefined).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}
