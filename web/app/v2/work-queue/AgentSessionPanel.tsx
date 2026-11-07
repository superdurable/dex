// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type ReactNode } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { formatDate } from '@/lib/format';
import { usePreferences } from '../../providers';
import { useWebCatalog } from '../WebCatalogProvider';
import { readFailureReason } from './liveness';
import { AGENT_INBOX_COPY } from './agentCopy';
import { agentActionInput, agentActionNotice, invokeAgentAction, type AgentAnswer } from './agentActions';
import { askedText, compareInterrupts, effectText, formattedArguments, waitText } from './agentInbox';
import {
  AGENT_SESSION,
  answeringAction,
  readAgentSession,
  type AgentInterrupt,
  type AgentSessionRead,
} from './agentSession';

interface Notice {
  interruptId: string;
  asked: string;
  title: string;
  text: string;
}

/**
 * The side panel of one session: every pending call, the last message, and the answers.
 *
 * After each answer it reads the session back, because the RPC result alone cannot tell
 * "someone else answered" from "its deadline passed".
 */
export function AgentSessionPanel({
  flowType,
  flowId,
  definition,
  read,
  readError,
  nowMs,
  workQueuePermissions,
  footer,
  onReadBack,
}: {
  flowType: string;
  flowId: string;
  definition: FlowV2Definition;
  read: AgentSessionRead | null;
  readError: string;
  nowMs: number;
  workQueuePermissions: readonly string[];
  footer: ReactNode;
  /** The session as read after an answer, so the list can drop or keep its row. */
  onReadBack: (flowId: string, read: AgentSessionRead) => void;
}) {
  const { catalog } = useWebCatalog();
  const { timezone } = usePreferences();
  const [notices, setNotices] = useState<Notice[]>([]);
  const [error, setError] = useState('');
  const [busyID, setBusyID] = useState('');

  async function answer(interrupt: AgentInterrupt, answerValue: AgentAnswer) {
    const rpcName = answerValue.rpc === 'resolve' ? AGENT_SESSION.resolveRPC : AGENT_SESSION.answerRPC;
    const action = answeringAction(definition, rpcName);
    const input = action ? agentActionInput(action, interrupt, answerValue) : null;
    if (!action || !input) {
      setError(AGENT_INBOX_COPY.undeclared(rpcName));
      return;
    }
    setBusyID(interrupt.interruptId);
    setError('');
    try {
      const result = await invokeAgentAction({
        flowType, flowId, rpcName, input, workQueuePermissions,
        definitionRevision: catalog?.definitionRevision ?? '',
      });
      const readBack = await readAgentSession(flowId);
      const notice = agentActionNotice(result, interrupt, readBack, Date.now());
      setNotices((current) => [
        ...current.filter((entry) => entry.interruptId !== interrupt.interruptId),
        {
          interruptId: interrupt.interruptId,
          asked: askedText(interrupt),
          title: notice?.title ?? AGENT_INBOX_COPY.done,
          text: notice?.text ?? '',
        },
      ]);
      onReadBack(flowId, readBack);
    } catch (actionError) {
      setError(readFailureReason(actionError));
    } finally {
      setBusyID('');
    }
  }

  const pending = [...(read?.interrupts ?? [])].sort(compareInterrupts);
  const pendingIDs = new Set(pending.map((interrupt) => interrupt.interruptId));
  const settled = notices.filter((notice) => !pendingIDs.has(notice.interruptId) || notice.title !== AGENT_INBOX_COPY.done);
  return (
    <div className="v2-case sc ai-panel">
      <div className="sc-head">
        <span className="sc-title t-mono">{flowId}</span>
        <span className="sc-status">{flowType}</span>
        {footer}
      </div>
      {readError !== '' && <p className="v2-error">{readError}</p>}
      {error !== '' && <p className="v2-error">{error}</p>}
      {settled.map((notice) => (
        <div className="ai-card" data-state="settled" key={notice.interruptId} role="status">
          <div className="ai-cardhead">
            <span className="ai-asked">{notice.asked}</span>
            <span className="ai-chip" data-tone="neutral">{notice.title}</span>
          </div>
          {notice.text !== '' && <p className="sc-state">{notice.text}</p>}
        </div>
      ))}
      {read === null && readError === '' && <p className="sc-state">{AGENT_INBOX_COPY.reading}</p>}
      {read !== null && (
        <>
          <div className="sc-block">
            <div className="sc-blockhead">{AGENT_INBOX_COPY.pendingCalls(pending.length)}</div>
            {pending.length === 0 && <p className="sc-state">{AGENT_INBOX_COPY.nothingPending}</p>}
            {pending.map((interrupt) => (
              <AgentCallCard
                busy={busyID === interrupt.interruptId}
                definition={definition}
                effect={read.toolCalls[interrupt.toolCallId]?.effect ?? ''}
                interrupt={interrupt}
                isActive={read.isActive}
                key={interrupt.interruptId}
                nowMs={nowMs}
                timezone={timezone}
                workQueuePermissions={workQueuePermissions}
                onAnswer={(value) => void answer(interrupt, value)}
              />
            ))}
          </div>
          <div className="sc-block">
            <div className="sc-blockhead">{AGENT_INBOX_COPY.lastMessage}</div>
            {read.lastMessage === null ? (
              <p className="sc-state">{AGENT_INBOX_COPY.noMessage}</p>
            ) : (
              <blockquote className="ai-message">
                <span className="ai-role">{read.lastMessage.role}</span>
                <span>{read.lastMessage.text}</span>
              </blockquote>
            )}
          </div>
        </>
      )}
    </div>
  );
}

