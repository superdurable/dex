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

/** How the resumed run would show one recorded result, which the runner's agent graph decides. */
export interface UnfinishedTurnResultShape {
  /** Whether the call's function tool has an output schema, so the SDK sends its output as JSON text. */
  readonly hasOutputSchema: boolean;
  /** Whether the runner has a media store, so a record's images reach the model. */
  readonly showsToolImages: boolean;
}

type RawItem = Record<string, unknown>;

// The SDK's getRunOutput leaves out approval placeholders and input admitted while resuming.
const skippedRunItemTypes = new Set(["tool_approval_item", "input_item"]);
// Local tool calls that the SDK pairs with one output item; only function calls can be pending here.
const pairedLocalCallResultTypes: ReadonlyMap<string, string> = new Map([
  ["computer_call", "computer_call_result"],
  ["shell_call", "shell_call_output"],
  ["apply_patch_call", "apply_patch_call_output"],
]);
// Items a model response may hold after its first function call.
const trailingResponseItemTypes = new Set(["function_call", "function_call_result", "reasoning"]);

/**
 * Reads the turn's items from the serialized `RunState` and answers every pending function call.
 *
 * It mirrors the SDK's `getRunOutput` on the serialized generated items, without dropping pending calls. Each
 * pending call gets the `function_call_result` the resumed run would make from its final record, or the
 * unknown-outcome text when the call has no final record.
 *
 * @param turn - The ended turn; its `frameworkRunState` is the `RunState` of the last `ToolCallsRequested`.
 * @param readResultShape - Returns how a function call's recorded result is shown.
 * @returns The turn's items not stored yet, in order.
 * @throws {Error} When the run state cannot be read, a call would be left without a result, or a record has no
 * call in the run state; the session then stores its note instead.
 */
export function readUnfinishedTurnItems(
  turn: AgentTurnContext,
  readResultShape: (functionCall: protocol.FunctionCallItem) => UnfinishedTurnResultShape,
): AgentInputItem[] {
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
  const resultCallIds = new Set<string>();
  const functionCallIds = new Set<string>();
  for (const item of items) {
    if (item["type"] === "function_call_result") {
      addUniqueCallId(resultCallIds, readCallId(item), "result");
    } else if (item["type"] === "function_call") {
      addUniqueCallId(functionCallIds, readCallId(item), "function call");
    }
  }
  requireLocalCallResults(items);
  const pendingIndexes = items.flatMap((item, index) =>
    item["type"] === "function_call" && !resultCallIds.has(readCallId(item)) ? [index] : [],
  );
  const firstPendingIndex = pendingIndexes[0];
  if (
    firstPendingIndex !== undefined &&
    items.slice(firstPendingIndex).some((item) => !trailingResponseItemTypes.has(String(item["type"])))
  ) {
    throw new Error(`turn ${turn.turnId} has a pending function call before the last model response`);
  }
  for (const resultCallId of resultCallIds) {
    if (!functionCallIds.has(resultCallId)) {
      throw new Error(`turn ${turn.turnId} has a result of tool call ${resultCallId} without its call`);
    }
  }
  for (const record of turn.toolCalls) {
    if (!functionCallIds.has(record.toolCallId)) {
      throw new Error(`tool call ${record.toolCallId} of turn ${turn.turnId} is not in the run state`);
    }
  }
  const storedCallIds = new Set(
    turn.messages.filter((message) => message.turnId === turn.turnId).map((message) => message.toolCallId),
  );
  for (const callId of functionCallIds) {
    if (storedCallIds.has(callId)) {
      throw new Error(`tool call ${callId} of turn ${turn.turnId} is already stored`);
    }
  }
  const pendingResults = pendingIndexes.map((index) =>
    buildPendingResult(turn, items[index] as unknown as protocol.FunctionCallItem, readResultShape),
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
  const generatedItems = document["generatedItems"];
  if (!Array.isArray(generatedItems)) {
    throw new Error(`RunState schema ${document["$schemaVersion"]} has no generatedItems list`);
  }
  return {
    generatedItems: generatedItems.map((runItem: unknown, index) => {
      if (!isRecord(runItem) || typeof runItem["type"] !== "string" || !isRecord(runItem["rawItem"])) {
        throw new Error(`generated item ${index} of the RunState has no type or rawItem`);
      }
      return { type: runItem["type"], rawItem: runItem["rawItem"] };
    }),
    reasoningItemIdPolicy: document["reasoningItemIdPolicy"],
  };
}

/** The SDK's extractOutputItemsFromRunItems for one serialized run item. */
function toOutputItem(runItem: { readonly type: string; readonly rawItem: RawItem }, reasoningItemIdPolicy: unknown): RawItem {
  const { created_by: _createdBy, ...item } = runItem.rawItem;
  if (item["type"] === "shell_call_output" && Array.isArray(item["output"])) {
    item["output"] = item["output"].map((entry: unknown) => {
      if (!isRecord(entry)) {
        return entry;
      }
      const { created_by: _entryCreatedBy, ...withoutCreatedBy } = entry;
      return withoutCreatedBy;
    });
  }
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

function requireLocalCallResults(items: readonly RawItem[]): void {
  const resultCallIds = new Set(
    items.map((item) => `${String(item["type"])}/${String(item["callId"])}`),
  );
  for (const item of items) {
    const resultType = pairedLocalCallResultTypes.get(String(item["type"]));
    // The provider runs a hosted shell call; it has no local output until it completes.
    const isPendingHostedShellCall =
      item["type"] === "shell_call" && (item["status"] === undefined || item["status"] === "in_progress");
    if (
      resultType !== undefined &&
      !isPendingHostedShellCall &&
      !resultCallIds.has(`${resultType}/${String(item["callId"])}`)
    ) {
      throw new Error(`${String(item["type"])} ${String(item["callId"])} has no output in the RunState`);
    }
  }
}

function buildPendingResult(
  turn: AgentTurnContext,
  functionCall: protocol.FunctionCallItem,
  readResultShape: (functionCall: protocol.FunctionCallItem) => UnfinishedTurnResultShape,
): protocol.FunctionCallResultItem {
  const record = turn.recordedToolCall(functionCall.callId);
  const isFinal = record !== undefined && isFinalToolCallStatus(record.status);
  const text = isFinal ? describeToolCallOutcome(record) : outcomeUnknownToolResult;
  return {
    type: "function_call_result",
    name: functionCall.name,
    ...(typeof functionCall.namespace === "string" ? { namespace: functionCall.namespace } : {}),
    callId: functionCall.callId,
    status: "completed",
    output: buildResultOutput(text, isFinal ? record : undefined, readResultShape(functionCall)),
  };
}

/** The output the gated tool's recorded result becomes in the SDK's getToolCallOutputItem. */
function buildResultOutput(
  text: string,
  record: AgentToolCall | undefined,
  shape: UnfinishedTurnResultShape,
): protocol.FunctionCallResultItem["output"] {
  if (shape.hasOutputSchema) {
    return { type: "text", text: JSON.stringify(text) };
  }
  const images = record?.images ?? [];
  if (shape.showsToolImages && images.length > 0) {
    return buildToolImageResultOutput(text, images);
  }
  return { type: "text", text };
}

function addUniqueCallId(callIds: Set<string>, callId: string, description: string): void {
  if (callIds.has(callId)) {
    throw new Error(`the RunState has more than one ${description} with call ID ${callId}`);
  }
  callIds.add(callId);
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
