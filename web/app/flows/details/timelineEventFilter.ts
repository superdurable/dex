// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowHistoryEvent } from '@/lib/types';
import { eventKindNames, eventKindOf, timelineEventKindOrder, type EventKind } from './timelineEventKinds';

/** Which names of one kind show; `except` shows names first seen later, `only` keeps them hidden. */
export type TimelineNameSelection =
  | { mode: 'all' }
  | { mode: 'except'; names: string[] }
  | { mode: 'only'; names: string[] };

/** The Timeline event filter: hidden kinds, and the name selection inside each kind. */
export interface TimelineEventFilter {
  hiddenKinds: EventKind[];
  /** A kind without an entry shows all its names. */
  nameSelections: Partial<Record<EventKind, TimelineNameSelection>>;
}

export const allTimelineEventsFilter: TimelineEventFilter = { hiddenKinds: [], nameSelections: {} };

/** How much of a kind the filter shows on the current page. */
export type TimelineKindSelection = 'all' | 'some' | 'none';

export interface TimelineEventFilterNameOption {
  name: string;
  /** Events on the page that carry this name. */
  count: number;
  isShown: boolean;
}

export interface TimelineEventFilterKindOption {
  kind: EventKind;
  /** Events of this kind on the page. */
  count: number;
  selection: TimelineKindSelection;
  /** The names on the page, sorted; empty for kinds without names. */
  names: TimelineEventFilterNameOption[];
}

/** The filter with its name lists as sets, built once per filter change and reused for every event. */
export interface CompiledTimelineEventFilter {
  hiddenKinds: ReadonlySet<EventKind>;
  nameSelections: ReadonlyMap<EventKind, { isExcept: boolean; names: ReadonlySet<string> }>;
}

export function compileTimelineEventFilter(filter: TimelineEventFilter): CompiledTimelineEventFilter {
  const nameSelections = new Map<EventKind, { isExcept: boolean; names: ReadonlySet<string> }>();
  for (const [kind, selection] of Object.entries(filter.nameSelections) as [EventKind, TimelineNameSelection][]) {
    if (selection.mode !== 'all') nameSelections.set(kind, { isExcept: selection.mode === 'except', names: new Set(selection.names) });
  }
  return { hiddenKinds: new Set(filter.hiddenKinds), nameSelections };
}

function isCompiledNameShown(compiled: CompiledTimelineEventFilter, kind: EventKind, name: string): boolean {
  const selection = compiled.nameSelections.get(kind);
  if (!selection) return true;
  return selection.names.has(name) !== selection.isExcept;
}

/** Reports whether the filter shows an event: its kind is shown, and so is one of its names when it has any. */
export function isCompiledTimelineEventShown(compiled: CompiledTimelineEventFilter, event: FlowHistoryEvent): boolean {
  const kind = eventKindOf(event);
  if (compiled.hiddenKinds.has(kind)) return false;
  if (!compiled.nameSelections.has(kind)) return true;
  const names = eventKindNames(event);
  return names.length === 0 || names.some((name) => isCompiledNameShown(compiled, kind, name));
}

/** Reports whether the filter shows one event; compile the filter once instead when checking a page. */
export function isTimelineEventShown(filter: TimelineEventFilter, event: FlowHistoryEvent): boolean {
  return isCompiledTimelineEventShown(compileTimelineEventFilter(filter), event);
}

/** One kind's event count on a page and its names with their counts, sorted by name. */
export interface TimelinePageKindCount {
  kind: EventKind;
  count: number;
  names: { name: string; count: number }[];
}

/** Counts a page's events per kind and per name, in Legend order; recompute only when the page changes. */
export function timelinePageKindCounts(events: readonly FlowHistoryEvent[]): TimelinePageKindCount[] {
  const kindCounts = new Map<EventKind, number>();
  const nameCounts = new Map<EventKind, Map<string, number>>();
  for (const event of events) {
    const kind = eventKindOf(event);
    kindCounts.set(kind, (kindCounts.get(kind) ?? 0) + 1);
    const counts = nameCounts.get(kind) ?? new Map<string, number>();
    for (const name of eventKindNames(event)) counts.set(name, (counts.get(name) ?? 0) + 1);
    nameCounts.set(kind, counts);
  }
  return timelineEventKindOrder.map((kind) => ({
    kind,
    count: kindCounts.get(kind) ?? 0,
    names: [...(nameCounts.get(kind) ?? new Map<string, number>())]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([name, count]) => ({ name, count })),
  }));
}

/** Returns every kind in Legend order with its page counts, names, and selection; absent names are left out. */
export function timelineEventFilterOptions(
  compiled: CompiledTimelineEventFilter,
  pageKindCounts: readonly TimelinePageKindCount[],
): TimelineEventFilterKindOption[] {
  return pageKindCounts.map(({ kind, count, names: pageNames }) => {
    const isKindHidden = compiled.hiddenKinds.has(kind);
    const names = pageNames.map(({ name, count: nameCount }) => ({
      name,
      count: nameCount,
      isShown: !isKindHidden && isCompiledNameShown(compiled, kind, name),
    }));
    const shownNameCount = names.filter((name) => name.isShown).length;
    let kindSelection: TimelineKindSelection = 'all';
    if (isKindHidden) kindSelection = 'none';
    else if (names.length > 0 && shownNameCount === 0) kindSelection = 'none';
    else if (shownNameCount < names.length) kindSelection = 'some';
    return { kind, count, selection: kindSelection, names };
  });
}

