// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";

import type { AgentImageReference, AgentMessage, AgentMessageRole, FrameworkMessage } from "@superdurable/dex-agents";

import {
  decodeImageReference,
  encodeImageReference,
  imageBlock,
  imageReferencesIn,
  type PromptImageBlock,
} from "./claude-transcript-images.js";

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
 * `entries` are the CLI's transcript lines, unchanged, except that `last-prompt` entries are left out; the
 * adapter also drops the ones adapter 0.4.0 stored when it reads them. Concatenating the entries of every
 * stored message in sequence order rebuilds the session transcript. The value matches the Python adapter.
 * With a media store, each base64 image `source` keeps `{"dex_image_reference": <reference fields>}` as its
 * `data` instead of the bytes.
 */
export const claudeTranscriptItemKind = "claude_transcript";

// turn_entries holds the turn's entries the session has not stored, none once it stores each segment.
const runStateFormatVersion = 1;
const unfinishedToolResultUuidNamespace = Buffer.from("6f0d3c1e8a2b4f57a9d4c3e2b1a09876", "hex");
// Claude Code's entry that names the leaf a resume continues from.
export const leafPointerEntryType = "last-prompt";

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

export function encodeUserPromptItem(text: string, images: readonly AgentImageReference[] = []): string {
  if (images.length === 0) {
    return JSON.stringify({ kind: userPromptItemKind, text });
  }
  return JSON.stringify({ kind: userPromptItemKind, text, images: images.map(encodeImageReference) });
}

/** One text block of a prompt that carries several stored texts. */
export interface PromptTextBlock {
  readonly type: "text";
  readonly text: string;
}

/** One content block of a prompt: text, or an image whose block holds a reference until the runner loads it. */
export type PromptContentBlock = PromptTextBlock | PromptImageBlock;

interface UnsentPrompt {
  readonly text: string;
  readonly images: readonly AgentImageReference[];
}

export function buildTurnPromptContent(
  messages: readonly AgentMessage[],
  turnId: string,
): string | PromptContentBlock[] {
  // Prompts and turn-failure notes after the last transcript never reached the CLI.
  let unsentPrompts: UnsentPrompt[] = [];
  for (const message of messages) {
    const item = parseJsonObject(message.frameworkItem, `message ${message.sequence}`);
    if (item["kind"] === claudeTranscriptItemKind) {
      unsentPrompts = [];
    } else if (item["kind"] === userPromptItemKind && typeof item["text"] === "string") {
      const images = Array.isArray(item["images"]) ? item["images"].map(decodeImageReference) : [];
      unsentPrompts.push({ text: item["text"], images });
    }
  }
  const currentPrompt = messages.at(-1);
  if (
    currentPrompt === undefined ||
    currentPrompt.role !== "user" ||
    currentPrompt.turnId !== turnId ||
    unsentPrompts.length === 0
  ) {
    throw new Error(`turn ${turnId} does not end with its stored prompt`);
  }
  const [onlyPrompt] = unsentPrompts;
  if (unsentPrompts.length === 1 && onlyPrompt !== undefined && onlyPrompt.images.length === 0) {
    return onlyPrompt.text;
  }
  return unsentPrompts.flatMap((prompt): PromptContentBlock[] => [
    { type: "text", text: prompt.text },
    ...prompt.images.map(imageBlock),
  ]);
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
  return { sessionId, entries: withoutLeafPointers(entries) };
}

/** Returns the transcript entries of the latest turn before `currentTurnId` that stored any. */
export function loadLastEndedTurnEntries(
  messages: readonly AgentMessage[],
  currentTurnId: string,
): ClaudeTranscriptEntry[] {
  const endedTurnMessages = messages.filter(
    (message) => message.turnId !== currentTurnId && message.role !== "user",
  );
  const lastTurnId = [...endedTurnMessages]
    .reverse()
    .find(
      (message) =>
        parseJsonObject(message.frameworkItem, `message ${message.sequence}`)["kind"] === claudeTranscriptItemKind,
    )?.turnId;
  if (lastTurnId === undefined) {
    return [];
  }
  return [...loadSessionTranscript(endedTurnMessages.filter((message) => message.turnId === lastTurnId)).entries];
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
    turnEntries: withoutLeafPointers(state["turn_entries"] as ClaudeTranscriptEntry[]),
  };
}

/** Drops `last-prompt` entries: a stored leaf pointer would resume at a stale message. */
export function withoutLeafPointers(entries: readonly ClaudeTranscriptEntry[]): ClaudeTranscriptEntry[] {
  return entries.filter((entry) => entry["type"] !== leafPointerEntryType);
}

// One message per assistant API message or tool result; other entries ride along with the next.
// storedTurnEntries name the tools of results whose tool use the session already stored.
export function projectTurnEntries(
  sessionId: string,
  turnEntries: readonly ClaudeTranscriptEntry[],
  policyToolName: (cliToolName: string) => string,
  storedTurnEntries: readonly ClaudeTranscriptEntry[] = [],
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
  for (const toolUse of mainToolUsesOf(storedTurnEntries)) {
    toolNamesById.set(toolUse.toolUseId, policyToolName(toolUse.cliToolName));
  }
  return groups.map((group) => toFrameworkMessage(sessionId, group, toolNamesById, policyToolName));
}

