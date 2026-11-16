// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useLayoutEffect, useState } from 'react';
import type { TimezonePreference } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import {
  CLONE_FORK_POINT_TYPE,
  ID_REUSE_POLICY,
  idReusePolicyOptions,
  newCloneForkRequestId,
  suggestedCloneForkFlowId,
} from '@/lib/cloneFork';
import { SUB_FLOW_RESET_POLICY } from '@/lib/timeTravel';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';
import { webURL } from '@/lib/basePath';
import { SESSION_FORK_DELETED_ATTRIBUTES, SESSION_TASK_QUEUE_PREFIX, sessionTaskQueue } from '../../agents/sessionFork';
import { useModalFocus } from '../../components/useModalFocus';
import { startTaskQueueError } from '../../start/StartFlowDialog';
import {
  HistoryPointFields,
  historyPointRequestFields,
  isHistoryPointComplete,
  SubFlowResetPolicyField,
  useStepTypesOf,
  type HistoryPointKind,
} from './HistoryPointFields';

/** The new Flow run a clone fork created. */
export interface CloneForkResult {
  flowId: string;
  runId: string;
}

const cloneForkPoints: { kind: HistoryPointKind; value: number; label: string }[] = [
  { kind: 'current', value: CLONE_FORK_POINT_TYPE.CURRENT, label: 'Current state' },
  { kind: 'beginning', value: CLONE_FORK_POINT_TYPE.BEGINNING, label: 'Beginning' },
  { kind: 'history-event-time', value: CLONE_FORK_POINT_TYPE.HISTORY_EVENT_TIME, label: 'History event time' },
  { kind: 'step-type', value: CLONE_FORK_POINT_TYPE.STEP_TYPE, label: 'Step type' },
  { kind: 'step-execution-id', value: CLONE_FORK_POINT_TYPE.STEP_EXECUTION_ID, label: 'Step execution ID' },
  { kind: 'event-id', value: CLONE_FORK_POINT_TYPE.EVENT_ID, label: 'Event ID' },
];

/** Whether the source run's task queue is read yet; 'failed' when its configuration could not be read. */
export type SourceTaskQueueStatus = 'loading' | 'loaded' | 'failed';

/** Whether the source runs on its own session task queue; 'unavailable' when Web Crypto cannot tell. */
type SessionQueueCheck = 'pending' | 'own' | 'other' | 'unavailable';

/** A derived session task queue, kept with the Flow ID it was derived from. */
interface DerivedSessionTaskQueue {
  flowId: string;
  taskQueue: string;
}

function initialPointKind(initialEvent: FlowHistoryEvent | null, isSourceRunning: boolean): HistoryPointKind {
  if (initialEvent) return 'event-id';
  return isSourceRunning ? 'current' : 'beginning';
}

