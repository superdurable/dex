// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useLayoutEffect, useState } from 'react';
import type { TimezonePreference } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import {
  eventTimeTravelTarget,
  positiveRunAttempt,
  SUB_FLOW_RESET_POLICY,
  TIME_TRAVEL_TYPE,
} from '@/lib/timeTravel';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';
import { webURL } from '@/lib/basePath';
import { useModalFocus } from '../../components/useModalFocus';
import {
  HistoryPointFields,
  historyPointRequestFields,
  isHistoryPointComplete,
  SubFlowResetPolicyField,
  useStepTypesOf,
  type HistoryPointKind,
} from './HistoryPointFields';

/** The run a time travel created. Servers with run attempts keep the run ID and return a new attempt. */
export interface TimeTravelResult {
  runId: string;
  runAttempt: number;
}

const timeTravelTypes: { value: number; label: string; kind: HistoryPointKind }[] = [
  { value: TIME_TRAVEL_TYPE.BEGINNING, label: 'Beginning', kind: 'beginning' },
  { value: TIME_TRAVEL_TYPE.HISTORY_EVENT_TIME, label: 'History event time', kind: 'history-event-time' },
  { value: TIME_TRAVEL_TYPE.STEP_TYPE, label: 'Step type', kind: 'step-type' },
  { value: TIME_TRAVEL_TYPE.STEP_EXECUTION_ID, label: 'Step execution ID', kind: 'step-execution-id' },
  { value: TIME_TRAVEL_TYPE.EVENT_ID, label: 'Event ID', kind: 'event-id' },
];

function pointKindOf(timeTravelType: number): HistoryPointKind {
  return timeTravelTypes.find((item) => item.value === timeTravelType)?.kind ?? 'beginning';
}

export function TimeTravelDialog({
  open,
  summary,
  events,
  initialEvent,
  runAttempt,
  timezone,
  supportsRunHistoryForks,
  onClose,
  onTimeTraveled,
}: {
  open: boolean;
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  initialEvent: FlowHistoryEvent | null;
  /** The run attempt whose history is shown; 0 selects the current one. */
  runAttempt: number;
  timezone: TimezonePreference;
  /** True when the server offers the Event ID point and SubFlow reset policies; otherwise neither is sent. */
  supportsRunHistoryForks: boolean;
  onClose: () => void;
  onTimeTraveled: (result: TimeTravelResult) => void;
}) {
  const offeredTimeTravelTypes = supportsRunHistoryForks
    ? timeTravelTypes
    : timeTravelTypes.filter((item) => item.value !== TIME_TRAVEL_TYPE.EVENT_ID);
  const hasRunAttempts = positiveRunAttempt(summary.runAttempt) > 0;
  const initialTarget = eventTimeTravelTarget(initialEvent);
  const [timeTravelType, setTimeTravelType] = useState<number>(() => (
    initialTarget ? TIME_TRAVEL_TYPE.STEP_EXECUTION_ID : TIME_TRAVEL_TYPE.BEGINNING
  ));
  const [target, setTarget] = useState(() => initialTarget?.stepExecutionId ?? '');
  const [stepMethod, setStepMethod] = useState<number>(() => initialTarget?.stepMethod ?? 0);
  const [subFlowResetPolicy, setSubFlowResetPolicy] = useState<number>(SUB_FLOW_RESET_POLICY.ATTACH_ELSE_RESTART);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const stepTypes = useStepTypesOf(events);
  const pointKind = pointKindOf(timeTravelType);

  // A layout effect resets the fields before anyone can type, even when the dialog opens outside an event.
  useLayoutEffect(() => {
    if (!open) return;
    const eventTarget = eventTimeTravelTarget(initialEvent);
    setTimeTravelType(eventTarget ? TIME_TRAVEL_TYPE.STEP_EXECUTION_ID : TIME_TRAVEL_TYPE.BEGINNING);
    setTarget(eventTarget?.stepExecutionId ?? '');
    setStepMethod(eventTarget?.stepMethod ?? 0);
    setSubFlowResetPolicy(SUB_FLOW_RESET_POLICY.ATTACH_ELSE_RESTART);
    setReason('');
    setError('');
  }, [initialEvent?.eventId, open]);

  const modalRef = useModalFocus<HTMLDivElement>(open, onClose);
  if (!open) return null;

  async function submit() {
    setSubmitting(true);
    setError('');
    const payload: Record<string, unknown> = {
      flowId: summary.flowId,
      runId: summary.runId,
      timeTravelType,
      reason,
      ...historyPointRequestFields(pointKind, target, stepMethod, timezone),
    };
    if (supportsRunHistoryForks) payload.subFlowResetPolicy = subFlowResetPolicy;
    if (runAttempt > 0) payload.runAttempt = runAttempt;
    try {
      const response = await fetch(webURL('/api/flows/time-travel'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await readResponseJSON<{ runId?: string; runAttempt?: number }>(response);
      onTimeTraveled({ runId: data.runId || summary.runId, runAttempt: positiveRunAttempt(data.runAttempt) });
      onClose();
    } catch (timeTravelError) {
      setError(timeTravelError instanceof Error ? timeTravelError.message : 'Time travel failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="modal" ref={modalRef} role="dialog" aria-modal="true" tabIndex={-1} onMouseDown={(event) => event.stopPropagation()}>
        <div className="modal-header">
          <h2>{hasRunAttempts ? 'Time travel to a new run attempt' : 'Time travel to a new Flow run'}</h2>
          <button className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          {hasRunAttempts
            ? 'Time travel creates a new run attempt from a selected Dex point. Earlier run attempts remain available in history.'
            : 'Time travel creates a new run from a selected Dex point. The current run remains available in history.'}
        </p>
        {runAttempt > 0 && (
          <p className="muted">Searching the history of run attempt {runAttempt}.</p>
        )}
        <label>
          Time travel point
          <select value={timeTravelType} onChange={(event) => {
            setTimeTravelType(Number(event.target.value));
            setTarget('');
            setStepMethod(0);
          }}>
            {offeredTimeTravelTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}
          </select>
        </label>
        <HistoryPointFields
          kind={pointKind}
          target={target}
          stepMethod={stepMethod}
          stepTypes={stepTypes}
          timezone={timezone}
          onTargetChange={setTarget}
          onStepMethodChange={setStepMethod}
        />
        {supportsRunHistoryForks && (
          <SubFlowResetPolicyField value={subFlowResetPolicy} onChange={setSubFlowResetPolicy} />
        )}
        <label>
          Reason
          <textarea
            aria-required="true"
            placeholder="Required: why this run goes back"
            rows={3}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button
            className="button danger"
            disabled={submitting
              || !reason.trim()
              || !isHistoryPointComplete(pointKind, target, stepMethod, timezone)}
            onClick={() => void submit()}
          >
            {submitting ? 'Time traveling…' : 'Time travel'}
          </button>
        </div>
      </div>
    </div>
  );
}
