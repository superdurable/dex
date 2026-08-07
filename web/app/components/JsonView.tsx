// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import { useEffect, useState } from 'react';
import { storedValueJSONReplacer } from '@/lib/blobs';
import { StructuredValue } from './StructuredValue';

const openByKey = new Map<string, boolean>();
const viewByKey = new Map<string, 'details' | 'raw'>();

export function JsonView({
  value,
  label = 'Details',
  initiallyOpen = false,
  persistKey,
  forceOpen,
  collapseNonce = 0,
}: {
  value: unknown;
  label?: string;
  initiallyOpen?: boolean;
  persistKey?: string;
  forceOpen?: boolean;
  collapseNonce?: number;
}) {
  const storageKey = persistKey ?? label;
  const [open, setOpen] = useState(() => openByKey.get(storageKey) ?? initiallyOpen);
  const [view, setView] = useState<'details' | 'raw'>(
    () => viewByKey.get(storageKey) ?? 'details',
  );

  useEffect(() => {
    if (!forceOpen) return;
    setOpen(true);
    openByKey.set(storageKey, true);
  }, [forceOpen, storageKey]);

  useEffect(() => {
    if (collapseNonce <= 0) return;
    setOpen(false);
    openByKey.set(storageKey, false);
  }, [collapseNonce, storageKey]);

  const effectiveOpen = forceOpen || open;

  return (
    <div className="json-view">
      <button
        type="button"
        className="json-toggle"
        disabled={forceOpen}
        onClick={() => {
          setOpen((current) => {
            const next = !current;
            openByKey.set(storageKey, next);
            return next;
          });
        }}
      >
        <span>{effectiveOpen ? '−' : '+'}</span>
        {label}
      </button>
      {effectiveOpen && (
        <div className="json-view-body">
          <div className="event-detail-tabs" role="tablist" aria-label={`${label} view`}>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'details'}
              className={view === 'details' ? 'active' : ''}
              onClick={() => {
                setView('details');
                viewByKey.set(storageKey, 'details');
              }}
            >
              Details
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'raw'}
              className={view === 'raw' ? 'active' : ''}
              onClick={() => {
                setView('raw');
                viewByKey.set(storageKey, 'raw');
              }}
            >
              Raw JSON
            </button>
          </div>
          {view === 'details'
            ? <StructuredValue value={value} />
            : (
              <pre className="json-view-raw">
                {JSON.stringify(value, storedValueJSONReplacer, 2)}
              </pre>
            )}
        </div>
      )}
    </div>
  );
}
