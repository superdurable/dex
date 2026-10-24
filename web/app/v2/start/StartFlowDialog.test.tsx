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
import { StartFlowDialog } from './StartFlowDialog';
import { startWorkerTargetOptions } from './workerTarget';

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

describe('Start Flow Worker target selection', () => {
  it('clears a health-check bypass when switching away from and back to an address', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ healthy: false, warning: 'Worker unavailable' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    renderDialog(false);

    fireEvent.change(screen.getByLabelText('Flow ID'), { target: { value: 'new-flow' } });
    fireEvent.change(screen.getByPlaceholderText('127.0.0.1:8803'), { target: { value: 'worker:9000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Check Worker' }));
    await screen.findByText('Worker unavailable');
    fireEvent.click(screen.getByRole('checkbox', { name: /Bypass worker health check/ }));

    fireEvent.click(screen.getByRole('radio', { name: 'Pull Worker' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Worker address' }));
    const previousRequestCount = fetchMock.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Start Flow' }));

    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(previousRequestCount));
    expect(fetchMock.mock.calls.slice(previousRequestCount).map(([url]) => url)).toEqual(['/api/v2/worker-health']);
  });

  it('opens in pull mode when configured and submits without an address check', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ flowId: 'new-flow' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const onStarted = vi.fn();
    renderDialog(true, onStarted);

    expect((screen.getByRole('radio', { name: 'Pull Worker' }) as HTMLInputElement).checked).toBe(true);
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
});

describe('Start Flow on server-zd', () => {
  it('derives a pull-only target from the catalog engine', () => {
    expect(startWorkerTargetOptions({ serverEngine: 'zd' })).toEqual({
      isDefaultPull: true, isAddressWorkerTargetSupported: false,
    });
    expect(startWorkerTargetOptions({ serverEngine: 'temporal' })).toEqual({
      isDefaultPull: false, isAddressWorkerTargetSupported: true,
    });
    expect(startWorkerTargetOptions({ serverEngine: 'temporal', startFlowWorkerTargetPull: true })).toEqual({
      isDefaultPull: true, isAddressWorkerTargetSupported: true,
    });
    expect(startWorkerTargetOptions({})).toEqual({
      isDefaultPull: false, isAddressWorkerTargetSupported: true,
    });
  });

  it('hides the address option and starts with a pull target', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(JSON.stringify({ flowId: 'zd-flow' }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const onStarted = vi.fn();
    renderDialog(false, onStarted, false);

    expect(screen.queryByRole('radio', { name: 'Worker address' })).toBeNull();
    expect((screen.getByRole('radio', { name: 'Pull Worker' }) as HTMLInputElement).checked).toBe(true);
    expect(screen.queryByPlaceholderText('127.0.0.1:8803')).toBeNull();
    fireEvent.change(screen.getByLabelText('Flow ID'), { target: { value: 'zd-flow' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Flow' }));

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith('zd-flow'));
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual(['/api/v2/start']);
    expect(JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string)).toEqual({
      flowType: 'ExampleFlow', flowId: 'zd-flow', workerTargetPull: true, input: null,
    });
  });
});

function renderDialog(isDefaultPull: boolean, onStarted = vi.fn(), isAddressWorkerTargetSupported = true) {
  render(<StartFlowDialog
    definition={definition}
    definitionRevision="sha256:test"
    flowType="ExampleFlow"
    isDefaultPull={isDefaultPull}
    isAddressWorkerTargetSupported={isAddressWorkerTargetSupported}
    onClose={() => undefined}
    onDefinitionChanged={() => false}
    onStarted={onStarted}
  />);
}
