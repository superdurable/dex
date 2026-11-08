// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import { describe, expect, it, vi } from 'vitest';
import { DexAPIError } from '@/lib/http';
import {
  actionBlockedReason,
  classifyActionFailure,
  invokeSessionAction,
  LOCK_CONFLICT_RETRY_DELAYS_MS,
  resolveInterruptInput,
  type SessionControlContext,
} from './sessionActions';

const resolveAction: FlowV2Action = {
  rpcName: 'resolve_interrupt',
  label: 'Resolve interrupt',
  requiredPermission: 'support-agent.operate',
  input: { kind: 'object', fields: [] },
};

const openContext: SessionControlContext = {
  isFlowOpen: true,
  workQueuePermissions: ['support-agent.operate'],
};

function respond(body: unknown, status = 200) {
  return vi.fn(async () => new Response(JSON.stringify(body), { status }));
}

describe('invokeSessionAction', () => {
  const request = {
    flowType: 'SupportAgent', flowId: 'ses-1', action: resolveAction,
    input: resolveInterruptInput('c1', true), workQueuePermissions: ['support-agent.operate'], definitionRevision: 'rev',
  };

  it('posts one call with its own input to the Action route', async () => {
    const fetcher = respond({ invoked: true, output: true });
    await expect(invokeSessionAction(request, fetcher)).resolves.toEqual({ kind: 'done' });
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v2/actions');
    expect(JSON.parse(String(init.body))).toMatchObject({
      flowType: 'SupportAgent', flowId: 'ses-1', rpcName: 'resolve_interrupt',
      input: { interrupt_id: 'c1', approved: true },
    });
  });

  it('reads false from resolve_interrupt as already handled', async () => {
    await expect(invokeSessionAction(request, respond({ invoked: true, output: false }))).resolves.toEqual({ kind: 'already_handled' });
  });

  const lockConflict = () => new Response(
    JSON.stringify({ error: 'RpcLockConflictError: PendingInterrupts is locked', grpcCode: 10 }),
    { status: 502 },
  );

  it('retries a lock conflict, which committed nothing, until the RPC answers', async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(lockConflict())
      .mockResolvedValueOnce(lockConflict())
      .mockResolvedValueOnce(new Response(JSON.stringify({ invoked: true, output: true }), { status: 200 }));
    await expect(invokeSessionAction(request, fetcher, [0, 0, 0])).resolves.toEqual({ kind: 'done' });
    expect(fetcher).toHaveBeenCalledTimes(3);
  });

  it('says the session is busy after the retries, without calling it handled', async () => {
    const fetcher = vi.fn(async () => lockConflict());
    await expect(invokeSessionAction(request, fetcher, [0, 0, 0])).resolves.toEqual({
      kind: 'failed', message: 'The session is busy. Try again.',
    });
    expect(fetcher).toHaveBeenCalledTimes(4);
  });

  it('waits 200, 500, then 1000 ms between lock conflict retries', () => {
    expect(LOCK_CONFLICT_RETRY_DELAYS_MS).toEqual([200, 500, 1000]);
  });

  it('reports false from a steer as refused, not as handled', async () => {
    const steer = { ...request, action: { ...resolveAction, rpcName: 'steer_user_message' }, input: { text: 'x' } };
    await expect(invokeSessionAction(steer, respond({ invoked: true, output: false }))).resolves.toMatchObject({ kind: 'refused' });
  });

  it('says no Worker is connected when the RPC dispatch finds none', async () => {
    const fetcher = respond({ error: 'resolve_interrupt failed', grpcCode: 14 }, 502);
    await expect(invokeSessionAction(request, fetcher)).resolves.toEqual({
      kind: 'failed',
      message: 'No Worker is connected to run this. Try again when Workers are back.',
    });
  });

  it('keeps other failures as failures', () => {
    expect(classifyActionFailure(new DexAPIError('worker RPC deadline exceeded', 408, 4)))
      .toEqual({ kind: 'failed', message: 'worker RPC deadline exceeded' });
  });
});

describe('resolveInterruptInput', () => {
  it('sends a reject reason only when there is one', () => {
    expect(resolveInterruptInput('c1', false, '  too large  ')).toEqual({ interrupt_id: 'c1', approved: false, rejection_message: 'too large' });
    expect(resolveInterruptInput('c1', false, ' ')).toEqual({ interrupt_id: 'c1', approved: false });
    expect(resolveInterruptInput('c1', true, 'ignored')).toEqual({ interrupt_id: 'c1', approved: true });
  });
});

describe('actionBlockedReason', () => {
  it('allows an Action that can run', () => {
    expect(actionBlockedReason('resolve_interrupt', resolveAction, openContext)).toBeNull();
  });

  it('says why an Action cannot run', () => {
    expect(actionBlockedReason('cancel_turn', undefined, openContext)).toBe("This Flow's definition declares no cancel_turn Action.");
    expect(actionBlockedReason('resolve_interrupt', resolveAction, { ...openContext, isFlowOpen: false })).toBe('The session is closed.');
    expect(actionBlockedReason('resolve_interrupt', resolveAction, { ...openContext, workQueuePermissions: [] }))
      .toBe('Choose Working as support-agent.operate to act.');
  });

});
