// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { newCloneForkRequestId } from '@/lib/cloneFork';
import { useModalFocus } from '../components/useModalFocus';
import {
  forkAgentSession,
  randomSessionIdSuffix,
  sessionForkTaskQueue,
  suggestedForkSessionId,
  type SessionForkPoint,
} from './sessionFork';

/** Forks one agent session into a new session that runs on its own task queue. */
export function SessionForkDialog({
  sourceSessionId,
  sourceRunId,
  sourceTaskQueue,
  point,
  onClose,
  onForked,
}: {
  sourceSessionId: string;
  sourceRunId: string;
  /** The source run's task queue; '' for the default task queue. */
  sourceTaskQueue: string;
  point: SessionForkPoint;
  onClose: () => void;
  onForked: (sessionId: string, runId: string) => void;
}) {
  const [sessionId, setSessionId] = useState(() => suggestedForkSessionId(sourceSessionId, new Date(), randomSessionIdSuffix()));
  const [reason, setReason] = useState(() => (point.kind === 'current'
    ? `Fork session ${sourceSessionId}`
    : `Fork session ${sourceSessionId} after event ${point.eventId}`));
  const [requestId] = useState(newCloneForkRequestId);
  /** Undefined while it is computed; null when the fork inherits the source's task queue. */
  const [taskQueue, setTaskQueue] = useState<string | null | undefined>(undefined);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const modalRef = useModalFocus<HTMLDivElement>(true, onClose);

  const trimmedSessionId = sessionId.trim();
  useEffect(() => {
    let isCurrent = true;
    setTaskQueue(undefined);
    if (!trimmedSessionId) return undefined;
    sessionForkTaskQueue(sourceSessionId, sourceTaskQueue, trimmedSessionId).then(
      (queue) => { if (isCurrent) setTaskQueue(queue); },
      (queueError: unknown) => { if (isCurrent) setError(queueError instanceof Error ? queueError.message : String(queueError)); },
    );
    return () => { isCurrent = false; };
  }, [sourceSessionId, sourceTaskQueue, trimmedSessionId]);

  const sessionIdError = trimmedSessionId === sourceSessionId ? 'Choose a session ID other than the source.' : '';

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      const runId = await forkAgentSession({
        sourceSessionId,
        sourceRunId,
        sourceTaskQueue,
        sessionId: trimmedSessionId,
        point,
        reason: reason.trim(),
        requestId,
      });
      onForked(trimmedSessionId, runId);
    } catch (forkError) {
      setError(forkError instanceof Error ? forkError.message : 'Fork failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        aria-labelledby="session-fork-title"
        aria-modal="true"
        className="modal clone-fork-dialog"
        onMouseDown={(event) => event.stopPropagation()}
        ref={modalRef}
        role="dialog"
        tabIndex={-1}
      >
        <div className="modal-header">
          <h2 id="session-fork-title">{point.kind === 'current' ? 'Fork session' : 'Fork from this message'}</h2>
          <button aria-label="Close Fork session" className="icon-button" onClick={onClose}>×</button>
        </div>
        <p>
          {point.kind === 'current'
            ? 'Starts a new session with a copy of this conversation as it is now.'
            : 'Starts a new session with a copy of this conversation up to the end of this message\'s turn.'}
          {' '}This session does not change. No terminal has the new session open.
        </p>
        <label>
          New session ID
          <input
            aria-invalid={sessionIdError ? true : undefined}
            spellCheck={false}
            value={sessionId}
            onChange={(event) => setSessionId(event.target.value)}
          />
          {sessionIdError
            ? <small className="field-error">{sessionIdError}</small>
            : <small className="muted"><TaskQueueNote sourceTaskQueue={sourceTaskQueue} taskQueue={taskQueue} /></small>}
        </label>
        <label>
          Reason
          <textarea
            aria-required="true"
            rows={2}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
        </label>
        {error && <div className="error-banner">{error}</div>}
        <div className="modal-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button
            className="button primary"
            disabled={submitting || !trimmedSessionId || Boolean(sessionIdError) || !reason.trim() || taskQueue === undefined}
            onClick={() => void submit()}
          >
            {submitting ? 'Forking…' : 'Fork'}
          </button>
        </div>
      </div>
    </div>
  );
}

/** Names the fork's task queue: its own, derived from the session ID, or the source's that it inherits. */
function TaskQueueNote({ sourceTaskQueue, taskQueue }: { sourceTaskQueue: string; taskQueue: string | null | undefined }) {
  if (taskQueue === undefined) return <>Task queue …</>;
  if (taskQueue === null) {
    return sourceTaskQueue
      ? <>Task queue: inherits <span className="mono">{sourceTaskQueue}</span> from this session.</>
      : <>Task queue: inherits the default task queue from this session.</>;
  }
  return <>Task queue <span className="mono">{taskQueue}</span>, derived from the session ID.</>;
}