/** The number of kinds the filter hides in whole or in part; zero means everything shows. */
export function filteredKindCount(options: readonly TimelineEventFilterKindOption[]): number {
  return options.filter((option) => option.selection !== 'all').length;
}

export function clearAllTimelineEventKinds(): TimelineEventFilter {
  return { hiddenKinds: [...timelineEventKindOrder], nameSelections: {} };
}

function withKind(
  filter: TimelineEventFilter,
  kind: EventKind,
  isHidden: boolean,
  selection: TimelineNameSelection,
): TimelineEventFilter {
  const nameSelections = { ...filter.nameSelections };
  if (selection.mode === 'all') delete nameSelections[kind];
  else nameSelections[kind] = selection;
  const hiddenKinds = filter.hiddenKinds.filter((hiddenKind) => hiddenKind !== kind);
  return { hiddenKinds: isHidden ? [...hiddenKinds, kind] : hiddenKinds, nameSelections };
}

/** Shows or hides a whole kind; either way its names return to all selected. */
export function setTimelineEventKindShown(filter: TimelineEventFilter, kind: EventKind, isShown: boolean): TimelineEventFilter {
  return withKind(filter, kind, !isShown, { mode: 'all' });
}

/** Shows the kind with none of its names, so the reader can pick a few. */
export function clearAllTimelineEventNames(filter: TimelineEventFilter, kind: EventKind): TimelineEventFilter {
  return withKind(filter, kind, false, { mode: 'only', names: [] });
}

/** Shows or hides one name of a kind; picking a name of a hidden kind shows the kind with only that name. */
export function toggleTimelineEventName(filter: TimelineEventFilter, kind: EventKind, name: string): TimelineEventFilter {
  if (filter.hiddenKinds.includes(kind)) return withKind(filter, kind, false, { mode: 'only', names: [name] });
  const selection = filter.nameSelections[kind] ?? { mode: 'all' };
  if (selection.mode === 'all') return withKind(filter, kind, false, { mode: 'except', names: [name] });
  const names = selection.names.includes(name)
    ? selection.names.filter((selectedName) => selectedName !== name)
    : [...selection.names, name];
  if (selection.mode === 'except' && names.length === 0) return withKind(filter, kind, false, { mode: 'all' });
  return withKind(filter, kind, false, { mode: selection.mode, names });
}

export const timelineEventFilterStorageKeyPrefix = 'dex.run.timeline-event-filter.';

export function timelineEventFilterStorageKey(flowType: string): string {
  return `${timelineEventFilterStorageKeyPrefix}${flowType}`;
}

function isEventKind(value: unknown): value is EventKind {
  return typeof value === 'string' && (timelineEventKindOrder as readonly string[]).includes(value);
}

function parsedNameSelection(value: unknown): TimelineNameSelection | null {
  if (!value || typeof value !== 'object') return null;
  const { mode, names } = value as Record<string, unknown>;
  if (mode === 'all') return { mode };
  if ((mode !== 'except' && mode !== 'only') || !Array.isArray(names)) return null;
  return { mode, names: names.filter((name): name is string => typeof name === 'string') };
}

/** Returns the saved filter without its invalid parts, or null when the text is not a filter. */
export function parseTimelineEventFilter(text: string): TimelineEventFilter | null {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const hiddenKinds = Array.isArray(record.hiddenKinds) ? [...new Set(record.hiddenKinds.filter(isEventKind))] : [];
  const nameSelections: TimelineEventFilter['nameSelections'] = {};
  if (record.nameSelections && typeof record.nameSelections === 'object') {
    for (const [kind, selectionValue] of Object.entries(record.nameSelections as Record<string, unknown>)) {
      const selection = parsedNameSelection(selectionValue);
      if (isEventKind(kind) && selection && selection.mode !== 'all') nameSelections[kind] = selection;
    }
  }
  return { hiddenKinds, nameSelections };
}

/** The filter saved for a Flow type; everything shown when none is saved, it is unreadable, or storage fails. */
export function storedTimelineEventFilter(flowType: string | undefined): TimelineEventFilter {
  if (!flowType || typeof window === 'undefined') return allTimelineEventsFilter;
  try {
    const text = window.localStorage.getItem(timelineEventFilterStorageKey(flowType));
    return (text === null ? null : parseTimelineEventFilter(text)) ?? allTimelineEventsFilter;
  } catch (storageError) {
    console.warn('Unable to read the Timeline event filter.', storageError);
    return allTimelineEventsFilter;
  }
}

/** Saves the filter for a Flow type; a filter that shows everything removes the saved entry. */
export function persistTimelineEventFilter(flowType: string | undefined, filter: TimelineEventFilter) {
  if (!flowType) return;
  try {
    const key = timelineEventFilterStorageKey(flowType);
    if (filter.hiddenKinds.length === 0 && Object.keys(filter.nameSelections).length === 0) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, JSON.stringify(filter));
  } catch (storageError) {
    console.warn('Unable to save the Timeline event filter.', storageError);
  }
}
