// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentMessage, AgentMessageRole, FrameworkMessage } from "@superdurable/dex-agents";

/**
 * `frameworkItem` `kind` of a user message that the session stored before its turn ran.
 *
 * The item is `{"kind": "user_prompt", "text": <text>}`. The Claude Code CLI records its own copy of
 * the prompt in the transcript when the turn runs, so this item is never replayed to the CLI. The
 * value matches the Python adapter.
 */
export const userPromptItemKind = "user_prompt";

/**
 * `frameworkItem` `kind` of a stored message that carries Claude Code transcript entries.
 *
 * The item is `{"kind": "claude_transcript", "session_id": <Claude session ID>, "entries": [...]}`.
 * `entries` are the CLI's transcript lines, unchanged. Concatenating the entries of every stored message
 * in sequence order rebuilds the session transcript. The value matches the Python adapter.
 */
export const claudeTranscriptItemKind = "claude_transcript";

const runStateFormatVersion = 1;

/** One Claude Code transcript line: an opaque JSON object with a string `type`. */
export type ClaudeTranscriptEntry = Record<string, unknown>;

interface ClaudeSessionTranscript {
  readonly sessionId: string | null;
  readonly entries: readonly ClaudeTranscriptEntry[];
}

interface SuspendedClaudeTurn {
  readonly sessionId: string;
  readonly turnEntries: readonly ClaudeTranscriptEntry[];
}

interface ContentBlock {
  readonly [key: string]: unknown;
}

export function encodeUserPromptItem(text: string): string {
  return JSON.stringify({ kind: userPromptItemKind, text });
}

/** One text block of a prompt that carries several stored texts. */
export interface PromptTextBlock {
  readonly type: "text";
  readonly text: string;
}

export function buildTurnPromptContent(
  messages: readonly AgentMessage[],
  turnId: string,
): string | PromptTextBlock[] {
  // Prompts and turn-failure notes after the last transcript never reached the CLI.
  let unsentPromptTexts: string[] = [];
  for (const message of messages) {
    const item = parseJsonObject(message.frameworkItem, `message ${message.sequence}`);
    if (item["kind"] === claudeTranscriptItemKind) {
      unsentPromptTexts = [];
    } else if (item["kind"] === userPromptItemKind && typeof item["text"] === "string") {
      unsentPromptTexts.push(item["text"]);
    }
  }
  const currentPrompt = messages.at(-1);
  if (
    currentPrompt === undefined ||
    currentPrompt.role !== "user" ||
    currentPrompt.turnId !== turnId ||
    unsentPromptTexts.length === 0
  ) {
    throw new Error(`turn ${turnId} does not end with its stored prompt`);
  }
  if (unsentPromptTexts.length === 1) {
    return unsentPromptTexts[0] as string;
  }
  return unsentPromptTexts.map((text) => ({ type: "text", text }));
}

export function loadSessionTranscript(messages: readonly AgentMessage[]): ClaudeSessionTranscript {
  let sessionId: string | null = null;
  const entries: ClaudeTranscriptEntry[] = [];
  for (const message of messages) {
    const item = parseJsonObject(message.frameworkItem, `message ${message.sequence}`);
    if (item["kind"] !== claudeTranscriptItemKind) {
      continue;
    }
    if (typeof item["session_id"] !== "string" || !Array.isArray(item["entries"])) {
      throw new TypeError(`message ${message.sequence} has a malformed Claude transcript item`);
    }
    sessionId = item["session_id"];
    entries.push(...(item["entries"] as ClaudeTranscriptEntry[]));
  }
  return { sessionId, entries };
}

export function encodeRunState(
  sessionId: string,
  turnEntries: readonly ClaudeTranscriptEntry[],
): string {
  return JSON.stringify({
    version: runStateFormatVersion,
    session_id: sessionId,
    turn_entries: turnEntries,
  });
}

export function decodeRunState(frameworkRunState: string): SuspendedClaudeTurn {
  const state = parseJsonObject(frameworkRunState, "the framework run state");
  if (state["version"] !== runStateFormatVersion) {
    throw new TypeError(`unsupported Claude run state version ${JSON.stringify(state["version"])}`);
  }
  if (typeof state["session_id"] !== "string" || !Array.isArray(state["turn_entries"])) {
    throw new TypeError("the Claude run state has no session_id or turn_entries");
  }
  return {
    sessionId: state["session_id"],
    turnEntries: state["turn_entries"] as ClaudeTranscriptEntry[],
  };
}

// One message per assistant API message or tool result; other entries ride along with the next.
export function projectTurnEntries(
  sessionId: string,
  turnEntries: readonly ClaudeTranscriptEntry[],
  policyToolName: (cliToolName: string) => string,
): FrameworkMessage[] {
  const groups: TranscriptEntryGroup[] = [];
  let leadingEntries: ClaudeTranscriptEntry[] = [];
  for (const entry of turnEntries) {
    const principalRole = principalRoleOf(entry);
    if (principalRole === undefined) {
      leadingEntries.push(entry);
      continue;
    }
    const previous = groups.at(-1);
    const messageId = assistantMessageIdOf(entry);
    if (
      principalRole === "assistant" &&
      leadingEntries.length === 0 &&
      previous?.role === "assistant" &&
      previous.messageId !== undefined &&
      previous.messageId === messageId
    ) {
      previous.entries.push(entry);
      continue;
    }
    groups.push({ role: principalRole, messageId, entries: [...leadingEntries, entry] });
    leadingEntries = [];
  }
  if (leadingEntries.length > 0) {
    const last = groups.at(-1);
    if (last === undefined) {
      groups.push({ role: "assistant", messageId: undefined, entries: leadingEntries });
    } else {
      last.entries.push(...leadingEntries);
    }
  }
  const toolNamesById = new Map<string, string>();
  return groups.map((group) => toFrameworkMessage(sessionId, group, toolNamesById, policyToolName));
}