export function CloneForkDialog({
  open,
  summary,
  events,
  initialEvent,
  runAttempt,
  sourceTaskQueue,
  sourceTaskQueueStatus,
  isAgentSessionDefinition,
  isSourceRunning,
  timezone,
  onClose,
  onCloneForked,
}: {
  open: boolean;
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  /** The event whose point is copied; null opens on the current state of a running run. */
  initialEvent: FlowHistoryEvent | null;
  /** The run attempt whose history is shown; 0 selects the current one. */
  runAttempt: number;
  /** The source run's task queue, or null when its configuration is unavailable. */
  sourceTaskQueue: string | null;
  /** Whether sourceTaskQueue is read; a null queue means the default task queue only once loaded. */
  sourceTaskQueueStatus: SourceTaskQueueStatus;
  /** Whether the source's Flow definition is an agent session; null while the definition loads. */
  isAgentSessionDefinition: boolean | null;
  /** The current state can be copied only while the source run is running. */
  isSourceRunning: boolean;
  timezone: TimezonePreference;
  onClose: () => void;
  onCloneForked: (result: CloneForkResult) => void;
}) {
  const [pointKind, setPointKind] = useState<HistoryPointKind>(() => initialPointKind(initialEvent, isSourceRunning));
  const [target, setTarget] = useState(() => (initialEvent ? String(initialEvent.eventId) : ''));
  const [stepMethod, setStepMethod] = useState(0);
  const [flowId, setFlowId] = useState(() => suggestedCloneForkFlowId(summary.flowId, new Date()));
  const [taskQueue, setTaskQueue] = useState(sourceTaskQueue ?? '');
  const [idReusePolicy, setIdReusePolicy] = useState<number>(ID_REUSE_POLICY.SERVER_DEFAULT);
  const [subFlowResetPolicy, setSubFlowResetPolicy] = useState<number>(SUB_FLOW_RESET_POLICY.ATTACH_ELSE_RESTART);
  const [reason, setReason] = useState('');
  const [requestId, setRequestId] = useState(newCloneForkRequestId);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [hashedSessionQueueCheck, setHashedSessionQueueCheck] =useState<SessionQueueCheck>('pending');
  const canBeOwnSessionQueue = sourceTaskQueue?.startsWith(SESSION_TASK_QUEUE_PREFIX) === true;
  const sessionQueueCheck: SessionQueueCheck = sourceTaskQueueStatus === 'loading'
    ? 'pending'
    : canBeOwnSessionQueue ? hashedSessionQueueCheck : 'other';
  const [derivedTaskQueue, setDerivedTaskQueue] = useState<DerivedSessionTaskQueue | null>(null);
  const stepTypes = useStepTypesOf(events);
  const points = isSourceRunning ? cloneForkPoints : cloneForkPoints.filter((point) => point.kind !== 'current');
  const trimmedFlowId = flowId.trim();

  useEffect(() => {
    if (!open || !sourceTaskQueue?.startsWith(SESSION_TASK_QUEUE_PREFIX)) return undefined;
    let isCurrent = true;
    setHashedSessionQueueCheck('pending');
    sessionTaskQueue(summary.flowId).then(
      (ownTaskQueue) => {
        if (isCurrent) setHashedSessionQueueCheck(ownTaskQueue === sourceTaskQueue ? 'own' : 'other');
      },
      () => {
        if (isCurrent) setHashedSessionQueueCheck('unavailable');
      },
    );
    return () => {
      isCurrent = false;
    };
  }, [open, sourceTaskQueue, summary.flowId]);

  const isOnOwnSessionQueue = sessionQueueCheck === 'own';
  useEffect(() => {
    if (!open || !isOnOwnSessionQueue || !trimmedFlowId) return undefined;
    let isCurrent = true;
    sessionTaskQueue(trimmedFlowId).then(
      (taskQueueOfNewFlow) => {
        if (isCurrent) setDerivedTaskQueue({ flowId: trimmedFlowId, taskQueue: taskQueueOfNewFlow });
      },
      (deriveError: unknown) => {
        if (isCurrent) setError(deriveError instanceof Error ? deriveError.message : 'Deriving the session task queue failed');
      },
    );
    return () => {
      isCurrent = false;
    };
  }, [isOnOwnSessionQueue, open, trimmedFlowId]);

  // A layout effect resets the fields before anyone can type, even when the dialog opens outside an event.
  useLayoutEffect(() => {
    if (!open) return;
    setPointKind(initialPointKind(initialEvent, isSourceRunning));
    setTarget(initialEvent ? String(initialEvent.eventId) : '');
    setStepMethod(0);
    setFlowId(suggestedCloneForkFlowId(summary.flowId, new Date()));
    setTaskQueue(sourceTaskQueue ?? '');
    setIdReusePolicy(ID_REUSE_POLICY.SERVER_DEFAULT);
    setSubFlowResetPolicy(SUB_FLOW_RESET_POLICY.ATTACH_ELSE_RESTART);
    setReason('');
    setRequestId(newCloneForkRequestId());
    setError('');
  }, [initialEvent?.eventId, open]);

  // A source task queue read after the dialog opened fills the field it would have opened with.
  useEffect(() => {
    if (open) setTaskQueue(sourceTaskQueue ?? '');
  }, [open, sourceTaskQueue]);

  const modalRef = useModalFocus<HTMLDivElement>(open, onClose);
  if (!open) return null;

  const trimmedTaskQueue = taskQueue.trim();
  const flowIdError = trimmedFlowId === summary.flowId
    ? 'Choose a Flow ID other than the source. Time travel returns this Flow to an earlier point.'
    : '';
  // An agent session is known by its definition or by running on its own session task queue.
  const isAgentSession = isAgentSessionDefinition === true || isOnOwnSessionQueue;
  const sessionTaskQueueOfNewFlow = isOnOwnSessionQueue && derivedTaskQueue?.flowId === trimmedFlowId
    ? derivedTaskQueue.taskQueue
    : null;
  // Without the source's task queue, Dex Web cannot tell whether the fork needs the session rules.
  const sessionQueueError = sourceTaskQueueStatus === 'failed'
    ? 'Dex Web could not read the source run\'s task queue, so it cannot tell whether this run is an agent session. Reload the run and try again.'
    : sessionQueueCheck === 'unavailable'
      ? 'This browser cannot check whether this run is on its own agent session task queue: Web Crypto needs HTTPS or localhost.'
      : '';
  const isSessionCheckPending = isAgentSessionDefinition === null
    || sessionQueueCheck === 'pending'
    || (isOnOwnSessionQueue && sessionTaskQueueOfNewFlow === null);
  const taskQueueError = isOnOwnSessionQueue ? '' : startTaskQueueError(trimmedTaskQueue) ?? '';

  async function submit() {
    setSubmitting(true);
    setError('');
    const payload: Record<string, unknown> = {
      sourceFlowId: summary.flowId,
      sourceRunId: summary.runId,
      flowId: trimmedFlowId,
      requestId,
      pointType: cloneForkPoints.find((point) => point.kind === pointKind)?.value ?? CLONE_FORK_POINT_TYPE.CURRENT,
      ...historyPointRequestFields(pointKind, target, stepMethod, timezone),
      idReusePolicy,
      subFlowResetPolicy,
      reason,
    };
    if (isOnOwnSessionQueue) {
      if (sessionTaskQueueOfNewFlow) payload.taskQueue = sessionTaskQueueOfNewFlow;
    } else if (trimmedTaskQueue && trimmedTaskQueue !== sourceTaskQueue) {
      // An unchanged task queue sends no override, so the new run keeps the source's.
      payload.taskQueue = trimmedTaskQueue;
    }
    if (isAgentSession) payload.deleteAttributeKeys = SESSION_FORK_DELETED_ATTRIBUTES;
    // The current state always belongs to the current run attempt.
    if (pointKind !== 'current' && runAttempt > 0) payload.runAttempt = runAttempt;
    try {
      const response = await fetch(webURL('/api/flows/clone-fork'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      const data = await readResponseJSON<{ flowId?: string; runId?: string }>(response);
      onCloneForked({ flowId: data.flowId || trimmedFlowId, runId: data.runId ?? '' });
      onClose();
    } catch (cloneForkError) {
      setError(cloneForkError instanceof Error ? cloneForkError.message : 'Clone fork failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        aria-labelledby="clone-fork-title"
        aria-modal="true"
        className="modal clone-fork-dialog"
        onMouseDown={(event) => event.stopPropagation()}
        ref={modalRef}
        role="dialog"
        tabIndex={-1}
      >
        <div className="modal-header">
          <h2 id="clone-fork-title">Clone fork to a new Flow</h2>
          <button aria-label="Close Clone fork" className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          Clone fork starts a new Flow from a copy of this run&apos;s state at the selected point.
          Dex only reads this run, so it never changes.
        </p>
        {isAgentSession && (
          <p className="muted" data-testid="clone-fork-session-note">
            Agent session fork: the new session starts released, without the source&apos;s terminal lease.
          </p>
        )}
        {runAttempt > 0 && pointKind !== 'current' && (
          <p className="muted">Searching the history of run attempt {runAttempt}.</p>
        )}
        <label>
          Clone fork point
          <select value={pointKind} onChange={(event) => {
            setPointKind(event.target.value as HistoryPointKind);
            setTarget('');
            setStepMethod(0);
          }}>
            {points.map((point) => <option value={point.kind} key={point.kind}>{point.label}</option>)}
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
        <label>
          New Flow ID
          <input
            aria-invalid={flowIdError ? true : undefined}
            spellCheck={false}
            value={flowId}
            onChange={(event) => setFlowId(event.target.value)}
          />
          {flowIdError && <small className="field-error">{flowIdError}</small>}
        </label>
        {isOnOwnSessionQueue ? (
          <label>
            Task queue
            <input readOnly spellCheck={false} value={sessionTaskQueueOfNewFlow ?? ''} />
            <small className="muted">Derived from the new Flow ID: the new session runs on its own task queue.</small>
          </label>
        ) : (
        <label>
          Task queue
          <input
            aria-invalid={taskQueueError ? true : undefined}
            placeholder="Source task queue (default)"
            spellCheck={false}
            value={taskQueue}
            onChange={(event) => setTaskQueue(event.target.value)}
          />
          {taskQueueError
            ? <small className="field-error">{taskQueueError}</small>
            : <small className="muted">{sourceTaskQueue === null
              ? 'Empty inherits the source\'s task queue; dex.default selects the default one.'
              : 'Empty keeps the source\'s task queue; dex.default selects the default one.'}</small>}
        </label>
        )}
        <label>
          ID reuse policy
          <select value={idReusePolicy} onChange={(event) => setIdReusePolicy(Number(event.target.value))}>
            {idReusePolicyOptions.map((option) => <option value={option.value} key={option.value}>{option.label}</option>)}
          </select>
        </label>
        <SubFlowResetPolicyField value={subFlowResetPolicy} onChange={setSubFlowResetPolicy} />
        <label>
          Reason
          <textarea
            aria-required="true"
            placeholder="Required: why this Flow is clone forked"
            rows={3}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        {(error || sessionQueueError) && <div className="error-banner">{error || sessionQueueError}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button
            className="button primary"
            disabled={submitting
              || !reason.trim()
              || !trimmedFlowId
              || Boolean(flowIdError)
              || Boolean(taskQueueError)
              || Boolean(sessionQueueError)
              || isSessionCheckPending
              || !isHistoryPointComplete(pointKind, target, stepMethod, timezone)}
            onClick={() => void submit()}
          >
            {submitting ? 'Clone forking…' : 'Clone fork'}
          </button>
        </div>
      </div>
    </div>
  );
}
