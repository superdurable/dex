// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { after, afterEach, before, describe, test } from "node:test";

import { ChannelMessageNotFoundError } from "@superdurable/dex";

import {
  toolCallCancelledBySteeringResult,
  toolCallCancelledByTurnCancellationResult,
  turnCancelledNoteHeader,
  turnFailureNoteFooter,
  turnSteeredNoteHeader,
  type AgentEvent,
  type AgentMessage,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
  waitForCompletedEvent,
} from "../../src/testing/index.js";
import { encodeAgentState } from "../../src/agent-session-codecs.js";
import { streamAgentUIMessageChunks } from "../../src/ui-stream/index.js";
import { createStepwisePlanAgentFlow, readPlanModelRequests } from "../fixtures/stepwise-plan-agent.js";
import {
  createCancelRpcGatedSteeringFlow,
  createSteeringNoteAgentFlow,
  createSteeringTestAgentFlow,
  readSteeringInvocations,
  steeringBuilderGateVariable,
  steeringFinishCancellationGateVariable,
  steeringMarkerDirectoryVariable,
} from "../fixtures/steering-test-agent.js";

const execFileAsync = promisify(execFile);
const workerEntryModulePath = fileURLToPath(new URL("../fixtures/steering-test-agent-worker.js", import.meta.url));
const raceRounds = 50;
const raceParallelSessions = 10;
// Staggered calls land before, during, and after ResumeTurn and the tool Step see the steer.
const raceOffsets = 5;
const raceOffsetMs = 30;

