// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { isBlobReferenceValue, isStoredValueUnavailable } from '@/lib/blobs';
import type { ActiveStepExecution, FlowHistoryEvent, KeyValue } from '@/lib/types';
import {
  SESSION_ATTRIBUTE,
  TOOL_CALL_NOT_RUN_PREFIX,
  TOOL_CALL_RECOVERY_STEP_TYPES,
  TOOL_CALL_STEP_TYPES,
  attributeMapInstance,
  decodeAgentMessage,
  decodeAgentMessageChunk,
  decodeAgentMessageIndex,
  decodeAgentState,
  decodeAgentToolCall,
  decodeAgentUsage,
  decodeSessionSettings,
  decodePendingInterrupts,
  type AgentInterruptRecord,
  type AgentMessageIndexRecord,
  type AgentMessageRecord,
  type AgentStateRecord,
  type AgentToolCallRecord,
  type AgentUsageRecord,
} from './sessionSchema';

/** How one tool call reads to an operator. Each value has its own wording and colour. */
export type ToolCallState =
  | 'awaiting_approval'
  | 'awaiting_answer'
  | 'deciding'
  | 'queued'
  | 'write_queued'
  | 'recording'
  | 'running'
  | 'retrying'
  | 'waiting'
  | 'done'
  | 'failed'
  | 'outcome_unknown'
  | 'skipped'
  | 'rejected'
  | 'cancelled'
  | 'returned'
  | 'no_result';

/** Where a call's facts came from: the live record, Flow history, or message text only. */
export type ToolCallSource = 'live' | 'history' | 'messages';

export interface ToolCallView {
  toolCallId: string;
  toolName: string;
  turnId: string;
  argumentsText: string;
  /** Null when only message text is left, which does not keep the effect class. */
  effect: string | null;
  requiresApproval: boolean | null;
  state: ToolCallState;
  source: ToolCallSource;
  interrupt?: AgentInterruptRecord;
  /** For a queued or running call: attempt n of maxAttempts. */
  attempt?: number;
  maxAttempts?: number;
  pendingReason?: 'worker' | 'supporting_worker' | 'locks';
  /** When the attempt became pending, or when a backoff ends. */
  since?: string;
  stepType?: string;
  stepExecutionId?: string;
  output?: string | null;
  error?: string | null;
  rejectionMessage?: string | null;
  cancellationReason?: string | null;
  /** Write ordering skipped the call, by the record's skipped marker. */
  skippedAfterWrite?: boolean;
}

export type ConversationItem =
  | { kind: 'message'; key: string; message: AgentMessageRecord; stepExecutionId?: string }
  | { kind: 'tool'; key: string; call: ToolCallView }
  | { kind: 'missing'; key: string; label: string };

export interface SessionReadInput {
  /** The session's Attributes after blob hydration. */
  attributes: readonly KeyValue[];
  activeSteps: readonly ActiveStepExecution[];
  /** The loaded Flow history window, oldest first. */
  events: readonly FlowHistoryEvent[];
  /** Older messages, loaded on request from AgentMessageChunks. */
  chunkMessages?: readonly AgentMessageRecord[];
}

export interface SessionView {
  state: AgentStateRecord | null;
  usage: AgentUsageRecord | null;
  /** The session's settings; null when the session has none, or they could not be read. */
  sessionSettings: Record<string, unknown> | null;
  messageIndex: AgentMessageIndexRecord | null;
  pendingInterrupts: AgentInterruptRecord[];
  items: ConversationItem[];
  /** Every pending approval or question, one entry per call, in deferral order. */
  waitingCalls: ToolCallView[];
  /** Names of Attributes or values that could not be read or did not match the schema. */
  missing: string[];
  /** True when a past call shows only what its message text keeps. */
  hasCallsFromMessagesOnly: boolean;
  chunkKeys: string[];
}

