// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState, type ReactNode } from 'react';
import type { SettledOutcome } from '../agents/ApprovalCard';
import { SESSION_COPY } from '../agents/copy';
import { InterruptControls } from '../agents/InterruptControls';
import type { AgentSessionSchema } from '../agents/sessionSchema';
import { ToolCallCard } from '../agents/ToolCallCard';
import { useAgentSession } from '../agents/useAgentSession';
import { AGENT_INBOX_COPY } from './agentCopy';
import { isOpenFlowStatusCode } from './liveness';

/**
 * The Work Queue side panel of one session: its waiting calls with their controls, and the last
 * message. The cards and Actions are the Session tab's; only the layout is the inbox's own.
 */
export function AgentSessionPanel({
  flowType,
  flowId,
  schema,
  workQueuePermissions,
  footer,
  onSettled,
}: {
  flowType: string;
  flowId: string;
  schema: AgentSessionSchema;
  workQueuePermissions: readonly string[];
  footer: ReactNode;
  /** A call was answered here or elsewhere, so the list re-reads this session. */
  onSettled: () => void;
}) {
  const [reloadKey, setReloadKey] = useState(0);
  const { held } = useAgentSession(flowId, reloadKey);
  /** Kept outside the card: the reload that follows removes the card's own controls. */
  const [handledElsewhere, setHandledElsewhere] = useState<ReadonlySet<string>>(new Set());
  const session = held.value;
  const now = Date.now();

  const settle = (toolCallId: string) => (outcome: SettledOutcome) => {
    if (outcome === 'already_handled') setHandledElsewhere((current) => new Set(current).add(toolCallId));
    setReloadKey((key) => key + 1);
    onSettled();
  };

  const waiting = session?.view.waitingCalls ?? [];
  const waitingIDs = new Set(waiting.map((call) => call.toolCallId));
  const handledCalls = session?.view.items
    .flatMap((item) => (item.kind === 'tool' && handledElsewhere.has(item.call.toolCallId) && !waitingIDs.has(item.call.toolCallId)
      ? [item.call]
      : [])) ?? [];
  const lastMessage = [...(session?.view.items ?? [])].reverse()
    .find((item) => item.kind === 'message' && (item.message.text ?? '').trim() !== '');
  const controlContext = {
    isFlowOpen: session ? isOpenFlowStatusCode(session.summary.flowStatusCode) : false,
    workQueuePermissions,
  };

  return (
    <div className="v2-case sc ai-panel">
      <div className="sc-head">
        <span className="sc-title t-mono">{flowId}</span>
        <span className="sc-status">{flowType}</span>
        {footer}
      </div>
      {!session && (held.liveness === 'unreachable'
        ? <p className="v2-error" role="alert">{SESSION_COPY.readFailed(held.reason ?? '')}</p>
        : <p className="sc-state">{SESSION_COPY.loading}</p>)}
      {held.liveness === 'stale' && <p className="sc-why" role="status">{SESSION_COPY.stale} {held.reason}</p>}
      {session && (
        <>
          <div className="sc-block">
            <div className="sc-blockhead">{AGENT_INBOX_COPY.pendingCalls(waiting.length)}</div>
            {waiting.length === 0 && <p className="sc-state">{AGENT_INBOX_COPY.nothingPending}</p>}
            {[...waiting, ...handledCalls].map((call) => (
              <ToolCallCard call={call} key={call.toolCallId} now={now}>
                <InterruptControls
                  call={call}
                  controlContext={controlContext}
                  flowId={flowId}
                  flowType={flowType}
                  isHandledElsewhere={handledElsewhere.has(call.toolCallId)}
                  now={now}
                  schema={schema}
                  onSettled={settle(call.toolCallId)}
                />
              </ToolCallCard>
            ))}
          </div>
          <div className="sc-block">
            <div className="sc-blockhead">{AGENT_INBOX_COPY.lastMessage}</div>
            {lastMessage?.kind === 'message' ? (
              <blockquote className="ai-message">
                <span className="ai-role">
                  {lastMessage.message.role === 'user' ? SESSION_COPY.user : SESSION_COPY.assistant}
                </span>
                <span>{lastMessage.message.text}</span>
              </blockquote>
            ) : (
              <p className="sc-state">{AGENT_INBOX_COPY.noMessage}</p>
            )}
          </div>
        </>
      )}
    </div>
  );
}
