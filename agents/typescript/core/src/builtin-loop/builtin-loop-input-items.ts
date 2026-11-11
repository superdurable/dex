// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  outcomeUnknownToolResult,
  type AgentImageReference,
  type AgentMessage,
  type FrameworkMessage,
} from "../agent-session-values.js";
import type { ModelInputItem, ModelToolCall, ModelToolResultItem } from "./model-client.js";

/**
 * Returns the session message that stores one item; `frameworkItem` is the item's compact snake_case JSON. A user
 * text or tool result item with images adds an `images` list of references; a tool result message also shows them
 * in `images`.
 */
export function toBuiltinLoopFrameworkMessage(item: ModelInputItem): FrameworkMessage {
  switch (item.type) {
    case "user_text":
      return {
        ...emptyProjection,
        role: "user",
        text: item.text,
        frameworkItem: JSON.stringify({ type: "user_text", text: item.text, ...encodeImages(item.images) }),
      };
    case "tool_result":
      return {
        ...emptyProjection,
        role: "tool",
        toolCallId: item.toolCallId,
        toolName: item.toolName,
        toolOutput: item.output,
        frameworkItem: JSON.stringify({
          type: "tool_result",
          tool_call_id: item.toolCallId,
          tool_name: item.toolName,
          output: item.output,
          ...encodeImages(item.images),
        }),
        ...((item.images ?? []).length === 0 ? {} : { images: item.images }),
      };
    case "model_output": {
      const singleToolCall = item.toolCalls.length === 1 ? item.toolCalls[0] : undefined;
      return {
        ...emptyProjection,
        role: "assistant",
        text: item.text,
        toolCallId: singleToolCall?.toolCallId ?? null,
        toolName: singleToolCall?.toolName ?? null,
        toolArguments: singleToolCall?.arguments ?? null,
        frameworkItem: JSON.stringify({
          type: "model_output",
          provider: item.provider,
          api: item.api,
          item: item.item,
          text: item.text,
          tool_calls: item.toolCalls.map((toolCall) => ({
            tool_call_id: toolCall.toolCallId,
            tool_name: toolCall.toolName,
            arguments: toolCall.arguments,
          })),
        }),
      };
    }
  }
}

/** Reads the built-in loop item stored in one session message. */
export function readStoredModelInputItem(message: AgentMessage): ModelInputItem {
  try {
    return decodeModelInputItem(JSON.parse(message.frameworkItem));
  } catch (error: unknown) {
    throw new TypeError(`message ${message.sequence} is not a built-in loop item`, { cause: error });
  }
}

/**
 * Returns the items with exactly one result after every tool call. A call without a result, such as one
 * whose turn failed before it was stored, gets the outcome-unknown text before the next user text; a
 * result without its call is dropped. Providers reject either kind of orphan.
 */
export function pairToolCallsWithResults(items: readonly ModelInputItem[]): ModelInputItem[] {
  const paired: ModelInputItem[] = [];
  const openToolCalls = new Map<string, ModelToolCall>();
  const closeOpenToolCalls = (): void => {
    for (const toolCall of openToolCalls.values()) {
      paired.push(createOutcomeUnknownResult(toolCall));
    }
    openToolCalls.clear();
  };
  for (const item of items) {
    switch (item.type) {
      case "model_output":
        paired.push(item);
        for (const toolCall of item.toolCalls) {
          openToolCalls.set(toolCall.toolCallId, toolCall);
        }
        break;
      case "tool_result":
        if (openToolCalls.delete(item.toolCallId)) {
          paired.push(item);
        }
        break;
      case "user_text":
        closeOpenToolCalls();
        paired.push(item);
        break;
    }
  }
  closeOpenToolCalls();
  return paired;
}

function createOutcomeUnknownResult(toolCall: ModelToolCall): ModelToolResultItem {
  return {
    type: "tool_result",
    toolCallId: toolCall.toolCallId,
    toolName: toolCall.toolName,
    output: outcomeUnknownToolResult,
  };
}

function decodeModelInputItem(value: unknown): ModelInputItem {
  const record = requireRecord(value, "built-in loop item");
  switch (record["type"]) {
    case "user_text":
      return { type: "user_text", text: requireString(record, "text"), ...decodeImages(record) };
    case "tool_result":
      return {
        type: "tool_result",
        toolCallId: requireString(record, "tool_call_id"),
        toolName: requireString(record, "tool_name"),
        output: requireString(record, "output"),
        ...decodeImages(record),
      };
    case "model_output": {
      const text = record["text"] ?? null;
      if (text !== null && typeof text !== "string") {
        throw new TypeError("model_output.text must be a string or null");
      }
      const toolCalls = record["tool_calls"] ?? [];
      if (!Array.isArray(toolCalls)) {
        throw new TypeError("model_output.tool_calls must be an array");
      }
      return {
        type: "model_output",
        provider: requireString(record, "provider"),
        api: requireString(record, "api"),
        item: requireString(record, "item"),
        text,
        toolCalls: toolCalls.map((toolCall): ModelToolCall => {
          const toolCallRecord = requireRecord(toolCall, "model_output.tool_calls item");
          return {
            toolCallId: requireString(toolCallRecord, "tool_call_id"),
            toolName: requireString(toolCallRecord, "tool_name"),
            arguments: requireString(toolCallRecord, "arguments"),
          };
        }),
      };
    }
    default:
      throw new TypeError(`unknown built-in loop item type ${JSON.stringify(record["type"])}`);
  }
}

/** An item without images keeps the bytes it had before images existed. */
function encodeImages(images: readonly AgentImageReference[] | undefined): { readonly images?: readonly object[] } {
  if (images === undefined || images.length === 0) {
    return {};
  }
  return {
    images: images.map((image) => ({
      type: image.type,
      uri: image.uri,
      mime_type: image.mimeType,
      sha256: image.sha256,
      size_bytes: image.sizeBytes,
    })),
  };
}

function decodeImages(record: Readonly<Record<string, unknown>>): {
  readonly images?: readonly AgentImageReference[];
} {
  const images = record["images"] ?? [];
  if (!Array.isArray(images)) {
    throw new TypeError("images must be an array");
  }
  if (images.length === 0) {
    return {};
  }
  return {
    images: images.map((image): AgentImageReference => {
      const imageRecord = requireRecord(image, "images item");
      const sizeBytes = imageRecord["size_bytes"];
      if (typeof sizeBytes !== "number") {
        throw new TypeError("size_bytes must be a number");
      }
      return {
        type: "image",
        uri: requireString(imageRecord, "uri"),
        mimeType: requireString(imageRecord, "mime_type"),
        sha256: requireString(imageRecord, "sha256"),
        sizeBytes,
      };
    }),
  };
}

const emptyProjection = {
  text: null,
  toolCallId: null,
  toolName: null,
  toolArguments: null,
  toolOutput: null,
} as const;

function requireRecord(value: unknown, typeName: string): Readonly<Record<string, unknown>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError(`${typeName} must be a JSON object`);
  }
  return value as Readonly<Record<string, unknown>>;
}

function requireString(record: Readonly<Record<string, unknown>>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") {
    throw new TypeError(`${field} must be a string`);
  }
  return value;
}
