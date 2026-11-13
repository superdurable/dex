// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef } from 'react';

const FOCUSABLE = 'input:not([disabled]), select:not([disabled]), textarea:not([disabled]), button:not([disabled]), [href], [tabindex]:not([tabindex="-1"])';

/**
 * Keeps keyboard focus in an open modal: it moves to the first field, Tab cycles inside, Escape closes, and
 * focus returns to where it was when the modal closes.
 */
export function useModalFocus<T extends HTMLElement>(isOpen: boolean, onClose: () => void) {
  const modalRef = useRef<T>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!isOpen) return undefined;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const modal = modalRef.current;
    const focusables = () => [...(modal?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])];
    // The first field, not the close button, so typing starts at once.
    const firstField = focusables().find((element) => !element.classList.contains('icon-button'));
    (firstField ?? modal)?.focus();
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== 'Tab') return;
      const elements = focusables();
      if (elements.length === 0) return;
      const first = elements[0];
      const last = elements[elements.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('keydown', handleKey);
      previous?.focus();
    };
  }, [isOpen]);

  return modalRef;
}
