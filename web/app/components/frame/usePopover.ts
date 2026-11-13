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
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setIsOpen(false);
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