// Non-text blocks become a placeholder such as `[image content omitted]`.
export function toolResultText(content: unknown): string {
  if (content === null || content === undefined) {
    return "";
  }
  if (typeof content === "string") {
    return content;
  }
  if (Array.isArray(content)) {
    return content
      .map((block: unknown) => {
        if (!isRecord(block)) {
          return JSON.stringify(block);
        }
        if (block["type"] === "text") {
          return String(block["text"] ?? "");
        }
        return `[${String(block["type"] ?? "unknown")} content omitted]`;
      })
      .join("\n");
  }
  return JSON.stringify(content);
}

interface TranscriptEntryGroup {
  readonly role: Exclude<AgentMessageRole, "user">;
  readonly messageId: string | undefined;
  readonly entries: ClaudeTranscriptEntry[];
}

function toFrameworkMessage(
  sessionId: string,
  group: TranscriptEntryGroup,
  toolNamesById: Map<string, string>,
  policyToolName: (cliToolName: string) => string,
): FrameworkMessage {
  const frameworkItem = JSON.stringify({
    kind: claudeTranscriptItemKind,
    session_id: sessionId,
    entries: group.entries,
  });
  const principalBlocks = group.entries
    .filter((entry) => principalRoleOf(entry) === group.role)
    .flatMap(contentBlocksOf);
  if (group.role === "assistant") {
    return toAssistantMessage(frameworkItem, principalBlocks, toolNamesById, policyToolName);
  }
  return toToolResultMessage(frameworkItem, principalBlocks, toolNamesById);
}

function toAssistantMessage(
  frameworkItem: string,
  blocks: readonly ContentBlock[],
  toolNamesById: Map<string, string>,
  policyToolName: (cliToolName: string) => string,
): FrameworkMessage {
  const texts = blocks.filter((block) => block["type"] === "text").map((block) => String(block["text"] ?? ""));
  const toolUses = blocks.filter((block) => block["type"] === "tool_use");
  for (const toolUse of toolUses) {
    toolNamesById.set(String(toolUse["id"]), policyToolName(String(toolUse["name"])));
  }
  const singleToolUse = toolUses.length === 1 ? toolUses[0] : undefined;
  const singleToolCallId = singleToolUse === undefined ? null : String(singleToolUse["id"]);
  return {
    role: "assistant",
    text: texts.length > 0 ? texts.join("") : null,
    toolCallId: singleToolCallId,
    toolName: singleToolCallId === null ? null : (toolNamesById.get(singleToolCallId) ?? null),
    toolArguments: singleToolUse === undefined ? null : JSON.stringify(singleToolUse["input"] ?? {}),
    toolOutput: null,
    frameworkItem,
  };
}

function toToolResultMessage(
  frameworkItem: string,
  blocks: readonly ContentBlock[],
  toolNamesById: ReadonlyMap<string, string>,
): FrameworkMessage {
  const toolResults = blocks.filter((block) => block["type"] === "tool_result");
  const singleResult = toolResults.length === 1 ? toolResults[0] : undefined;
  if (singleResult === undefined) {
    return {
      role: "tool",
      text: null,
      toolCallId: null,
      toolName: null,
      toolArguments: null,
      toolOutput: null,
      frameworkItem,
    };
  }
  const toolCallId = String(singleResult["tool_use_id"]);
  return {
    role: "tool",
    text: null,
    toolCallId,
    toolName: toolNamesById.get(toolCallId) ?? null,
    toolArguments: null,
    toolOutput: toolResultText(singleResult["content"]),
    frameworkItem,
  };
}

function principalRoleOf(entry: ClaudeTranscriptEntry): Exclude<AgentMessageRole, "user"> | undefined {
  const entryType = entry["type"];
  if (entryType !== "assistant" && entryType !== "user") {
    return undefined;
  }
  if (entry["isMeta"] === true || entry["isSidechain"] === true || isPresent(entry["teamName"])) {
    return undefined;
  }
  if (entryType === "assistant") {
    return "assistant";
  }
  return contentBlocksOf(entry).some((block) => block["type"] === "tool_result") ? "tool" : undefined;
}

function contentBlocksOf(entry: ClaudeTranscriptEntry): ContentBlock[] {
  const message = entry["message"];
  if (!isRecord(message) || !Array.isArray(message["content"])) {
    return [];
  }
  return message["content"].filter(isRecord);
}

function assistantMessageIdOf(entry: ClaudeTranscriptEntry): string | undefined {
  if (entry["type"] !== "assistant") {
    return undefined;
  }
  const message = entry["message"];
  return isRecord(message) && typeof message["id"] === "string" ? message["id"] : undefined;
}

function parseJsonObject(text: string, description: string): Record<string, unknown> {
  const value: unknown = JSON.parse(text);
  if (!isRecord(value)) {
    throw new TypeError(`${description} is not a JSON object`);
  }
  return value;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isPresent(value: unknown): boolean {
  return value !== undefined && value !== null && value !== "" && value !== false;
}