describe("steering and turn cancellation with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let noteSessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createSteeringTestAgentFlow(markerDirectory);
    const noteFlow = createSteeringNoteAgentFlow(markerDirectory);
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, noteFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    noteSessions = new AgentSessionTestClient(environment.client, noteFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("A5-1: a steer during a model call lets it finish and cancels the calls it asked for", async () => {
    const token = newToken();
    const sessionId = `steer-model-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall-then-call model-${token} order-1 | launch notes`);
    await gate.waitUntilReached();

    assert.equal(await sessions.steerUserMessage(sessionId, "say steered"), true);
    const steering = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(steering.steeredMessages?.map((pending) => pending.message.text), ["say steered"]);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(countInvocations(`model:turn-1:1`, sessionId), 1);
    assert.equal(countInvocations(`model:turn-1:2`, sessionId), 0);
    assert.deepEqual(describeMessages(snapshot.messages).slice(0, 6), [
      "turn-1 user null",
      "turn-1 assistant lookup_order",
      "turn-1 assistant publish_note",
      "turn-1 tool lookup_order",
      "turn-1 tool publish_note",
      "turn-2 user null",
    ]);
    assert.deepEqual(
      snapshot.messages.slice(3, 5).map((message) => message.toolOutput),
      [toolCallCancelledBySteeringResult, toolCallCancelledBySteeringResult],
    );
    assert.equal(snapshot.messages[5]?.text, "say steered");
    assert.equal(snapshot.messages.at(-1)?.text, "said: steered");
    assert.deepEqual(snapshot.steeredMessages, []);
    assert.equal(snapshot.state?.lastTurnError, null);

    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(payloadsOf(events, "turn_steered"), [{ message_count: 4, steered_message_count: 1 }]);
    assert.deepEqual(
      payloadsOf(events, "tool_call_finished").map((payload) => payload.status),
      ["rejected", "rejected"],
    );
    assert.deepEqual(payloadsOf(events, "tool_call_started"), []);
    assert.deepEqual(payloadsOf(events, "interrupt_resolved"), [
      {
        answered: false,
        approved: false,
        cancellation_reason: "steered",
        interrupt_id: snapshot.messages[2]?.toolCallId,
        kind: "tool_approval",
        rejection_message: toolCallCancelledBySteeringResult,
      },
    ]);
    const executed = await executedStepTypes(sessionId);
    assert.deepEqual(executed.slice(0, 2), ["StartAgentSession", "RunModel"]);
    assert.deepEqual(executed.slice(-2), ["ApplyTurnControl", "RunModel"]);
    assert.equal(executed.filter((stepType) => stepType === "RunModel").length, 2);
  });

  test("A5-2: a steer during a single-attempt tool lets it run once and applies after it", async () => {
    const token = newToken();
    const sessionId = `steer-write-${token}`;
    const gate = new FileGate(markerDirectory, `write-${token}`);
    await sessions.startSession(sessionId, `slow-write write-${token} ledger entry`);
    await gate.waitUntilReached();

    assert.equal(await sessions.steerUserMessage(sessionId, "say after the write"), true);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(gate.reachedCount(), 1);
    assert.equal(countInvocations("model:turn-1:2", sessionId), 0);
    assert.deepEqual(toolResults(snapshot.messages), ["slow_write wrote ledger entry"]);
    assert.equal(snapshot.messages.at(-1)?.text, "said: after the write");
    const executed = await executedStepTypes(sessionId);
    assert.equal(executed.filter((stepType) => stepType === "CallSingleAttemptTool").length, 1);
    assert.deepEqual(executed.slice(-2), ["ApplyTurnControl", "RunModel"]);
  });

  test("A5-3: a steer cancels a waiting approval while a parallel read finishes", async () => {
    const token = newToken();
    const sessionId = `steer-approval-${token}`;
    const gate = new FileGate(markerDirectory, `read-${token}`);
    await sessions.startSession(sessionId, `parallel read-${token} order-2 | launch notes`);
    await gate.waitUntilReached();
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    assert.equal(approval.kind, "tool_approval");

    assert.equal(await sessions.steerUserMessage(sessionId, "say redirected"), true);
    const cancelled = await sessions.waitForAgentSnapshot(
      sessionId,
      (snapshot) =>
        snapshot.pendingInterrupts.length === 0 && snapshot.toolCalls.some((call) => call.status === "rejected"),
      "the cancelled approval",
    );
    const publish = cancelled.toolCalls.find((call) => call.toolName === "publish_note");
    assert.equal(publish?.cancellationReason, "steered");
    assert.equal(publish?.rejectionMessage, toolCallCancelledBySteeringResult);
    assert.equal(cancelled.toolCalls.find((call) => call.toolName === "lookup_order")?.status, "approved");
    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), false);

    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(toolResults(snapshot.messages), [
      "lookup_order found order-2",
      `publish_note ${toolCallCancelledBySteeringResult}`,
    ]);
    assert.equal(snapshot.messages.at(-1)?.text, "said: redirected");
    const executed = await executedStepTypes(sessionId);
    assert.deepEqual(executed.slice(executed.lastIndexOf("ResumeTurn") + 1), ["ApplyTurnControl", "RunModel"]);
    assert.equal(executed.includes("CallSingleAttemptTool"), false);
  });

  test("A5-4: racing approvals and steers give every call one result and never stall", async () => {
    const token = newToken();
    const outcomes: { readonly isResolved: boolean; readonly result: string }[] = [];
    const race = async (round: number): Promise<void> => {
      const sessionId = `steer-race-${token}-${round}`;
      await sessions.startSession(sessionId, `publish race-${round}`);
      const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
      // A negative offset steers after the approval; a positive one approves after the steer.
      const offsetMs = ((round % raceOffsets) - Math.floor(raceOffsets / 2)) * raceOffsetMs;
      const [isResolved, isSteered] = await Promise.all([
        delay(Math.max(offsetMs, 0)).then(() => sessions.resolveInterrupt(sessionId, approval.interruptId, true)),
        delay(Math.max(-offsetMs, 0)).then(() => sessions.steerUserMessage(sessionId, `say next-${round}`)),
      ]);
      assert.equal(isSteered, true);
      await sessions.waitForCompletedTurns(sessionId, 2);
      const snapshot = await sessions.getAgentSnapshot(sessionId);
      assert.equal(snapshot.messages.at(-1)?.text, `said: next-${round}`);
      const results = snapshot.messages.filter((message) => message.role === "tool").map((message) => message.toolOutput);
      assert.equal(results.length, 1);
      const result = results[0] ?? "";
      assert.ok(
        [toolCallCancelledBySteeringResult, `{"published": "race-${round}"}`].includes(result),
        `unexpected result ${result}`,
      );
      const events = await sessions.listAgentEvents(sessionId);
      assert.equal(payloadsOf(events, "tool_call_finished").length, 1);
      // A tool that started has a result of its own; a cancelled one never started.
      const isPublished = result !== toolCallCancelledBySteeringResult;
      assert.equal(payloadsOf(events, "tool_call_started").length > 0, isPublished);
      if (!isResolved) {
        assert.equal(isPublished, false);
      }
      outcomes.push({ isResolved, result });
    };
    for (let first = 0; first < raceRounds; first += raceParallelSessions) {
      await Promise.all(
        Array.from({ length: Math.min(raceParallelSessions, raceRounds - first) }, (_, index) => race(first + index)),
      );
    }
    const publishedCount = outcomes.filter((outcome) => outcome.result !== toolCallCancelledBySteeringResult).length;
    // No tool ran twice, and none ran without its result.
    assert.equal(
      readSteeringInvocations(markerDirectory)
        .names()
        .filter((name) => name.startsWith(`steer-race-${token}-`) && name.endsWith(" publish_note")).length,
      publishedCount,
    );
  });

  test("A5-6: queued messages can be deleted or steered until a turn consumes them", async () => {
    const token = newToken();
    const sessionId = `steer-queue-${token}`;
    const gate = new FileGate(markerDirectory, `busy-${token}`);
    await sessions.startSession(sessionId, `stall busy-${token}`);
    await gate.waitUntilReached();
    assert.equal(await sessions.sendUserMessage(sessionId, "say deleted"), true);
    assert.equal(await sessions.sendUserMessage(sessionId, "say kept"), true);
    const queued = (await sessions.getAgentSnapshot(sessionId)).queuedMessages ?? [];
    assert.deepEqual(queued.map((pending) => pending.message.text), ["say deleted", "say kept"]);
    const [deleted, kept] = queued;
    assert.ok(deleted !== undefined && kept !== undefined);

    assert.equal(await sessions.deleteQueuedMessage(sessionId, deleted.messageId), true);
    assert.equal(await sessions.deleteQueuedMessage(sessionId, deleted.messageId), false);
    assert.equal(await sessions.steerQueuedMessage(sessionId, "no-such-id"), false);
    assert.equal(await sessions.steerUserMessage(sessionId, "   "), false);
    assert.equal(
      await environment.client.invokeRPC(sessions.flow.steerUserMessage, sessionId, { messageId: null, text: null }),
      false,
    );
    assert.equal(
      await environment.client.invokeRPC(sessions.flow.steerUserMessage, sessionId, {
        messageId: kept.messageId,
        text: "say both",
      }),
      false,
    );
    const remaining = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(remaining.queuedMessages?.map((pending) => pending.message.text), ["say kept"]);
    assert.equal(remaining.queuedMessageCount, 1);
    assert.deepEqual(remaining.steeredMessages, []);

    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.slice(-2).map((message) => message.text), ["say kept", "said: kept"]);
    assert.equal(await sessions.steerQueuedMessage(sessionId, kept.messageId), false);
    assert.equal(await sessions.deleteQueuedMessage(sessionId, kept.messageId), false);
    assert.deepEqual(snapshot.queuedMessages, []);
  });

  test("A5-6: steering one queued message twice at once moves it once and the loser commits nothing", async () => {
    const token = newToken();
    const sessionId = `steer-twice-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall model-${token}`);
    await gate.waitUntilReached();
    const texts = Array.from({ length: 20 }, (_, index) => `say queued-${index}`);
    for (const text of texts) {
      assert.equal(await sessions.sendUserMessage(sessionId, text), true);
    }
    const queued = (await sessions.getAgentSnapshot(sessionId)).queuedMessages ?? [];
    const steer = async (messageId: string): Promise<boolean | null> => {
      try {
        return await sessions.steerQueuedMessage(sessionId, messageId);
      } catch (error: unknown) {
        if (error instanceof ChannelMessageNotFoundError) {
          return null;
        }
        throw error;
      }
    };
    for (const pending of queued) {
      const outcomes = await Promise.all([steer(pending.messageId), steer(pending.messageId)]);
      // One call moves the message; the other finds it gone, before or at its commit.
      assert.equal(outcomes.filter((outcome) => outcome === true).length, 1, JSON.stringify(outcomes));
    }
    const steering = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(steering.queuedMessages, []);
    assert.deepEqual(steering.steeredMessages?.map((pending) => pending.message.text), texts);

    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const userTexts = snapshot.messages.filter((message) => message.role === "user").map((message) => message.text);
    assert.deepEqual(userTexts.slice(-texts.length), texts);
    // The stalled turn answered before the steer applied, so the steered messages start turn 2.
    assert.deepEqual(payloadsOf(await sessions.listAgentEvents(sessionId), "turn_steered"), []);
    assert.equal(snapshot.messages.at(-1)?.text, "said: queued-19");
    const executed = await executedStepTypes(sessionId);
    assert.equal(executed.filter((stepType) => stepType === "ApplyTurnControl").length, 1);
  });

  test("A5-7: cancel_turn ends an approval wait and aborts a running model call", async () => {
    const token = newToken();
    const sessionId = `cancel-approval-${token}`;
    await sessions.startSession(sessionId, "publish launch notes");
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    assert.equal(await sessions.sendUserMessage(sessionId, "say queued"), true);
    assert.equal(await sessions.cancelTurn(sessionId, "turn-7"), false);

    assert.equal(await sessions.cancelTurn(sessionId, "turn-1"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages
        .filter((message) => message.role === "tool")
        .map((message) => `${message.turnId} ${message.toolName} ${message.toolOutput}`),
      [`turn-1 publish_note ${toolCallCancelledByTurnCancellationResult}`],
    );
    assert.equal(snapshot.messages.at(-1)?.text, "said: queued");
    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), false);
    assert.equal(await sessions.cancelTurn(sessionId), false);
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(payloadsOf(events, "turn_cancelled"), [{ message_count: 2 }]);
    assert.deepEqual(
      payloadsOf(events, "interrupt_resolved").map((payload) => payload.cancellation_reason),
      ["turn_cancelled"],
    );

    const gate = new FileGate(markerDirectory, `model-${token}`);
    assert.equal(await sessions.sendUserMessage(sessionId, `stall-then-call model-${token} order-3 | never`), true);
    await gate.waitUntilReached();
    assert.equal(await sessions.cancelTurn(sessionId), true);
    await sessions.waitForCompletedTurns(sessionId, 3);
    gate.release();
    const cancelled = await sessions.getAgentSnapshot(sessionId);
    // The model call stopped inside the gate, so it asked for no tool calls.
    assert.equal(countInvocations("model:turn-3:1", sessionId), 1);
    assert.equal(countGateCancellations(`model-${token}`), 1);
    assert.deepEqual(
      cancelled.messages.filter((message) => message.turnId === "turn-3").map((message) => message.role),
      ["user"],
    );
  });

  test("A5-7: cancel_turn stops a running model call at once, and its answer never exists", async () => {
    const token = newToken();
    const sessionId = `cancel-model-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall model-${token}`);
    await gate.waitUntilReached();
    const cancelledAt = Date.now();
    assert.equal(await sessions.cancelTurn(sessionId), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1);
    assert.ok(Date.now() - cancelledAt < 15_000);
    gate.release();

    assert.equal(countGateCancellations(`model-${token}`), 1);
    assert.equal(countInvocations("model:turn-1:1", sessionId), 1);
    assert.equal(state.status, "waiting_for_user_message");
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.role} ${message.text}`),
      [`user stall model-${token}`],
    );
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(
      events.filter((event) => event.type.startsWith("turn_")).map((event) => event.type),
      ["turn_started", "turn_cancelled"],
    );
    assert.deepEqual(payloadsOf(events, "turn_cancelled"), [{ message_count: 0 }]);
    // A cancelled RunModel completes no Execute and is never retried.
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "FinishTurnCancellation",
      "ApplyTurnControl",
    ]);
    assert.equal(await sessions.cancelTurn(sessionId), false);

    // With no active turn, a steer is queued, so the idle wait of any Worker version takes it.
    assert.equal(await sessions.steerUserMessage(sessionId, "say idle steer"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const steered = await sessions.getAgentSnapshot(sessionId);
    assert.equal(steered.messages.at(-1)?.text, "said: idle steer");
    assert.deepEqual((await executedStepTypes(sessionId)).slice(-2), ["AwaitUserMessage", "RunModel"]);
  });

  test("cancel_turn ends a question and a durable wait at once", async () => {
    const token = newToken();
    const sessionId = `cancel-wait-${token}`;
    await sessions.startSession(sessionId, "wait 3600 the nightly build");
    await waitForStepMethod(sessionId, "AwaitTimer");
    assert.equal(await sessions.cancelTurn(sessionId), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(await sessions.sendUserMessage(sessionId, "ask Which build? | a | b"), true);
    const question = await sessions.waitForPendingInterrupt(sessionId, "ask_user");
    assert.equal(await sessions.cancelTurn(sessionId, "turn-2"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(await sessions.answerQuestion(sessionId, question.interruptId, "a"), false);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages
        .filter((message) => message.role === "tool")
        .map((message) => `${message.turnId} ${message.toolName} ${message.toolOutput}`),
      [
        `turn-1 durable_wait ${toolCallCancelledByTurnCancellationResult}`,
        `turn-2 ask_user ${toolCallCancelledByTurnCancellationResult}`,
      ],
    );
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(
      payloadsOf(events, "interrupt_resolved").map((payload) => `${payload.kind} ${payload.cancellation_reason}`),
      ["question turn_cancelled"],
    );
    assert.equal(events.filter((event) => event.type === "turn_cancelled").length, 2);
    // Neither wait completed its Execute: cancel_turn cancelled both Steps.
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.includes("AwaitTimer"), false);
    assert.equal(steps.includes("AwaitQuestionAnswer"), false);
    assert.equal(steps.filter((stepType) => stepType === "FinishTurnCancellation").length, 2);
  });

  test("cancel_turn lets a started tool finish first", async () => {
    const token = newToken();
    const sessionId = `cancel-tool-${token}`;
    const gate = new FileGate(markerDirectory, `write-${token}`);
    await sessions.startSession(sessionId, `slow-write write-${token} the memo`);
    await gate.waitUntilReached();

    assert.equal(await sessions.cancelTurn(sessionId), true);
    await pollUntil(
      async () => ((await executedStepTypes(sessionId)).includes("FinishTurnCancellation") ? true : undefined),
      { description: "FinishTurnCancellation to decide" },
    );
    const running = await sessions.getAgentSnapshot(sessionId);
    assert.equal(running.state?.activeTurnId, "turn-1");
    assert.deepEqual(running.toolCalls.map((toolCall) => toolCall.status), ["approved"]);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal(countGateCancellations(`write-${token}`), 0);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(toolResults(snapshot.messages), ["slow_write wrote the memo"]);
    // The model is not called after the tool: the cancelled turn ends with its result.
    assert.equal(snapshot.messages.at(-1)?.role, "tool");
    assert.deepEqual(
      payloadsOf(await sessions.listAgentEvents(sessionId), "tool_call_finished").map((payload) => payload.status),
      ["succeeded"],
    );
    const steps = await executedStepTypes(sessionId);
    assert.deepEqual(steps.slice(-2), ["ResumeTurn", "ApplyTurnControl"]);
    assert.equal(steps.filter((stepType) => stepType === "RunModel").length, 1);
  });

  test("cancel_turn twice cancels once", async () => {
    const token = newToken();
    const sessionId = `cancel-twice-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall model-${token}`);
    await gate.waitUntilReached();
    // The second call finds the request recorded, or the turn already ended.
    const results = await Promise.all([sessions.cancelTurn(sessionId), sessions.cancelTurn(sessionId)]);
    assert.ok(results.includes(true));
    await sessions.waitForCompletedTurns(sessionId, 1);
    gate.release();
    const steps = await executedStepTypes(sessionId);
    assert.equal(steps.filter((stepType) => stepType === "FinishTurnCancellation").length, 1);
    assert.equal(steps.filter((stepType) => stepType === "ApplyTurnControl").length, 1);
  });

  test("a steer never aborts a running model call", async () => {
    const token = newToken();
    const sessionId = `steer-no-abort-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall-then-call model-${token} order-1 | x`);
    await gate.waitUntilReached();
    assert.equal(await sessions.steerUserMessage(sessionId, "say steered"), true);
    await delay(1_000);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(countGateCancellations(`model-${token}`), 0);
    assert.equal((await executedStepTypes(sessionId)).includes("FinishTurnCancellation"), false);
  });

  test("A5-8: an adapter without unfinished-turn messages gets a note", async () => {
    const token = newToken();
    const sessionId = `steer-note-${token}`;
    await noteSessions.startSession(sessionId, "call lookup_a and charge_card");
    const gate = new FileGate(markerDirectory, "lookups");
    await gate.waitUntilReached();
    await noteSessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await noteSessions.steerUserMessage(sessionId, "instead"), true);
    gate.release();
    await noteSessions.waitForCompletedTurns(sessionId, 2);
    const steered = await noteSessions.getAgentSnapshot(sessionId);

    assert.deepEqual(describeMessages(steered.messages), [
      "turn-1 user null",
      "turn-1 user null",
      "turn-2 user null",
      "turn-2 assistant null",
    ]);
    assert.equal(
      steered.messages[1]?.text,
      [
        turnSteeredNoteHeader,
        '- lookup_a {"requested_by":"call lookup_a and charge_card"} -> succeeded: lookup_a found',
        `- charge_card {"requested_by":"call lookup_a and charge_card"} -> rejected: ${toolCallCancelledBySteeringResult}`,
        turnFailureNoteFooter,
      ].join("\n"),
    );
    assert.deepEqual(
      steered.messages.map((message) => message.origin),
      [undefined, "turn_steered_note", undefined, undefined],
    );
    assert.equal(steered.messages.at(-1)?.text, "echo: instead");

    assert.equal(await noteSessions.sendUserMessage(sessionId, "call charge_card"), true);
    await noteSessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await noteSessions.cancelTurn(sessionId, "turn-3"), true);
    await noteSessions.waitForCompletedTurns(sessionId, 3);
    const cancelled = await noteSessions.getAgentSnapshot(sessionId);
    assert.ok(cancelled.messages.at(-1)?.text?.startsWith(`${turnCancelledNoteHeader}\n- charge_card `));
    assert.equal(cancelled.messages.at(-1)?.origin, "turn_cancelled_note");
    assert.equal(cancelled.state?.turnCount, 3);
  });

  test("A4-3: a steer ends a question and a durable wait, but a queued message does not", async () => {
    const token = newToken();
    const sessionId = `steer-wait-${token}`;
    await sessions.startSession(sessionId, "wait 3600 the nightly build");
    await waitForStepMethod(sessionId, "AwaitTimer");
    assert.equal(await sessions.sendUserMessage(sessionId, "say queued"), true);
    const stillWaiting = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(stillWaiting.toolCalls.map((call) => call.status), ["waiting_for_timer"]);
    assert.equal(stillWaiting.queuedMessageCount, 1);

    assert.equal(await sessions.steerUserMessage(sessionId, "ask Which build? | a | b"), true);
    const question = await sessions.waitForPendingInterrupt(sessionId, "ask_user");
    assert.equal(question.kind, "question");

    assert.equal(await sessions.steerUserMessage(sessionId, "say done"), true);
    await sessions.waitForCompletedTurns(sessionId, 4);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(await sessions.answerQuestion(sessionId, question.interruptId, "a"), false);
    const turnTexts = snapshot.messages.filter((message) => message.text !== null).map((message) => `${message.turnId} ${message.text}`);
    assert.ok(turnTexts.includes("turn-3 said: done"));
    assert.equal(turnTexts.at(-1), "turn-4 said: queued");
    assert.deepEqual(
      snapshot.messages
        .filter((message) => message.role === "tool")
        .map((message) => `${message.turnId} ${message.toolName} ${message.toolOutput}`),
      [
        `turn-1 durable_wait ${toolCallCancelledBySteeringResult}`,
        `turn-2 ask_user ${toolCallCancelledBySteeringResult}`,
      ],
    );
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(payloadsOf(events, "interrupt_resolved").map((payload) => payload.kind), ["question"]);
  });

  test("a tool question asked after a steer is never raised", async () => {
    const token = newToken();
    const sessionId = `steer-tool-question-${token}`;
    const gate = new FileGate(markerDirectory, `ask-${token}`);
    await sessions.startSession(sessionId, `transfer-late-ask 9 ask-${token}`);
    await gate.waitUntilReached();

    assert.equal(await sessions.steerUserMessage(sessionId, "say skip it"), true);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(countInvocations("transfer_funds:run", sessionId), 0);
    assert.deepEqual(toolResults(snapshot.messages), [`transfer_funds ${toolCallCancelledBySteeringResult}`]);
    assert.equal(snapshot.messages.at(-1)?.text, "said: skip it");
    assert.deepEqual(payloadsOf(await sessions.listAgentEvents(sessionId), "interrupt_raised"), []);
    const executed = await executedStepTypes(sessionId);
    assert.equal(executed.filter((stepType) => stepType === "AwaitQuestionAnswer").length, 1);
  });

  test("a turn that a Worker without steering advanced refuses steering and cancellation", async () => {
    const token = newToken();
    const sessionId = `steer-refused-${token}`;
    await sessions.startSession(sessionId, "publish upgrade notes");
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    // An earlier Worker writes AgentState without the field it does not know.
    await rewriteAgentStateAsEarlierWorker(sessionId);
    const running = await sessions.getAgentSnapshot(sessionId);
    assert.equal(running.state?.acceptsTurnControl ?? false, false);

    assert.equal(await sessions.steerUserMessage(sessionId, "say steered"), false);
    assert.equal(await sessions.cancelTurn(sessionId), false);
    assert.equal(await sessions.sendUserMessage(sessionId, "say after"), true);
    const [queued] = (await sessions.getAgentSnapshot(sessionId)).queuedMessages ?? [];
    assert.ok(queued !== undefined);
    assert.equal(await sessions.steerQueuedMessage(sessionId, queued.messageId), false);
    assert.deepEqual((await sessions.getAgentSnapshot(sessionId)).steeredMessages, []);

    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.filter((message) => message.role === "user").map((message) => message.text),
      ["publish upgrade notes", "say after"],
    );
    assert.equal(snapshot.messages.at(-1)?.text, "said: after");
    assert.equal(countInvocations("publish_note", sessionId), 1);
  });

  test("A5-9: a wake-up left from an earlier turn never cancels a call", async () => {
    const token = newToken();
    const sessionId = `steer-stale-${token}`;
    await sessions.startSession(sessionId, null);
    await publishStaleToolCallCancellation(sessionId, "reused-call");

    assert.equal(await sessions.sendUserMessage(sessionId, "publish-as reused-call memo"), true);
    await waitForStepMethod(sessionId, "AwaitToolApproval", executeCompletedEvent);
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    assert.equal(approval.interruptId, "reused-call");
    assert.equal(await sessions.resolveInterrupt(sessionId, "reused-call", true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(snapshot.messages.at(-1)?.text, 'publish_note: {"published": "memo"}');
    const executed = await executedStepTypes(sessionId);
    assert.equal(executed.filter((stepType) => stepType === "AwaitToolApproval").length, 2);
  });

  test("a retry reuses the first attempt's state, so steering never abandons a started call", async () => {
    const token = newToken();
    const sessionId = `steer-retry-${token}`;
    await noteSessions.startSession(sessionId, "call retry_payout");
    const approval = await noteSessions.waitForPendingInterrupt(sessionId, "retry_payout");
    assert.equal(await noteSessions.resolveInterrupt(sessionId, approval.interruptId, true), true);
    const gate = new FileGate(markerDirectory, "retry_payout");
    await gate.waitUntilReached();
    assert.equal(await noteSessions.steerUserMessage(sessionId, "later"), true);
    gate.release();
    await noteSessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await noteSessions.getAgentSnapshot(sessionId);

    assert.equal(gate.reachedCount(), 2);
    const note = snapshot.messages.find((message) => message.turnId === "turn-1" && message.sequence > 1);
    assert.ok(note?.text?.startsWith(turnSteeredNoteHeader));
    assert.ok(note?.text?.includes("-> succeeded: payout sent"));
    assert.equal(snapshot.messages.at(-1)?.text, "echo: later");
  });

  test("the UI message stream ends a steered turn with finish other, and never pauses as for an approval", async () => {
    const token = newToken();
    const sessionId = `steer-ui-${token}`;
    const gate = new FileGate(markerDirectory, `read-${token}`);
    await sessions.startSession(sessionId, `lookup-gated read-${token} order-8`);
    await gate.waitUntilReached();
    const streaming = collectChunks(
      streamAgentUIMessageChunks({ client: environment.client, flow: sessions.flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
    );
    await delay(1_500);
    assert.equal(await sessions.steerUserMessage(sessionId, "say elsewhere"), true);
    gate.release();
    const chunks = await streaming;

    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "other" }],
    );
    const eventTypes = chunks.flatMap((chunk) => {
      const data = (chunk as { type: string; data?: { type?: string } }).data;
      return chunk.type === "data-agent-event" && data?.type !== undefined ? [data.type] : [];
    });
    assert.ok(eventTypes.includes("turn_steered"), JSON.stringify(eventTypes));
    assert.equal(eventTypes.includes("turn_started"), false);
    await sessions.waitForCompletedTurns(sessionId, 2);
  });

  test("the UI message stream ends a turn cancelled during its model call with finish other", async () => {
    const token = newToken();
    const sessionId = `cancel-ui-${token}`;
    const gate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall model-${token}`);
    await gate.waitUntilReached();
    const streaming = collectChunks(
      streamAgentUIMessageChunks({ client: environment.client, flow: sessions.flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
    );
    await delay(1_500);
    assert.equal(await sessions.cancelTurn(sessionId), true);
    const chunks = await streaming;
    gate.release();

    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "other" }],
    );
    const eventTypes = chunks.flatMap((chunk) => {
      const data = (chunk as { type: string; data?: { type?: string } }).data;
      return chunk.type === "data-agent-event" && data?.type !== undefined ? [data.type] : [];
    });
    assert.ok(eventTypes.includes("turn_cancelled"), JSON.stringify(eventTypes));
    // The cancelled call streamed text before the gate; the stream closes that part before it finishes.
    const textStarts = chunks.filter((chunk) => chunk.type === "text-start").length;
    assert.equal(chunks.filter((chunk) => chunk.type === "text-end").length, textStarts);
  });

  function countGateCancellations(gateName: string): number {
    return readSteeringInvocations(markerDirectory)
      .names()
      .filter((recorded) => recorded === `gate ${gateName} cancelled`).length;
  }

  function countInvocations(name: string, sessionId: string): number {
    return readSteeringInvocations(markerDirectory)
      .names()
      .filter((recorded) => recorded === `${sessionId} ${name}`).length;
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    const history = await readStepHistory(engine.serverAddress, sessionId);
    return history.filter((entry) => entry.eventType === executeCompletedEvent).map((entry) => entry.stepType);
  }

  async function waitForStepMethod(sessionId: string, stepType: string, eventType = waitForCompletedEvent): Promise<void> {
    await pollUntil(
      async () =>
        (await readStepHistory(engine.serverAddress, sessionId)).some(
          (entry) => entry.stepType === stepType && entry.eventType === eventType,
        )
          ? true
          : undefined,
      { description: `${stepType} ${eventType} in ${sessionId}` },
    );
  }

  async function rewriteAgentStateAsEarlierWorker(sessionId: string): Promise<void> {
    const [state] = await environment.client.getAttributes(sessionId, sessions.flow.agentState);
    assert.ok(state !== undefined);
    const { accepts_turn_control: _dropped, ...earlierWorkerState } = encodeAgentState(state) as Record<
      string,
      unknown
    >;
    const payload = Buffer.from(JSON.stringify(earlierWorkerState)).toString("base64");
    await execFileAsync(process.env.DEXCLI ?? "dexcli", [
      "api",
      "call",
      "SetAttributes",
      "--server",
      engine.serverAddress,
      "--yes",
      "--data",
      JSON.stringify({
        flowId: sessionId,
        requestId: randomUUID(),
        attributes: [{ key: "AgentState", value: { objValue: { encoding: "json", payload } } }],
      }),
    ]);
  }

  function publishStaleToolCallCancellation(sessionId: string, toolCallId: string): Promise<void> {
    return publishToChannel(engine, sessionId, `ToolCallCancellations/${encodeURIComponent(toolCallId)}`, {
      reason: "steered",
    });
  }
});

