/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import {type RefObject, useEffect} from 'react';

// Closes an open details element on Escape, refocusing its summary, and on an outside pointer press.
export function useDismissibleDetails(detailsRef: RefObject<HTMLDetailsElement | null>): void {
  useEffect(() => {
    const details = detailsRef.current;
    if (details === null) {
      return undefined;
    }
    const summary = details.querySelector('summary');
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && details.open) {
        details.open = false;
        summary?.focus();
      }
    };
    const closeOnOutsidePointerDown = (event: PointerEvent) => {
      if (details.open && event.target instanceof Node && !details.contains(event.target)) {
        details.open = false;
      }
    };
    document.addEventListener('keydown', closeOnEscape);
    document.addEventListener('pointerdown', closeOnOutsidePointerDown);
    return () => {
      document.removeEventListener('keydown', closeOnEscape);
      document.removeEventListener('pointerdown', closeOnOutsidePointerDown);
    };
  }, [detailsRef]);
}
