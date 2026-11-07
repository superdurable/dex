// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { V2Flow } from '@/lib/types';
import type { AgentInterrupt, AgentSessionRead } from './agentSession';

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
  return timeOf(left.createdAt) - timeOf(right.createdAt);
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

/** Arguments as indented JSON when they parse, else as the model wrote them. */
export function formattedArguments(argumentsText: string): string {
  try {
    return JSON.stringify(JSON.parse(argumentsText), null, 2);
  } catch {
    return argumentsText;
  }
}

/** Time left before the deadline, or how long it has waited when there is none. */
export function waitText(interrupt: AgentInterrupt, nowMs: number): string {
  if (interrupt.deadline !== null) {
    const left = timeOf(interrupt.deadline) - nowMs;
    return left <= 0 ? 'past its deadline' : `${durationText(left)} left`;
  }
  return interrupt.createdAt === '' ? 'no deadline' : `waiting ${durationText(nowMs - timeOf(interrupt.createdAt))}`;
}

export function isPastDeadline(interrupt: AgentInterrupt, nowMs: number): boolean {
  return interrupt.deadline !== null && timeOf(interrupt.deadline) <= nowMs;
}

export function durationText(milliseconds: number): string {
  const minutes = Math.max(0, Math.floor(milliseconds / 60_000));
  if (minutes < 1) return 'under 1 min';
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h`;
  return `${Math.floor(hours / 24)} d`;
}

/** The effect class a tool source declared, in words. */
export function effectText(effect: string): string {
  switch (effect) {
    case 'read': return 'Read';
    case 'idempotent_write': return 'Write, safe to repeat';
    case 'non_idempotent_write': return 'Write, not safe to repeat';
    case 'unclassified': return 'Unclassified';
    default: return effect === '' ? 'Not recorded' : effect;
  }
}

function timeOf(value: string): number {
  const time = Date.parse(value);
  return Number.isNaN(time) ? 0 : time;
}
