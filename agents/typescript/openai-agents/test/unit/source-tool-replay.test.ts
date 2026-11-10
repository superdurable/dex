// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// How a resumed RunModel replays tool source calls. These states need a listing that changes between two model
// calls or a forged record, so the tests call the runner directly.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import {
  Agent,
  Usage,
  type AgentInputItem,
  type Model,
  type ModelRequest,
  type ModelResponse,
  type StreamEvent,
  type protocol,
} from "@openai/agents";
import {
  resolveToolPolicy,
  type AgentEventWriter,
  type AgentToolCall,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type SourceToolDefinition,
  type ToolExecutionResult,
  type ToolSource,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";

const usage = { requests: 1, inputTokens: 1, outputTokens: 1, totalTokens: 2 };
const lookupTool: SourceToolDefinition = {
  name: "lookup",
  description: "Look a record up.",
  inputSchema: { type: "object", properties: {} },
};

const fetchTool: SourceToolDefinition = {
  name: "fetch",
  description: "Fetch a page.",
  inputSchema: { type: "object", properties: {} },
};

/** A source whose listing a test changes between model calls. */
class ChangingToolSource implements ToolSource {
  public readonly sourceName = "changing";
  public listing: readonly SourceToolDefinition[] = [lookupTool];

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return this.listing;
  }

  public deferredToolCall(
    _turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return { ...call, policy: { effect: "read", requiresApproval: false }, toolSourceReference: "lookup" };
  }

  public async executeDeferredToolCall(): Promise<ToolExecutionResult> {
    throw new Error("a replay never runs the source");
  }

  public async pollToolTask(): Promise<ToolTaskPollOutcome> {
    throw new Error("the source starts no tasks");
  }
}

/** A model whose reply is a function of its input. */
class FunctionModel implements Model {
  public constructor(private readonly decide: (input: readonly AgentInputItem[]) => protocol.OutputModelItem[]) {}

  public async getResponse(request: ModelRequest): Promise<ModelResponse> {
    return { usage: new Usage(usage), output: this.decide(readInput(request)), responseId: "resp_function" };
  }

