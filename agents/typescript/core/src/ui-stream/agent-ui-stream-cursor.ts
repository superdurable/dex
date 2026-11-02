// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { isFinalToolCallStatus, type AgentToolCallRecord } from "../agent-session-values.js";

/**
 * What the browser already shows of one tool call in the current model call.
 *
 * - `input`: the tool part exists; no approval was requested.
 * - `approval_requested`: the part waits for `resolve_interrupt`.
 * - `approval_resolved`: the session accepted the approval or rejection.
 * - `finished`: the part shows the call's result.
 */
export type ToolCallUIState = "input" | "approval_requested" | "approval_resolved" | "finished";

/** The `RunModel` Step execution whose parts the browser shows last. */
export interface ModelStepPosition {
  readonly stepExecutionId: string;
  attempt: number;
  openedPartCount: number;
  isUsageRecorded: boolean;
}

/** A text or reasoning part that the browser keeps open for further deltas. */
export interface OpenUIPart {
  readonly kind: "text" | "reasoning";
  readonly id: string;
}

/**
 * Everything a stateless server needs to continue a UI message stream exactly where a browser left it:
 * the Dex resume token plus the parts the browser has open.
 */
export interface AgentUIStreamCursor {
  /** Dex resume token of the last event whose parts the browser holds, or `""` for the retained head. */
  eventResumeToken: string;
  /** Parts of the event after `eventResumeToken` that the browser already holds. */
  skippedChunkCount: number;
  turnId: string | null;
  /** While `turnId` is `null`, only a turn with a higher number is followed. */
  afterTurnNumber: number | null;
  /** Dex resume token just before the followed turn's first event, when known. */
  turnStartResumeToken: string | null;
  isMessageStarted: boolean;
  isTurnFinished: boolean;
  modelStep: ModelStepPosition | null;
  openPart: OpenUIPart | null;
  readonly toolCalls: Map<string, ToolCallUIState>;
}

/** Maximum length of a UI stream resume token; it must fit an HTTP header. */
export const maximumAgentUIStreamResumeTokenLength = 64 * 1024;

const cursorVersion = 2;
const toolCallUIStates: readonly ToolCallUIState[] = [
  "input",
  "approval_requested",
  "approval_resolved",
  "finished",
];

/** Creates a cursor at one Stream position with nothing shown yet. */
export function createAgentUIStreamCursor(
  eventResumeToken: string,
  turnId: string | null,
  isMessageStarted: boolean,
): AgentUIStreamCursor {
  return {
    eventResumeToken,
    skippedChunkCount: 0,
    turnId,
    afterTurnNumber: null,
    turnStartResumeToken: null,
    isMessageStarted,
    isTurnFinished: false,
    modelStep: null,
    openPart: null,
    toolCalls: new Map(),
  };
}

/** Returns an independent copy of a cursor. */
export function cloneAgentUIStreamCursor(cursor: AgentUIStreamCursor): AgentUIStreamCursor {
  return {
    ...cursor,
    modelStep: cursor.modelStep === null ? null : { ...cursor.modelStep },
    toolCalls: new Map(cursor.toolCalls),
  };
}

/** Returns what a browser that rendered these tool call records shows for them. */
export function toolCallUIStateOfRecord(toolCall: AgentToolCallRecord): ToolCallUIState {
  if (isFinalToolCallStatus(toolCall.status)) {
    return "finished";
  }
  if (toolCall.status === "awaiting_approval") {
    return "approval_requested";
  }
  return toolCall.requiresApproval ? "approval_resolved" : "input";
}

/**
 * Encodes a cursor as the opaque, URL-safe UI stream resume token.
 *
 * Finished tool calls are dropped when the token would exceed the limit; a repeated event for one then
 * repeats an idempotent tool part update.
 * @throws {RangeError} When the token exceeds {@link maximumAgentUIStreamResumeTokenLength} anyway.
 */
export function encodeAgentUIStreamCursor(cursor: AgentUIStreamCursor): string {
  const resumeToken = encodeWireCursor(cursor, [...cursor.toolCalls.entries()]);
  if (resumeToken.length <= maximumAgentUIStreamResumeTokenLength) {
    return resumeToken;
  }
  const openToolCalls = [...cursor.toolCalls.entries()].filter(([, state]) => state !== "finished");
  const compactResumeToken = encodeWireCursor(cursor, openToolCalls);
  if (compactResumeToken.length > maximumAgentUIStreamResumeTokenLength) {
    throw new RangeError(
      `the agent UI stream resume token exceeds ${maximumAgentUIStreamResumeTokenLength} characters`,
    );
  }
  return compactResumeToken;
}

