// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import { flowIdPrefixQuery, linkedRunPathOf } from '../../components/frame/JumpTo';
import { OperateMenu } from './OperateMenu';
import { runFailureOf, runStateOf } from './RunFailureLine';

afterEach(cleanup);

function renderOperate(isRunning: boolean) {
  const onStop = vi.fn();
  const onTimeTravel = vi.fn();
  render(<MemoryRouter><OperateMenu isRunning={isRunning} onStop={onStop} onTimeTravel={onTimeTravel} /></MemoryRouter>);
  return { onStop, onTimeTravel };
}

describe('Operate', () => {
  it('leads with Stop while the run runs and keeps Time travel one menu away', () => {
    const { onStop, onTimeTravel } = renderOperate(true);
    fireEvent.click(screen.getByRole('button', { name: 'Stop…' }));
    expect(onStop).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole('button', { name: 'More run actions' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /^Time travel/ }));
    expect(onTimeTravel).toHaveBeenCalledOnce();
  });

  it('offers only Time travel once the run ended', () => {
    const { onTimeTravel } = renderOperate(false);
    expect(screen.queryByRole('button', { name: 'Stop…' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'More run actions' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Time travel…' }));
    expect(onTimeTravel).toHaveBeenCalledOnce();
  });
});

function event(eventId: number, type: FlowHistoryEvent['type'], payload: Record<string, unknown> = {}): FlowHistoryEvent {
  return { eventId, eventTime: null, type, payload };
}

describe('run failure line', () => {
  it('names the closing error and points at the last failed Step event', () => {
    expect(runFailureOf([
      event(5, 'StepExecuteCompleted'),
      event(6, 'StepExecuteFailed'),
      event(7, 'FlowClosed', { errorMessage: ' card declined ' }),
    ])).toEqual({ message: 'card declined', eventId: 6 });
  });

  it('points at FlowClosed when no Step failed, and says nothing without an error', () => {
    expect(runFailureOf([event(3, 'FlowClosed', { errorMessage: 'timed out' })])).toEqual({ message: 'timed out', eventId: 3 });
    expect(runFailureOf([event(3, 'FlowClosed', {})])).toBeNull();
    expect(runFailureOf([event(3, 'StepExecuteFailed')])).toBeNull();
  });
});

describe('run state line', () => {
  it('names the newest event and the newest Step, whatever order events arrive in', () => {
    const events = [
      event(9, 'StepWaitForCompleted', { context: { stepType: 'AwaitNextWindow' } }),
      event(4, 'StepExecuteCompleted', { context: { stepType: 'SyncBatch' } }),
    ];
    expect(runStateOf(events, true)).toEqual({ isOpen: true, title: 'Waiting for conditions', stepType: 'AwaitNextWindow', eventId: 9 });
    expect(runStateOf([], false)).toBeNull();
  });
});

describe('jump to', () => {
  it('opens a pasted run link as that run and searches anything else by Flow ID prefix', () => {
    expect(linkedRunPathOf('http://127.0.0.1:8802/v1/flows/order-1/run-2?view=timeline')).toBe('/v1/flows/order-1/run-2');
    expect(linkedRunPathOf('/dex/v1/flows/SubFlow%3Aa')).toBe('/v1/flows/SubFlow%3Aa');
    expect(linkedRunPathOf('order-1')).toBeNull();
    expect(flowIdPrefixQuery('order-"1')).toBe('WorkflowId STARTS_WITH "order-\\"1"');
  });
});
