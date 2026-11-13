// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

/** The parts of the bar a page fills: what it is, its views and its actions. */
export type AppBarSlotName = 'page' | 'views' | 'actions';

type SlotTargets = Record<AppBarSlotName, HTMLElement | null>;

interface AppBarSlotsValue {
  targets: SlotTargets;
  registerTarget: (name: AppBarSlotName, element: HTMLElement | null) => void;
}

const AppBarSlotsContext = createContext<AppBarSlotsValue | null>(null);

const noTargets: SlotTargets = { page: null, views: null, actions: null };

export function AppBarSlotsProvider({ children }: { children: ReactNode }) {
  const [targets, setTargets] = useState<SlotTargets>(noTargets);
  const registerTarget = useCallback((name: AppBarSlotName, element: HTMLElement | null) => {
    setTargets((current) => (current[name] === element ? current : { ...current, [name]: element }));
  }, []);
  const value = useMemo(() => ({ targets, registerTarget }), [registerTarget, targets]);
  return <AppBarSlotsContext.Provider value={value}>{children}</AppBarSlotsContext.Provider>;
}

/** Where the bar shows one slot's content. */
export function AppBarSlotTarget({ name, className }: { name: AppBarSlotName; className: string }) {
  const registerTarget = useContext(AppBarSlotsContext)?.registerTarget;
  const ref = useCallback((element: HTMLDivElement | null) => registerTarget?.(name, element), [name, registerTarget]);
  return <div className={className} data-slot={name} ref={ref} />;
}

/** Puts a page's content into the bar, or in place outside the app frame. */
export function AppBarSlot({ name, children }: { name: AppBarSlotName; children: ReactNode }) {
  const slots = useContext(AppBarSlotsContext);
  if (!slots) return <>{children}</>;
  const target = slots.targets[name];
  return target ? createPortal(children, target) : null;
}
