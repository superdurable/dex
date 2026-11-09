// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApprovalCard } from './ApprovalCard';
import { SESSION_COPY } from './copy';
import type { ToolCallView } from './sessionModel';
import type { AgentInterruptRecord } from './sessionSchema';
import { ToolCallCard, toolCallDetail } from './ToolCallCard';

afterEach(cleanup);

const interrupt: AgentInterruptRecord = {
  interruptId: 'c1', kind: 'tool_approval', toolCallId: 'c1', toolName: 'create_refund', arguments: '{}',
  createdAt: null, deadline: null, question: null, choices: [], allowsFreeText: true,
};

const call = (overrides: Partial<ToolCallView>): ToolCallView => ({
  toolCallId: 'c1', toolName: 'create_refund', turnId: 'turn-1', argumentsText: '{"amount":24000}',
  effect: 'non_idempotent_write', requiresApproval: true, state: 'done', source: 'live', ...overrides,
});

describe('ApprovalCard', () => {
  it('approves and reloads', async () => {
    const onResolve = vi.fn(async () => ({ kind: 'done' as const }));
    const onSettled = vi.fn();
    render(<ApprovalCard blockedReason={null} interrupt={interrupt} onResolve={onResolve} onSettled={onSettled} />);
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(onSettled).toHaveBeenCalledWith('done'));
    expect(onResolve).toHaveBeenCalledWith(true, '');
  });

  it('rejects with a reason for the model', async () => {
    const onResolve = vi.fn(async () => ({ kind: 'done' as const }));
    render(<ApprovalCard blockedReason={null} interrupt={interrupt} onResolve={onResolve} onSettled={() => undefined} />);
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    fireEvent.change(screen.getByLabelText('Tell the agent why (optional)'), { target: { value: 'Too large' } });
    fireEvent.click(screen.getByRole('button', { name: 'Reject' }));
    await waitFor(() => expect(onResolve).toHaveBeenCalledWith(false, 'Too large'));
  });

  it('says already handled when someone else acted, and reloads', async () => {
    const onSettled = vi.fn();
    render(<ApprovalCard
      blockedReason={null}
      interrupt={interrupt}
      onResolve={async () => ({ kind: 'already_handled' })}
      onSettled={onSettled}
    />);
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    expect((await screen.findByRole('status')).textContent).toMatch(/Already handled/);
    expect(onSettled).toHaveBeenCalledWith('already_handled');
  });

  it('keeps the call answerable when the session stayed busy', async () => {
    const onSettled = vi.fn();
    render(<ApprovalCard
      blockedReason={null}
      interrupt={interrupt}
      onResolve={async () => ({ kind: 'failed', message: SESSION_COPY.busy })}
      onSettled={onSettled}
    />);
    fireEvent.click(screen.getByRole('button', { name: 'Approve' }));
    expect((await screen.findByRole('status')).textContent).toBe('The session is busy. Try again.');
    expect(onSettled).not.toHaveBeenCalled();
    expect((screen.getByRole('button', { name: 'Approve' }) as HTMLButtonElement).disabled).toBe(false);
  });

  it('disables both decisions with the reason', () => {
    render(<ApprovalCard blockedReason="The session is closed." interrupt={interrupt} onResolve={vi.fn()} onSettled={vi.fn()} />);
    expect((screen.getByRole('button', { name: 'Approve' }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: 'Reject' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText('The session is closed.')).toBeTruthy();
  });

  it('shows the deadline', () => {
    const now = Date.parse('2026-11-07T22:00:00Z');
    render(<ApprovalCard
      blockedReason={null}
      interrupt={{ ...interrupt, deadline: '2026-11-07T22:42:00Z' }}
      now={now}
      onResolve={vi.fn()}
      onSettled={vi.fn()}
    />);
    expect(screen.getByText(/Rejected automatically at .* \(in 42m 0s\)\./)).toBeTruthy();
  });
});

describe('ToolCallCard', () => {
  it('names the tool, effect class, and state', () => {
    render(<ToolCallCard call={call({ state: 'awaiting_approval' })} />);
    expect(screen.getByRole('article', { name: 'create_refund waiting for approval' })).toBeTruthy();
    expect(screen.getByText('non-idempotent write')).toBeTruthy();
  });

  it('offers to tell the agent about an unknown outcome', () => {
    const onTellAgent = vi.fn();
    render(<ToolCallCard call={call({ state: 'outcome_unknown' })} onTellAgent={onTellAgent} />);
    expect(screen.getByText(/It may have run/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Tell the agent' }));
    expect(onTellAgent).toHaveBeenCalled();
  });

  it('says the effect class is not kept for a call read from message text', () => {
    render(<ToolCallCard call={call({ state: 'returned', effect: null, source: 'messages' })} />);
    expect(screen.getByText('effect not kept')).toBeTruthy();
    expect(screen.getByText('From message text only.')).toBeTruthy();
  });
});

describe('toolCallDetail', () => {
  const now = Date.parse('2026-11-07T22:10:00Z');
  it('reads queued, running, and skipped differently', () => {
    expect(toolCallDetail(call({ state: 'queued', stepType: 'CallTool', pendingReason: 'worker', since: '2026-11-07T22:00:00Z', attempt: 1 }), now))
      .toBe('Approved. Waiting for a Worker for 10m 0s. It has not run.');
    expect(toolCallDetail(call({ state: 'running', attempt: 2, maxAttempts: 3, requiresApproval: false }), now))
      .toBe('A Worker is running it. Attempt 2 of 3.');
    expect(toolCallDetail(call({ state: 'skipped', requiresApproval: false }), now)).toBe('It did not run.');
    expect(toolCallDetail(call({ state: 'rejected', rejectionMessage: 'no' }), now)).toBe('Rejected. The agent was told: “no”');
  });
});
