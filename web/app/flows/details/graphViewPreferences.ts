// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';

/** Graph view choices this browser remembers for every run. */
export const graphViewStorageKeys = {
  isFolded: 'dex.run.graph-folded',
  isFoldedIntroDismissed: 'dex.run.graph-folded-intro-dismissed',
  isMiniMapHidden: 'dex.run.graph-minimap-hidden',
  /** True for Similar Round grouping; false or missing for Exact. */
  isSimilarRoundGrouping: 'dex.run.graph-similar-rounds',
} as const;

/** Reads a remembered choice; blocked or missing storage gives the fallback. */
export function readStoredGraphFlag(key: string, fallback: boolean): boolean {
  try {
    const value = window.localStorage.getItem(key);
    if (value === 'true') return true;
    if (value === 'false') return false;
    return fallback;
  } catch {
    return fallback;
  }
}

export function storeGraphFlag(key: string, value: boolean): void {
  try {
    window.localStorage.setItem(key, String(value));
  } catch (storageError) {
    console.warn('Unable to save the Graph view preference.', storageError);
  }
}

/** A boolean Graph view choice that starts from storage and saves each change. */
export function useStoredGraphFlag(key: string, fallback: boolean): [boolean, (value: boolean) => void] {
  const [value, setValue] = useState(() => readStoredGraphFlag(key, fallback));
  const update = (next: boolean) => {
    setValue(next);
    storeGraphFlag(key, next);
  };
  return [value, update];
}
