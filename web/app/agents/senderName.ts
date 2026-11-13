// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The most code points a sender name may have, as the agents cores check it. */
export const MAX_SENDER_NAME_LENGTH = 64;

const SENDER_NAME_STORAGE_KEY = 'dex.agents.senderName';

/** The name this browser last entered, or '' when none is stored or storage is unavailable. */
export function readStoredSenderName(): string {
  try {
    return window.localStorage.getItem(SENDER_NAME_STORAGE_KEY) ?? '';
  } catch {
    return '';
  }
}

/** Remembers the name for this browser; a blank name forgets it. */
export function writeStoredSenderName(name: string): void {
  try {
    if (name.trim() === '') {
      window.localStorage.removeItem(SENDER_NAME_STORAGE_KEY);
    } else {
      window.localStorage.setItem(SENDER_NAME_STORAGE_KEY, name);
    }
  } catch {
    // Private mode can throw; the name then lasts only for this page.
  }
}

/** The trimmed name to send with a message, or null to send none. */
export function senderNameToSend(name: string): string | null {
  const trimmed = name.trim();
  return trimmed === '' ? null : trimmed;
}