const PHASE_PENDING = new Set<unknown>([1, 'STEP_METHOD_INVOCATION_PHASE_PENDING']);
const PHASE_RUNNING = new Set<unknown>([2, 'STEP_METHOD_INVOCATION_PHASE_RUNNING']);
const PHASE_BACKOFF = new Set<unknown>([3, 'STEP_METHOD_INVOCATION_PHASE_BACKOFF']);
const REASON_WORKER = new Set<unknown>([1, 'STEP_METHOD_PENDING_REASON_WAITING_FOR_WORKER']);
const REASON_SUPPORTING_WORKER = new Set<unknown>([2, 'STEP_METHOD_PENDING_REASON_WAITING_FOR_SUPPORTING_WORKER']);
const REASON_LOCKS = new Set<unknown>([3, 'STEP_METHOD_PENDING_REASON_WAITING_FOR_LOCKS']);

type Data = Record<string, unknown>;

function asData(value: unknown): Data {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Data : {};
}

function isUnreadable(value: unknown): boolean {
  return isStoredValueUnavailable(value) || isBlobReferenceValue(value);
}

/** Builds the conversation from the session's Attributes, active Steps, and Flow history. */
export function readSession(input: SessionReadInput): SessionView {
  const missing: string[] = [];
  const values = new Map(input.attributes.map((entry) => [entry.key, entry.value]));

  const readSingle = <T>(name: string, decode: (value: unknown) => T | null, required: boolean): T | null => {
    if (!values.has(name)) {
      if (required) missing.push(`${name} is not set`);
      return null;
    }
    const raw = values.get(name);
    if (isUnreadable(raw)) {
      missing.push(`${name} could not be loaded`);
      return null;
    }
    const decoded = decode(raw);
    if (decoded === null) missing.push(`${name} does not match the session schema`);
    return decoded;
  };

  const state = readSingle(SESSION_ATTRIBUTE.state, decodeAgentState, true);
  const usage = readSingle(SESSION_ATTRIBUTE.usage, decodeAgentUsage, false);
  const sessionSettings = readSingle(SESSION_ATTRIBUTE.sessionSettings, decodeSessionSettings, false);
  const messageIndex = readSingle(SESSION_ATTRIBUTE.messageIndex, decodeAgentMessageIndex, false);
  const pendingInterrupts = values.has(SESSION_ATTRIBUTE.pendingInterrupts)
    ? readSingle(SESSION_ATTRIBUTE.pendingInterrupts, decodePendingInterrupts, false) ?? []
    : [];

  const messages: AgentMessageRecord[] = [...(input.chunkMessages ?? [])];
  const unreadableMessages: Array<{ sequence: number; key: string }> = [];
  const liveCalls = new Map<string, AgentToolCallRecord>();
  const chunkKeys: string[] = [];
  for (const [key, raw] of values) {
    const messageKey = attributeMapInstance(key, SESSION_ATTRIBUTE.messages);
    if (messageKey !== null) {
      const message = isUnreadable(raw) ? null : decodeAgentMessage(raw);
      if (message === null) {
        unreadableMessages.push({ sequence: Number(messageKey), key });
        missing.push(`${key} ${isUnreadable(raw) ? 'could not be loaded' : 'does not match the session schema'}`);
      } else {
        messages.push(message);
      }
      continue;
    }
    if (attributeMapInstance(key, SESSION_ATTRIBUTE.messageChunks) !== null) {
      chunkKeys.push(key);
      continue;
    }
    const callKey = attributeMapInstance(key, SESSION_ATTRIBUTE.toolCalls);
    if (callKey !== null) {
      const call = isUnreadable(raw) ? null : decodeAgentToolCall(raw);
      if (call === null) missing.push(`${key} ${isUnreadable(raw) ? 'could not be loaded' : 'does not match the session schema'}`);
      else liveCalls.set(call.toolCallId, call);
    }
  }
  chunkKeys.sort();
  const bySequence = new Map<number, AgentMessageRecord>();
  for (const message of messages) bySequence.set(message.sequence, message);
  const ordered = [...bySequence.values()].sort((left, right) => left.sequence - right.sequence);

  const fromHistory = historyRecords(input.events);
  const results = new Map<string, AgentMessageRecord>();
  for (const message of ordered) {
    if (message.role === 'tool' && message.toolCallId) results.set(message.toolCallId, message);
  }

  const items: ConversationItem[] = [];
  const placed = new Set<string>();
  let hasCallsFromMessagesOnly = false;
  const missingBySequence = [...unreadableMessages].sort((left, right) => left.sequence - right.sequence);
  let missingIndex = 0;

  const placeLiveCallsOfTurn = (turnId: string) => {
    const unplaced = [...liveCalls.values()]
      .filter((call) => call.turnId === turnId && !placed.has(call.toolCallId))
      .sort((left, right) => left.deferralOrder - right.deferralOrder);
    for (const call of unplaced) {
      placed.add(call.toolCallId);
      items.push({ kind: 'tool', key: `tool:${call.toolCallId}`, call: liveView(call, pendingInterrupts, input.activeSteps, input.events) });
    }
  };

  for (let index = 0; index < ordered.length; index += 1) {
    const message = ordered[index];
    while (missingIndex < missingBySequence.length && missingBySequence[missingIndex].sequence < message.sequence) {
      const gap = missingBySequence[missingIndex];
      items.push({ kind: 'missing', key: gap.key, label: `Message ${gap.sequence} could not be read.` });
      missingIndex += 1;
    }
    if (message.role === 'tool' && message.toolCallId && placed.has(message.toolCallId)) continue;
    if (message.toolCallId && message.role !== 'user') {
      const toolCallId = message.toolCallId;
      if (placed.has(toolCallId)) continue;
      placed.add(toolCallId);
      const live = liveCalls.get(toolCallId);
      let call: ToolCallView;
      if (live) call = liveView(live, pendingInterrupts, input.activeSteps, input.events);
      else if (fromHistory.calls.has(toolCallId)) call = historyView(fromHistory.calls.get(toolCallId) as AgentToolCallRecord);
      else {
        call = messageView(message, results.get(toolCallId));
        hasCallsFromMessagesOnly = true;
      }
      items.push({ kind: 'tool', key: `tool:${toolCallId}`, call });
    } else if (message.text !== null && message.text !== '') {
      items.push({
        kind: 'message',
        key: `message:${message.sequence}`,
        message,
        stepExecutionId: fromHistory.messageWriters.get(message.sequence),
      });
    }
    const next = ordered[index + 1];
    if (!next || next.turnId !== message.turnId) placeLiveCallsOfTurn(message.turnId);
  }
  for (; missingIndex < missingBySequence.length; missingIndex += 1) {
    const gap = missingBySequence[missingIndex];
    items.push({ kind: 'missing', key: gap.key, label: `Message ${gap.sequence} could not be read.` });
  }
  for (const turnId of new Set([...liveCalls.values()].map((call) => call.turnId))) placeLiveCallsOfTurn(turnId);

  const waitingCalls = items
    .flatMap((item) => item.kind === 'tool' ? [item.call] : [])
    .filter((call) => call.interrupt !== undefined);

  return {
    state,
    usage,
    sessionSettings,
    messageIndex,
    pendingInterrupts,
    items,
    waitingCalls,
    missing,
    hasCallsFromMessagesOnly,
    chunkKeys,
  };
}

