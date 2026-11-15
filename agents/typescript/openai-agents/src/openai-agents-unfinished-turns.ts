// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentInputItem, protocol } from "@openai/agents";
import {
  describeToolCallOutcome,
  isFinalToolCallStatus,
  outcomeUnknownToolResult,
  type AgentToolCall,
  type AgentTurnContext,
} from "@superdurable/dex-agents";

import { buildToolImageResultOutput } from "./openai-agents-images.js";

/** What the runner's agent graph says about one function call of the run state. */
export interface UnfinishedTurnFunctionCall {
  /** Whether the record may be this call's record: the record names the call's tool. */
  readonly isRecordOfCall: (record: AgentToolCall) => boolean;
  /** Whether the call's function tool has an output schema, whose recorded result only the SDK can encode. */
  readonly hasOutputSchema: boolean;
}

/** Settings of {@link readUnfinishedTurnItems}. */
export interface UnfinishedTurnItemOptions {
  /** Whether the runner has a media store, so a record's images reach the model. */
  readonly showsToolImages: boolean;
  /** Describes a function call of the run state. */
  readonly inspectFunctionCall: (functionCall: protocol.FunctionCallItem) => UnfinishedTurnFunctionCall;
}

type RawItem = Record<string, unknown>;

// The RunState schema versions whose items this reader mirrors; the SDK's tests pin the current one.
const supportedSchemaVersions = new Set(["1.23"]);
// The SDK's getRunOutput leaves out approval placeholders and input admitted while resuming.
const skippedRunItemTypes = new Set(["tool_approval_item", "input_item"]);
// The run item types this reader mirrors, with the raw item types each may hold.
const rawItemTypesByRunItemType: ReadonlyMap<string, ReadonlySet<string>> = new Map([
  ["message_output_item", new Set(["message"])],
  ["tool_call_item", new Set(["function_call", "hosted_tool_call"])],
  ["tool_call_output_item", new Set(["function_call_result"])],
  ["reasoning_item", new Set(["reasoning"])],
  ["handoff_call_item", new Set(["function_call"])],
  ["handoff_output_item", new Set(["function_call_result"])],
  ["compaction_item", new Set(["compaction"])],
]);

/**
 * Reads the turn's items from the serialized `RunState` and answers every pending function call.
 *
 * It mirrors the SDK's `getRunOutput` on the serialized generated items, without dropping pending calls. Each
 * pending call gets the `function_call_result` the resumed run would make from its final record, or the
 * unknown-outcome text when the call has no final record. It accepts only what it can mirror exactly.
 *
 * @param turn - The ended turn; its `frameworkRunState` is the `RunState` of the last `ToolCallsRequested`.
 * @param options - How the runner shows a recorded result and identifies a call's tool.
 * @returns The turn's items not stored yet, in order.
 * @throws {Error} When the run state has an unknown schema or item, its calls and results are not in order, a
 * record names another tool or has no call, or a pending call's tool has an output schema; the session then
 * stores its note instead.
 */
export function readUnfinishedTurnItems(turn: AgentTurnContext, options: UnfinishedTurnItemOptions): AgentInputItem[] {
  if (turn.frameworkRunState === null) {
    if (turn.toolCalls.length > 0) {
      throw new Error(`turn ${turn.turnId} has tool call records but no run state`);
    }
    return [];
  }
  const runState = parseRunState(turn.frameworkRunState);
  const items = trimToLatestCompaction(
    runState.generatedItems
      .filter((runItem) => !skippedRunItemTypes.has(runItem.type))
      .map((runItem) => toOutputItem(runItem, runState.reasoningItemIdPolicy)),
  );
  const { functionCalls, pendingCalls } = pairCallsWithResults(turn, items);
  const recordsById = new Map(turn.toolCalls.map((record) => [record.toolCallId, record]));
  for (const record of turn.toolCalls) {
    const functionCall = functionCalls.get(record.toolCallId);
    if (functionCall === undefined) {
      throw new Error(`tool call ${record.toolCallId} of turn ${turn.turnId} is not in the run state`);
    }
    if (!options.inspectFunctionCall(functionCall).isRecordOfCall(record)) {
      throw new Error(`tool call ${record.toolCallId} of turn ${turn.turnId} is recorded for tool ${record.toolName}`);
    }
  }
  const storedCallIds = new Set(
    turn.messages.filter((message) => message.turnId === turn.turnId).map((message) => message.toolCallId),
  );
  for (const callId of functionCalls.keys()) {
    if (storedCallIds.has(callId)) {
      throw new Error(`tool call ${callId} of turn ${turn.turnId} is already stored`);
    }
  }
  const pendingResults = pendingCalls.map((functionCall) =>
    buildPendingResult(turn, functionCall, recordsById.get(functionCall.callId), options),
  );
  return [...items, ...pendingResults] as unknown as AgentInputItem[];
}

interface SerializedRunState {
  readonly generatedItems: readonly { readonly type: string; readonly rawItem: RawItem }[];
  readonly reasoningItemIdPolicy: unknown;
}

