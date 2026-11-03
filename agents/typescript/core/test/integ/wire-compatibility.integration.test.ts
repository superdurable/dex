// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import {
  decodeAgentInterrupt,
  decodeAgentInterruptResolution,
  decodeAgentState,
  decodeAgentToolCall,
  decodeAgentUserMessage,
  encodeAgentInterrupt,
  encodeAgentInterruptResolution,
  encodeAgentState,
  encodeAgentToolCall,
  encodeAgentUserMessage,
} from "../../src/agent-session-codecs.js";
import type { Context, StepAttemptProgress } from "@superdurable/dex";

import { isToolNeverStarted } from "../../src/agent-session-steps.js";
import { isCancellableByTurnControl } from "../../src/agent-session-transitions.js";
import type { AgentToolCallRecord } from "../../src/index.js";

// Wire values exactly as @superdurable/dex-agents 0.1.0 encodes them.
const recordOf010 = {
  tool_call_id: "call-1",
  turn_id: "turn-1",
  tool_name: "send_email",
  arguments: '{"to":"ops"}',
  effect: "unclassified",
  requires_approval: true,
  status: "awaiting_approval",
  output: null,
  error: null,
  rejection_message: null,
  deferral_order: 0,
};
const interruptOf010 = {
  interrupt_id: "call-1",
  kind: "tool_approval",
  tool_call_id: "call-1",
  tool_name: "send_email",
  arguments: '{"to":"ops"}',
  created_at: "2026-11-01T12:00:00.000Z",
  deadline: null,
};
const resolutionOf010 = { approved: false, rejection_message: "not today" };
const stateOf010 = {
  status: "waiting_for_user_message",
  turn_count: 2,
  active_turn_id: null,
  next_message_sequence: 5,
  last_turn_error: null,
};
const userMessageOf010 = { text: "hello" };

