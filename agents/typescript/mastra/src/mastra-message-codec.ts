// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AIV5Type,
  MastraDBMessage,
  MastraMessagePart,
  MastraToolInvocationPart,
} from "@mastra/core/agent/message-list";
import type { AgentMessage, FrameworkMessage } from "@superdurable/dex-agents";

/** ID prefix that replay gives stored messages without an ID, such as user messages. */
export const replayedMessageIdPrefix = "dex-message-";

const mastraTurnRunStateVersion = 1;

export interface PendingMastraToolCall {
  readonly toolCallId: string;
  readonly toolName: string;
}

export interface PendingMastraToolInvocation extends PendingMastraToolCall {
  readonly args: unknown;
}

export interface MastraTurnRunState {
  /** Messages after the turn's user message; Mastra merges later steps into them. */
  readonly turnMessages: readonly MastraDBMessage[];
  readonly pendingToolCalls: readonly PendingMastraToolCall[];
}

/** Omits `id` and `createdAt` to stay deterministic; {@link decodeStoredMastraMessages} fills both. */
export function buildMastraUserFrameworkMessage(text: string): FrameworkMessage {
  const storedUserMessage: Omit<MastraDBMessage, "id" | "createdAt"> = {
    role: "user",
    content: { format: 2, parts: [{ type: "text", text }], content: text },
  };
  return {
    role: "user",
    text,
    toolCallId: null,
    toolName: null,
    toolArguments: null,
    toolOutput: null,
    frameworkItem: JSON.stringify(storedUserMessage),
  };
}

export function decodeStoredMastraMessages(messages: readonly AgentMessage[]): MastraDBMessage[] {
  return messages.map((message) => {
    const decoded = decodeMastraMessage(JSON.parse(message.frameworkItem), `message ${message.sequence}`);
    return {
      ...decoded,
      id: decoded.id === "" ? `${replayedMessageIdPrefix}${message.sequence}` : decoded.id,
      createdAt: Number.isNaN(decoded.createdAt.getTime()) ? new Date(message.createdAt) : decoded.createdAt,
    };
  });
}

/** MessageList sorts by `createdAt`; a skewed Worker clock must not merge a response into an older answer. */
export function assignIncreasingCreatedAt(messages: readonly MastraDBMessage[]): MastraDBMessage[] {
  let previousTime = Number.NEGATIVE_INFINITY;
  return messages.map((message) => {
    const time = Math.max(message.createdAt.getTime(), previousTime + 1);
    previousTime = time;
    return time === message.createdAt.getTime() ? message : { ...message, createdAt: new Date(time) };
  });
}

export function projectMastraMessage(message: MastraDBMessage): FrameworkMessage {
  const parts = message.content.parts;
  const texts = parts.flatMap((part) => (part.type === "text" && part.text !== "" ? [part.text] : []));
  const toolInvocations = parts.filter(isToolInvocationPart);
  const onlyToolInvocation = toolInvocations.length === 1 ? toolInvocations[0]?.toolInvocation : undefined;
  return {
    role: message.role === "assistant" ? "assistant" : "user",
    text: texts.length === 0 ? null : texts.join("\n\n"),
    toolCallId: onlyToolInvocation?.toolCallId ?? null,
    toolName: onlyToolInvocation?.toolName ?? null,
    toolArguments: onlyToolInvocation === undefined ? null : JSON.stringify(onlyToolInvocation.args ?? {}),
    toolOutput:
      onlyToolInvocation === undefined
        ? null
        : onlyToolInvocation.state === "result"
          ? formatMastraToolOutputText(onlyToolInvocation.result)
          : (onlyToolInvocation.errorText ?? null),
    frameworkItem: JSON.stringify(message),
  };
}

/** A client tool call stays in state `call` until the runner appends its result. */
export function listPendingMastraToolInvocations(
  messages: readonly MastraDBMessage[],
): PendingMastraToolInvocation[] {
  return messages.flatMap((message) =>
    message.content.parts.filter(isToolInvocationPart).flatMap((part) =>
      part.toolInvocation.state === "call"
        ? [
            {
              toolCallId: part.toolInvocation.toolCallId,
              toolName: part.toolInvocation.toolName,
              args: part.toolInvocation.args,
            },
          ]
        : [],
    ),
  );
}

/** Mastra merges this AI SDK tool message into the assistant message holding the call. */
export function buildMastraToolResultMessage(
  toolCall: PendingMastraToolCall,
  outputText: string,
): AIV5Type.ToolModelMessage {
  return {
    role: "tool",
    content: [
      {
        type: "tool-result",
        toolCallId: toolCall.toolCallId,
        toolName: toolCall.toolName,
        output: { type: "text", value: outputText },
      },
    ],
  };
}

export function formatMastraToolOutputText(result: unknown): string {
  if (typeof result === "string") {
    return result;
  }
  if (result === undefined) {
    return "";
  }
  return JSON.stringify(result) ?? String(result);
}

export function encodeMastraTurnRunState(runState: MastraTurnRunState): string {
  return JSON.stringify({
    version: mastraTurnRunStateVersion,
    turn_messages: runState.turnMessages,
    pending_tool_calls: runState.pendingToolCalls.map((toolCall) => ({
      tool_call_id: toolCall.toolCallId,
      tool_name: toolCall.toolName,
    })),
  });
}

export function decodeMastraTurnRunState(frameworkRunState: string): MastraTurnRunState {
  const value: unknown = JSON.parse(frameworkRunState);
  if (!isRecord(value) || value["version"] !== mastraTurnRunStateVersion) {
    throw new TypeError("FrameworkRunState is not a Mastra turn run state of version 1");
  }
  const turnMessages = value["turn_messages"];
  const pendingToolCalls = value["pending_tool_calls"];
  if (!Array.isArray(turnMessages) || !Array.isArray(pendingToolCalls)) {
    throw new TypeError("Mastra turn run state lacks turn_messages or pending_tool_calls");
  }
  return {
    turnMessages: turnMessages.map((message, index) => decodeMastraMessage(message, `turn message ${index}`)),
    pendingToolCalls: pendingToolCalls.map((toolCall, index) => {
      if (
        !isRecord(toolCall) ||
        typeof toolCall["tool_call_id"] !== "string" ||
        typeof toolCall["tool_name"] !== "string"
      ) {
        throw new TypeError(`Mastra turn run state has a malformed pending tool call at index ${index}`);
      }
      return { toolCallId: toolCall["tool_call_id"], toolName: toolCall["tool_name"] };
    }),
  };
}

// An empty ID and an invalid Date mark values the caller fills.
function decodeMastraMessage(value: unknown, description: string): MastraDBMessage {
  if (!isRecord(value) || typeof value["role"] !== "string" || !isRecord(value["content"])) {
    throw new TypeError(`${description} is not a Mastra message`);
  }
  const content = value["content"];
  if (content["format"] !== 2 || !Array.isArray(content["parts"])) {
    throw new TypeError(`${description} is not a Mastra message in content format 2`);
  }
  const createdAt = value["createdAt"];
  return {
    ...(value as unknown as MastraDBMessage),
    id: typeof value["id"] === "string" ? value["id"] : "",
    createdAt:
      typeof createdAt === "string" || typeof createdAt === "number" ? new Date(createdAt) : new Date(Number.NaN),
  };
}

function isToolInvocationPart(part: MastraMessagePart): part is MastraToolInvocationPart {
  return part.type === "tool-invocation";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
