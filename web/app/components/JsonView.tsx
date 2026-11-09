// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { useEffect, useState } from 'react';
import { ChevronRight } from 'lucide-react';
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
  parentFlowId = '',
  stepExecutionId = '',
  persistenceKind,
  variant = 'collapsible',
  expandValues = false,
}: {
  value: unknown;
  label?: string;
  initiallyOpen?: boolean;
  persistKey?: string;
  forceOpen?: boolean;
  collapseNonce?: number;
  parentFlowId?: string;
  stepExecutionId?: string;
  persistenceKind?: 'attributes' | 'channels';
  /** Inline views stay open under a section heading, so they show no toggle of their own. */
  variant?: 'collapsible' | 'inline';
  /** Shows long values in full and opens map instances. */
  expandValues?: boolean;
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

  const effectiveOpen = variant === 'inline' || forceOpen || open;
  const selectView = (next: 'details' | 'raw') => {
    setView(next);
    viewByKey.set(storageKey, next);
  };

  return (
    <div className={`json-view json-view--${variant}`}>
      {variant === 'collapsible' && (
        <button
          type="button"
          className="json-toggle"
          aria-expanded={effectiveOpen}
          disabled={forceOpen}
          onClick={() => {
            setOpen((current) => {
              const next = !current;
              openByKey.set(storageKey, next);
              return next;
            });
          }}
        >
          <ChevronRight aria-hidden="true" className="json-toggle-icon" size={15} strokeWidth={2} />
          {label}
        </button>
      )}
      {effectiveOpen && (
        <div className="json-view-body">
          <div className="event-detail-tabs" role="tablist" aria-label={`${label} view`}>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'details'}
              className={view === 'details' ? 'active' : ''}
              onClick={() => selectView('details')}
            >
              Details
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'raw'}
              className={view === 'raw' ? 'active' : ''}
              onClick={() => selectView('raw')}
            >
              Raw JSON
            </button>
          </div>
          {view === 'details'
            ? <StructuredValue
                value={value}
                parentFlowId={parentFlowId}
                stepExecutionId={stepExecutionId}
                persistenceKind={persistenceKind}
                expandValues={expandValues}
              />
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
