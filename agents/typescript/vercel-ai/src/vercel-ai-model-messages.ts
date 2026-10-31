// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { inspect } from "node:util";

import type { ModelMessage, TextPart, Tool, ToolCallPart, ToolResultPart } from "ai";

import {
  defaultRejectionToolResult,
  describeToolCallOutcome,
  type AgentMessage,
  type AgentToolCall,
  type FrameworkMessage,
} from "@superdurable/dex-agents";

/** Tool result output in an AI SDK `ToolResultPart`. */
export type ToolResultOutput = ToolResultPart["output"];

const modelMessageRoles: ReadonlySet<string> = new Set(["system", "user", "assistant", "tool"]);

/** Projects one AI SDK message onto display fields; tool fields need exactly one tool call or result. */
export function toFrameworkMessage(message: ModelMessage): FrameworkMessage {
  const frameworkItem = serializeModelMessage(message);
  switch (message.role) {
    case "system":
      throw new TypeError("system messages are configured as instructions and never stored");
    case "user":
      return {
        role: "user",
        text: typeof message.content === "string" ? message.content : joinTextParts(message.content),
        toolCallId: null,
        toolName: null,
        toolArguments: null,
        toolOutput: null,
        frameworkItem,
      };
    case "assistant": {
      if (typeof message.content === "string") {
        return displayOnlyMessage("assistant", message.content === "" ? null : message.content, frameworkItem);
      }
      const toolCalls = message.content.filter((part): part is ToolCallPart => part.type === "tool-call");
      const singleToolCall = toolCalls.length === 1 ? toolCalls[0] : undefined;
      return {
        role: "assistant",
        text: joinTextParts(message.content),
        toolCallId: singleToolCall?.toolCallId ?? null,
        toolName: singleToolCall?.toolName ?? null,
        toolArguments: singleToolCall === undefined ? null : serializeJsonValue(singleToolCall.input ?? null),
        toolOutput: null,
        frameworkItem,
      };
    }
    case "tool": {
      const toolResults = message.content.filter(
        (part): part is ToolResultPart => part.type === "tool-result",
      );
      const singleToolResult = toolResults.length === 1 ? toolResults[0] : undefined;
      return {
        role: "tool",
        text: null,
        toolCallId: singleToolResult?.toolCallId ?? null,
        toolName: singleToolResult?.toolName ?? null,
        toolArguments: null,
        toolOutput: singleToolResult === undefined ? null : describeToolResultOutput(singleToolResult.output),
        frameworkItem,
      };
    }
  }
}

/** Rebuilds the AI SDK message stored in one session message. */
export function parseStoredModelMessage(message: AgentMessage): ModelMessage {
  const parsed: unknown = JSON.parse(message.frameworkItem);
  if (!isModelMessage(parsed)) {
    throw new TypeError(`stored message ${message.sequence} is not an AI SDK ModelMessage`);
  }
  return parsed;
}

export function isModelMessage(value: unknown): value is ModelMessage {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { role?: unknown }).role === "string" &&
    modelMessageRoles.has((value as { role: string }).role) &&
    "content" in value
  );
}

export function serializeModelMessage(message: ModelMessage): string {
  return serializeJsonValue(message);
}

/** Serializes a value as JSON, encoding binary data as base64 so AI SDK data content survives a round trip. */
export function serializeJsonValue(value: unknown): string {
  const text = JSON.stringify(convertToJsonCompatibleValue(value));
  if (text === undefined) {
    throw new TypeError("value cannot be serialized as JSON");
  }
  return text;
}

function convertToJsonCompatibleValue(value: unknown, ancestors: Set<object> = new Set()): unknown {
  if (typeof value === "bigint") {
    return value.toString();
  }
  if (value instanceof Uint8Array) {
    return Buffer.from(value.buffer, value.byteOffset, value.byteLength).toString("base64");
  }
  if (value instanceof ArrayBuffer) {
    return Buffer.from(value).toString("base64");
  }
  if (typeof value !== "object" || value === null) {
    return value;
  }
  if (typeof (value as { toJSON?: unknown }).toJSON === "function") {
    return convertToJsonCompatibleValue((value as { toJSON: () => unknown }).toJSON(), ancestors);
  }
  if (ancestors.has(value)) {
    throw new TypeError("value contains a circular reference and cannot be serialized as JSON");
  }
  ancestors.add(value);
  try {
    if (Array.isArray(value)) {
      return value.map((item: unknown) => convertToJsonCompatibleValue(item, ancestors) ?? null);
    }
    const record = value as Record<string, unknown>;
    const converted: Record<string, unknown> = {};
    for (const key of Object.keys(record)) {
      const nestedValue = convertToJsonCompatibleValue(record[key], ancestors);
      if (nestedValue !== undefined) {
        converted[key] = nestedValue;
      }
    }
    return converted;
  } finally {
    ancestors.delete(value);
  }
}

