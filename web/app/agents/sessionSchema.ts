// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import type { V2CatalogEntry } from '@/lib/types';

/** Names Dex Web reads by name; the Dex Web reference states the compatibility rule. */
export const SESSION_ATTRIBUTE = {
  state: 'AgentState',
  messages: 'AgentMessages',
  messageChunks: 'AgentMessageChunks',
  messageIndex: 'AgentMessageIndex',
  toolCalls: 'AgentToolCalls',
  pendingInterrupts: 'PendingInterrupts',
  usage: 'AgentUsage',
  sessionSettings: 'SessionSettings',
} as const;

/** A Flow shows the Session tab only when its graph declares all of these. */
export const REQUIRED_SESSION_ATTRIBUTES = [
  SESSION_ATTRIBUTE.state,
  SESSION_ATTRIBUTE.messages,
  SESSION_ATTRIBUTE.toolCalls,
  SESSION_ATTRIBUTE.pendingInterrupts,
] as const;

export const SESSION_RPC = {
  resolveInterrupt: 'resolve_interrupt',
  answerQuestion: 'answer_question',
  steerUserMessage: 'steer_user_message',
  cancelTurn: 'cancel_turn',
  sendUserMessage: 'send_user_message',
  updateSessionSettings: 'update_session_settings',
} as const;

/** Step types whose input is one tool call ID. */
export const TOOL_CALL_STEP_TYPES: readonly string[] = ['CallTool', 'CallSingleAttemptTool', 'PollToolTask'];
/** Step types that record a tool call's failure; their input is the tool call ID too. */
export const TOOL_CALL_RECOVERY_STEP_TYPES: readonly string[] = ['RecordToolFailure', 'RecordToolOutcomeUnknown'];
export const APPROVAL_STEP_TYPE = 'AwaitToolApproval';
export const QUESTION_STEP_TYPE = 'AwaitQuestionAnswer';

/**
 * Start of the error of a failed call whose tool never ran. Records from before the skipped marker,
 * and calls whose tool Step failed before the tool ran, are read as skipped by this prefix.
 */
export const TOOL_CALL_NOT_RUN_PREFIX = 'the tool did not run, so it is safe to call again';

export interface AgentSessionSchema {
  language: string;
  resolveInterrupt?: FlowV2Action;
  answerQuestion?: FlowV2Action;
  steerUserMessage?: FlowV2Action;
  cancelTurn?: FlowV2Action;
  /** False for a session Flow without the question Step, such as a Go session. */
  hasQuestions: boolean;
  /** The RPCs the graph declares; sending and settings need theirs. */
  rpcNames: readonly string[];
}

/** The session Start Step type, which Dex Web's start route uses. */
export const SESSION_START_STEP_TYPE = 'StartAgentSession';

/**
 * The session schema of a v2 catalog entry, or null when the Flow is not a Dex Agents session.
 *
 * Dex Web's server reads the session parts from the graph with loadAgentSession in
 * web/flow_rendering_agent_session.go, which also guards the session routes, so both use one rule.
 */
export function agentSessionSchema(entry: V2CatalogEntry | null | undefined): AgentSessionSchema | null {
  const session = entry?.agentSession;
  if (!entry || !session) return null;
  const action = (rpcName: string) => entry.definition.actions.find((candidate) => candidate.rpcName === rpcName);
  return {
    language: session.language,
    resolveInterrupt: action(SESSION_RPC.resolveInterrupt),
    answerQuestion: action(SESSION_RPC.answerQuestion),
    steerUserMessage: action(SESSION_RPC.steerUserMessage),
    cancelTurn: action(SESSION_RPC.cancelTurn),
    hasQuestions: session.hasQuestions,
    rpcNames: session.rpcNames,
  };
}

export interface AgentStateRecord {
  status: string;
  turnCount: number;
  activeTurnId: string | null;
  lastTurnError: string | null;
  acceptsTurnControl: boolean;
}

export interface AgentMessageRecord {
  sequence: number;
  turnId: string;
  role: string;
  text: string | null;
  toolCallId: string | null;
  toolName: string | null;
  toolArguments: string | null;
  toolOutput: string | null;
  createdAt: string | null;
}

export interface AgentToolCallRecord {
  toolCallId: string;
  turnId: string;
  toolName: string;
  arguments: string;
  effect: string;
  requiresApproval: boolean;
  status: string;
  output: string | null;
  error: string | null;
  rejectionMessage: string | null;
  deferralOrder: number;
  cancellationReason: string | null;
  inputQuestion: string | null;
  waitUntil: string | null;
  taskId: string | null;
  /** Write ordering skipped the call because an earlier write of its turn did not succeed. */
  skipped: boolean;
  /** The call waits for an earlier write of its turn; no Step runs it yet. */
  writeQueued: boolean;
}

export interface AgentInterruptRecord {
  interruptId: string;
  kind: string;
  toolCallId: string;
  toolName: string;
  arguments: string;
  createdAt: string | null;
  deadline: string | null;
  question: string | null;
  choices: string[];
  allowsFreeText: boolean;
}

