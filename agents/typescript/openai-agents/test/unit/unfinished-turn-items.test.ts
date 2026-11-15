// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Run states that sessions rarely produce, so the tests read them directly.

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { outcomeUnknownToolResult, type AgentToolCall, type AgentTurnContext } from "@superdurable/dex-agents";

import {
  readUnfinishedTurnItems,
  type UnfinishedTurnItemOptions,
} from "../../src/openai-agents-unfinished-turns.js";

const options: UnfinishedTurnItemOptions = {
  showsToolImages: false,
  inspectFunctionCall: (functionCall) => ({
    isRecordOfCall: (record) => record.toolName === functionCall.name,
    hasOutputSchema: false,
  }),
};

describe("readUnfinishedTurnItems", () => {
  test("answers a pending call without a final record with the unknown-outcome text", () => {
    const items = readUnfinishedTurnItems(createTurn(serializeRunState([functionCall("call_a")]), []), options);
    assert.deepEqual(items.at(-1), {
      type: "function_call_result",
      name: "lookup",
      callId: "call_a",
      status: "completed",
      output: { type: "text", text: outcomeUnknownToolResult },
    });
  });

  test("throws for a run state that is not a serialized RunState of a known schema", () => {
    for (const frameworkRunState of [
      "not json",
      '{"format":"dex-agents-langgraph.checkpoint.v1"}',
      '{"$schemaVersion":"1.23"}',
      '{"$schemaVersion":"999","generatedItems":[]}',
    ]) {
      assert.throws(() => readUnfinishedTurnItems(createTurn(frameworkRunState, []), options), Error);
    }
  });

  test("throws for an unknown run item or a run item of another raw item type", () => {
    for (const runItem of [
      { type: "future_run_item", rawItem: { type: "future_call", opaque: "x" } },
      { type: "tool_call_item", rawItem: { type: "future_call", callId: "call_a" } },
      { type: "tool_call_item", rawItem: { type: "program", callId: "program_a" } },
    ]) {
      assert.throws(() => readUnfinishedTurnItems(createTurn(serializeRunState([runItem]), []), options), Error);
    }
  });

  test("throws for a programmatic call, whose program pairing is not mirrored", () => {
    const programCall = functionCall("call_a", { caller: { type: "program", callerId: "program_a" } });
    assert.throws(
      () => readUnfinishedTurnItems(createTurn(serializeRunState([programCall]), []), options),
      /belongs to a program/,
    );
  });

  test("throws when a result comes before its call or twice", () => {
    for (const generatedItems of [
      [functionResult("call_a"), functionCall("call_a")],
      [functionCall("call_a"), functionResult("call_a"), functionResult("call_a")],
      [functionCall("call_a"), functionResult("call_b")],
      [functionCall("call_a"), functionCall("call_b"), functionResult("call_a"), functionCall("call_c")],
    ]) {
      assert.throws(
        () => readUnfinishedTurnItems(createTurn(serializeRunState(generatedItems), []), options),
        /does not follow one open call|starts before earlier calls/,
      );
    }
  });

  test("throws when a pending call comes before a later message", () => {
    const message = { type: "message_output_item", rawItem: { type: "message", role: "assistant", content: [] } };
    const turn = createTurn(serializeRunState([functionCall("call_a"), message]), []);
    assert.throws(() => readUnfinishedTurnItems(turn, options), /before earlier calls have results/);
  });

  test("throws when a record's call is not in the run state or names another tool", () => {
    const runState = serializeRunState([functionCall("call_a")]);
    assert.throws(() => readUnfinishedTurnItems(createTurn(runState, [record("call_b", "lookup")]), options), /not in the run state/);
    assert.throws(
      () => readUnfinishedTurnItems(createTurn(runState, [record("call_a", "charge")]), options),
      /recorded for tool charge/,
    );
  });

  test("throws for a pending call of a tool with an output schema", () => {
    const schemaOptions: UnfinishedTurnItemOptions = {
      ...options,
      inspectFunctionCall: (functionCall) => ({ ...options.inspectFunctionCall(functionCall), hasOutputSchema: true }),
    };
    const turn = createTurn(serializeRunState([functionCall("call_a")]), [record("call_a", "lookup")]);
    assert.throws(() => readUnfinishedTurnItems(turn, schemaOptions), /output schema/);
  });
});

function serializeRunState(generatedItems: readonly unknown[]): string {
  return JSON.stringify({ $schemaVersion: "1.23", generatedItems });
}

function functionCall(callId: string, extra: Record<string, unknown> = {}): unknown {
  return {
    type: "tool_call_item",
    rawItem: { type: "function_call", callId, name: "lookup", status: "completed", arguments: "{}", ...extra },
  };
}

function functionResult(callId: string): unknown {
  return {
    type: "tool_call_output_item",
    rawItem: { type: "function_call_result", callId, name: "lookup", status: "completed", output: "found" },
  };
}

function record(toolCallId: string, toolName: string): AgentToolCall {
  return { toolCallId, toolName, status: "succeeded", output: "found" } as unknown as AgentToolCall;
}

function createTurn(frameworkRunState: string, toolCalls: readonly AgentToolCall[]): AgentTurnContext {
  return {
    turnId: "turn-1",
    frameworkRunState,
    toolCalls,
    messages: [],
    recordedToolCall: (toolCallId: string) => toolCalls.find((toolCall) => toolCall.toolCallId === toolCallId),
  } as unknown as AgentTurnContext;
}
