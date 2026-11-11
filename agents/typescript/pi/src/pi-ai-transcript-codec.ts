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
  type ImageContent,
  type JsonObject,
  type Message,
  type ToolCall,
  type ToolResultMessage,
  type Usage,
} from "@earendil-works/pi-ai";
import type { AgentImageReference } from "@superdurable/dex-agents";
import type { ModelInputItem, ModelOutputItem, ModelToolCall } from "@superdurable/dex-agents/builtin-loop";

/**
 * API name recorded on every output item of the pi-ai model client, as `ModelOutputItem.api`.
 *
 * @since 0.5.0
 */
export const piAiModelClientApi = "pi-ai";

/** Format number of the stored item envelope; a new number is added only for an incompatible envelope. */
const storedMessageFormat = 1;

/** Token counts of a stored response; pi-ai's catalog `cost` is local and not stored. */
type StoredUsage = Omit<Usage, "cost">;

/** The replay fields of an assistant message; local telemetry such as `timestamp` is not stored. */
type StoredAssistantMessage = Pick<
  AssistantMessage,
  "role" | "content" | "api" | "provider" | "model" | "responseModel" | "responseId" | "providerThinkingLevel" | "stopReason"
> & { readonly usage: StoredUsage };

/** The JSON stored in `ModelOutputItem.item`: the pi-ai version that produced it and its replay fields. */
interface StoredPiAiMessage {
  readonly format: typeof storedMessageFormat;
  readonly piAiVersion: string;
  /** Set when the call ran without thinking because another model answered earlier in its tool loop. */
  readonly thinkingDisabledInToolLoop?: true;
  readonly message: StoredAssistantMessage;
}

/** Identifies the model a pi-ai assistant message came from, as pi-ai compares them. */
export interface PiAiModelIdentity {
  readonly provider: string;
  readonly api: string;
  readonly model: string;
}

/** Serializes a response's content, identity, and token usage, with the running pi-ai version. */
export function encodeStoredPiAiMessage(message: AssistantMessage, isThinkingDisabledInToolLoop: boolean): string {
  const { cost: _localCost, ...usage } = message.usage;
  const stored: StoredPiAiMessage = {
    format: storedMessageFormat,
    piAiVersion: PI_AI_VERSION,
    ...(isThinkingDisabledInToolLoop ? { thinkingDisabledInToolLoop: true } : {}),
    message: {
      role: "assistant",
      content: message.content,
      api: message.api,
      provider: message.provider,
      model: message.model,
      ...(message.responseModel === undefined ? {} : { responseModel: message.responseModel }),
      ...(message.responseId === undefined ? {} : { responseId: message.responseId }),
      ...(message.providerThinkingLevel === undefined ? {} : { providerThinkingLevel: message.providerThinkingLevel }),
      stopReason: message.stopReason,
      usage,
    },
  };
  return JSON.stringify(stored);
}

/**
 * Parses a stored item; it checks only replay's fields, so later pi-ai fields still replay.
 * @throws {TypeError} When the item is not an envelope this package wrote.
 */
export function decodeStoredPiAiMessage(itemText: string): AssistantMessage {
  return decodeStoredPiAiItem(itemText).message;
}

/** Whether a stored item records that its call ran without thinking for a model change in its tool loop. */
export function isStoredThinkingDisabledInToolLoop(item: ModelOutputItem): boolean {
  return item.api === piAiModelClientApi && decodeStoredPiAiItem(item.item).isThinkingDisabledInToolLoop;
}

function decodeStoredPiAiItem(itemText: string): {
  readonly message: AssistantMessage;
  readonly isThinkingDisabledInToolLoop: boolean;
} {
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
  const flag: unknown = stored.thinkingDisabledInToolLoop;
  const problem =
    flag === undefined || typeof flag === "boolean"
      ? findStoredMessageProblem(stored.message)
      : "thinkingDisabledInToolLoop is not a boolean";
  if (problem !== undefined) {
    throw new TypeError(unrecognizedItemMessage(storedVersion, problem));
  }
  const message = stored.message as StoredAssistantMessage;
  // Fixed values for the local fields pi-ai's type requires, so a replayed request never depends on a clock.
  return {
    message: { ...message, usage: { ...message.usage, cost: zeroCost() }, timestamp: 0 },
    isThinkingDisabledInToolLoop: flag === true,
  };
}

/** The pi-ai model of a stored item, or `undefined` when another client stored it. */
export function readStoredModelIdentity(item: ModelOutputItem): PiAiModelIdentity | undefined {
  if (item.api !== piAiModelClientApi) {
    return undefined;
  }
  const message = decodeStoredPiAiMessage(item.item);
  return { provider: message.provider, api: message.api, model: message.model };
}

/**
 * Converts the conversation; pi-ai items replay unchanged, since pi-ai converts between providers itself. Images of
 * user text and tool result items follow the item's text as pi-ai image content, with bytes from `imageBytes`.
 * @throws {TypeError} When `imageBytes` lacks the bytes of an item's image.
 */
