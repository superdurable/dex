// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { Check, Copy } from 'lucide-react';

const copiedFeedbackMilliseconds = 1500;

/** Copies one value to the clipboard and briefly confirms it. */
export function CopyButton({
  value,
  label,
  children,
  className = '',
}: {
  value: string;
  /** The accessible name, such as "Copy run ID". */
  label: string;
  /** Visible text beside the icon; omitted for an icon-only button. */
  children?: string;
  className?: string;
}) {
  const [isCopied, setIsCopied] = useState(false);

  useEffect(() => {
    if (!isCopied) return;
    const timer = window.setTimeout(() => setIsCopied(false), copiedFeedbackMilliseconds);
    return () => window.clearTimeout(timer);
  }, [isCopied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setIsCopied(true);
    } catch (copyError) {
      console.warn('Unable to copy to the clipboard.', copyError);
    }
  };

  const Icon = isCopied ? Check : Copy;
  return (
    <button
      aria-label={label}
      className={`copy-button${children ? '' : ' copy-button--icon'}${isCopied ? ' is-copied' : ''} ${className}`.trim()}
      onClick={() => void copy()}
      title={isCopied ? 'Copied' : label}
      type="button"
    >
      <Icon aria-hidden="true" size={14} strokeWidth={2} />
      {children}
      <span aria-live="polite" className="sr-only">{isCopied ? 'Copied' : ''}</span>
    </button>
  );
}
