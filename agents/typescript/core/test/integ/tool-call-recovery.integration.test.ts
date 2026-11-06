// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { StepAttemptProgress, type RecoveryErrorInfo } from "@superdurable/dex";

import { recoveredToolCall } from "../../src/agent-session-steps.js";
import {
  toolCallNotRunErrorPrefix,
  toolTaskAbandonedError,
  type AgentToolCallRecord,
} from "../../src/agent-session-values.js";

const approvedCall: AgentToolCallRecord = {
  toolCallId: "call-1",
  turnId: "turn-1",
  toolName: "publish",
  arguments: "{}",
  effect: "non_idempotent_write",
  requiresApproval: false,
  status: "approved",
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
};

function failure(attemptProgress: StepAttemptProgress): RecoveryErrorInfo {
  return { detail: "worker lost", errorType: "FLOW_ERROR_TYPE_WORKER_API_FAIL", attemptProgress };
}

describe("the record a tool Step's failure route writes", () => {
  test("a call whose tool Step never started records failed, safe to call again", () => {
    for (const status of ["outcome_unknown", "failed"] as const) {
      const recorded = recoveredToolCall(approvedCall, failure(StepAttemptProgress.NOT_STARTED), status);
      assert.equal(recorded.status, "failed", status);
      assert.equal(recorded.error, `${toolCallNotRunErrorPrefix}: worker lost`, status);
    }
  });

  test("a call whose tool may have run keeps the status of its effect", () => {
    for (const progress of [StepAttemptProgress.NOT_COMPLETED, StepAttemptProgress.UNSPECIFIED]) {
      const recorded = recoveredToolCall(approvedCall, failure(progress), "outcome_unknown");
      assert.equal(recorded.status, "outcome_unknown", progress);
      assert.equal(recorded.error, "worker lost", progress);
    }
    const withoutProgress = recoveredToolCall(
      approvedCall,
      { detail: "worker lost", errorType: "FLOW_ERROR_TYPE_WORKER_API_FAIL" },
      "outcome_unknown",
    );
    assert.equal(withoutProgress.status, "outcome_unknown");
  });

  test("a failure without a recovery error keeps outcome_unknown", () => {
    const recorded = recoveredToolCall(approvedCall, undefined, "outcome_unknown");
    assert.equal(recorded.status, "outcome_unknown");
    assert.equal(recorded.error, "unknown failure");
  });

  test("a task poll that never started keeps outcome_unknown, because the task ran", () => {
    const waiting: AgentToolCallRecord = { ...approvedCall, status: "waiting_for_task", taskId: "task-1" };
    const recorded = recoveredToolCall(waiting, failure(StepAttemptProgress.NOT_STARTED), "outcome_unknown");
    assert.equal(recorded.status, "outcome_unknown");
    assert.equal(recorded.error, "worker lost");
  });

  test("an abandoned task whose cancellation never started keeps its abandoned error", () => {
    const abandoned: AgentToolCallRecord = {
      ...approvedCall,
      status: "waiting_for_task",
      taskId: "task-1",
      cancellationReason: "turn_cancelled",
    };
    const recorded = recoveredToolCall(abandoned, failure(StepAttemptProgress.NOT_STARTED), "outcome_unknown");
    assert.equal(recorded.status, "outcome_unknown");
    assert.equal(recorded.error, toolTaskAbandonedError);
  });

  test("a call that asked a question already ran its tool", () => {
    const answered: AgentToolCallRecord = {
      ...approvedCall,
      inputAnswers: [{ interruptId: "call-1-input-1", question: "Which list?", answer: "news" }],
    };
    const recorded = recoveredToolCall(answered, failure(StepAttemptProgress.NOT_STARTED), "outcome_unknown");
    assert.equal(recorded.status, "outcome_unknown");
    assert.equal(recorded.error, "worker lost");
  });
});