/** Builds the model's tool result for a final record, as the AI SDK does for tools it runs. */
export async function createRecordedToolResultPart(
  record: AgentToolCall,
  tool: Tool | undefined,
): Promise<ToolResultPart> {
  return {
    type: "tool-result",
    toolCallId: record.toolCallId,
    toolName: record.toolName,
    output: await createRecordedToolResultOutput(record, tool),
  };
}

async function createRecordedToolResultOutput(
  record: AgentToolCall,
  tool: Tool | undefined,
): Promise<ToolResultOutput> {
  switch (record.status) {
    case "succeeded": {
      const output = parseRecordedJson(record.output ?? "null");
      if (tool?.toModelOutput !== undefined) {
        return await tool.toModelOutput({
          toolCallId: record.toolCallId,
          input: parseRecordedJson(record.arguments),
          output,
        });
      }
      return typeof output === "string"
        ? { type: "text", value: output }
        : { type: "json", value: output as ToolResultJsonValue };
    }
    case "rejected":
      return { type: "execution-denied", reason: describeToolCallOutcome(record) };
    case "failed":
    case "outcome_unknown":
      return { type: "error-text", value: describeToolCallOutcome(record) };
    case "awaiting_approval":
    case "approved":
      throw new TypeError(`tool call ${record.toolCallId} has no final outcome yet`);
  }
}

type ToolResultJsonValue = Extract<ToolResultOutput, { type: "json" }>["value"];

/** Serializes tool output as JSON text; output JSON cannot hold becomes inspected text, since the write already ran. */
export function serializeToolOutput(output: unknown): string {
  try {
    return serializeJsonValue(output === undefined ? null : output);
  } catch {
    return serializeJsonValue(inspect(output, { depth: 4, breakLength: Number.POSITIVE_INFINITY }));
  }
}

export function parseRecordedJson(text: string): unknown {
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return text;
  }
}

/** Returns display text for a tool result output. */
export function describeToolResultOutput(output: ToolResultOutput): string | null {
  switch (output.type) {
    case "text":
    case "error-text":
      return output.value;
    case "json":
    case "error-json":
      return serializeJsonValue(output.value);
    case "execution-denied":
      return output.reason ?? defaultRejectionToolResult;
    case "content": {
      const text = output.value
        .flatMap((part) => (part.type === "text" ? [part.text] : []))
        .join("\n");
      return text === "" ? null : text;
    }
  }
}

/** Adds recorded results to the response's tool message in tool call order, as one AI SDK step does. */
export function appendToolResultParts(
  turnMessages: readonly ModelMessage[],
  toolResultParts: readonly ToolResultPart[],
): ModelMessage[] {
  const lastMessage = turnMessages.at(-1);
  const hasTrailingToolMessage = lastMessage?.role === "tool";
  const precedingMessages = hasTrailingToolMessage ? turnMessages.slice(0, -1) : [...turnMessages];
  const toolCallOrder = readToolCallOrder(precedingMessages.at(-1));
  const content = [...(hasTrailingToolMessage ? lastMessage.content : []), ...toolResultParts];
  const orderOf = (part: (typeof content)[number]): number =>
    part.type === "tool-result" ? (toolCallOrder.get(part.toolCallId) ?? toolCallOrder.size) : toolCallOrder.size;
  const sortedContent = content
    .map((part, index) => ({ part, index }))
    .sort((left, right) => orderOf(left.part) - orderOf(right.part) || left.index - right.index)
    .map(({ part }) => part);
  return [...precedingMessages, { ...(hasTrailingToolMessage ? lastMessage : {}), role: "tool", content: sortedContent }];
}

function readToolCallOrder(message: ModelMessage | undefined): Map<string, number> {
  const order = new Map<string, number>();
  if (message?.role !== "assistant" || typeof message.content === "string") {
    return order;
  }
  for (const part of message.content) {
    if (part.type === "tool-call" && !order.has(part.toolCallId)) {
      order.set(part.toolCallId, order.size);
    }
  }
  return order;
}

function joinTextParts(content: readonly ({ readonly type: string } | TextPart)[]): string | null {
  const text = content
    .flatMap((part) => (isTextPart(part) ? [part.text] : []))
    .join("");
  return text === "" ? null : text;
}

function isTextPart(part: { readonly type: string } | TextPart): part is TextPart {
  return part.type === "text" && typeof (part as Partial<TextPart>).text === "string";
}

function displayOnlyMessage(
  role: "assistant",
  text: string | null,
  frameworkItem: string,
): FrameworkMessage {
  return { role, text, toolCallId: null, toolName: null, toolArguments: null, toolOutput: null, frameworkItem };
}
