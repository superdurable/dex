// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { StructuredValue } from '@/app/components/StructuredValue';
import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { FlowOverview } from './FlowOverview';

function expectStrongLabelsInOrder(markup: string, labels: string[]): void {
  let previousPosition = -1;
  for (const label of labels) {
    const position = markup.indexOf(`>${label}</strong>`);
    expect(position).toBeGreaterThan(previousPosition);
    previousPosition = position;
  }
}

describe('live Flow state failures', () => {
  it('hides an ancestor close result while the restored close is pending', () => {
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Running',
      flowStatusCode: 1,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: null,
    };
    const events: FlowHistoryEvent[] = [{
      eventId: 1,
      eventTime: null,
      type: 'FlowClosed',
      payload: { results: ['ancestor-result'] },
    }];
    const markup = renderToStaticMarkup(
      <FlowOverview summary={summary} events={events} state={null} selectedEvent={null} />,
    );
    expect(markup).not.toContain('Close result');
  });

  it('shows the Java Worker stack expanded with a named gRPC status', () => {
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Running',
      flowStatusCode: 1,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: null,
    };
    const state: FlowState = {
      flowConfig: {},
      attributes: [],
      activeStepExecutions: [{
        stepExecutionId: 'ChargeCard-1',
        fromStepExecutionId: '__start__',
        stepType: 'ChargeCard',
        phase: 'Active',
        stepExecutionLocals: [],
        timers: [],
        lastFailureInfo: {
          attempt: 2,
          backendError: 'FLOW_ERROR_TYPE_WORKER_API_FAIL',
          details: {
            originalWorkerErrorType: 'PaymentProcessorException',
            originalWorkerErrorDetail: 'processor unavailable',
            originalWorkerErrorStatus: 13,
            originalWorkerErrorStackTrace: 'java stack trace',
          },
        },
      }],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
    };

    const markup = renderToStaticMarkup(
      <FlowOverview
        summary={summary}
        events={[]}
        state={state}
        selectedEvent={null}
      />,
    );

    expect(markup).toContain('Last failure');
    expect(markup).not.toContain('Attempt');
    expect(markup).toContain('Error type');
    expect(markup).not.toContain('Backend error');
    expect(markup).toContain('Worker method failed');
    expect(markup).not.toContain('Retry state');
    expect(markup).not.toContain('Retry scheduled');
    expect(markup).toContain('INTERNAL (13)');
    expect(markup).toContain('java stack trace');
    expect(markup).toContain('<details class="active-step-card" open="">');
    expect(markup).toContain('Collapse all');
    expect(markup).toContain('<details class="failure-stack" open="">');
  });

  it('expands Attributes while keeping AttributeMap instance values collapsed', () => {
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Running',
      flowStatusCode: 1,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: null,
    };
    const state: FlowState = {
      flowConfig: {},
      attributes: [
        { key: 'status', value: 'ready' },
        { key: 'orders/9007199254740993', value: 'largest' },
        { key: 'orders/first%20order', value: 'pending' },
        { key: 'orders/11', value: 'eleven' },
        { key: 'orders/02', value: 'two-padded' },
        { key: 'orders/2', value: 'two' },
        { key: 'orders/9007199254740992', value: 'large' },
        { key: 'orders/text', value: 'complete' },
      ],
      activeStepExecutions: [],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
    };

    const markup = renderToStaticMarkup(
      <FlowOverview summary={summary} events={[]} state={state} selectedEvent={null} />,
    );

    expect(markup).toContain('status');
    expect(markup).toContain('ready');
    expect(markup).toContain('orders');
    expect(markup).toContain('first order');
    expect(markup).toContain('<details class="semantic-record semantic-map-entry">');
    expectStrongLabelsInOrder(markup, [
      '02',
      '2',
      '11',
      '9007199254740992',
      '9007199254740993',
      'first order',
      'text',
    ]);
  });

  it('shows ChannelMap instances before their queued messages', () => {
    const markup = renderToStaticMarkup(
      <StructuredValue
        persistenceKind="channels"
        value={{
          'updates/text': { messages: [{ channelName: 'updates/text', value: 'queued', messageId: 'm-text' }] },
          'updates/11': { messages: [{ channelName: 'updates/11', value: 'eleven', messageId: 'm-11' }] },
          'updates/2': { messages: [{ channelName: 'updates/2', value: 'two', messageId: 'm-2' }] },
        }}
      />,
    );

    expect(markup).toContain('updates');
    expect(markup).toContain('eleven');
    expect(markup).not.toContain('m-11');
    expect(markup).toContain('<details class="semantic-record semantic-map-entry">');
    expectStrongLabelsInOrder(markup, ['2', '11', 'text']);
  });

  it('shows pending Channel message IDs and deletion controls', () => {
    const state: FlowState = {
      flowConfig: {},
      attributes: [],
      activeStepExecutions: [],
      queuedSteps: [],
      pendingChannelMessages: {
        approvals: { messages: [{ channelName: 'approvals', messageId: '0198-message', value: 'approve' }] },
      },
      completedSteps: [],
    };
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Running',
      flowStatusCode: 1,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: null,
    };
    const markup = renderToStaticMarkup(
      <FlowOverview
        summary={summary}
        events={[]}
        state={state}
        selectedEvent={null}
        onDeleteChannelMessage={async () => {}}
      />,
    );

    expect(markup).toContain('0198-message');
    expect(markup).toContain('Delete');
    expect(markup).toContain('Cadence deletion is best effort');
  });
});