function liveView(
  record: AgentToolCallRecord,
  interrupts: readonly AgentInterruptRecord[],
  activeSteps: readonly ActiveStepExecution[],
  events: readonly FlowHistoryEvent[],
): ToolCallView {
  const view = recordView(record, 'live');
  const interrupt = interrupts.find((candidate) => candidate.toolCallId === record.toolCallId);
  if (record.status === 'awaiting_approval' || record.status === 'awaiting_answer') {
    if (interrupt) {
      return { ...view, interrupt, state: record.status };
    }
    // The interrupt is resolved, and the waiting Step has not committed the decision yet.
    return { ...view, state: 'deciding' };
  }
  if (record.status !== 'approved' && record.status !== 'waiting_for_task') return view;
  const recovery = activeSteps.find((candidate) => (
    TOOL_CALL_RECOVERY_STEP_TYPES.includes(candidate.stepType)
    && stepInputText(candidate.movement) === record.toolCallId
  ));
  if (recovery) {
    return { ...view, state: 'recording', stepType: recovery.stepType, stepExecutionId: recovery.stepExecutionId };
  }
  const step = activeSteps.find((candidate) => (
    TOOL_CALL_STEP_TYPES.includes(candidate.stepType)
    && stepInputText(candidate.movement) === record.toolCallId
  ));
  if (!step) return view;
  const invocation = asData(step.executeInvocation ?? step.waitForInvocation);
  const attempt = typeof invocation.attempt === 'number' ? invocation.attempt : undefined;
  const placed: ToolCallView = {
    ...view,
    stepType: step.stepType,
    stepExecutionId: step.stepExecutionId,
    attempt,
    maxAttempts: maximumAttempts(step, events),
  };
  if (PHASE_PENDING.has(invocation.phase)) {
    const reason = REASON_LOCKS.has(invocation.pendingReason)
      ? 'locks'
      : REASON_SUPPORTING_WORKER.has(invocation.pendingReason)
        ? 'supporting_worker'
        : REASON_WORKER.has(invocation.pendingReason) ? 'worker' : undefined;
    return { ...placed, state: 'queued', pendingReason: reason, since: stringOrUndefined(invocation.pendingSinceTime) };
  }
  if (PHASE_RUNNING.has(invocation.phase)) {
    return { ...placed, state: 'running', since: stringOrUndefined(invocation.attemptStartedTime) };
  }
  if (PHASE_BACKOFF.has(invocation.phase)) {
    return { ...placed, state: 'retrying', since: stringOrUndefined(invocation.backoffFiresAtTime) };
  }
  return placed;
}

