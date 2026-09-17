// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export type PersistenceGroup<T> =
  | { kind: 'value'; entry: T; index: number }
  | { kind: 'map'; name: string; entries: Array<PersistenceMapEntry<T>> };

type PersistenceMapEntry<T> = { entry: T; instance: string; index: number };

const decimalInstancePattern = /^[0-9]+$/u;

export function groupPersistenceEntries<T>(
  entries: readonly T[],
  keyForEntry: (entry: T) => string,
): Array<PersistenceGroup<T>> {
  const groups: Array<PersistenceGroup<T>> = [];
  const mapsByName = new Map<string, Extract<PersistenceGroup<T>, { kind: 'map' }>>();
  for (const [index, entry] of entries.entries()) {
    const mapped = parsePersistenceMapEntry(keyForEntry(entry));
    if (!mapped) {
      groups.push({ kind: 'value', entry, index });
      continue;
    }
    let group = mapsByName.get(mapped.name);
    if (!group) {
      group = { kind: 'map', name: mapped.name, entries: [] };
      mapsByName.set(mapped.name, group);
      groups.push(group);
    }
    group.entries.push({ entry, instance: mapped.instance, index });
  }
  for (const group of groups) {
    if (group.kind === 'map') group.entries.sort(compareMapEntries);
  }
  return groups;
}

function parsePersistenceMapEntry(key: string): { name: string; instance: string } | null {
  const separator = key.indexOf('/');
  if (separator <= 0 || separator === key.length - 1) return null;
  const name = key.slice(0, separator);
  const encodedInstance = key.slice(separator + 1);
  try {
    return { name, instance: decodeURIComponent(encodedInstance) };
  } catch {
    return { name, instance: encodedInstance };
  }
}

function compareMapEntries<T>(left: PersistenceMapEntry<T>, right: PersistenceMapEntry<T>): number {
  const leftDecimal = decimalSortValue(left.instance);
  const rightDecimal = decimalSortValue(right.instance);
  if (leftDecimal !== null && rightDecimal !== null) {
    if (leftDecimal.length !== rightDecimal.length) return leftDecimal.length - rightDecimal.length;
    if (leftDecimal < rightDecimal) return -1;
    if (leftDecimal > rightDecimal) return 1;
    return left.index - right.index;
  }
  if (leftDecimal !== null) return -1;
  if (rightDecimal !== null) return 1;
  return left.index - right.index;
}

function decimalSortValue(instance: string): string | null {
  if (!decimalInstancePattern.test(instance)) return null;
  return instance.replace(/^0+(?=[0-9])/u, '');
}