export interface AgentUsageRecord {
  modelCalls: number;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number;
  reasoningTokens: number;
}

export interface AgentMessageIndexRecord {
  firstSequence: number;
  windowFirstSequence: number;
  chunkCount: number;
}

type Data = Record<string, unknown>;

function isData(value: unknown): value is Data {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function text(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function integer(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && /^-?\d+$/.test(value)) return Number(value);
  return null;
}

export function decodeAgentState(value: unknown): AgentStateRecord | null {
  if (!isData(value) || typeof value.status !== 'string') return null;
  return {
    status: value.status,
    turnCount: integer(value.turn_count) ?? 0,
    activeTurnId: text(value.active_turn_id),
    lastTurnError: text(value.last_turn_error),
    acceptsTurnControl: value.accepts_turn_control === true,
  };
}

export function decodeAgentMessage(value: unknown): AgentMessageRecord | null {
  if (!isData(value)) return null;
  const sequence = integer(value.sequence);
  if (sequence === null || typeof value.role !== 'string') return null;
  return {
    sequence,
    turnId: text(value.turn_id) ?? '',
    role: value.role,
    text: text(value.text),
    toolCallId: text(value.tool_call_id),
    toolName: text(value.tool_name),
    toolArguments: text(value.tool_arguments),
    toolOutput: text(value.tool_output),
    createdAt: text(value.created_at),
  };
}

export function decodeAgentMessageChunk(value: unknown): AgentMessageRecord[] | null {
  if (!isData(value) || !Array.isArray(value.messages)) return null;
  const messages = value.messages.map(decodeAgentMessage);
  return messages.every((message) => message !== null) ? messages as AgentMessageRecord[] : null;
}

export function decodeAgentToolCall(value: unknown): AgentToolCallRecord | null {
  if (!isData(value)) return null;
  const toolCallId = text(value.tool_call_id);
  const toolName = text(value.tool_name);
  const status = text(value.status);
  if (!toolCallId || toolName === null || status === null) return null;
  return {
    toolCallId,
    turnId: text(value.turn_id) ?? '',
    toolName,
    arguments: text(value.arguments) ?? '',
    effect: text(value.effect) ?? '',
    requiresApproval: value.requires_approval === true,
    status,
    output: text(value.output),
    error: text(value.error),
    rejectionMessage: text(value.rejection_message),
    deferralOrder: integer(value.deferral_order) ?? 0,
    cancellationReason: text(value.cancellation_reason),
    inputQuestion: text(value.input_question),
    waitUntil: text(value.wait_until),
    taskId: text(value.task_id),
    skipped: value.skipped === true,
    writeQueued: value.write_queued === true,
  };
}

export function decodePendingInterrupts(value: unknown): AgentInterruptRecord[] | null {
  if (value === null || value === undefined) return [];
  if (!Array.isArray(value)) return null;
  const interrupts: AgentInterruptRecord[] = [];
  for (const item of value) {
    if (!isData(item)) return null;
    const interruptId = text(item.interrupt_id);
    const kind = text(item.kind);
    if (!interruptId || !kind) return null;
    interrupts.push({
      interruptId,
      kind,
      toolCallId: text(item.tool_call_id) ?? '',
      toolName: text(item.tool_name) ?? '',
      arguments: text(item.arguments) ?? '',
      createdAt: text(item.created_at),
      deadline: text(item.deadline),
      question: text(item.question),
      choices: Array.isArray(item.choices) ? item.choices.filter((choice): choice is string => typeof choice === 'string') : [],
      allowsFreeText: item.allows_free_text !== false,
    });
  }
  return interrupts;
}

/** Decodes the SessionSettings Attribute: one JSON object. */
export function decodeSessionSettings(value: unknown): Record<string, unknown> | null {
  return isData(value) ? value : null;
}

export function decodeAgentUsage(value: unknown): AgentUsageRecord | null {
  if (!isData(value)) return null;
  return {
    modelCalls: integer(value.model_calls) ?? 0,
    inputTokens: integer(value.input_tokens) ?? 0,
    outputTokens: integer(value.output_tokens) ?? 0,
    cachedInputTokens: integer(value.cached_input_tokens) ?? 0,
    reasoningTokens: integer(value.reasoning_tokens) ?? 0,
  };
}

export function decodeAgentMessageIndex(value: unknown): AgentMessageIndexRecord | null {
  if (!isData(value)) return null;
  const firstSequence = integer(value.first_sequence);
  const windowFirstSequence = integer(value.window_first_sequence);
  if (firstSequence === null || windowFirstSequence === null) return null;
  return {
    firstSequence,
    windowFirstSequence,
    chunkCount: Array.isArray(value.chunks) ? value.chunks.length : 0,
  };
}

/** Splits an AttributeMap key such as "AgentToolCalls/RunModel-2-0" into name and instance. */
export function attributeMapInstance(key: string, attributeName: string): string | null {
  const prefix = `${attributeName}/`;
  return key.startsWith(prefix) ? key.slice(prefix.length) : null;
}
