// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { FlowRouting, PullRouting, flowRoutingData } from './PullRouting';

describe('pull routing', () => {
  it('shows sticky and fallback routing with the dispatch sticky ID', () => {
    const sticky = renderToStaticMarkup(<PullRouting value={{ servedByAppWorkerId: 'worker-a', stickyAppWorkerId: 'worker-a', source: 1 }} />);
    expect(sticky).toContain('Sticky queue');
    expect(sticky).toContain('worker-a');
    const fallback = renderToStaticMarkup(<PullRouting value={{ servedByAppWorkerId: 'worker-b', stickyAppWorkerId: 'worker-a', source: 2 }} />);
    expect(fallback).toContain('Flow type queue');
    expect(fallback).toContain('>default<');
    expect(fallback).toContain('worker-b');
    expect(fallback).toContain('worker-a');
  });
  it('shows blanks for old routing', () => {
    const missing = renderToStaticMarkup(<PullRouting value={undefined} />);
    expect(missing.match(/—/g)).toHaveLength(4);
  });
  it('shows the named dispatch task queue and hides an empty routing', () => {
    const named = renderToStaticMarkup(<PullRouting value={{ servedByAppWorkerId: 'worker-a', source: 2, taskQueue: 'env-1' }} />);
    expect(named).toMatch(/<dt>Task queue<\/dt><dd[^>]*>env-1<\/dd>/);
    expect(renderToStaticMarkup(<PullRouting value={{}} hideEmpty />)).toBe('');
  });
  it('shows the queue rows without a Worker target, even when the start event has no target', () => {
    const markup = renderToStaticMarkup(<FlowRouting flowType="PaymentFlow" routing={{ workerTarget: {} }} />);
    expect(markup).not.toContain('Worker target');
    expect(markup).toMatch(/<dt>Task queue<\/dt><dd[^>]*>default<\/dd>/);
    expect(markup).not.toContain('PaymentFlow');
    expect(markup).toContain('>Sticky Worker ID<');
  });
  it('shows the Task queue and restores closed-run sticky data from Step history', () => {
    const routing = flowRoutingData(null, [{ eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: { flowConfig: { workerTarget: { isPullMode: true } } } }, { eventId: 2, eventTime: null, type: 'StepExecuteCompleted', payload: { context: { pullRouting: { servedByAppWorkerId: 'last-worker' } } } }]);
    const markup = renderToStaticMarkup(<FlowRouting flowType="PaymentFlow" routing={routing} />);
    expect(markup).toMatch(/<dt>Task queue<\/dt><dd[^>]*>default<\/dd>/);
    expect(markup).not.toContain('PaymentFlow');
    expect(markup).toContain('last-worker');
    expect(markup).not.toContain('Worker target');
    expect(markup).not.toContain('>Flow type<');
    const overview = renderToStaticMarkup(<FlowRouting flowType="PaymentFlow" routing={routing} showFlowType />);
    expect(overview.indexOf('>Flow type<')).toBeGreaterThan(-1);
    expect(overview.indexOf('>Flow type<')).toBeLessThan(overview.indexOf('>Task queue<'));
    expect(overview).toContain('PaymentFlow');
  });
  it('shows the Flow task queue from its Worker target', () => {
    const routing = flowRoutingData({ flowConfig: { workerTarget: { isPullMode: true, taskQueue: 'env-1' } } } as never, []);
    const markup = renderToStaticMarkup(<FlowRouting flowType="PaymentFlow" routing={routing} showFlowType />);
    expect(markup).toMatch(/<dt>Task queue<\/dt><dd[^>]*>env-1<\/dd>/);
    expect(markup).toMatch(/<dt>Flow type<\/dt><dd[^>]*>PaymentFlow<\/dd>/);
  });
  it('uses closed-run sticky state after an RPC and ignores failed method Workers', () => {
    const routing = flowRoutingData(null, [
      { eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: { flowConfig: { workerTarget: { isPullMode: true } } } },
      { eventId: 2, eventTime: null, type: 'StepExecuteFailed', payload: { context: { pullRouting: { servedByAppWorkerId: 'failed-worker' } } } },
      { eventId: 3, eventTime: null, type: 'FlowClosed', payload: { stickyAppWorkerId: 'rpc-worker' } },
    ]);
    expect(routing.stickyAppWorkerId).toBe('rpc-worker');
  });

});