/** Content of a tool result that the adapter records for a tool use the CLI left without one. */
export interface UnfinishedToolResult {
  readonly text: string;
  readonly isError: boolean;
  readonly images: readonly AgentImageReference[];
}

/** A main-conversation tool use of a turn transcript. */
export interface TranscriptToolUse {
  readonly toolUseId: string;
  readonly cliToolName: string;
}

// Entries shaped like the CLI's own results; uuid and timestamp derive from the inputs, so rebuilding is exact.
export function buildUnfinishedToolResultEntries(
  sessionId: string,
  turnEntries: readonly ClaudeTranscriptEntry[],
  describeResult: (toolUse: TranscriptToolUse) => UnfinishedToolResult,
): ClaudeTranscriptEntry[] {
  const answeredIds = answeredToolUseIds(turnEntries);
  const unansweredToolUses = mainToolUsesOf(turnEntries).filter((toolUse) => !answeredIds.has(toolUse.toolUseId));
  if (unansweredToolUses.length === 0) {
    return [];
  }
  const conversationEntries = turnEntries.filter(
    (entry) => (entry["type"] === "user" || entry["type"] === "assistant") && typeof entry["uuid"] === "string",
  );
  const timestamps = turnEntries.map((entry) => entry["timestamp"]).filter((value) => typeof value === "string");
  const timestamp = timestamps.length === 0 ? undefined : [...timestamps].sort().at(-1);
  // The CLI chains a deferred call's result to the last conversation entry, skipping attachments.
  let parentUuid = conversationEntries.at(-1)?.["uuid"] ?? null;
  const resultEntries: ClaudeTranscriptEntry[] = [];
  for (const toolUse of unansweredToolUses) {
    const result = describeResult(toolUse);
    const uuid = nameBasedUuid(`${sessionId}/${toolUse.toolUseId}`);
    resultEntries.push({
      parentUuid,
      isSidechain: false,
      type: "user",
      message: {
        role: "user",
        content: [
          {
            tool_use_id: toolUse.toolUseId,
            type: "tool_result",
            content:
              result.images.length === 0
                ? result.text
                : [{ type: "text", text: result.text }, ...result.images.map(imageBlock)],
            ...(result.isError ? { is_error: true } : {}),
          },
        ],
      },
      uuid,
      ...(timestamp === undefined ? {} : { timestamp }),
      toolUseResult: result.isError ? `Error: ${result.text}` : result.text,
      sourceToolAssistantUUID: toolUse.assistantUuid,
      ...toolUse.envelope,
    });
    parentUuid = uuid;
  }
  return resultEntries;
}

interface MainToolUse extends TranscriptToolUse {
  readonly assistantUuid: string | null;
  // The session fields of the tool use's assistant entry, which the CLI writes on every entry.
  readonly envelope: Readonly<Record<string, unknown>>;
}

const envelopeFieldNames = ["userType", "entrypoint", "cwd", "sessionId", "version", "gitBranch"] as const;

function mainToolUsesOf(entries: readonly ClaudeTranscriptEntry[]): MainToolUse[] {
  return entries
    .filter((entry) => principalRoleOf(entry) === "assistant")
    .flatMap((entry) => {
      const envelope = Object.fromEntries(
        envelopeFieldNames.filter((fieldName) => entry[fieldName] !== undefined).map((fieldName) => [fieldName, entry[fieldName]]),
      );
      const assistantUuid = typeof entry["uuid"] === "string" ? entry["uuid"] : null;
      return contentBlocksOf(entry)
        .filter((block) => block["type"] === "tool_use" && typeof block["id"] === "string")
        .map((block) => ({ toolUseId: String(block["id"]), cliToolName: String(block["name"]), assistantUuid, envelope }));
    });
}

// A name-based (version 5) UUID in a namespace of this adapter, so a rebuilt entry keeps its uuid.
function nameBasedUuid(name: string): string {
  const digest = createHash("sha1").update(unfinishedToolResultUuidNamespace).update(name, "utf8").digest();
  const bytes = digest.subarray(0, 16);
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x50;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
}

/** Returns the IDs of the tool uses that have a `tool_result` in `entries`. */
export function answeredToolUseIds(entries: readonly ClaudeTranscriptEntry[]): ReadonlySet<string> {
  return new Set(
    entries
      .flatMap(contentBlocksOf)
      .filter((block) => block["type"] === "tool_result" && typeof block["tool_use_id"] === "string")
      .map((block) => String(block["tool_use_id"])),
  );
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
  // The images the model saw in this result; stored blocks hold only references.
  const images = imageReferencesIn(singleResult["content"]);
  return {
    role: "tool",
    text: null,
    toolCallId,
    toolName: toolNamesById.get(toolCallId) ?? null,
    toolArguments: null,
    toolOutput: toolResultText(singleResult["content"]),
    frameworkItem,
    ...(images.length === 0 ? {} : { images }),
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
