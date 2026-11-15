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

import { readUnfinishedTurnItems } from "../../src/openai-agents-unfinished-turns.js";

const textShape = () => ({ hasOutputSchema: false, showsToolImages: false });

describe("readUnfinishedTurnItems", () => {
  test("answers a pending call without a final record with the unknown-outcome text", () => {
    const items = readUnfinishedTurnItems(createTurn(serializeRunState([functionCall("call_a")]), []), textShape);
    assert.deepEqual(items.at(-1), {
      type: "function_call_result",
      name: "lookup",
      callId: "call_a",
      status: "completed",
      output: { type: "text", text: outcomeUnknownToolResult },
    });
  });

  test("throws for a run state that is not a serialized RunState", () => {
    for (const frameworkRunState of ["not json", '{"format":"dex-agents-langgraph.checkpoint.v1"}', '{"$schemaVersion":"1.23"}']) {
      assert.throws(() => readUnfinishedTurnItems(createTurn(frameworkRunState, []), textShape), Error);
    }
  });

  test("throws when a record's call is not in the run state", () => {
    const turn = createTurn(serializeRunState([functionCall("call_a")]), [record("call_b")]);
    assert.throws(() => readUnfinishedTurnItems(turn, textShape), /call_b .* is not in the run state/);
  });

  test("throws when a pending call comes before the last model response", () => {
    const message = { type: "message_output_item", rawItem: { type: "message", role: "assistant", content: [] } };
    const turn = createTurn(serializeRunState([functionCall("call_a"), message]), []);
    assert.throws(() => readUnfinishedTurnItems(turn, textShape), /before the last model response/);
  });
});

function serializeRunState(generatedItems: readonly unknown[]): string {
  return JSON.stringify({ $schemaVersion: "1.23", generatedItems });
}

function functionCall(callId: string): unknown {
  return {
    type: "tool_call_item",
    rawItem: { type: "function_call", callId, name: "lookup", status: "completed", arguments: "{}" },
  };
}

function record(toolCallId: string): AgentToolCall {
  return { toolCallId, toolName: "lookup", status: "succeeded", output: "found" } as unknown as AgentToolCall;
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
