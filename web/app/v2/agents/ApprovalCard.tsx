// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';
import { formatElapsedDuration } from '@/lib/timeline';
import { SESSION_COPY } from './copy';
import type { SessionActionOutcome } from './sessionActions';
import type { AgentInterruptRecord } from './sessionSchema';
import './session.css';

/** How a request ended when the host must reload: it changed the session, or someone else did. */
export type SettledOutcome = 'done' | 'already_handled';

/** Shared by the approval and question cards: one request at a time, and its result. */
function useInterruptRequest(onSettled: (outcome: SettledOutcome) => void) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<{ tone: 'note' | 'error'; text: string } | null>(null);
  const run = async (request: () => Promise<SessionActionOutcome>) => {
    setBusy(true);
    setMessage(null);
    try {
      const outcome = await request();
      if (outcome.kind === 'done') {
        onSettled('done');
        return;
      }
      if (outcome.kind === 'already_handled') {
        setMessage({ tone: 'note', text: SESSION_COPY.alreadyHandled });
        onSettled('already_handled');
        return;
      }
      setMessage({ tone: 'error', text: outcome.message });
    } finally {
      setBusy(false);
    }
  };
  return { busy, message, run };
}

/** Approve or reject one pending tool call; a reject may carry a reason for the model. */
export function ApprovalCard({
  interrupt,
  blockedReason,
  onResolve,
  onSettled,
  now = Date.now(),
}: {
  interrupt: AgentInterruptRecord;
  /** Non-null disables both buttons and says why. */
  blockedReason: string | null;
  onResolve: (approved: boolean, reason: string) => Promise<SessionActionOutcome>;
  /** Called after the session changed or someone else acted, so the host reloads. */
  onSettled: (outcome: SettledOutcome) => void;
  now?: number;
}) {
  const [isRejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState('');
  const { busy, message, run } = useInterruptRequest(onSettled);
  const disabled = busy || blockedReason !== null;

  return (
    <div className="agc" data-testid={`approval-${interrupt.interruptId}`}>
      <InterruptDeadline deadline={interrupt.deadline} now={now} />
      {isRejecting ? (
        <form
          className="agc-reject"
          onSubmit={(event) => {
            event.preventDefault();
            void run(() => onResolve(false, reason));
          }}
        >
          <label className="agc-label">
            <span>{SESSION_COPY.rejectReasonLabel}</span>
            <textarea
              aria-label={SESSION_COPY.rejectReasonLabel}
              rows={2}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
          </label>
          <div className="agc-buttons">
            <button className="agc-danger" disabled={disabled} type="submit">
              {busy ? SESSION_COPY.working : SESSION_COPY.confirmReject}
            </button>
            <button className="v2-ghost" disabled={busy} onClick={() => setRejecting(false)} type="button">
              {SESSION_COPY.cancel}
            </button>
          </div>
        </form>
      ) : (
        <div className="agc-buttons">
          <button
            className="v2-primary"
            disabled={disabled}
            onClick={() => void run(() => onResolve(true, ''))}
            type="button"
          >
            {busy ? SESSION_COPY.working : SESSION_COPY.approve}
          </button>
          <button className="v2-ghost" disabled={disabled} onClick={() => setRejecting(true)} type="button">
            {SESSION_COPY.reject}
          </button>
        </div>
      )}
      {blockedReason && <p className="agc-blocked">{blockedReason}</p>}
      {message && <p className={message.tone === 'error' ? 'v2-error' : 'agc-note'} role="status">{message.text}</p>}
    </div>
  );
}

/** Answer or decline one pending question, from its choices or as free text. */
export function QuestionCard({
  interrupt,
  answerBlockedReason,
  declineBlockedReason,
  onAnswer,
  onDecline,
  onSettled,
  now = Date.now(),
}: {
  interrupt: AgentInterruptRecord;
  answerBlockedReason: string | null;
  declineBlockedReason: string | null;
  onAnswer: (answer: string) => Promise<SessionActionOutcome>;
  onDecline: () => Promise<SessionActionOutcome>;
  onSettled: (outcome: SettledOutcome) => void;
  now?: number;
}) {
  const [answer, setAnswer] = useState('');
  const { busy, message, run } = useInterruptRequest(onSettled);
  return (
    <div className="agc" data-testid={`question-${interrupt.interruptId}`}>
      {interrupt.question && <p className="agc-question">{interrupt.question}</p>}
      <InterruptDeadline deadline={interrupt.deadline} now={now} />
      {interrupt.choices.length > 0 && (
        <div className="agc-buttons">
          {interrupt.choices.map((choice) => (
            <button
              className="v2-ghost"
              disabled={busy || answerBlockedReason !== null}
              key={choice}
              onClick={() => void run(() => onAnswer(choice))}
              type="button"
            >
              {choice}
            </button>
          ))}
        </div>
      )}
      <form
        className="agc-reject"
        onSubmit={(event) => {
          event.preventDefault();
          if (answer.trim()) void run(() => onAnswer(answer));
        }}
      >
        {interrupt.allowsFreeText && (
          <label className="agc-label">
            <span>{SESSION_COPY.answerLabel}</span>
            <textarea
              aria-label={SESSION_COPY.answerLabel}
              rows={2}
              value={answer}
              onChange={(event) => setAnswer(event.target.value)}
            />
          </label>
        )}
        <div className="agc-buttons">
          {interrupt.allowsFreeText && (
            <button className="v2-primary" disabled={busy || answerBlockedReason !== null || !answer.trim()} type="submit">
              {busy ? SESSION_COPY.working : SESSION_COPY.answer}
            </button>
          )}
          <button
            className="v2-ghost"
            disabled={busy || declineBlockedReason !== null}
            onClick={() => void run(onDecline)}
            type="button"
          >
            {SESSION_COPY.decline}
          </button>
        </div>
      </form>
      {(answerBlockedReason ?? declineBlockedReason) && (
        <p className="agc-blocked">{answerBlockedReason ?? declineBlockedReason}</p>
      )}
      {message && <p className={message.tone === 'error' ? 'v2-error' : 'agc-note'} role="status">{message.text}</p>}
    </div>
  );
}

function InterruptDeadline({ deadline, now }: { deadline: string | null; now: number }) {
  if (!deadline) return null;
  const at = Date.parse(deadline);
  if (!Number.isFinite(at)) return null;
  if (at <= now) return <p className="agc-deadline" data-passed="true">{SESSION_COPY.deadlinePassed}</p>;
  return (
    <p className="agc-deadline">
      {SESSION_COPY.deadline(new Date(at).toLocaleString(), formatElapsedDuration(at - now))}
    </p>
  );
}
