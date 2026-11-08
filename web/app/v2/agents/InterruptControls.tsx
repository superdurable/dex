// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useWebCatalog } from '../WebCatalogProvider';
import { ApprovalCard, QuestionCard, type SettledOutcome } from './ApprovalCard';
import { SESSION_COPY } from './copy';
import {
  actionBlockedReason,
  invokeSessionAction,
  resolveInterruptInput,
  type SessionActionOutcome,
  type SessionControlContext,
} from './sessionActions';
import type { ToolCallView } from './sessionModel';
import { SESSION_RPC, type AgentSessionSchema } from './sessionSchema';

export type SessionActionInvoker = (
  action: AgentSessionSchema['resolveInterrupt'],
  input: Record<string, unknown>,
) => Promise<SessionActionOutcome>;

/** Invokes one session Action of this session; a missing Action fails without a request. */
export function useSessionActionInvoker(
  flowType: string,
  flowId: string,
  workQueuePermissions: readonly string[],
): SessionActionInvoker {
  const { catalog } = useWebCatalog();
  return (action, input) => {
    if (!action) return Promise.resolve<SessionActionOutcome>({ kind: 'failed', message: SESSION_COPY.closed });
    return invokeSessionAction({
      flowType,
      flowId,
      action,
      input,
      workQueuePermissions,
      definitionRevision: catalog?.definitionRevision ?? '',
    });
  };
}

/**
 * The approval or question controls of one waiting call, wired to the session's Actions.
 *
 * Shared by the Session tab and the Work Queue agent inbox.
 */
export function InterruptControls({
  flowType,
  flowId,
  call,
  schema,
  controlContext,
  isHandledElsewhere,
  onSettled,
  now,
}: {
  flowType: string;
  flowId: string;
  call: ToolCallView;
  schema: AgentSessionSchema;
  controlContext: SessionControlContext;
  /** The call left after another operator or a deadline answered it; the host keeps this across reloads. */
  isHandledElsewhere: boolean;
  onSettled: (outcome: SettledOutcome) => void;
  now: number;
}) {
  const invoke = useSessionActionInvoker(flowType, flowId, controlContext.workQueuePermissions);
  const interrupt = call.interrupt;
  if (!interrupt) {
    return isHandledElsewhere ? <p className="agc-note" role="status">{SESSION_COPY.alreadyHandled}</p> : null;
  }
  const resolveBlocked = actionBlockedReason(SESSION_RPC.resolveInterrupt, schema.resolveInterrupt, controlContext);
  if (interrupt.kind === 'question') {
    return (
      <QuestionCard
        answerBlockedReason={actionBlockedReason(SESSION_RPC.answerQuestion, schema.answerQuestion, controlContext)}
        declineBlockedReason={resolveBlocked}
        interrupt={interrupt}
        now={now}
        onAnswer={(answer) => invoke(schema.answerQuestion, { interrupt_id: interrupt.interruptId, answer })}
        onDecline={() => invoke(schema.resolveInterrupt, resolveInterruptInput(interrupt.interruptId, false))}
        onSettled={onSettled}
      />
    );
  }
  return (
    <ApprovalCard
      blockedReason={resolveBlocked}
      interrupt={interrupt}
      now={now}
      onResolve={(approved, reason) => invoke(
        schema.resolveInterrupt,
        resolveInterruptInput(interrupt.interruptId, approved, reason),
      )}
      onSettled={onSettled}
    />
  );
}