function historyView(record: AgentToolCallRecord): ToolCallView {
  return recordView(record, 'history');
}

function recordView(record: AgentToolCallRecord, source: ToolCallSource): ToolCallView {
  return {
    toolCallId: record.toolCallId,
    toolName: record.toolName,
    turnId: record.turnId,
    argumentsText: record.arguments,
    effect: record.effect || null,
    requiresApproval: record.requiresApproval,
    state: stateOfRecord(record),
    source,
    output: record.output,
    error: record.error,
    rejectionMessage: record.rejectionMessage,
    cancellationReason: record.cancellationReason,
    skippedAfterWrite: record.skipped,
  };
}

/** The state a record's status alone gives; a live call refines it from its Step. */
export function stateOfRecord(
  record: Pick<AgentToolCallRecord, 'status' | 'error' | 'cancellationReason'> & Partial<Pick<AgentToolCallRecord, 'skipped' | 'writeQueued'>>,
): ToolCallState {
  switch (record.status) {
    case 'awaiting_approval': return 'awaiting_approval';
    case 'awaiting_answer': return 'awaiting_answer';
    case 'approved': return record.writeQueued ? 'write_queued' : 'queued';
    case 'waiting_for_timer':
    case 'waiting_for_task': return 'waiting';
    case 'succeeded': return 'done';
    case 'outcome_unknown': return 'outcome_unknown';
    case 'failed':
      // The prefix reads records written before the skipped marker existed.
      return record.skipped || record.error?.startsWith(TOOL_CALL_NOT_RUN_PREFIX) ? 'skipped' : 'failed';
    case 'rejected': return record.cancellationReason ? 'cancelled' : 'rejected';
    default: return 'waiting';
  }
}