/** One pending call: what it is, what it would do, until when, and the answers it accepts. */
function AgentCallCard({
  interrupt,
  effect,
  definition,
  isActive,
  busy,
  nowMs,
  timezone,
  workQueuePermissions,
  onAnswer,
}: {
  interrupt: AgentInterrupt;
  effect: string;
  definition: FlowV2Definition;
  isActive: boolean;
  busy: boolean;
  nowMs: number;
  timezone: ReturnType<typeof usePreferences>['timezone'];
  workQueuePermissions: readonly string[];
  onAnswer: (answer: AgentAnswer) => void;
}) {
  const [isRejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState('');
  const [answerText, setAnswerText] = useState('');
  const isQuestion = interrupt.kind === 'question';
  const action = answeringAction(definition, isQuestion ? AGENT_SESSION.answerRPC : AGENT_SESSION.resolveRPC);
  const resolveAction = answeringAction(definition, AGENT_SESSION.resolveRPC);
  const blocked = !isActive
    ? AGENT_INBOX_COPY.ended
    : action === undefined
      ? AGENT_INBOX_COPY.undeclared(isQuestion ? AGENT_SESSION.answerRPC : AGENT_SESSION.resolveRPC)
      : workQueuePermissions.includes(action.requiredPermission)
        ? ''
        : AGENT_INBOX_COPY.needsPermission(action.requiredPermission);
  const disabled = blocked !== '' || busy;
  const canDecline = resolveAction !== undefined && workQueuePermissions.includes(resolveAction.requiredPermission);

  return (
    <article className="ai-card" aria-label={askedText(interrupt)}>
      <div className="ai-cardhead">
        <span className="ai-asked">{isQuestion && interrupt.question ? interrupt.question : interrupt.toolName}</span>
        <span className="ai-chip" data-tone="attention">
          {isQuestion ? AGENT_INBOX_COPY.question : AGENT_INBOX_COPY.approval}
        </span>
      </div>
      <dl className="sc-facts">
        <dt className="sc-fname">{AGENT_INBOX_COPY.tool}</dt>
        <dd className="sc-fvalue t-mono">{interrupt.toolName}</dd>
        <dt className="sc-fname">{AGENT_INBOX_COPY.effect}</dt>
        <dd className="sc-fvalue">{effectText(effect)}</dd>
        <dt className="sc-fname">{AGENT_INBOX_COPY.deadline}</dt>
        <dd className="sc-fvalue">
          {interrupt.deadline === null
            ? `${AGENT_INBOX_COPY.noDeadline} · ${waitText(interrupt, nowMs)}`
            : `${formatDate(interrupt.deadline, timezone)} · ${waitText(interrupt, nowMs)}`}
        </dd>
      </dl>
      <div className="sc-fname">{AGENT_INBOX_COPY.arguments}</div>
      <pre className="ai-arguments">{formattedArguments(interrupt.arguments)}</pre>
      {blocked !== '' && <p className="sc-state">{blocked}</p>}
      {isQuestion ? (
        <form
          className="ai-answer"
          onSubmit={(event) => {
            event.preventDefault();
            if (answerText.trim() !== '') onAnswer({ rpc: 'answer', answer: answerText.trim() });
          }}
        >
          {interrupt.choices.length > 0 && (
            <div className="ai-choices">
              {interrupt.choices.map((choice) => (
                <button
                  className="v2-ghost"
                  data-on={answerText === choice ? 'true' : undefined}
                  disabled={disabled}
                  key={choice}
                  onClick={() => setAnswerText(choice)}
                  type="button"
                >
                  {choice}
                </button>
              ))}
            </div>
          )}
          {interrupt.allowsFreeText && (
            <label className="ai-field">
              <span className="sc-fname">{AGENT_INBOX_COPY.answerLabel}</span>
              <input disabled={disabled} value={answerText} onChange={(event) => setAnswerText(event.target.value)} />
            </label>
          )}
          <div className="ai-buttons">
            <button className="v2-primary" disabled={disabled || answerText.trim() === ''} type="submit">
              {busy ? AGENT_INBOX_COPY.working : AGENT_INBOX_COPY.answer}
            </button>
            <button
              className="v2-ghost"
              disabled={disabled || !canDecline}
              onClick={() => onAnswer({ rpc: 'resolve', approved: false, rejectionMessage: '' })}
              type="button"
            >
              {AGENT_INBOX_COPY.decline}
            </button>
          </div>
        </form>
      ) : isRejecting ? (
        <form
          className="ai-answer"
          onSubmit={(event) => {
            event.preventDefault();
            onAnswer({ rpc: 'resolve', approved: false, rejectionMessage: reason });
          }}
        >
          <label className="ai-field">
            <span className="sc-fname">{AGENT_INBOX_COPY.rejectReason}</span>
            <textarea disabled={disabled} rows={2} value={reason} onChange={(event) => setReason(event.target.value)} />
          </label>
          <div className="ai-buttons">
            <button className="v2-primary" disabled={disabled} type="submit">
              {busy ? AGENT_INBOX_COPY.working : AGENT_INBOX_COPY.reject}
            </button>
            <button className="v2-ghost" onClick={() => setRejecting(false)} type="button">{AGENT_INBOX_COPY.cancel}</button>
          </div>
        </form>
      ) : (
        <div className="ai-buttons">
          <button
            className="v2-primary"
            disabled={disabled}
            onClick={() => onAnswer({ rpc: 'resolve', approved: true, rejectionMessage: '' })}
            type="button"
          >
            {busy ? AGENT_INBOX_COPY.working : AGENT_INBOX_COPY.approve}
          </button>
          <button className="v2-ghost" disabled={disabled} onClick={() => setRejecting(true)} type="button">
            {AGENT_INBOX_COPY.reject}
          </button>
        </div>
      )}
    </article>
  );
}
