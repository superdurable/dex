// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { Check, Copy, X, type LucideIcon } from 'lucide-react';

const copyFeedbackMilliseconds = 1500;

type CopyState = 'idle' | 'copied' | 'failed';

/** Copies one value and briefly shows and announces Copied or Copy failed. */
export function CopyButton({
  value,
  label,
  children,
  icon = Copy,
  className = 'copy-button',
}: {
  /** The text to copy, or a function that reads it at click time, such as the current page URL. */
  value: string | (() => string);
  /** The accessible name, such as "Copy run ID". */
  label: string;
  /** Visible text beside the icon; omitted for an icon-only button. */
  children?: string;
  /** The icon shown while idle. */
  icon?: LucideIcon;
  /** The button's look; the copy feedback classes are added to it. */
  className?: string;
}) {
  const [copyState, setCopyState] = useState<CopyState>('idle');

  useEffect(() => {
    if (copyState === 'idle') return;
    const timer = window.setTimeout(() => setCopyState('idle'), copyFeedbackMilliseconds);
    return () => window.clearTimeout(timer);
  }, [copyState]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(typeof value === 'function' ? value() : value);
      setCopyState('copied');
    } catch (copyError) {
      console.warn('Unable to copy to the clipboard.', copyError);
      setCopyState('failed');
    }
  };

  const Icon = copyState === 'copied' ? Check : copyState === 'failed' ? X : icon;
  const announcement = copyState === 'copied' ? 'Copied' : copyState === 'failed' ? 'Copy failed' : '';
  return (
    <button
      aria-label={label}
      className={[
        className,
        children ? '' : 'copy-button--icon',
        copyState === 'copied' ? 'is-copied' : '',
        copyState === 'failed' ? 'is-copy-failed' : '',
      ].filter(Boolean).join(' ')}
      onClick={() => void copy()}
      title={announcement || label}
      type="button"
    >
      <Icon aria-hidden="true" className="copy-button-icon" key={copyState} size={14} strokeWidth={2} />
      {children}
      <span aria-live="polite" className="sr-only">{announcement}</span>
    </button>
  );
}
