// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

/** Open state for a bar menu that closes on navigation, Escape, or a pointer outside it. */
export function usePopover<T extends HTMLElement>() {
  const [isOpen, setIsOpen] = useState(false);
  const rootRef = useRef<T>(null);
  const { pathname } = useLocation();

  useEffect(() => { setIsOpen(false); }, [pathname]);
  useEffect(() => {
    if (!isOpen) return undefined;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setIsOpen(false);
    };
    const items = () => [...(rootRef.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)') ?? [])];
    // A menu takes focus on its first item, so arrow keys work at once.
    window.requestAnimationFrame(() => items()[0]?.focus());
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsOpen(false);
        return;
      }
      const menuItems = items();
      const index = menuItems.indexOf(document.activeElement as HTMLElement);
      if (index < 0 && !['ArrowDown', 'ArrowUp'].includes(event.key)) return;
      const moves: Record<string, number> = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: menuItems.length - 1 };
      const next = moves[event.key];
      if (next === undefined || menuItems.length === 0) return;
      event.preventDefault();
      menuItems[(next + menuItems.length) % menuItems.length].focus();
    };
    document.addEventListener('pointerdown', closeOnOutsidePointer);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsidePointer);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [isOpen]);

  return { isOpen, setIsOpen, rootRef };
}
