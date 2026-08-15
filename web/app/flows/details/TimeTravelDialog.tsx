// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';

const timeTravelTypes = [
  { value: 2, label: 'Beginning' },
  { value: 1, label: 'History event ID' },
  { value: 3, label: 'History event time' },
  { value: 4, label: 'Step type' },
  { value: 5, label: 'Step execution ID' },
];

export function TimeTravelDialog({
  open,
  summary,
  events,
  initialEvent,
  onClose,
}: {
  open: boolean;
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  initialEvent: FlowHistoryEvent | null;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const [timeTravelType, setTimeTravelType] = useState(() => initialEvent ? 1 : 2);
  const [target, setTarget] = useState(() => initialEvent ? String(initialEvent.eventId) : '');
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const stepTypes = useMemo(() => {
    const values = new Set<string>();
    events.forEach((event) => {
      const context = event.payload.context as Record<string, unknown> | undefined;
      if (typeof context?.stepType === 'string') values.add(context.stepType);
    });
    return [...values];
  }, [events]);

  useEffect(() => {
    if (!open) return;
    setTimeTravelType(initialEvent ? 1 : 2);
    setTarget(initialEvent ? String(initialEvent.eventId) : '');
    setReason('');
    setError('');
  }, [initialEvent?.eventId, open]);

  if (!open) return null;

  async function submit() {
    setSubmitting(true);
    setError('');
    const payload: Record<string, unknown> = {
      flowId: summary.flowId,
      runId: summary.runId,
      timeTravelType,
      reason,
    };
    if (timeTravelType === 1) payload.historyEventId = Number(target);
    if (timeTravelType === 3) payload.historyEventTime = target;
    if (timeTravelType === 4) payload.stepType = target;
    if (timeTravelType === 5) payload.stepExecutionId = target;
    try {
      const response = await fetch('/api/flows/time-travel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await response.json() as { runId?: string; error?: string };
      if (!response.ok) throw new Error(data.error || 'Time travel failed');
      navigate(`/flows/${encodeURIComponent(summary.flowId)}/${encodeURIComponent(data.runId || '')}`);
      onClose();
    } catch (timeTravelError) {
      setError(timeTravelError instanceof Error ? timeTravelError.message : 'Time travel failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="modal" role="dialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <div><p className="eyebrow">New Flow run</p><h2>Time travel</h2></div>
          <button className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          Time travel creates a new run from a selected Dex point. The current run remains available in history.
        </p>
        <label>
          Time travel point
          <select value={timeTravelType} onChange={(event) => {
            setTimeTravelType(Number(event.target.value));
            setTarget('');
          }}>
            {timeTravelTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}
          </select>
        </label>
        {timeTravelType !== 2 && (
          <label>
            Target
            {timeTravelType === 4 ? (
              <>
                <input list="time-travel-step-types" value={target} onChange={(event) => setTarget(event.target.value)} />
                <datalist id="time-travel-step-types">{stepTypes.map((value) => <option value={value} key={value} />)}</datalist>
              </>
            ) : (
              <input
                type={timeTravelType === 1 ? 'number' : timeTravelType === 3 ? 'datetime-local' : 'text'}
                value={target}
                onChange={(event) => setTarget(event.target.value)}
              />
            )}
          </label>
        )}
        <label>
          Reason
          <textarea rows={3} value={reason} onChange={(event) => setReason(event.target.value)} />
        </label>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button
            className="button danger"
            disabled={submitting || !reason.trim() || (timeTravelType !== 2 && !target)}
            onClick={() => void submit()}
          >
            {submitting ? 'Time traveling…' : 'Time travel'}
          </button>
        </div>
      </div>
    </div>
  );
}
