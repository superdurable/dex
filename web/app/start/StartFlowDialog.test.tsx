// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StartFlowDialog, startTaskQueueError } from './StartFlowDialog';

const definition = { stepType: 'StartFlow', input: { kind: 'null' as const } };

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) { this.open = true; },
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Start Flow', () => {
  it('starts with a pull target without an address check', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ flowId: 'new-flow' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const onStarted = vi.fn();
    renderDialog(onStarted);

    expect(screen.queryByRole('radio')).toBeNull();
    expect(screen.queryByText('Worker target')).toBeNull();
    expect(screen.queryByPlaceholderText('127.0.0.1:8803')).toBeNull();
    fireEvent.change(screen.getByLabelText('Flow ID'), { target: { value: 'new-flow' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Flow' }));

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith('new-flow'));
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/v2/start']);
    const request = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(request.body as string)).toEqual({
      flowType: 'ExampleFlow', flowId: 'new-flow', workerTargetPull: true, input: null,
    });
  });

  it('starts on a named task queue', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ flowId: 'new-flow' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const onStarted = vi.fn();
    renderDialog(onStarted);

    fireEvent.change(screen.getByLabelText('Flow ID'), { target: { value: 'new-flow' } });
    fireEvent.change(screen.getByLabelText('Task queue'), { target: { value: ' env-1 ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Flow' }));

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith('new-flow'));
    const request = fetchMock.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(request.body as string)).toEqual({
      flowType: 'ExampleFlow', flowId: 'new-flow', workerTargetPull: true, workerTargetTaskQueue: 'env-1', input: null,
    });
  });

  it.each(['dex.internal', 'q'.repeat(64), 'env 1'])('rejects the task queue name %s', async (taskQueue) => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    renderDialog();

    fireEvent.change(screen.getByLabelText('Flow ID'), { target: { value: 'new-flow' } });
    fireEvent.change(screen.getByLabelText('Task queue'), { target: { value: taskQueue } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Flow' }));

    expect(await screen.findByRole('alert')).toHaveProperty('textContent', expect.stringContaining('Task queue names'));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each(['', 'dex.default', 'q'.repeat(63), 'env-1.A_b'])('accepts the task queue name %j', (taskQueue) => {
    expect(startTaskQueueError(taskQueue)).toBeUndefined();
  });
});

function renderDialog(onStarted = vi.fn()) {
  render(<StartFlowDialog
    definition={definition}
    definitionRevision="sha256:test"
    flowType="ExampleFlow"
    onClose={() => undefined}
    onDefinitionChanged={() => false}
    onStarted={onStarted}
  />);
}
