// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { FlowDefinitionGraph } from '@/lib/types';
import { FlowDefinitionGraphView } from './FlowDefinitionGraph';

describe('Flow Definition Graph renderer', () => {
  it('renders semantic shapes, icons, and failure diagnostics', () => {
    const markup = renderToStaticMarkup(<FlowDefinitionGraphView displayName="Example" graph={graph} />);

    expect(markup).toContain('definition-flow-frame');
    expect(markup).toContain('definition-step-frame');
    expect(markup).toContain('definition-wait-shape');
    expect(markup).toContain('WaitFor <span>anyOf</span>');
    expect(markup).toContain('📨 approval.for 2');
    expect(markup).toContain('⏱️ 1 hour timer');
    expect(markup).toContain('decision-forceFail');
    expect(markup).toContain('definition-channel-pipe');
    expect(markup).toContain('definition-attributes-box');
    expect(markup).toContain('definition-rpc-hexagon');
    expect(markup).toContain('definition-timeout-handler');
    expect(markup).toContain('TIMEOUT HANDLER');
    expect(markup).toContain('handleTimeout');
    expect(markup).toContain('definition-subflow-frame');
    expect(markup).toContain('Needs attention');
    expect(markup).toContain('aria-label="Expand Mini Map"');
    expect(markup).not.toContain('aria-label="Flow rendering Mini Map"');
    expect(markup).not.toContain('definition-edge-label');
    expect(markup).not.toContain('title="lines ');
  });

  it('keeps Streams hidden by default while resources remain visible', () => {
    const markup = renderToStaticMarkup(<FlowDefinitionGraphView displayName="Example" graph={graph} />);

    expect(markup).not.toContain('definition-stream-node');
    expect(markup).toContain('aria-pressed="true"');
    expect(markup).toContain('aria-pressed="false"');
  });
});

const graph: FlowDefinitionGraph = {
  schemaVersion: '1.0',
  valid: false,
  source: { language: 'python', path: 'example.py' },
  flow: { name: 'ExampleFlow', startStepId: 'step:start' },
  nodes: [
    { id: 'step:start', kind: 'step', name: 'StartStep', start: true },
    {
      id: 'wait:start:10:1', kind: 'wait', name: 'anyOf', parentId: 'step:start',
      wait: {
        type: 'anyOf',
        conditions: [
          { kind: 'channel', label: 'approval.for 2', resourceId: 'resource:channel:approval' },
          { kind: 'timer', label: '1 hour timer' },
          { kind: 'subflow', label: 'ChildFlow', subFlowId: 'subflow:ChildFlow:12' },
        ],
      },
    },
    {
      id: 'decision:step:start:20:1', kind: 'decision', name: 'forceFail', parentId: 'step:start',
      decision: { type: 'forceFail' },
    },
    { id: 'resource:channel:approval', kind: 'channel', name: 'approval', resource: { valueType: 'str' } },
    { id: 'resource:attribute:status', kind: 'attribute', name: 'status', resource: { valueType: 'str' } },
    { id: 'resource:stream:progress', kind: 'stream', name: 'progress', resource: { valueType: 'str' } },
    { id: 'rpc:approve', kind: 'rpc', name: 'approve' },
    { id: 'timeout_handler:ExampleFlow', kind: 'timeout_handler', name: 'handleTimeout' },
    {
      id: 'decision:timeout:30:1', kind: 'decision', name: 'gracefulComplete', parentId: 'timeout_handler:ExampleFlow', phase: 'timeout',
      decision: { type: 'gracefulComplete' },
    },
    { id: 'subflow:ChildFlow:12', kind: 'subflow', name: 'ChildFlow', external: true },
  ],
  edges: [
    { id: 'publish', kind: 'resource_publish', from: 'rpc:approve', to: 'resource:channel:approval' },
    { id: 'wait', kind: 'wait_condition', from: 'resource:channel:approval', to: 'wait:start:10:1' },
    { id: 'subflow', kind: 'subflow', from: 'wait:start:10:1', to: 'subflow:ChildFlow:12' },
  ],
  diagnostics: [{ severity: 'error', code: 'example', message: 'Needs work' }],
};