export function toPiAiMessages(
  items: readonly ModelInputItem[],
  imageBytes: ReadonlyMap<string, Uint8Array>,
): Message[] {
  const messages: Message[] = [];
  for (const item of items) {
    switch (item.type) {
      case "user_text": {
        const images = toPiAiImageContent(item.images, imageBytes);
        // Text alone stays a string, so a request without images is unchanged.
        const content = images.length === 0 ? item.text : [{ type: "text" as const, text: item.text }, ...images];
        messages.push({ role: "user", content, timestamp: 0 });
        break;
      }
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
          content: [{ type: "text", text: item.output }, ...toPiAiImageContent(item.images, imageBytes)],
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

/** One pi-ai image content part per image, with the bytes the runner loaded for the request. */
function toPiAiImageContent(
  images: readonly AgentImageReference[] | undefined,
  imageBytes: ReadonlyMap<string, Uint8Array>,
): ImageContent[] {
  return (images ?? []).map((image): ImageContent => {
    const data = imageBytes.get(image.sha256);
    if (data === undefined) {
      throw new TypeError(`the model request has no bytes for image ${image.uri}`);
    }
    return { type: "image", data: Buffer.from(data).toString("base64"), mimeType: image.mimeType };
  });
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

function findStoredMessageProblem(message: unknown): string | undefined {
  if (!isRecord(message)) {
    return "message is not an object";
  }
  if (message["role"] !== "assistant") {
    return `message.role is ${JSON.stringify(message["role"])}, not "assistant"`;
  }
  const fieldProblem =
    findFieldProblem(message, "message", ["api", "provider", "model", "stopReason"], "string") ??
    findFieldProblem(message, "message", ["responseModel", "responseId", "providerThinkingLevel"], "string", true);
  if (fieldProblem !== undefined) {
    return fieldProblem;
  }
  const usage = message["usage"];
  if (!isRecord(usage)) {
    return "message.usage is not an object";
  }
  const usageProblem =
    findFieldProblem(usage, "message.usage", ["input", "output", "cacheRead", "cacheWrite", "totalTokens"], "number") ??
    findFieldProblem(usage, "message.usage", ["reasoning", "cacheWrite1h"], "number", true);
  if (usageProblem !== undefined) {
    return usageProblem;
  }
  const content = message["content"];
  if (!Array.isArray(content)) {
    return "message.content is not an array";
  }
  for (const [index, part] of content.entries()) {
    const problem = findContentPartProblem(part, `message.content[${index}]`);
    if (problem !== undefined) {
      return problem;
    }
  }
  return undefined;
}

/** Checks the fields replay reads from one content part; unknown extra fields are allowed. */
function findContentPartProblem(part: unknown, path: string): string | undefined {
  if (!isRecord(part)) {
    return `${path} is not an object`;
  }
  switch (part["type"]) {
    case "text":
      return findFieldProblem(part, path, ["text"], "string") ?? findFieldProblem(part, path, ["textSignature"], "string", true);
    case "thinking":
      return (
        findFieldProblem(part, path, ["thinking"], "string") ??
        findFieldProblem(part, path, ["thinkingSignature"], "string", true) ??
        findFieldProblem(part, path, ["redacted"], "boolean", true)
      );
    case "toolCall": {
      const problem =
        findFieldProblem(part, path, ["id", "name"], "string") ??
        findFieldProblem(part, path, ["thoughtSignature", "namespace", "argumentsError"], "string", true);
      if (problem !== undefined) {
        return problem;
      }
      return isRecord(part["arguments"]) ? undefined : `${path}.arguments is not an object`;
    }
    default:
      return `${path}.type is ${JSON.stringify(part["type"])}, not text, thinking, or toolCall`;
  }
}

function findFieldProblem(
  record: Readonly<Record<string, unknown>>,
  path: string,
  fields: readonly string[],
  expectedType: "string" | "number" | "boolean",
  isOptional = false,
): string | undefined {
  for (const field of fields) {
    const value = record[field];
    if (isOptional && value === undefined) {
      continue;
    }
    if (typeof value !== expectedType) {
      return value === undefined ? `${path}.${field} is missing` : `${path}.${field} is not a ${expectedType}`;
    }
  }
  return undefined;
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function unrecognizedItemMessage(storedVersion: string | undefined, problem: string): string {
  const storedBy = storedVersion === undefined ? "an unknown pi-ai version" : `pi-ai ${storedVersion}`;
  return `a stored ${piAiModelClientApi} model output from ${storedBy} cannot be replayed by pi-ai ${PI_AI_VERSION}: ${problem}`;
}

function emptyUsage(): Usage {
  return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: zeroCost() };
}

function zeroCost(): Usage["cost"] {
  return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
}
