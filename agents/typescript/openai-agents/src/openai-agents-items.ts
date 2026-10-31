// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentInputItem, AgentOutputItem, Usage } from "@openai/agents";
import { getToolCallDisplayName } from "@openai/agents/utils";
import type { AgentMessage, AgentUsage, FrameworkMessage } from "@superdurable/dex-agents";

/**
 * Builds the OpenAI Agents SDK user message item for user text.
 *
 * @param text - User message text.
 * @returns A user message item without an ID, so the result depends only on `text`.
 */
export function buildUserMessageItem(text: string): AgentInputItem {
  return { type: "message", role: "user", content: text };
}

/**
 * Rebuilds the OpenAI Agents SDK input list from stored session messages.
 *
 * @param messages - Stored messages in sequence order.
 * @returns One input item per message, parsed from its `frameworkItem` JSON.
 */
export function readAgentInputItems(messages: readonly AgentMessage[]): AgentInputItem[] {
  return messages.map((message) => JSON.parse(message.frameworkItem) as AgentInputItem);
}

/**
 * Projects one OpenAI Agents SDK item onto the session message fields.
 *
 * @param item - A user message, a model output item, or a tool result item.
 * @returns The framework message: the item's JSON in `frameworkItem` and display fields beside it.
 */
export function toFrameworkMessage(item: AgentInputItem | AgentOutputItem): FrameworkMessage {
  const frameworkItem = JSON.stringify(item);
  const emptyProjection = {
    text: null,
    toolCallId: null,
    toolName: null,
    toolArguments: null,
    toolOutput: null,
    frameworkItem,
  };
  switch (item.type) {
    case "function_call":
      return {
        ...emptyProjection,
        role: "assistant",
        toolCallId: item.callId,
        toolName: getToolCallDisplayName(item) ?? item.name,
        toolArguments: item.arguments,
      };
    case "function_call_result":
      return {
        ...emptyProjection,
        role: "tool",
        toolCallId: item.callId,
        toolName: getToolCallDisplayName(item) ?? item.name,
        toolOutput: describeFunctionCallOutput(item.output),
      };
    case "hosted_tool_call":
      return {
        ...emptyProjection,
        role: "assistant",
        toolName: item.name,
        toolArguments: item.arguments ?? null,
        toolOutput: item.output ?? null,
      };
    case "message":
    case undefined:
      if ("role" in item && item.role === "user") {
        return { ...emptyProjection, role: "user", text: describeMessageContent(item.content) };
      }
      if ("role" in item && item.role === "assistant") {
        return { ...emptyProjection, role: "assistant", text: describeMessageContent(item.content) };
      }
      return { ...emptyProjection, role: "assistant" };
    default:
      return { ...emptyProjection, role: "assistant" };
  }
}

/**
 * Converts OpenAI Agents SDK usage into session usage.
 *
 * @param usage - Usage aggregated by one `RunModel` Step.
 * @returns Session usage; cached and reasoning tokens are summed from the per-request details.
 */
export function toAgentUsage(usage: Usage): AgentUsage {
  return {
    modelCalls: usage.requests,
    inputTokens: usage.inputTokens,
    outputTokens: usage.outputTokens,
    cachedInputTokens: sumTokenDetail(usage.inputTokensDetails, "cached_tokens"),
    reasoningTokens: sumTokenDetail(usage.outputTokensDetails, "reasoning_tokens"),
  };
}

function describeMessageContent(content: unknown): string | null {
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return null;
  }
  const texts = content.flatMap((part: unknown) => {
    if (typeof part !== "object" || part === null) {
      return [];
    }
    const typedPart = part as { type?: unknown; text?: unknown; refusal?: unknown };
    if (typeof typedPart.text === "string") {
      return [typedPart.text];
    }
    return typeof typedPart.refusal === "string" ? [typedPart.refusal] : [];
  });
  return texts.length === 0 ? null : texts.join("");
}

function describeFunctionCallOutput(output: unknown): string {
  if (typeof output === "string") {
    return output;
  }
  const text = describeMessageContent(Array.isArray(output) ? output : [output]);
  return text ?? JSON.stringify(output);
}

function sumTokenDetail(details: readonly Readonly<Record<string, unknown>>[], detailName: string): number {
  return details.reduce((total, detail) => {
    const count = detail[detailName];
    return total + (typeof count === "number" ? count : 0);
  }, 0);
}