describe("cancel_turn racing a model call that commits", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;
  const rpcGateName = "cancel-rpc";

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createCancelRpcGatedSteeringFlow(markerDirectory, rpcGateName);
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

  test("a model call that commits between cancel_turn's read and its commit ends the turn once", async () => {
    const token = newToken();
    const sessionId = `cancel-race-${token}`;
    const modelGate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall-then-call model-${token} order-3 | launch notes`);
    await modelGate.waitUntilReached();

    const cancelling = sessions.cancelTurn(sessionId);
    const rpcGate = new FileGate(markerDirectory, `${rpcGateName}-${sessionId}`);
    await rpcGate.waitUntilReached();
    // cancel_turn read a running model call. Now the model call commits its tool calls.
    modelGate.release();
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    await pollUntil(
      async () =>
        payloadsOf(await sessions.listAgentEvents(sessionId), "tool_call_finished").some(
          (payload) => payload.tool_name === "lookup_order",
        )
          ? true
          : undefined,
      { description: "lookup_order to finish" },
    );
    rpcGate.release();
    assert.equal(await cancelling, true);

    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const invocations = readSteeringInvocations(markerDirectory).names();
    assert.equal(invocations.includes(`gate model-${token} cancelled`), false);
    assert.equal(invocations.includes(`${sessionId} model:turn-1:2`), false);
    assert.deepEqual(toolResults(snapshot.messages), [
      "lookup_order found order-3",
      `publish_note ${toolCallCancelledByTurnCancellationResult}`,
    ]);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), false);
    const events = await sessions.listAgentEvents(sessionId);
    assert.equal(events.filter((event) => event.type === "turn_cancelled").length, 1);
    assert.equal(events.some((event) => event.type === "turn_completed"), false);
    assert.deepEqual(
      payloadsOf(events, "interrupt_resolved").map((payload) => payload.cancellation_reason),
      ["turn_cancelled"],
    );
    const steps = (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
    // RunModel won the race and committed; the approval wait was cancelled; ResumeTurn ended the turn.
    assert.equal(steps.filter((stepType) => stepType === "RunModel").length, 1);
    assert.equal(steps.includes("AwaitToolApproval"), false);
    assert.equal(steps.filter((stepType) => stepType === "FinishTurnCancellation").length, 1);
    assert.deepEqual(steps.slice(-2), ["ResumeTurn", "ApplyTurnControl"]);
  });

  test("a cancel that loses to a final answer never cancels the next queued turn", async () => {
    const token = newToken();
    const sessionId = `cancel-race-next-${token}`;
    const modelGate = new FileGate(markerDirectory, `model-${token}`);
    await sessions.startSession(sessionId, `stall model-${token}`);
    await modelGate.waitUntilReached();
    assert.equal(await sessions.sendUserMessage(sessionId, `stall-then-call next-${token} order-2 | note`), true);

    const cancelling = sessions.cancelTurn(sessionId);
    const rpcGate = new FileGate(markerDirectory, `${rpcGateName}-${sessionId}`);
    await rpcGate.waitUntilReached();
    // cancel_turn read a running model call. Now it commits its final answer.
    modelGate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);
    const nextGate = new FileGate(markerDirectory, `next-${token}`);
    await delay(3_000);
    // The queued turn waits for the RPC's commit before its model call starts.
    assert.equal(nextGate.reachedCount(), 0);
    rpcGate.release();
    assert.equal(await cancelling, true);

    await nextGate.waitUntilReached();
    nextGate.release();
    const approval = await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    const invocations = readSteeringInvocations(markerDirectory).names();
    assert.equal(invocations.includes(`gate next-${token} cancelled`), false);
    const running = await sessions.getAgentSnapshot(sessionId);
    assert.equal(running.state?.activeTurnId, "turn-2");
    assert.equal(running.state?.status, "running_tools");
    assert.equal(
      (await sessions.listAgentEvents(sessionId)).some((event) => event.type === "turn_cancelled"),
      false,
    );
    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, false), true);
    const finished = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(finished.lastTurnError, null);
    const steps = (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
    assert.equal(steps.filter((stepType) => stepType === "FinishTurnCancellation").length, 1);
    assert.equal(steps.includes("ApplyTurnControl"), false);
  });
});

describe("steering with plans", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createStepwisePlanAgentFlow(markerDirectory);
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

  test("a steered message starts a planning turn while a draft exists", async () => {
    const token = newToken();
    const sessionId = `steer-draft-${token}`;
    await sessions.startSession(sessionId, null);
    assert.equal(await sessions.sendUserMessage(sessionId, "plan A | B", true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).plan?.status, "draft");

    // The model call answers, so the steer starts the next turn through ApplyTurnControl.
    const gate = new FileGate(markerDirectory, `draft-${token}`);
    assert.equal(await sessions.sendUserMessage(sessionId, `stall draft-${token}`), true);
    await gate.waitUntilReached();
    assert.equal(await sessions.steerUserMessage(sessionId, "revise C | D"), true);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 3);
    const revised = await sessions.getAgentSnapshot(sessionId);
    assert.equal(revised.plan?.revision, 2);
    assert.equal(revised.plan?.status, "draft");
    assert.deepEqual(revised.plan?.tasks.map((task) => task.content), ["C", "D"]);
    assert.equal(revised.state?.interactionMode, "planning");
    const turnThreeTools = readPlanModelRequests(markerDirectory)
      .filter((request) => request.flowId === sessionId && request.turnId === "turn-3")
      .map((request) => request.tools.join(","));
    assert.deepEqual([...new Set(turnThreeTools)], ["write_todos"]);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    assert.equal(
      history.filter((entry) => entry.stepType === "ApplyTurnControl" && entry.eventType === executeCompletedEvent)
        .length,
      1,
    );
  });

  test("a queued planMode message moved by a steer keeps planMode", async () => {
    const token = newToken();
    const sessionId = `steer-plan-mode-${token}`;
    const gate = new FileGate(markerDirectory, `chat-${token}`);
    await sessions.startSession(sessionId, `stall chat-${token}`);
    await gate.waitUntilReached();
    assert.equal(await sessions.sendUserMessage(sessionId, "plan X | Y", true), true);
    const [queued] = (await sessions.getAgentSnapshot(sessionId)).queuedMessages ?? [];
    assert.ok(queued !== undefined);
    assert.equal(queued.message.planMode, true);
    assert.equal(await sessions.steerQueuedMessage(sessionId, queued.messageId), true);
    const steering = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(steering.steeredMessages?.map((pending) => pending.message.planMode), [true]);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const planned = await sessions.getAgentSnapshot(sessionId);
    assert.equal(planned.plan?.status, "draft");
    assert.equal(planned.state?.interactionMode, "planning");
  });

  test("execute_plan returns false while a steered message is pending", async () => {
    const token = newToken();
    const sessionId = `steer-execute-${token}`;
    await sessions.startSession(sessionId, null);
    assert.equal(await sessions.sendUserMessage(sessionId, "plan A | B", true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    // A steer that ended its turn and has not applied yet, held pending with no wake-up.
    await publishToChannel(engine, sessionId, "SteeredUserMessages", { text: "discuss", plan_mode: false });
    const pending = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(pending.steeredMessages?.map((message) => message.message.text), ["discuss"]);

    assert.equal(await sessions.executePlan(sessionId, 1), false);
    await publishToChannel(engine, sessionId, "TurnControlRequests", { kind: "steer" });
    await sessions.waitForCompletedTurns(sessionId, 2);
    const applied = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(applied.steeredMessages, []);
    assert.equal(applied.messages.at(-1)?.text, "the plan has 2 tasks");
    assert.equal(await sessions.executePlan(sessionId, 1), true);
  });
});

describe("steering with a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createSteeringTestAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
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

  test("A5-5: a Worker killed during ApplyTurnControl ends the turn once and consumes each steered message once", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const startWorker = async (): Promise<KillableWorkerProcess> => {
      const worker = await KillableWorkerProcess.start({
        entryModulePath: workerEntryModulePath,
        serverAddress: engine.serverAddress,
        logPath: join(markerDirectory, `worker-${workers.length + 1}.log`),
        environment: {
          [steeringMarkerDirectoryVariable]: markerDirectory,
          [steeringBuilderGateVariable]: "builder",
        },
      });
      workers.push(worker);
      return worker;
    };
    const firstWorker = await startWorker();
    const sessionId = `steer-kill-${newToken()}`;
    const readGate = new FileGate(markerDirectory, "read");
    await sessions.startSession(sessionId, "lookup-gated read order-5");
    await readGate.waitUntilReached();
    assert.equal(await sessions.steerUserMessage(sessionId, "say one"), true);
    assert.equal(await sessions.steerUserMessage(sessionId, "say two"), true);
    readGate.release();
    const builderGate = new FileGate(markerDirectory, "builder");
    await builderGate.waitUntilReached();

    await firstWorker.kill();
    builderGate.release();
    await startWorker();
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(builderGate.reachedCount(), 2);
    assert.deepEqual(
      snapshot.messages.map((message) => message.sequence),
      snapshot.messages.map((_message, index) => index + 1),
    );
    assert.deepEqual(
      snapshot.messages.filter((message) => message.role === "user").map((message) => message.text),
      ["lookup-gated read order-5", "say one", "say two"],
    );
    assert.deepEqual(toolResults(snapshot.messages), ["lookup_order found order-5"]);
    assert.equal(snapshot.messages.at(-1)?.text, "said: two");
    assert.deepEqual(snapshot.steeredMessages, []);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    assert.equal(
      history.filter((entry) => entry.stepType === "ApplyTurnControl" && entry.eventType === executeCompletedEvent)
        .length,
      1,
    );
    assert.deepEqual(payloadsOf(await sessions.listAgentEvents(sessionId), "turn_steered"), [
      { message_count: 2, steered_message_count: 2 },
    ]);
  });

  test("a Worker killed in FinishTurnCancellation ends the turn once", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const startWorker = async (): Promise<KillableWorkerProcess> => {
      const worker = await KillableWorkerProcess.start({
        entryModulePath: workerEntryModulePath,
        serverAddress: engine.serverAddress,
        logPath: join(markerDirectory, `worker-${workers.length + 1}.log`),
        environment: {
          [steeringMarkerDirectoryVariable]: markerDirectory,
          [steeringFinishCancellationGateVariable]: "finish",
        },
      });
      workers.push(worker);
      return worker;
    };
    const firstWorker = await startWorker();
    const sessionId = `cancel-kill-${newToken()}`;
    await sessions.startSession(sessionId, "publish launch notes");
    await sessions.waitForPendingInterrupt(sessionId, "publish_note");
    assert.equal(await sessions.cancelTurn(sessionId), true);
    const finishGate = new FileGate(markerDirectory, "finish");
    await finishGate.waitUntilReached();

    await firstWorker.kill();
    finishGate.release();
    await startWorker();
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(finishGate.reachedCount(), 2);
    assert.deepEqual(toolResults(snapshot.messages), [`publish_note ${toolCallCancelledByTurnCancellationResult}`]);
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(
      payloadsOf(events, "tool_call_finished").map((payload) => payload.status),
      ["rejected"],
    );
    assert.equal(events.filter((event) => event.type === "turn_cancelled").length, 1);
    const history = await readStepHistory(engine.serverAddress, sessionId);
    const executed = (stepType: string) =>
      history.filter((entry) => entry.stepType === stepType && entry.eventType === executeCompletedEvent).length;
    // Dex fails the killed attempt and retries it, so the decision commits once.
    assert.equal(executed("FinishTurnCancellation"), 1);
    assert.equal(executed("ApplyTurnControl"), 1);
  });
});

async function publishToChannel(
  engine: PrivateDexEngine,
  sessionId: string,
  channelName: string,
  value: Readonly<Record<string, unknown>>,
): Promise<void> {
  const payload = Buffer.from(JSON.stringify(value)).toString("base64");
  const request = {
    flowId: sessionId,
    messages: [{ channelName, value: { objValue: { encoding: "json", payload } } }],
  };
  await execFileAsync(process.env.DEXCLI ?? "dexcli", [
    "api",
    "call",
    "PublishToChannel",
    "--server",
    engine.serverAddress,
    "--yes",
    "--data",
    JSON.stringify(request),
  ]);
}

function newToken(): string {
  return randomUUID().slice(0, 8);
}

function describeMessages(messages: readonly AgentMessage[]): string[] {
  return messages.map((message) => `${message.turnId} ${message.role} ${message.toolName}`);
}

function toolResults(messages: readonly AgentMessage[]): string[] {
  return messages
    .filter((message) => message.role === "tool")
    .map((message) => `${message.toolName} ${message.toolOutput}`);
}

function payloadsOf(events: readonly AgentEvent[], eventType: string): Record<string, unknown>[] {
  return events
    .filter((event) => event.type === eventType)
    .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
}

async function collectChunks<T>(chunks: AsyncIterable<T>): Promise<T[]> {
  const collected: T[] = [];
  for await (const chunk of chunks) {
    collected.push(chunk);
  }
  return collected;
}
