// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type RefObject } from 'react';
import { ListPlus, SendHorizontal, Square } from 'lucide-react';
import { SESSION_COPY } from './copy';
import type { SessionActionInvoker } from './InterruptControls';
import { actionBlockedReason, type SessionActionOutcome, type SessionControlContext } from './sessionActions';
import { sendSessionMessage, sessionRouteBlockedReason, type SessionRouteContext } from './sessionRequests';
import { SESSION_RPC, type AgentSessionSchema } from './sessionSchema';
import type { LoadedAgentSession } from './useAgentSession';

type ComposerAction = '' | 'send' | 'steer' | 'stop';

/**
 * The message box under the conversation.
 *
 * Between turns it sends a message, which starts the next turn. While a turn runs it queues a
 * message for the next turn, or steers the running turn when the turn accepts steering.
 */
export function SessionComposer({
  schema,
  session,
  context,
  routeContext,
  hintShownAbove,
  invoke,
  onActed,
  textRef,
  text,
  setText,
}: {
  schema: AgentSessionSchema;
  session: LoadedAgentSession;
  context: SessionControlContext;
  routeContext: SessionRouteContext;
  /** The Working as hint at the top of the tab, which the composer does not repeat. */
  hintShownAbove: string | null;
  invoke: SessionActionInvoker;
  onActed: () => void;
  textRef: RefObject<HTMLTextAreaElement | null>;
  text: string;
  setText: (text: string) => void;
}) {
  const [busy, setBusy] = useState<ComposerAction>('');
  const [message, setMessage] = useState<{ tone: 'note' | 'error'; text: string } | null>(null);
  const state = session.view.state;
  const activeTurnId = state?.activeTurnId ?? null;
  const isTurnRunning = activeTurnId !== null;
  const turnControlReason = isTurnRunning && state?.acceptsTurnControl === false ? SESSION_COPY.noTurnControl : null;
  const sendBlocked = sessionRouteBlockedReason(schema, context);
  const steerBlocked = actionBlockedReason(SESSION_RPC.steerUserMessage, schema.steerUserMessage, context) ?? turnControlReason;
  const stopBlocked = actionBlockedReason(SESSION_RPC.cancelTurn, schema.cancelTurn, context)
    ?? (isTurnRunning ? turnControlReason : SESSION_COPY.noActiveTurn);
  const canSteer = isTurnRunning && steerBlocked === null;
  const isComposerBlocked = sendBlocked !== null && !canSteer;
  const hasText = text.trim() !== '';

  const run = async (kind: ComposerAction, request: () => Promise<SessionActionOutcome>, success: string) => {
    setBusy(kind);
    setMessage(null);
    try {
      const outcome = await request();
      if (outcome.kind === 'done') {
        setMessage({ tone: 'note', text: success });
        if (kind !== 'stop') setText('');
        onActed();
      } else if (outcome.kind === 'already_handled') {
        onActed();
      } else {
        setMessage({ tone: 'error', text: outcome.message });
      }
    } finally {
      setBusy('');
    }
  };
  const send = () => run(
    'send',
    () => sendSessionMessage(routeContext, text),
    isTurnRunning ? SESSION_COPY.queued : SESSION_COPY.steered,
  );
  const steer = () => run('steer', () => invoke(schema.steerUserMessage, { text }), SESSION_COPY.steered);

  const shownReasons = new Set<string | null>([null, hintShownAbove]);
  const blockedNotes: string[] = [];
  const addBlockedNote = (label: string | null, reason: string | null) => {
    if (shownReasons.has(reason)) return;
    shownReasons.add(reason);
    blockedNotes.push(label ? `${label}: ${reason}` : reason as string);
  };
  if (isComposerBlocked) addBlockedNote(null, sendBlocked);
  if (isTurnRunning) {
    addBlockedNote(SESSION_COPY.queue, sendBlocked);
    addBlockedNote(SESSION_COPY.steer, steerBlocked);
    addBlockedNote(SESSION_COPY.stop, stopBlocked);
  }

  return (
    <footer className="ses-controls">
      <form
        className="ses-steer"
        onSubmit={(event) => {
          event.preventDefault();
          if (!hasText || busy !== '') return;
          void (sendBlocked === null ? send() : steer());
        }}
      >
        <div className="ses-composer" data-disabled={isComposerBlocked ? 'true' : undefined}>
          <textarea
            aria-label={SESSION_COPY.steerLabel}
            disabled={isComposerBlocked}
            placeholder={isTurnRunning ? SESSION_COPY.composerRunningPlaceholder : SESSION_COPY.composerIdlePlaceholder}
            ref={textRef}
            rows={2}
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
          <div className="ses-composer-buttons">
            {isTurnRunning && (
              <button
                className="agc-danger"
                disabled={busy !== '' || stopBlocked !== null}
                onClick={() => void run('stop', () => invoke(schema.cancelTurn, { turn_id: activeTurnId }), SESSION_COPY.stopped)}
                type="button"
              >
                <Square aria-hidden="true" size={11} strokeWidth={2.6} />
                {busy === 'stop' ? SESSION_COPY.stopping : SESSION_COPY.stop}
              </button>
            )}
            {isTurnRunning && canSteer && (
              <button
                className="agc-secondary"
                disabled={busy !== '' || !hasText}
                onClick={() => void steer()}
                type="button"
              >
                {busy === 'steer' ? SESSION_COPY.sending : SESSION_COPY.steer}
              </button>
            )}
            {sendBlocked === null && (
              <button className="agc-primary" disabled={busy !== '' || !hasText} type="submit">
                {isTurnRunning
                  ? <ListPlus aria-hidden="true" size={14} strokeWidth={2.2} />
                  : <SendHorizontal aria-hidden="true" size={14} strokeWidth={2.2} />}
                {busy === 'send' ? SESSION_COPY.sending : isTurnRunning ? SESSION_COPY.queue : SESSION_COPY.send}
              </button>
            )}
          </div>
        </div>
        {!isComposerBlocked && !isTurnRunning && <p className="agc-note">{SESSION_COPY.steerIdleNote}</p>}
        {!isComposerBlocked && isTurnRunning && sendBlocked === null && canSteer && (
          <p className="agc-note">{SESSION_COPY.runningNote}</p>
        )}
        {blockedNotes.map((note) => <p className="agc-blocked" key={note}>{note}</p>)}
        {message && <p className={message.tone === 'error' ? 'agc-error' : 'agc-note'} role="status">{message.text}</p>}
      </form>
    </footer>
  );
}
