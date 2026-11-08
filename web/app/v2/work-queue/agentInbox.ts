// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { hydrateBlobs } from '@/lib/blobs';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionEntry, FlowState, FlowSummary, V2CatalogEntry, V2Flow } from '@/lib/types';
import {
  APPROVAL_STEP_TYPE,
  QUESTION_STEP_TYPE,
  SESSION_ATTRIBUTE,
  agentSessionSchema,
  decodePendingInterrupts,
  type AgentInterruptRecord,
  type AgentSessionSchema,
} from '../agents/sessionSchema';
import { isOpenFlowStatusCode } from './liveness';

type AgentInterrupt = AgentInterruptRecord;

/** The Step types where a session waits for a person. */
export const AGENT_WAITING_STEP_TYPES = [APPROVAL_STEP_TYPE, QUESTION_STEP_TYPE];

/** What a row needs from one session: its pending calls, read from stored state. */
export interface AgentSessionRead {
  interrupts: AgentInterruptRecord[];
  isActive: boolean;
}

/** The session schema of every loaded Flow type that can ask for an approval. */
export function inboxSessionSchemas(
  flows: readonly V2CatalogEntry[],
  definitions: readonly FlowDefinitionEntry[],
): Map<string, { definition: FlowV2Definition; schema: AgentSessionSchema }> {
  const schemas = new Map<string, { definition: FlowV2Definition; schema: AgentSessionSchema }>();
  for (const entry of [...flows].sort((left, right) => left.flowType.localeCompare(right.flowType))) {
    const graph = definitions.find((candidate) => (
      candidate.flowName === entry.flowType && candidate.schemaVersion === '2.0' && candidate.valid
    ))?.graph;
    const schema = agentSessionSchema(graph, entry.definition);
    if (schema?.resolveInterrupt) schemas.set(entry.flowType, { definition: entry.definition, schema });
  }
  return schemas;
}

/** Reads only PendingInterrupts, so a page of rows costs one small read each and no Worker. */
export async function readInboxSession(flowId: string, signal?: AbortSignal): Promise<AgentSessionRead> {
  const summary = await readResponseJSON<FlowSummary>(
    await fetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, { cache: 'no-store', signal }),
  );
  const state = await readResponseJSON<FlowState>(await fetch(
    `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(summary.runId)}`,
    { cache: 'no-store', signal },
  ));
  const stored = state.attributes.find((entry) => entry.key === SESSION_ATTRIBUTE.pendingInterrupts)?.value;
  const hydrated = await hydrateBlobs(flowId, stored ?? [], new Map(), signal);
  const interrupts = hydrated.error ? null : decodePendingInterrupts(hydrated.value);
  if (interrupts === null) throw new Error(`${SESSION_ATTRIBUTE.pendingInterrupts} could not be read`);
  return { interrupts, isActive: isOpenFlowStatusCode(summary.flowStatusCode) };
}

/** What the inbox knows about one session row: the search result and, once read, what it waits on. */
export interface AgentInboxRow {
  flow: V2Flow;
  read: AgentSessionRead | null;
  readError: string;
}

export interface AgentInboxGroup {
  flowType: string;
  rows: AgentInboxRow[];
}

/**
 * Rows grouped by agent, which is the session's Flow type, groups in name order.
 *
 * A session read that found nothing pending has moved on, so its row leaves before the index does.
 */
export function groupInboxRows(rows: readonly AgentInboxRow[]): AgentInboxGroup[] {
  const groups = new Map<string, AgentInboxRow[]>();
  for (const row of rows) {
    if (row.read !== null && row.read.interrupts.length === 0) continue;
    const group = groups.get(row.flow.flowType) ?? [];
    group.push(row);
    groups.set(row.flow.flowType, group);
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([flowType, groupRows]) => ({ flowType, rows: sortByDeadline(groupRows) }));
}

/** Earliest deadline first, then the longest wait. A row not read yet keeps its search order at the end. */
export function sortByDeadline(rows: readonly AgentInboxRow[]): AgentInboxRow[] {
  return rows
    .map((row, index) => ({ row, index }))
    .sort((left, right) => compareRows(left.row, right.row) || left.index - right.index)
    .map(({ row }) => row);
}

function compareRows(left: AgentInboxRow, right: AgentInboxRow): number {
  const leftFirst = left.read ? firstInterrupt(left.read.interrupts) : null;
  const rightFirst = right.read ? firstInterrupt(right.read.interrupts) : null;
  if (leftFirst === null || rightFirst === null) return Number(leftFirst === null) - Number(rightFirst === null);
  return compareInterrupts(leftFirst, rightFirst);
}

export function compareInterrupts(left: AgentInterrupt, right: AgentInterrupt): number {
  if (left.deadline !== null && right.deadline !== null) return timeOf(left.deadline) - timeOf(right.deadline);
  if (left.deadline !== null) return -1;
  if (right.deadline !== null) return 1;
  return timeOf(left.createdAt ?? '') - timeOf(right.createdAt ?? '');
}

/** The interrupt a row names: the one that runs out first. */
export function firstInterrupt(interrupts: readonly AgentInterrupt[]): AgentInterrupt | null {
  return [...interrupts].sort(compareInterrupts)[0] ?? null;
}

/** What is asked, in one line: the question, or the tool with a short summary of its arguments. */
export function askedText(interrupt: AgentInterrupt): string {
  if (interrupt.kind === 'question' && interrupt.question) return interrupt.question;
  const summary = argumentSummary(interrupt.arguments);
  return summary === '' ? interrupt.toolName : `${interrupt.toolName} · ${summary}`;
}

const SUMMARY_LENGTH = 80;

export function argumentSummary(argumentsText: string): string {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText);
  } catch {
    return clip(argumentsText.trim());
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return clip(JSON.stringify(parsed));
  const parts = Object.entries(parsed).map(([key, value]) => `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`);
  return clip(parts.join(', '));
}

function clip(text: string): string {
  return text.length > SUMMARY_LENGTH ? `${text.slice(0, SUMMARY_LENGTH - 1)}…` : text;
}

/** Time left before the deadline, or how long it has waited when there is none. */
export function waitText(interrupt: AgentInterrupt, nowMs: number): string {
  if (interrupt.deadline !== null) {
    const left = timeOf(interrupt.deadline) - nowMs;
    return left <= 0 ? 'past its deadline' : `${durationText(left)} left`;
  }
  return interrupt.createdAt ? `waiting ${durationText(nowMs - timeOf(interrupt.createdAt))}` : 'no deadline';
}

export function durationText(milliseconds: number): string {
  const minutes = Math.max(0, Math.floor(milliseconds / 60_000));
  if (minutes < 1) return 'under 1 min';
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h`;
  return `${Math.floor(hours / 24)} d`;
}

function timeOf(value: string): number {
  const time = Date.parse(value);
  return Number.isNaN(time) ? 0 : time;
}
