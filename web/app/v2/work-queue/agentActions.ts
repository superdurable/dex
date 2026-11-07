// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import { DexAPIError, definitionRevisionHeaders, readResponseJSON } from '@/lib/http';
import { AGENT_INBOX_COPY } from './agentCopy';
import { isPastDeadline } from './agentInbox';
import type { AgentInterrupt, AgentSessionRead } from './agentSession';

/** An RPC that cannot take its Attribute locks fails with gRPC ABORTED. */
const GRPC_ABORTED = 10;

export type AgentAnswer =
  | { rpc: 'resolve'; approved: boolean; rejectionMessage: string }
  | { rpc: 'answer'; answer: string };

/** What the RPC said: true when it applied the answer, false when the interrupt was no longer pending. */
export type AgentActionResult = { state: 'applied' } | { state: 'not-applied' } | { state: 'lock-conflict' };

/** The Action input, keyed by the field names the session graph declares; null when one is missing. */
export function agentActionInput(
  action: FlowV2Action,
  interrupt: AgentInterrupt,
  answer: AgentAnswer,
): Record<string, unknown> | null {
  const input: Record<string, unknown> = answer.rpc === 'resolve'
    ? {
      interrupt_id: interrupt.interruptId,
      approved: answer.approved,
      rejection_message: answer.approved || answer.rejectionMessage.trim() === '' ? null : answer.rejectionMessage.trim(),
    }
    : { interrupt_id: interrupt.interruptId, answer: answer.answer };
  const declared = new Set((action.input.fields ?? []).map((field) => field.fieldName));
  return Object.keys(input).every((name) => declared.has(name)) ? input : null;
}

export async function invokeAgentAction(request: {
  flowType: string;
  flowId: string;
  rpcName: string;
  input: Record<string, unknown>;
  workQueuePermissions: readonly string[];
  definitionRevision: string;
}): Promise<AgentActionResult> {
  try {
    const response = await fetch('/api/v2/actions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...definitionRevisionHeaders(request.definitionRevision) },
      body: JSON.stringify({
        flowType: request.flowType,
        flowId: request.flowId,
        rpcName: request.rpcName,
        workQueuePermissions: request.workQueuePermissions,
        input: request.input,
        attributeSnapshot: {},
      }),
    });
    const result = await readResponseJSON<{ output: unknown }>(response);
    return result.output === true ? { state: 'applied' } : { state: 'not-applied' };
  } catch (error) {
    if (error instanceof DexAPIError && error.grpcCode === GRPC_ABORTED) return { state: 'lock-conflict' };
    throw error;
  }
}

/**
 * What to tell the operator, read back from committed state rather than from the RPC answer.
 *
 * Returns null when the answer applied, else the sentence that explains the state read back.
 */
export function agentActionNotice(
  result: AgentActionResult,
  interrupt: AgentInterrupt,
  readBack: AgentSessionRead,
  nowMs: number,
): { title: string; text: string } | null {
  if (result.state === 'applied') return null;
  const stillPending = readBack.interrupts.some((pending) => pending.interruptId === interrupt.interruptId);
  if (!readBack.isActive) return { title: AGENT_INBOX_COPY.notApplied, text: AGENT_INBOX_COPY.ended };
  if (stillPending) {
    return {
      title: AGENT_INBOX_COPY.notApplied,
      text: result.state === 'lock-conflict' ? AGENT_INBOX_COPY.stillWaitingAfterLock : AGENT_INBOX_COPY.notAccepted,
    };
  }
  return {
    title: AGENT_INBOX_COPY.alreadyHandled,
    text: isPastDeadline(interrupt, nowMs) ? AGENT_INBOX_COPY.deadlinePassed : AGENT_INBOX_COPY.alreadyHandledWhy,
  };
}
