// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// These calls never reach the tool, so a direct runner call checks how each failure is classified.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { Agent, ToolGuardrailFunctionOutputFactory, defineToolInputGuardrail, tool } from "@openai/agents";
import { ToolExecutionError, type AgentEventWriter, type DeferredToolExecution } from "@superdurable/dex-agents";
import { z } from "zod";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";

const silentEvents: AgentEventWriter = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};

async function executeSendEmail(
  toolArguments: string,
  guardrailOutput: ReturnType<typeof ToolGuardrailFunctionOutputFactory.allow>,
): Promise<{ readonly output: string; readonly runCount: number }> {
  let runCount = 0;
  const sendEmail = tool({
    name: "send_email",
    description: "Send an email.",
    parameters: z.object({ to: z.string() }),
    errorFunction: null,
    inputGuardrails: [defineToolInputGuardrail({ name: "recipient_check", run: async () => guardrailOutput })],
    execute: () => {
      runCount += 1;
      return "sent";
    },
  });
  const flow = createOpenAIAgentsSessionFlow(new Agent({ name: "Mailer", tools: [sendEmail] }), "MailerAgent");
  const execution: DeferredToolExecution = {
    flowId: "mailer-session",
    toolCall: {
      toolCallId: "call_1",
      turnId: "turn-1",
      toolName: "send_email",
      arguments: toolArguments,
      effect: "unclassified",
      requiresApproval: true,
      status: "approved",
      output: null,
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
    },
    idempotencyKey: "mailer-session/turn-1/call_1",
    attempt: 1,
    events: silentEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: async () => undefined,
  };
  const result = await flow.runner.executeDeferredToolCall(execution);
  return { output: result.output, runCount };
}

describe("executeDeferredToolCall failure classification", () => {
  test("arguments that fail the tool schema record failed, because the tool never ran", async () => {
    await assert.rejects(
      executeSendEmail('{"to":5}', ToolGuardrailFunctionOutputFactory.allow()),
      (error: unknown) => error instanceof ToolExecutionError,
    );
  });

  test("an input guardrail rejection and a tripwire record failed before the tool runs", async () => {
    await assert.rejects(
      executeSendEmail('{"to":"ana"}', ToolGuardrailFunctionOutputFactory.rejectContent("recipient not allowed")),
      (error: unknown) => error instanceof ToolExecutionError && error.message === "recipient not allowed",
    );
    await assert.rejects(
      executeSendEmail('{"to":"ana"}', ToolGuardrailFunctionOutputFactory.throwException()),
      (error: unknown) => error instanceof ToolExecutionError,
    );
  });

  test("valid arguments that pass the guardrail run the tool once", async () => {
    assert.deepEqual(await executeSendEmail('{"to":"ana"}', ToolGuardrailFunctionOutputFactory.allow()), {
      output: "sent",
      runCount: 1,
    });
  });
});