describe("wire compatibility between 0.1.0 and 0.2.0", () => {
  test("values a 0.1.0 Worker wrote decode in 0.2.0 with the new fields at their defaults", () => {
    assert.deepEqual(decodeAgentToolCall(recordOf010), {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "send_email",
      arguments: '{"to":"ops"}',
      effect: "unclassified",
      requiresApproval: true,
      status: "awaiting_approval",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      waitUntil: null,
      inputContinuation: null,
      inputAnswers: [],
      inputQuestion: null,
      inputChoices: [],
      inputAllowsFreeText: true,
      cancellationReason: null,
      taskId: null,
      taskPollIntervalSeconds: null,
      taskAnswerPending: false,
      marksAttemptStarted: false,
    });
    assert.deepEqual(decodeAgentInterrupt(interruptOf010), {
      interruptId: "call-1",
      kind: "tool_approval",
      toolCallId: "call-1",
      toolName: "send_email",
      arguments: '{"to":"ops"}',
      createdAt: "2026-11-01T12:00:00.000Z",
      deadline: null,
      question: null,
      choices: [],
      allowsFreeText: true,
    });
    assert.deepEqual(decodeAgentState(stateOf010), {
      status: "waiting_for_user_message",
      turnCount: 2,
      activeTurnId: null,
      nextMessageSequence: 5,
      lastTurnError: null,
    });
    assert.deepEqual(decodeAgentUserMessage(userMessageOf010), { text: "hello" });
    // An explicit null reads as the default, as in the Python core.
    assert.deepEqual(decodeAgentUserMessage({ ...userMessageOf010, plan_mode: null }), { text: "hello" });
    assert.deepEqual(decodeAgentUserMessage({ ...userMessageOf010, plan_mode: true }), { text: "hello", planMode: true });
    assert.deepEqual(decodeAgentState({ ...stateOf010, interaction_mode: null }), decodeAgentState(stateOf010));
    assert.deepEqual(decodeAgentInterruptResolution(resolutionOf010), {
      approved: false,
      rejectionMessage: "not today",
      answer: null,
    });
  });

  test("a record built by hand without the optional fields reads them as their defaults", () => {
    // Compiles without cancellationReason, taskId, taskPollIntervalSeconds, or taskAnswerPending.
    const handBuiltRecord: AgentToolCallRecord = {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "send_email",
      arguments: '{"to":"ops"}',
      effect: "unclassified",
      requiresApproval: false,
      status: "awaiting_answer",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
      waitUntil: null,
      inputContinuation: null,
      inputAnswers: [],
      inputQuestion: "Which account?",
      inputChoices: [],
      inputAllowsFreeText: true,
    };

    assert.deepEqual(decodeAgentToolCall(encodeAgentToolCall(handBuiltRecord)), {
      ...handBuiltRecord,
      cancellationReason: null,
      taskId: null,
      taskPollIntervalSeconds: null,
      taskAnswerPending: false,
      marksAttemptStarted: false,
    });
    // A question with no taskId is not a task's question, so steering may still cancel it.
    assert.equal(isCancellableByTurnControl(handBuiltRecord), true);
    assert.equal(isCancellableByTurnControl({ ...handBuiltRecord, taskId: "task-1" }), false);
  });

  test("values 0.2.0 writes with no new option enabled keep every 0.1.0 field, so a 0.1.0 Worker decodes them", () => {
    const pairs: [Readonly<Record<string, unknown>>, Readonly<Record<string, unknown>>][] = [
      [recordOf010, encodeAgentToolCall(decodeAgentToolCall(recordOf010)) as Record<string, unknown>],
      [interruptOf010, encodeAgentInterrupt(decodeAgentInterrupt(interruptOf010)) as Record<string, unknown>],
      [
        resolutionOf010,
        encodeAgentInterruptResolution(decodeAgentInterruptResolution(resolutionOf010)) as Record<string, unknown>,
      ],
      [stateOf010, encodeAgentState(decodeAgentState(stateOf010)) as Record<string, unknown>],
      [userMessageOf010, encodeAgentUserMessage(decodeAgentUserMessage(userMessageOf010)) as Record<string, unknown>],
    ];
    for (const [valueOf010, valueOf020] of pairs) {
      // A 0.1.0 decoder reads only its own fields, so the 0.2.0 value must repeat each one unchanged.
      for (const [field, value] of Object.entries(valueOf010)) {
        assert.deepEqual(valueOf020[field], value, `field ${field}`);
      }
    }
  });
});

describe("a never-started call needs a stamped record unless no Worker received it", () => {
  const record = (marksAttemptStarted: boolean | undefined): AgentToolCallRecord => ({
    ...decodeAgentToolCall(recordOf010),
    status: "approved",
    ...(marksAttemptStarted === undefined ? {} : { marksAttemptStarted }),
  });
  const failed = (attemptProgress: StepAttemptProgress): Context =>
    ({ recoveryError: { detail: "lost", errorType: "FLOW_ERROR_TYPE_WORKER_API_FAIL", attemptProgress } }) as Context;

  test("a record 0.2.0 deferred counts assigned and not assigned as never started", () => {
    assert.equal(isToolNeverStarted(failed("notAssigned"), record(true)), true);
    assert.equal(isToolNeverStarted(failed("assigned"), record(true)), true);
    assert.equal(isToolNeverStarted(failed("started"), record(true)), false);
    assert.equal(isToolNeverStarted(failed("unspecified"), record(true)), false);
  });

  test("a record 0.1.0 deferred, which ran its tool without a mark, keeps assigned as possibly run", () => {
    assert.equal(isToolNeverStarted(failed("assigned"), record(undefined)), false);
    assert.equal(isToolNeverStarted(failed("assigned"), record(false)), false);
    assert.equal(isToolNeverStarted(failed("notAssigned"), record(undefined)), true);
  });

  test("a call waiting for its task is never counted as not run", () => {
    assert.equal(
      isToolNeverStarted(failed("notAssigned"), { ...record(true), status: "waiting_for_task", taskId: "task-1" }),
      false,
    );
  });
});