function parseRunState(frameworkRunState: string): SerializedRunState {
  const document: unknown = JSON.parse(frameworkRunState);
  if (!isRecord(document) || typeof document["$schemaVersion"] !== "string") {
    throw new Error("the framework run state is not a serialized OpenAI Agents SDK RunState");
  }
  const schemaVersion = document["$schemaVersion"];
  if (!supportedSchemaVersions.has(schemaVersion)) {
    throw new Error(`RunState schema ${schemaVersion} is not one this runner reads without the SDK`);
  }
  const generatedItems = document["generatedItems"];
  if (!Array.isArray(generatedItems)) {
    throw new Error(`RunState schema ${schemaVersion} has no generatedItems list`);
  }
  return {
    generatedItems: generatedItems.map((runItem: unknown, index) => readRunItem(runItem, index)),
    reasoningItemIdPolicy: document["reasoningItemIdPolicy"],
  };
}

function readRunItem(runItem: unknown, index: number): { readonly type: string; readonly rawItem: RawItem } {
  if (!isRecord(runItem) || typeof runItem["type"] !== "string" || !isRecord(runItem["rawItem"])) {
    throw new Error(`generated item ${index} of the RunState has no type or rawItem`);
  }
  const type = runItem["type"];
  const rawItem = runItem["rawItem"];
  if (skippedRunItemTypes.has(type)) {
    return { type, rawItem };
  }
  const rawItemTypes = rawItemTypesByRunItemType.get(type);
  if (rawItemTypes === undefined || !rawItemTypes.has(String(rawItem["type"]))) {
    throw new Error(`generated item ${index} of the RunState is a ${type} of ${String(rawItem["type"])}`);
  }
  // A programmatic tool call belongs to its program, whose pairing rules are not mirrored.
  if ("caller" in rawItem) {
    throw new Error(`generated item ${index} of the RunState belongs to a program`);
  }
  return { type, rawItem };
}

/** The SDK's extractOutputItemsFromRunItems for one serialized run item. */
function toOutputItem(runItem: { readonly type: string; readonly rawItem: RawItem }, reasoningItemIdPolicy: unknown): RawItem {
  const { created_by: _createdBy, ...item } = runItem.rawItem;
  if ("status" in item && item["status"] === null) {
    delete item["status"];
  }
  if (runItem.type === "reasoning_item" && reasoningItemIdPolicy === "omit") {
    delete item["id"];
  }
  return item;
}

function trimToLatestCompaction(items: RawItem[]): RawItem[] {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (items[index]?.["type"] === "compaction") {
      return items.slice(index);
    }
  }
  return items;
}

/**
 * Checks that each call's result follows its model response and comes once, in item order.
 * @returns Every function call by call ID, and the calls of the last model response without a result.
 */
function pairCallsWithResults(
  turn: AgentTurnContext,
  items: readonly RawItem[],
): { functionCalls: Map<string, protocol.FunctionCallItem>; pendingCalls: protocol.FunctionCallItem[] } {
  const functionCalls = new Map<string, protocol.FunctionCallItem>();
  const openCalls = new Map<string, protocol.FunctionCallItem>();
  let hasResultSinceLastCall = false;
  for (const item of items) {
    const itemType = item["type"];
    if (itemType === "function_call") {
      const callId = readCallId(item);
      if (functionCalls.has(callId)) {
        throw new Error(`the RunState of turn ${turn.turnId} repeats tool call ${callId}`);
      }
      if (openCalls.size > 0 && hasResultSinceLastCall) {
        throw new Error(`tool call ${callId} of turn ${turn.turnId} starts before earlier calls have results`);
      }
      const functionCall = item as unknown as protocol.FunctionCallItem;
      functionCalls.set(callId, functionCall);
      openCalls.set(callId, functionCall);
      hasResultSinceLastCall = false;
    } else if (itemType === "function_call_result") {
      const callId = readCallId(item);
      if (!openCalls.delete(callId)) {
        throw new Error(`the result of tool call ${callId} of turn ${turn.turnId} does not follow one open call`);
      }
      hasResultSinceLastCall = true;
    } else if (openCalls.size > 0) {
      throw new Error(`a ${String(itemType)} item of turn ${turn.turnId} comes before earlier calls have results`);
    }
  }
  return { functionCalls, pendingCalls: [...openCalls.values()] };
}

function buildPendingResult(
  turn: AgentTurnContext,
  functionCall: protocol.FunctionCallItem,
  record: AgentToolCall | undefined,
  options: UnfinishedTurnItemOptions,
): protocol.FunctionCallResultItem {
  if (options.inspectFunctionCall(functionCall).hasOutputSchema) {
    throw new Error(
      `tool call ${functionCall.callId} of turn ${turn.turnId} has an output schema, whose result the SDK encodes`,
    );
  }
  const finalRecord = record !== undefined && isFinalToolCallStatus(record.status) ? record : undefined;
  const text = finalRecord === undefined ? outcomeUnknownToolResult : describeToolCallOutcome(finalRecord);
  const images = finalRecord?.images ?? [];
  return {
    type: "function_call_result",
    name: functionCall.name,
    ...(typeof functionCall.namespace === "string" ? { namespace: functionCall.namespace } : {}),
    callId: functionCall.callId,
    status: "completed",
    output:
      options.showsToolImages && images.length > 0
        ? buildToolImageResultOutput(text, images)
        : { type: "text", text },
  };
}

function readCallId(item: RawItem): string {
  const callId = item["callId"];
  if (typeof callId !== "string" || callId === "") {
    throw new Error(`a ${String(item["type"])} item of the RunState has no callId`);
  }
  return callId;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
