// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Lock, LockOpen } from 'lucide-react';
import {
  encryptedPayloadProblemText,
  isDecryptedPayload,
  isEncryptedPayloadPlaceholder,
  isEncryptedStoredValue,
} from '@/lib/payloadEncryption';

/** Whether a stored value was decrypted in this browser, is still encrypted, or was never encrypted. */
export function payloadEncryptionStateOf(value: unknown): 'decrypted' | 'encrypted' | null {
  if (isDecryptedPayload(value)) return 'decrypted';
  if (isEncryptedPayloadPlaceholder(value) || isEncryptedStoredValue(value)) return 'encrypted';
  return null;
}

/** A small lock beside a value that was encrypted: open when this browser decrypted it, closed when it could not. */
export function PayloadLockBadge({ value }: { value: unknown }) {
  const state = payloadEncryptionStateOf(value);
  if (state === null) return null;
  if (state === 'decrypted') {
    return (
      <span
        aria-label="Decrypted in this browser"
        className="payload-lock-badge is-decrypted"
        role="img"
        title="Encrypted value, decrypted in this browser with the codec endpoint. Dex Web never receives the plaintext."
      >
        <LockOpen aria-hidden="true" size={12} strokeWidth={2} />
      </span>
    );
  }
  const problem = isEncryptedPayloadPlaceholder(value) ? value.__dexEncryptedValue.problem : 'not_configured';
  return (
    <span aria-label="Encrypted" className="payload-lock-badge is-encrypted" role="img" title={encryptedPayloadProblemText(problem)}>
      <Lock aria-hidden="true" size={12} strokeWidth={2} />
    </span>
  );
}