function messageView(request: AgentMessageRecord, result: AgentMessageRecord | undefined): ToolCallView {
  return {
    toolCallId: request.toolCallId ?? '',
    toolName: request.toolName ?? 'tool',
    turnId: request.turnId,
    argumentsText: request.toolArguments ?? '',
    effect: null,
    requiresApproval: null,
    state: result ? 'returned' : 'no_result',
    source: 'messages',
    output: result?.toolOutput ?? null,
  };
}

interface HistoryRecords {
  /** The newest tool call record each call had inside the loaded window. */
  calls: Map<string, AgentToolCallRecord>;
  /** The Step or RPC execution that wrote each message sequence. */
  messageWriters: Map<number, string>;
}

/** Reads AgentToolCalls and AgentMessages writes from the loaded history window. */
export function historyRecords(events: readonly FlowHistoryEvent[]): HistoryRecords {
  const calls = new Map<string, AgentToolCallRecord>();
  const messageWriters = new Map<number, string>();
  for (const event of events) {
    const payload = asData(event.payload);
    const upserts = asData(payload.output).upsertAttributes;
    if (!Array.isArray(upserts)) continue;
    const writer = String(asData(payload.context).stepExecutionId ?? '') || event.type;
    for (const upsert of upserts) {
      const key = String(asData(upsert).key ?? '');
      const messageKey = attributeMapInstance(key, SESSION_ATTRIBUTE.messages);
      if (messageKey !== null) {
        messageWriters.set(Number(messageKey), writer);
        continue;
      }
      if (attributeMapInstance(key, SESSION_ATTRIBUTE.toolCalls) === null) continue;
      const record = decodeAgentToolCall(decodeDexValue(asData(upsert).value));
      if (record) calls.set(record.toolCallId, record);
    }
  }
  return { calls, messageWriters };
}

/** Decodes a Dex Value from history; blob references stay as they are. */
export function decodeDexValue(value: unknown): unknown {
  const message = asData(value);
  if ('stringValue' in message) return message.stringValue;
  if ('intValue' in message) return message.intValue;
  if ('doubleValue' in message) return message.doubleValue;
  if ('boolValue' in message) return message.boolValue;
  if ('nullValue' in message) return null;
  const object = asData(message.objValue);
  if (typeof object.payload !== 'string') return value;
  try {
    const bytes = Uint8Array.from(atob(object.payload), (character) => character.charCodeAt(0));
    const decoded = new TextDecoder().decode(bytes);
    return object.encoding === 'json' ? JSON.parse(decoded) as unknown : decoded;
  } catch {
    return value;
  }
}

/** Decodes the messages of loaded AgentMessageChunks values, skipping unreadable chunks. */
export function messagesOfChunks(values: readonly unknown[]): { messages: AgentMessageRecord[]; unreadable: number } {
  const messages: AgentMessageRecord[] = [];
  let unreadable = 0;
  for (const value of values) {
    const decoded = isUnreadable(value) ? null : decodeAgentMessageChunk(value);
    if (decoded === null) unreadable += 1;
    else messages.push(...decoded);
  }
  return { messages, unreadable };
}

function stepInputText(movement: Record<string, unknown> | undefined): string {
  const input = asData(asData(movement).stepInput);
  return typeof input.stringValue === 'string' ? input.stringValue : '';
}

function maximumAttempts(step: ActiveStepExecution, events: readonly FlowHistoryEvent[]): number | undefined {
  const options = asData(asData(step.movement).stepOptions);
  const fromMovement = asData(options.executeRetryPolicy).maximumAttempts;
  if (typeof fromMovement === 'number' && fromMovement > 0) return fromMovement;
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    if (event.type !== 'StepExecuteCompleted' && event.type !== 'StepExecuteFailed') continue;
    const context = asData(event.payload.context);
    if (context.stepType !== step.stepType) continue;
    const fromHistory = asData(asData(context.methodOptions).retryPolicy).maximumAttempts;
    if (typeof fromHistory === 'number' && fromHistory > 0) return fromHistory;
  }
  return undefined;
}

function stringOrUndefined(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined;
}
