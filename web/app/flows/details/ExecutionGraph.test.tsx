// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StepGraphNode } from '@/lib/types';
import { StepNodeLabel } from './ExecutionGraph';

afterEach(cleanup);

const completedNode: StepGraphNode = {
  id: 'ChargeCard-1',
  label: 'ChargeCard',
  kind: 'step',
  status: 'Completed',
  execute: { eventId: 3, eventTime: null, type: 'StepExecuteCompleted', payload: {} },
};

describe('Execution graph Step node', () => {
  it('shows the Step status as visible text beside its icon', () => {
    render(<StepNodeLabel node={completedNode} onSelect={vi.fn()} />);
    const status = screen.getAllByText('Completed')[0];
    expect(status.closest('.graph-step-meta')).not.toBeNull();
    expect(status.className).not.toContain('sr-only');
    expect(screen.getByText('ChargeCard-1')).toBeTruthy();
  });

  it('names a focused method by its method and status', () => {
    render(<StepNodeLabel node={completedNode} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute Completed' });
    execute.focus();
    expect(document.activeElement).toBe(execute);
    expect(execute.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
  });
});
