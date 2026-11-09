/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

const SIDEBAR_STATE_STORAGE_KEY = 'dex.docs.sidebar.category-state.v1';

type SidebarCategoryState = Record<string, boolean>;

// Set after the first page hydrates. Later sidebar mounts can read saved state while rendering.
let isSidebarHydrated = false;

// What the reader sees now, including categories opened because they held the current page.
const visibleCategoryCollapsedState = new Map<string, boolean>();

export function rememberVisibleCategoryCollapsed(categoryKey: string, collapsed: boolean): void {
  visibleCategoryCollapsedState.set(categoryKey, collapsed);
}

export function hasSidebarHydrated(): boolean {
  return isSidebarHydrated;
}

export function markSidebarHydrated(): void {
  isSidebarHydrated = true;
}

export function readSidebarCategoryState(): SidebarCategoryState {
  try {
    const serializedState = window.localStorage.getItem(SIDEBAR_STATE_STORAGE_KEY);
    if (serializedState === null) {
      return {};
    }
    const parsedState: unknown = JSON.parse(serializedState);
    if (
      parsedState === null ||
      typeof parsedState !== 'object' ||
      Array.isArray(parsedState)
    ) {
      return {};
    }
    return Object.fromEntries(
      Object.entries(parsedState).filter(
        ([, value]) => typeof value === 'boolean',
      ),
    );
  } catch {
    return {};
  }
}

/**
 * Returns the saved collapsed state of a category when it is safe to use during render.
 * Before the first hydration the server markup decides, so this returns undefined.
 */
export function readSavedCategoryCollapsedBeforePaint(categoryKey: string): boolean | undefined {
  if (!isSidebarHydrated) {
    return undefined;
  }
  return visibleCategoryCollapsedState.get(categoryKey) ?? readSidebarCategoryState()[categoryKey];
}

export function persistSidebarCategoryState(categoryKey: string, collapsed: boolean): void {
  try {
    window.localStorage.setItem(
      SIDEBAR_STATE_STORAGE_KEY,
      JSON.stringify({...readSidebarCategoryState(), [categoryKey]: collapsed}),
    );
  } catch {
    // The sidebar remains usable when browser storage is unavailable.
  }
}