  public async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    yield { type: "response_started" };
    const output = this.decide(readInput(request));
    yield { type: "response_done", response: { id: "resp_function", usage, output } };
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

describe("replay of tool source calls in the OpenAI Agents runner", () => {
  test("a call whose tool the listing lacks replays its record, and a new call of that tool is denied", async () => {
    const source = new ChangingToolSource();
    const { runModelStep, recordOf } = createReplayHarness(source, (input) => {
      const results = readResults(input);
      if (!results.has("c1")) {
        return [functionCall("c1", "lookup")];
      }
      if (!results.has("c2")) {
        return [functionCall("c2", "lookup")];
      }
      return [answer(`c1=${results.get("c1")} | c2=${results.get("c2")}`)];
    });
    const first = await runModelStep(null, []);
    assert.equal(first.kind, "tool_calls_requested");
    const record = recordOf(first, "c1", "lookup");
    source.listing = [];
    const second = await runModelStep(first.kind === "tool_calls_requested" ? first.frameworkRunState : null, [record]);
    assert.equal(second.kind, "completed");
    const text = second.kind === "completed" ? second.newMessages.at(-1)?.text : undefined;
    assert.equal(text, "c1=found | c2=The tool call was not run: its tool source does not offer lookup now.");
  });

  test("a new call of a replay-only tool with invalid JSON arguments ends in the turn without a Step", async () => {
    const source = new ChangingToolSource();
    const { runModelStep, recordOf } = createReplayHarness(source, (input) => {
      const results = readResults(input);
      if (!results.has("c1")) {
        return [functionCall("c1", "lookup")];
      }
      if (!results.has("c2")) {
        return [functionCall("c2", "lookup", "{not json")];
      }
      return [answer(`c1=${results.get("c1")} | c2=${results.get("c2")}`)];
    });
    const first = await runModelStep(null, []);
    const record = recordOf(first, "c1", "lookup");
    source.listing = [];
    const second = await runModelStep(first.kind === "tool_calls_requested" ? first.frameworkRunState : null, [record]);
    assert.equal(second.kind, "completed");
    const text = second.kind === "completed" ? second.newMessages.at(-1)?.text : undefined;
    // The SDK answers invalid arguments itself before any approval, as for every function tool.
    assert.equal(text, "c1=found | c2=An error occurred while parsing tool arguments. Please try again with valid JSON.");
  });

  test("a refused replay-only call next to a listed source call leaves only the listed call to run", async () => {
    const source = new ChangingToolSource();
    source.listing = [lookupTool, fetchTool];
    const { runModelStep, recordOf } = createReplayHarness(source, (input) => {
      const results = readResults(input);
      if (!results.has("c1")) {
        return [functionCall("c1", "lookup")];
      }
      if (!results.has("c2")) {
        return [functionCall("c2", "lookup"), functionCall("c3", "fetch")];
      }
      return [answer(`c2=${results.get("c2")} | c3=${results.get("c3")}`)];
    });
    const first = await runModelStep(null, []);
    const lookupRecord = recordOf(first, "c1", "lookup");
    source.listing = [fetchTool];
    const second = await runModelStep(
      first.kind === "tool_calls_requested" ? first.frameworkRunState : null,
      [lookupRecord],
    );
    assert.equal(second.kind, "tool_calls_requested");
    assert.deepEqual(
      second.kind === "tool_calls_requested" ? second.toolCalls.map((toolCall) => toolCall.toolCallId) : [],
      ["c3"],
    );
    const fetchRecord = { ...recordOf(second, "c3", "fetch"), deferralOrder: 1 };
    const third = await runModelStep(
      second.kind === "tool_calls_requested" ? second.frameworkRunState : null,
      [lookupRecord, fetchRecord],
    );
    assert.equal(third.kind, "completed");
    assert.equal(
      third.kind === "completed" ? third.newMessages.at(-1)?.text : undefined,
      "c2=The tool call was not run: its tool source does not offer lookup now. | c3=found",
    );
  });

  for (const status of ["succeeded", "rejected"] as const) {
    test(`a ${status} record of another tool under the requested call ID never replays`, async () => {
      const source = new ChangingToolSource();
      const { runModelStep, recordOf } = createReplayHarness(source, (input) =>
        readResults(input).size === 0 ? [functionCall("c1", "lookup")] : [answer("done")],
      );
      const first = await runModelStep(null, []);
      const forged: AgentToolCall = {
        ...recordOf(first, "c1", "lookup"),
        toolName: "other",
        status,
        ...(status === "rejected" ? { output: null, rejectionMessage: "no" } : {}),
      };
      await assert.rejects(
        runModelStep(first.kind === "tool_calls_requested" ? first.frameworkRunState : null, [forged]),
        /tool call c1 of lookup with the record of tool other/,
      );
    });
  }

  for (const status of ["succeeded", "rejected", "failed", "outcome_unknown"] as const) {
    test(`a run state that holds one ${status} call ID twice never replays it twice`, async () => {
      const source = new ChangingToolSource();
      const { runModelStep, recordOf } = createReplayHarness(source, (input) =>
        readResults(input).size === 0 ? [functionCall("c1", "lookup")] : [answer("done")],
      );
      const first = await runModelStep(null, []);
      const record: AgentToolCall = {
        ...recordOf(first, "c1", "lookup"),
        status,
        ...(status === "succeeded" ? {} : { output: null }),
        ...(status === "rejected" ? { rejectionMessage: "no" } : {}),
        ...(status === "failed" || status === "outcome_unknown" ? { error: "provider error" } : {}),
      };
      const duplicated = duplicateInterruption(first.kind === "tool_calls_requested" ? first.frameworkRunState : "{}");
      await assert.rejects(runModelStep(duplicated, [record]), /resumed tool call c1 more than once/);
    });
  }

  test("a later model call that requests a replayed call ID again fails instead of replaying twice", async () => {
    const source = new ChangingToolSource();
    const { runModelStep, recordOf } = createReplayHarness(source, () => [functionCall("c1", "lookup")]);
    const first = await runModelStep(null, []);
    const record = recordOf(first, "c1", "lookup");
    await assert.rejects(
      runModelStep(first.kind === "tool_calls_requested" ? first.frameworkRunState : null, [record]),
      /reuses an earlier tool call ID c1/,
    );
  });
});

function createReplayHarness(
  source: ToolSource,
  decide: (input: readonly AgentInputItem[]) => protocol.OutputModelItem[],
): {
  runModelStep(frameworkRunState: string | null, records: readonly AgentToolCall[]): Promise<AgentTurnOutcome>;
  recordOf(outcome: AgentTurnOutcome, toolCallId: string, toolName: string): AgentToolCall;
} {
  const agent = new Agent({ name: "Replay", model: new FunctionModel(decide) });
  const flow = createOpenAIAgentsSessionFlow(agent, "ReplayAgent", {
    runConfig: { tracingDisabled: true },
    toolSources: [source],
  });
  const userMessage = {
    ...flow.runner.buildUserMessage("look it up"),
    sequence: 1,
    turnId: "turn-1",
    createdAt: new Date(0).toISOString(),
  };
  return {
    runModelStep: (frameworkRunState, records) => {
      const turn: AgentTurnContext = {
        flowId: "replay-session",
        turnId: "turn-1",
        attempt: 1,
        messages: [userMessage],
        frameworkRunState,
        isResuming: frameworkRunState !== null,
        toolCalls: records,
        events: silentEvents,
        cancellationSignal: new AbortController().signal,
        deadline: undefined,
        resolveToolPolicy: (toolName, frameworkEffect) =>
          resolveToolPolicy(toolName, {}, "unclassified", frameworkEffect),
        recordedToolCall: (toolCallId) => records.find((record) => record.toolCallId === toolCallId),
        heartbeat: async () => undefined,
      };
      return flow.runner.runModelStep(turn);
    },
    recordOf: (outcome, toolCallId, toolName) => {
      assert.equal(outcome.kind, "tool_calls_requested");
      const call = outcome.kind === "tool_calls_requested"
        ? outcome.toolCalls.find((toolCall) => toolCall.toolCallId === toolCallId)
        : undefined;
      assert.ok(call !== undefined);
      assert.equal(call.toolName, toolName);
      return {
        toolCallId,
        turnId: "turn-1",
        toolName: call.toolName,
        arguments: call.arguments,
        effect: call.policy.effect,
        requiresApproval: call.policy.requiresApproval,
        status: "succeeded",
        output: "found",
        error: null,
        rejectionMessage: null,
        deferralOrder: 0,
        ...(call.toolSourceReference === undefined ? {} : { toolSourceReference: call.toolSourceReference }),
      };
    },
  };
}

/** The serialized RunState with its first pending interruption listed twice, as a corrupt store could hold it. */
function duplicateInterruption(frameworkRunState: string): string {
  const runState = JSON.parse(frameworkRunState) as {
    readonly currentStep: { readonly type: string; readonly data: { readonly interruptions: unknown[] } };
    readonly generatedItems: unknown[];
    readonly currentResponseGeneratedItemOwnership: {
      generatedItemEndIndexExclusive: number;
      readonly interruptionGeneratedItemIndexes: number[];
    };
  };
  assert.equal(runState.currentStep.type, "next_step_interruption");
  const [interruption] = runState.currentStep.data.interruptions;
  const ownership = runState.currentResponseGeneratedItemOwnership;
  const [approvalItemIndex] = ownership.interruptionGeneratedItemIndexes;
  assert.ok(interruption !== undefined && approvalItemIndex !== undefined);
  assert.equal(ownership.generatedItemEndIndexExclusive, runState.generatedItems.length);
  runState.currentStep.data.interruptions.push(structuredClone(interruption));
  ownership.interruptionGeneratedItemIndexes.push(runState.generatedItems.length);
  runState.generatedItems.push(structuredClone(runState.generatedItems[approvalItemIndex]));
  ownership.generatedItemEndIndexExclusive = runState.generatedItems.length;
  return JSON.stringify(runState);
}

function functionCall(callId: string, name: string, toolArguments = "{}"): protocol.OutputModelItem {
  return { type: "function_call", id: `fc_${callId}`, callId, name, arguments: toolArguments, status: "completed" };
}

function answer(text: string): protocol.OutputModelItem {
  return {
    type: "message",
    id: "msg_function",
    role: "assistant",
    status: "completed",
    content: [{ type: "output_text", text }],
  };
}

function readResults(input: readonly AgentInputItem[]): Map<string, string> {
  const results = new Map<string, string>();
  for (const item of input) {
    if (item.type === "function_call_result") {
      const output: unknown = item.output;
      results.set(
        item.callId,
        typeof output === "string"
          ? output
          : typeof output === "object" && output !== null && "text" in output
            ? String(output.text)
            : JSON.stringify(output),
      );
    }
  }
  return results;
}

function readInput(request: ModelRequest): AgentInputItem[] {
  return typeof request.input === "string" ? [{ type: "message", role: "user", content: request.input }] : request.input;
}
