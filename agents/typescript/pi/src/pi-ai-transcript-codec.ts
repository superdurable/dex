// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  PI_AI_VERSION,
  type AssistantMessage,
  type JsonObject,
  type Message,
  type ToolCall,
  type ToolResultMessage,
  type Usage,
} from "@earendil-works/pi-ai";
import type { ModelInputItem, ModelOutputItem, ModelToolCall } from "@superdurable/dex-agents/stepwise";

/**
 * API name recorded on every output item of the pi-ai model client, as `ModelOutputItem.api`.
 *
 * @since 0.5.0
 */
export const piAiModelClientApi = "pi-ai";

/** Format number of the stored item envelope; a new number is added only for an incompatible envelope. */
const storedMessageFormat = 1;
const assistantContentTypes: ReadonlySet<string> = new Set(["text", "thinking", "toolCall"]);

/** The JSON stored in `ModelOutputItem.item`: the pi-ai version that produced it and its whole message. */
interface StoredPiAiMessage {
  readonly format: typeof storedMessageFormat;
  readonly piAiVersion: string;
  readonly message: AssistantMessage;
}

/** Identifies the model a pi-ai assistant message came from, as pi-ai compares them. */
export interface PiAiModelIdentity {
  readonly provider: string;
  readonly api: string;
  readonly model: string;
}

/** Serializes a pi-ai assistant message, unchanged, with the running pi-ai version. */
export function encodeStoredPiAiMessage(message: AssistantMessage): string {
  const stored: StoredPiAiMessage = { format: storedMessageFormat, piAiVersion: PI_AI_VERSION, message };
  return JSON.stringify(stored);
}

/**
 * Parses a stored item; it checks only replay's fields, so later pi-ai fields still replay.
 * @throws {TypeError} When the item is not an envelope this package wrote.
 */
export function decodeStoredPiAiMessage(itemText: string): AssistantMessage {
  let stored: Partial<StoredPiAiMessage> | null;
  try {
    stored = JSON.parse(itemText) as Partial<StoredPiAiMessage> | null;
  } catch {
    throw new TypeError(unrecognizedItemMessage(undefined, "it is not JSON"));
  }
  const storedVersion = typeof stored?.piAiVersion === "string" ? stored.piAiVersion : undefined;
  if (stored?.format !== storedMessageFormat) {
    throw new TypeError(unrecognizedItemMessage(storedVersion, `its format is ${JSON.stringify(stored?.format)}, not 1`));
  }
  const problem = findAssistantMessageProblem(stored.message);
  if (problem !== undefined) {
    throw new TypeError(unrecognizedItemMessage(storedVersion, problem));
  }
  return stored.message as AssistantMessage;
}

/** The pi-ai model of a stored item, or `undefined` when another client stored it. */
export function readStoredModelIdentity(item: ModelOutputItem): PiAiModelIdentity | undefined {
  if (item.api !== piAiModelClientApi) {
    return undefined;
  }
  const message = decodeStoredPiAiMessage(item.item);
  return { provider: message.provider, api: message.api, model: message.model };
}

/** Converts the conversation; pi-ai items replay unchanged, since pi-ai converts between providers itself. */
export function toPiAiMessages(items: readonly ModelInputItem[]): Message[] {
  const messages: Message[] = [];
  for (const item of items) {
    switch (item.type) {
      case "user_text":
        messages.push({ role: "user", content: item.text, timestamp: 0 });
        break;
      case "model_output": {
        const message = item.api === piAiModelClientApi ? decodeStoredPiAiMessage(item.item) : rebuildAssistantMessage(item);
        if (message.content.length > 0) {
          messages.push(message);
        }
        break;
      }
      case "tool_result": {
        const result: ToolResultMessage = {
          role: "toolResult",
          toolCallId: item.toolCallId,
          toolName: item.toolName,
          content: [{ type: "text", text: item.output }],
          isError: false,
          timestamp: 0,
        };
        messages.push(result);
        break;
      }
    }
  }
  return messages;
}

/** Tool calls Dex runs: none unless pi-ai stopped for tool use, so a broken stream runs none. */
export function readModelToolCalls(message: AssistantMessage): ModelToolCall[] {
  if (message.stopReason !== "toolUse") {
    return [];
  }
  return message.content.flatMap((part): ModelToolCall[] =>
    part.type === "toolCall"
      ? [{ toolCallId: part.id, toolName: part.name, arguments: serializeToolCallArguments(part) }]
      : [],
  );
}

/** Visible text of a response. */
export function readMessageText(message: AssistantMessage): string {
  return message.content.map((part) => (part.type === "text" ? part.text : "")).join("");
}

/** Invalid arguments become a JSON string, which the tool Step rejects instead of running with `{}`. */
function serializeToolCallArguments(toolCall: ToolCall): string {
  if (toolCall.argumentsError !== undefined) {
    return JSON.stringify(toolCall.argumentsError);
  }
  return JSON.stringify(toolCall.arguments);
}

/** Rebuilds an item of another client from its text and tool calls; pi-ai sees it as another model's message. */
function rebuildAssistantMessage(item: ModelOutputItem): AssistantMessage {
  const content: AssistantMessage["content"] = [];
  if (item.text !== null && item.text !== "") {
    content.push({ type: "text", text: item.text });
  }
  for (const toolCall of item.toolCalls) {
    content.push({
      type: "toolCall",
      id: toolCall.toolCallId,
      name: toolCall.toolName,
      arguments: parseToolCallArguments(toolCall.arguments),
    });
  }
  return {
    role: "assistant",
    content,
    api: item.api,
    provider: item.provider,
    model: "",
    usage: emptyUsage(),
    stopReason: item.toolCalls.length > 0 ? "toolUse" : "stop",
    timestamp: 0,
  };
}

function parseToolCallArguments(argumentsText: string): JsonObject {
  try {
    const parsed: unknown = JSON.parse(argumentsText);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? (parsed as JsonObject) : {};
  } catch {
    return {};
  }
}

function findAssistantMessageProblem(message: unknown): string | undefined {
  if (typeof message !== "object" || message === null) {
    return "it has no message object";
  }
  const record = message as Readonly<Record<string, unknown>>;
  if (record["role"] !== "assistant") {
    return `its message role is ${JSON.stringify(record["role"])}, not "assistant"`;
  }
  for (const field of ["api", "provider", "model", "stopReason"]) {
    if (typeof record[field] !== "string") {
      return `its message field ${field} is not a string`;
    }
  }
  const content = record["content"];
  if (!Array.isArray(content)) {
    return "its message content is not an array";
  }
  for (const part of content as unknown[]) {
    const partType = (part as { readonly type?: unknown } | null)?.type;
    if (typeof partType !== "string" || !assistantContentTypes.has(partType)) {
      return `its message has a content part of type ${JSON.stringify(partType)}`;
    }
  }
  return undefined;
}

function unrecognizedItemMessage(storedVersion: string | undefined, problem: string): string {
  const storedBy = storedVersion === undefined ? "an unknown pi-ai version" : `pi-ai ${storedVersion}`;
  return `a stored ${piAiModelClientApi} model output from ${storedBy} cannot be replayed by pi-ai ${PI_AI_VERSION}: ${problem}`;
}

function emptyUsage(): Usage {
  return {
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    totalTokens: 0,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
  };
}
