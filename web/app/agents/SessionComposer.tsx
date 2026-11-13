// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState, type RefObject } from 'react';
import { ListPlus, SendHorizontal, ShieldCheck, Square } from 'lucide-react';
import { SESSION_COPY } from './copy';
import type { SessionActionInvoker } from './InterruptControls';
import { actionBlockedReason, type SessionActionOutcome, type SessionControlContext } from './sessionActions';
import { limitSenderNameLength, readStoredSenderName, senderNameToSend, writeStoredSenderName } from './senderName';
import { sendSessionMessage, sessionRouteBlockedReason, type SessionRouteContext } from './sessionRequests';
import { SESSION_RPC, type AgentSessionSchema } from './sessionSchema';
import type { LoadedAgentSession } from './useAgentSession';

type ComposerAction = '' | 'send' | 'steer' | 'stop';

/** The Working as choice that unblocks the session's Actions, offered in the composer. */
export interface ComposerWorkingAsChoice {
  permission: string;
  choose: () => void;
}

/** The message box: Send between turns; Queue, Steer, or Stop while a turn runs. */
export function SessionComposer({
  schema,
  session,
  context,
  routeContext,
  hintShownAbove,
  workingAsChoice,
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
  /** Offered in place of Send while only Working as blocks the composer. */
  workingAsChoice: ComposerWorkingAsChoice | null;
  invoke: SessionActionInvoker;
  onActed: () => void;
  textRef: RefObject<HTMLTextAreaElement | null>;
  text: string;
  setText: (text: string) => void;
}) {
  const [busy, setBusy] = useState<ComposerAction>('');
  const [message, setMessage] = useState<{ tone: 'note' | 'error'; text: string } | null>(null);
  const [senderName, setSenderName] = useState(readStoredSenderName);
  const isComposingSenderNameRef = useRef(false);
  const changeSenderName = (name: string) => {
    // An input method's unfinished text may pass the limit, so the name is cut once it is committed.
    const next = isComposingSenderNameRef.current ? name : limitSenderNameLength(name);
    setSenderName(next);
    if (!isComposingSenderNameRef.current) writeStoredSenderName(next);
  };
  const state = session.view.state;
  const activeTurnId = state?.activeTurnId ?? null;
  const isTurnRunning = activeTurnId !== null;
  const turnControlReason = isTurnRunning && state?.acceptsTurnControl === false ? SESSION_COPY.noTurnControl : null;
  const sendBlocked = sessionRouteBlockedReason(schema, SESSION_RPC.sendUserMessage, context);
  const steerBlocked = actionBlockedReason(SESSION_RPC.steerUserMessage, schema.steerUserMessage, context) ?? turnControlReason;
  const stopBlocked = actionBlockedReason(SESSION_RPC.cancelTurn, schema.cancelTurn, context)
    ?? (isTurnRunning ? turnControlReason : SESSION_COPY.noActiveTurn);
  const canSteer = isTurnRunning && steerBlocked === null;
  const isComposerBlocked = sendBlocked !== null && !canSteer;
  const workingAsOffer = isComposerBlocked
    && workingAsChoice !== null
    && sendBlocked === SESSION_COPY.chooseWorkingAs(workingAsChoice.permission)
    ? workingAsChoice
    : null;
  const hasText = text.trim() !== '';
  const [workingAsChoiceCount, setWorkingAsChoiceCount] = useState(0);

  // Runs once per choice, in the render the choice produced; a choice that leaves the box blocked focuses nothing.
  useEffect(() => {
    if (workingAsChoiceCount > 0 && !isComposerBlocked) textRef.current?.focus();
  }, [workingAsChoiceCount, textRef]);

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
  const namedSender = senderNameToSend(senderName);
  // A session graph from before sender names refuses the field, so it is sent only when declared.
  const canSteerWithName = schema.steerUserMessage?.input.fields?.some((field) => field.fieldName === 'sender_name') === true;
  const steerInput = namedSender !== null && canSteerWithName ? { text, sender_name: namedSender } : { text };
  const send = () => run(
    'send',
    () => sendSessionMessage(routeContext, text, namedSender),
    isTurnRunning ? SESSION_COPY.queued : SESSION_COPY.steered,
  );
  const steer = () => run('steer', () => invoke(schema.steerUserMessage, steerInput), SESSION_COPY.steered);

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
        {!isComposerBlocked && (
          <label className="ses-sender" title={SESSION_COPY.senderNameTitle}>
            <span>{SESSION_COPY.senderNameLabel}</span>
            <input
              autoComplete="nickname"
              placeholder={SESSION_COPY.senderNamePlaceholder}
              type="text"
              value={senderName}
              onChange={(event) => changeSenderName(event.target.value)}
              onCompositionStart={() => { isComposingSenderNameRef.current = true; }}
              onCompositionEnd={(event) => {
                isComposingSenderNameRef.current = false;
                changeSenderName(event.currentTarget.value);
              }}
            />
          </label>
        )}
        <div className="ses-composer" data-disabled={isComposerBlocked ? 'true' : undefined}>
          <textarea
            aria-label={SESSION_COPY.steerLabel}
            disabled={isComposerBlocked}
            placeholder={workingAsOffer
              ? SESSION_COPY.composerWorkingAsPlaceholder(workingAsOffer.permission)
              : isTurnRunning ? SESSION_COPY.composerRunningPlaceholder : SESSION_COPY.composerIdlePlaceholder}
            ref={textRef}
            rows={2}
            value={text}
            onChange={(event) => setText(event.target.value)}
          />
          <div className="ses-composer-buttons">
            {workingAsOffer && (
              <button
                className="agc-primary"
                onClick={() => {
                  setWorkingAsChoiceCount((count) => count + 1);
                  workingAsOffer.choose();
                }}
                type="button"
              >
                <ShieldCheck aria-hidden="true" size={14} strokeWidth={2.2} />
                {SESSION_COPY.workAs(workingAsOffer.permission)}
              </button>
            )}
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
