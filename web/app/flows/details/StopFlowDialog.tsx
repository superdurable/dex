// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';

const stopTypes = [
  { value: 1, label: 'Cancel', needsReason: false },
  { value: 2, label: 'Terminate', needsReason: true },
  { value: 3, label: 'Fail', needsReason: true },
];

export function StopFlowDialog({
  open,
  summary,
  onClose,
  onStopped,
}: {
  open: boolean;
  summary: FlowSummary;
  onClose: () => void;
  onStopped: () => void;
}) {
  const [stopType, setStopType] = useState(1);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const selected = stopTypes.find((item) => item.value === stopType) || stopTypes[0];
  const reasonRequired = selected.needsReason;
  const canSubmit = !submitting && (!reasonRequired || reason.trim() !== '');

  if (!open) return null;

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      const response = await fetch('/api/flows/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowId: summary.flowId,
          runId: summary.runId,
          stopType,
          reason,
        }),
      });
      await readResponseJSON(response);
      onStopped();
      onClose();
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : 'Stop failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <div><p className="eyebrow">Destructive operation</p><h2>Stop flow</h2></div>
          <button className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          Stop ends the current run. Cancel requests graceful cancellation,
          terminate forces an immediate stop, and fail records a client failure.
        </p>
        <label>
          Stop type
          <select value={stopType} onChange={(event) => setStopType(Number(event.target.value))}>
            {stopTypes.map((item) => (
              <option value={item.value} key={item.value}>{item.label}</option>
            ))}
          </select>
        </label>
        <label>
          Reason{reasonRequired ? '' : ' (optional)'}
          <textarea rows={3} value={reason} onChange={(event) => setReason(event.target.value)} />
        </label>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Close</button>
          <button
            className="button danger"
            disabled={!canSubmit}
            onClick={() => void submit()}
          >
            {submitting ? 'Stopping…' : 'Stop flow'}
          </button>
        </div>
      </div>
    </div>
  );
}
