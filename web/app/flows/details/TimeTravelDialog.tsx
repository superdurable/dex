// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  eventTimeTravelTarget,
  TIME_TRAVEL_STEP_METHOD,
  TIME_TRAVEL_TYPE,
} from '@/lib/timeTravel';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';

const timeTravelTypes = [
  { value: TIME_TRAVEL_TYPE.BEGINNING, label: 'Beginning' },
  { value: TIME_TRAVEL_TYPE.HISTORY_EVENT_TIME, label: 'History event time' },
  { value: TIME_TRAVEL_TYPE.STEP_TYPE, label: 'Step type' },
  { value: TIME_TRAVEL_TYPE.STEP_EXECUTION_ID, label: 'Step execution ID' },
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
  const initialTarget = eventTimeTravelTarget(initialEvent);
  const [timeTravelType, setTimeTravelType] = useState<number>(() => (
    initialTarget ? TIME_TRAVEL_TYPE.STEP_EXECUTION_ID : TIME_TRAVEL_TYPE.BEGINNING
  ));
  const [target, setTarget] = useState(() => initialTarget?.stepExecutionId ?? '');
  const [stepMethod, setStepMethod] = useState<number>(() => initialTarget?.stepMethod ?? 0);
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
    const eventTarget = eventTimeTravelTarget(initialEvent);
    setTimeTravelType(eventTarget ? TIME_TRAVEL_TYPE.STEP_EXECUTION_ID : TIME_TRAVEL_TYPE.BEGINNING);
    setTarget(eventTarget?.stepExecutionId ?? '');
    setStepMethod(eventTarget?.stepMethod ?? 0);
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
    if (timeTravelType === TIME_TRAVEL_TYPE.HISTORY_EVENT_TIME) payload.historyEventTime = target;
    if (timeTravelType === TIME_TRAVEL_TYPE.STEP_TYPE) payload.stepType = target;
    if (timeTravelType === TIME_TRAVEL_TYPE.STEP_EXECUTION_ID) {
      payload.stepExecutionId = target;
      payload.stepMethod = stepMethod;
    }
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
            setStepMethod(0);
          }}>
            {timeTravelTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}
          </select>
        </label>
        {timeTravelType !== TIME_TRAVEL_TYPE.BEGINNING && (
          <label>
            Target
            {timeTravelType === TIME_TRAVEL_TYPE.STEP_TYPE ? (
              <>
                <input list="time-travel-step-types" value={target} onChange={(event) => setTarget(event.target.value)} />
                <datalist id="time-travel-step-types">{stepTypes.map((value) => <option value={value} key={value} />)}</datalist>
              </>
            ) : (
              <input
                type={timeTravelType === TIME_TRAVEL_TYPE.HISTORY_EVENT_TIME ? 'datetime-local' : 'text'}
                value={target}
                onChange={(event) => setTarget(event.target.value)}
              />
            )}
          </label>
        )}
        {timeTravelType === TIME_TRAVEL_TYPE.STEP_EXECUTION_ID && (
          <label>
            Step method
            <select value={stepMethod} onChange={(event) => setStepMethod(Number(event.target.value))}>
              <option value={0}>Select a method</option>
              <option value={TIME_TRAVEL_STEP_METHOD.WAIT_FOR}>WaitFor</option>
              <option value={TIME_TRAVEL_STEP_METHOD.EXECUTE}>Execute</option>
            </select>
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
            disabled={submitting
              || !reason.trim()
              || (timeTravelType !== TIME_TRAVEL_TYPE.BEGINNING && !target)
              || (timeTravelType === TIME_TRAVEL_TYPE.STEP_EXECUTION_ID && stepMethod === 0)}
            onClick={() => void submit()}
          >
            {submitting ? 'Time traveling…' : 'Time travel'}
          </button>
        </div>
      </div>
    </div>
  );
}
