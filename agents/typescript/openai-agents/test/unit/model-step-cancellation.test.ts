// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Dex discards a cancelled attempt's result, so only a direct runner call can observe the outcome.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { Agent, tool, type Model, type ModelRequest, type StreamEvent, type protocol } from "@openai/agents";
import {
  resolveToolPolicy,
  type AgentEventWriter,
  type AgentTurnContext,
} from "@superdurable/dex-agents";
import { z } from "zod";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";

const usage = {
  requests: 1,
  inputTokens: 1,
  outputTokens: 1,
  totalTokens: 2,
};

/** A model that aborts the attempt's signal mid-stream and still finishes its response. */
class AbortingModel implements Model {
  public constructor(
    private readonly cancellation: AbortController,
    private readonly output: protocol.OutputModelItem[],
  ) {}

  public async getResponse(): Promise<never> {
    throw new Error("the cancellation test streams every model call");
  }

  public async *getStreamedResponse(_request: ModelRequest): AsyncIterable<StreamEvent> {
    yield { type: "response_started" };
    this.cancellation.abort(new Error("Dex cancelled the attempt"));
    yield { type: "response_done", response: { id: "resp_aborted", usage, output: this.output } };
  }
}

const silentEvents: AgentEventWriter = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};

async function runCancelledModelStep(output: protocol.OutputModelItem[]): Promise<unknown> {
  const cancellation = new AbortController();
  const sendEmail = tool({
    name: "send_email",
    description: "Send an email.",
    parameters: z.object({ to: z.string() }),
    errorFunction: null,
    execute: () => "sent",
  });
  const agent = new Agent({ name: "Cancelled", model: new AbortingModel(cancellation, output), tools: [sendEmail] });
  const flow = createOpenAIAgentsSessionFlow(agent, "CancelledAgent", { runConfig: { tracingDisabled: true } });
  const turn: AgentTurnContext = {
    flowId: "cancelled-session",
    turnId: "turn-1",
    attempt: 1,
    messages: [
      {
        ...flow.runner.buildUserMessage("send it"),
        sequence: 1,
        turnId: "turn-1",
        createdAt: new Date(0).toISOString(),
      },
    ],
    frameworkRunState: null,
    isResuming: false,
    toolCalls: [],
    events: silentEvents,
    cancellationSignal: cancellation.signal,
    deadline: undefined,
    resolveToolPolicy: (toolName, frameworkEffect) => resolveToolPolicy(toolName, {}, "unclassified", frameworkEffect),
    recordedToolCall: () => undefined,
    heartbeat: async () => undefined,
  };
  return flow.runner.runModelStep(turn);
}

describe("runModelStep cancellation", () => {
  test("a model call cancelled after it asked for a tool fails the attempt instead of completing", async () => {
    await assert.rejects(
      runCancelledModelStep([
        { type: "function_call", callId: "call_1", name: "send_email", arguments: '{"to":"ana"}', status: "completed" },
      ]),
      /Dex cancelled the attempt/,
    );
  });

  test("a model call cancelled after its final answer fails the attempt instead of completing", async () => {
    await assert.rejects(
      runCancelledModelStep([
        {
          type: "message",
          role: "assistant",
          status: "completed",
          content: [{ type: "output_text", text: "done" }],
        },
      ]),
      /Dex cancelled the attempt/,
    );
  });
});