/**
 * Decodes a UI stream resume token from a browser.
 *
 * @throws {TypeError} When the token was not produced by {@link encodeAgentUIStreamCursor}.
 */
export function decodeAgentUIStreamCursor(resumeToken: string): AgentUIStreamCursor {
  if (resumeToken.length === 0 || resumeToken.length > maximumAgentUIStreamResumeTokenLength) {
    throw invalidResumeToken();
  }
  let wireCursor: unknown;
  try {
    wireCursor = JSON.parse(Buffer.from(resumeToken, "base64url").toString("utf8"));
  } catch {
    throw invalidResumeToken();
  }
  if (!isRecord(wireCursor) || wireCursor["v"] !== cursorVersion) {
    throw invalidResumeToken();
  }
  const {
    e: eventResumeToken,
    k: skippedChunkCount,
    t: turnId,
    a: afterTurnNumber,
    b: turnStartResumeToken,
    s: isMessageStarted,
    f: isTurnFinished,
  } = wireCursor;
  if (
    typeof eventResumeToken !== "string" ||
    !isNonNegativeInteger(skippedChunkCount) ||
    (turnId !== null && typeof turnId !== "string") ||
    (afterTurnNumber !== null && !isNonNegativeInteger(afterTurnNumber)) ||
    (turnStartResumeToken !== null && typeof turnStartResumeToken !== "string") ||
    typeof isMessageStarted !== "boolean" ||
    typeof isTurnFinished !== "boolean"
  ) {
    throw invalidResumeToken();
  }
  return {
    eventResumeToken,
    skippedChunkCount,
    turnId,
    afterTurnNumber,
    turnStartResumeToken,
    isMessageStarted,
    isTurnFinished,
    modelStep: decodeModelStep(wireCursor["m"]),
    openPart: decodeOpenPart(wireCursor["o"]),
    toolCalls: decodeToolCalls(wireCursor["c"]),
  };
}

function encodeWireCursor(
  cursor: AgentUIStreamCursor,
  toolCalls: readonly (readonly [string, ToolCallUIState])[],
): string {
  const modelStep = cursor.modelStep;
  const wireCursor = {
    v: cursorVersion,
    e: cursor.eventResumeToken,
    k: cursor.skippedChunkCount,
    t: cursor.turnId,
    a: cursor.afterTurnNumber,
    b: cursor.turnStartResumeToken,
    s: cursor.isMessageStarted,
    f: cursor.isTurnFinished,
    m:
      modelStep === null
        ? null
        : [modelStep.stepExecutionId, modelStep.attempt, modelStep.openedPartCount, modelStep.isUsageRecorded],
    o: cursor.openPart === null ? null : [cursor.openPart.kind, cursor.openPart.id],
    c: toolCalls,
  };
  return Buffer.from(JSON.stringify(wireCursor), "utf8").toString("base64url");
}

function decodeModelStep(value: unknown): ModelStepPosition | null {
  if (value === null) {
    return null;
  }
  if (!Array.isArray(value) || value.length !== 4) {
    throw invalidResumeToken();
  }
  const [stepExecutionId, attempt, openedPartCount, isUsageRecorded] = value as unknown[];
  if (
    typeof stepExecutionId !== "string" ||
    !isNonNegativeInteger(attempt) ||
    !isNonNegativeInteger(openedPartCount) ||
    typeof isUsageRecorded !== "boolean"
  ) {
    throw invalidResumeToken();
  }
  return { stepExecutionId, attempt, openedPartCount, isUsageRecorded };
}

function decodeOpenPart(value: unknown): OpenUIPart | null {
  if (value === null) {
    return null;
  }
  if (!Array.isArray(value) || value.length !== 2) {
    throw invalidResumeToken();
  }
  const [kind, id] = value as unknown[];
  if ((kind !== "text" && kind !== "reasoning") || typeof id !== "string") {
    throw invalidResumeToken();
  }
  return { kind, id };
}

function decodeToolCalls(value: unknown): Map<string, ToolCallUIState> {
  if (!Array.isArray(value)) {
    throw invalidResumeToken();
  }
  const toolCalls = new Map<string, ToolCallUIState>();
  for (const entry of value as unknown[]) {
    if (!Array.isArray(entry) || entry.length !== 2) {
      throw invalidResumeToken();
    }
    const [toolCallId, state] = entry as unknown[];
    if (typeof toolCallId !== "string" || !toolCallUIStates.includes(state as ToolCallUIState)) {
      throw invalidResumeToken();
    }
    toolCalls.set(toolCallId, state as ToolCallUIState);
  }
  return toolCalls;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

function invalidResumeToken(): TypeError {
  return new TypeError("the agent UI stream resume token is invalid");
}
