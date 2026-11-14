// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { StructuredValue } from '@/app/components/StructuredValue';
import { PreferencesProvider } from '@/app/providers';
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

afterEach(cleanup);

describe('live Flow state failures', () => {
  it('shows the newest close result after a reset from one closed point to another', () => {
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Completed',
      flowStatusCode: 2,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: '2026-08-10T00:05:00Z',
    };
    const events: FlowHistoryEvent[] = [
      { eventId: 1, eventTime: null, type: 'FlowClosed', payload: { results: ['ancestor-result'] } },
      { eventId: 2, eventTime: null, type: 'TimeTravelFork', payload: {} },
      { eventId: 3, eventTime: null, type: 'FlowClosed', payload: { results: ['newest-result'] } },
    ];
    render(<FlowOverview summary={summary} events={events} state={null} />);
    expect(screen.getByRole('heading', { name: 'Close result' })).toBeTruthy();
    expect(screen.getByText(/newest-result/)).toBeTruthy();
    expect(screen.queryByText(/ancestor-result/)).toBeNull();
  });

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
      <FlowOverview summary={summary} events={events} state={null} />,
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
    // One active Step needs no Collapse all control.
    expect(markup).not.toContain('Collapse all');
    expect(markup).toContain('<details class="failure-stack" open="">');
  });

  it('shows why a queued Execute attempt waits', () => {
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
        executeInvocation: { phase: 1, pendingReason: 2, attempt: 1, pendingSinceTime: '2026-08-10T00:00:01Z' },
      }],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
    };

    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <FlowOverview summary={summary} events={[]} state={state} />
      </PreferencesProvider>,
    );

    expect(markup).toContain('Execute invocation');
    expect(markup).toContain('Waiting for a Worker that supports this Step type');
    expect(markup).toContain('Pending since');
    expect(markup).not.toContain('WaitFor invocation');
  });

  it('lists AttributeMap instances as compact rows under their map, in numeric order', () => {
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
      <FlowOverview summary={summary} events={[]} state={state} />,
    );

    expect(markup).toContain('status');
    expect(markup).toContain('ready');
    expect(markup).toContain('orders');
    expect(markup).toContain('first order');
    expect(markup).toContain('aria-label="AttributeMap orders"');
    expect(markup).toContain('7 instances');
    expect(markup).not.toContain('semantic-map-entry');
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
       
        onDeleteChannelMessage={async () => {}}
      />,
    );

    expect(markup).toContain('0198-message');
    expect(markup).toContain('Delete');
    expect(markup).not.toContain('Cadence');
    expect(markup).not.toContain('Temporal');
  });
});

describe('redesigned Overview', () => {
  const runningSummary: FlowSummary = {
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
  const emptyState: FlowState = {
    flowConfig: {},
    attributes: [],
    activeStepExecutions: [],
    queuedSteps: [],
    pendingChannelMessages: {},
    completedSteps: [],
  };

  it('never shows ignored Flow configuration fields', () => {
    const state: FlowState = {
      ...emptyState,
      flowConfig: {
        deleteExpiredHistoryObjects: true,
        stepDurability: 1,
      },
    };
    render(<FlowOverview summary={runningSummary} events={[]} state={state} />);
    expect(screen.getByText('Sync')).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/deleteExpiredHistoryObjects|delete expired/i);
  });

  it('puts live state on the left and config, identity and records on the right', () => {
    const { container } = render(<FlowOverview summary={runningSummary} events={[]} state={emptyState} />);
    const headings = (column: string) => [...container.querySelectorAll(`.${column} > .overview-section h2`)]
      .map((heading) => heading.firstChild?.textContent);
    expect(headings('run-overview-main')).toEqual(['Active steps', 'Attributes', 'Channels']);
    expect(headings('run-overview-side')).toEqual(['Configuration', 'Identity', 'Start input']);
    expect(container.querySelector('.run-overview-side > .overview-quiet-line')?.textContent)
      .toBe('No queued steps · No completed outputs');
    expect(screen.queryByRole('heading', { name: 'Details' })).toBeNull();
  });

  it('lists Completed outputs before Queued steps in the right column', () => {
    const state: FlowState = {
      ...emptyState,
      queuedSteps: [{ stepType: 'Next' }] as unknown as FlowState['queuedSteps'],
      completedSteps: [{ stepExecutionId: 'Done-1' }] as unknown as FlowState['completedSteps'],
    };
    const { container } = render(<FlowOverview summary={runningSummary} events={[]} state={state} />);
    const side = [...container.querySelectorAll('.run-overview-side > .overview-section h2')]
      .map((heading) => heading.firstChild?.textContent);
    expect(side).toEqual(['Configuration', 'Identity', 'Start input', 'Completed outputs', 'Queued steps']);
  });

  it('leaves the start-time Flow configuration out of Start input, since Configuration shows current values', () => {
    const started: FlowHistoryEvent = {
      eventId: 1,
      eventTime: '2026-08-10T00:00:00Z',
      type: 'FlowStartedOrContinued',
      payload: {
        initialStart: { startStepType: 'Intake', stepInput: { order: 7 } },
        flowConfig: { stepDurability: 2, activeStepSearchMode: 3 },
      },
    };
    render(<FlowOverview summary={runningSummary} events={[started]} state={emptyState} />);
    const startInput = screen.getByRole('heading', { name: 'Start input' }).closest('section')!;
    expect(startInput.textContent).toContain('Intake');
    expect(within(startInput).queryByText('Flow configuration')).toBeNull();
  });

  it('shows the first run ID only when it differs from the run ID', () => {
    const sameRun = renderToStaticMarkup(<FlowOverview summary={runningSummary} events={[]} state={emptyState} />);
    expect(sameRun).not.toContain('First run ID');
    const laterRun = renderToStaticMarkup(
      <FlowOverview summary={{ ...runningSummary, firstRunId: 'run-0' }} events={[]} state={emptyState} />,
    );
    expect(laterRun).toContain('First run ID');
    expect(laterRun).toContain('run-0');
  });

  it('truncates a long Attribute value behind Show all', () => {
    const longValue = 'x'.repeat(1200);
    render(
      <FlowOverview
        summary={runningSummary}
        events={[]}
        state={{ ...emptyState, attributes: [{ key: 'payload', value: longValue }] }}
      />,
    );
    expect(document.body.textContent).not.toContain(longValue);
    const showAll = screen.getByRole('button', { name: /Show all/ });
    expect(showAll.getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(showAll);
    expect(document.body.textContent).toContain(longValue);
    expect(screen.getByRole('button', { name: 'Show less' }).getAttribute('aria-expanded')).toBe('true');
  });

  it('collapses empty sections into one quiet line and keeps the write actions', () => {
    render(
      <FlowOverview summary={runningSummary} events={[]} state={emptyState} onOpenStateWrite={() => {}} />,
    );
    expect(screen.getByText('No queued steps · No completed outputs')).toBeTruthy();
    expect(screen.queryByRole('heading', { name: /Queued steps/ })).toBeNull();
    expect(screen.getByRole('button', { name: 'Set attribute' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Publish message' })).toBeTruthy();
    expect(screen.queryByText('Selected event')).toBeNull();
  });
});

describe('decoded Channel values and close failures', () => {
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
  const jsonValue = (value: unknown) => ({ objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } });

  // Message IDs differ per test because JsonView remembers each value's Details or Raw JSON choice.
  function renderPending(values: unknown[], idPrefix: string) {
    const state: FlowState = {
      flowConfig: {},
      attributes: [],
      activeStepExecutions: [],
      queuedSteps: [],
      pendingChannelMessages: {
        approvals: {
          messages: values.map((value, index) => ({ channelName: 'approvals', messageId: `${idPrefix}-${index}`, value })),
        },
      },
      completedSteps: [],
    };
    return render(<FlowOverview summary={summary} events={[]} state={state} />);
  }

  it('decodes Channel message values the way Attributes do', () => {
    renderPending([jsonValue(true), { stringValue: 'approved' }, jsonValue({ amount: 42 })], 'decode');
    const text = document.body.textContent ?? '';
    expect(text).not.toContain('objValue');
    expect(text).not.toContain('dHJ1ZQ==');
    expect(screen.getByText('true')).toBeTruthy();
    expect(screen.getByText('approved')).toBeTruthy();
    // A short flat object reads on one line.
    expect(screen.getByText('{"amount":42}')).toBeTruthy();
  });

  it('keeps the envelope in Raw JSON', () => {
    renderPending([jsonValue(true)], 'raw');
    fireEvent.click(screen.getAllByRole('button', { name: 'Raw JSON' })[0]);
    expect(document.body.textContent).toContain('dHJ1ZQ==');
  });

  it('truncates a long Channel string behind Show all', () => {
    const longText = 'y'.repeat(1200);
    renderPending([{ stringValue: longText }], 'long');
    expect(document.body.textContent).not.toContain(longText);
    fireEvent.click(screen.getByRole('button', { name: /Show all/ }));
    expect(document.body.textContent).toContain(longText);
  });

  it('truncates long primitive array items and object fields', () => {
    const longText = 'z'.repeat(1200);
    const arrayMarkup = renderToStaticMarkup(<StructuredValue value={[longText, 7]} />);
    expect(arrayMarkup).not.toContain(longText);
    expect(arrayMarkup).toContain('Show all');
    expect(arrayMarkup).toContain('>7<');
    const objectMarkup = renderToStaticMarkup(<StructuredValue value={{ note: longText }} />);
    expect(objectMarkup).not.toContain(longText);
    expect(objectMarkup).toContain('Show all');
  });

  function renderClosed(flowStatus: FlowSummary['flowStatus'], payload: Record<string, unknown>) {
    return renderToStaticMarkup(
      <FlowOverview
        summary={{ ...summary, flowStatus, flowStatusCode: flowStatus === 'Failed' ? 3 : 2 }}
        events={[{ eventId: 9, eventTime: null, type: 'FlowClosed', payload }]}
        state={null}
      />,
    );
  }

  it('shows a failure type without a message', () => {
    const markup = renderClosed('Failed', { errorType: 1 });
    expect(markup).toContain('Step decision failed the flow');
    expect(markup).not.toContain('closed without results');
  });

  it('shows a failure message with its type', () => {
    const markup = renderClosed('Failed', { errorType: 3, errorMessage: 'card declined' });
    expect(markup).toContain('card declined');
    expect(markup).toContain('Worker method failed');
  });

  it('says a completed run without results closed without results', () => {
    const markup = renderClosed('Completed', { results: [] });
    expect(markup).toContain('The run closed without results.');
    expect(markup).not.toContain('Close result');
  });
});

describe('nested arrays in decoded values', () => {
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
  const jsonValue = (value: unknown) => ({ objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } });
  const cases: Array<[string, unknown, string[]]> = [
    ['an array of arrays', [[11, 22]], ['11', '22']],
    ['an array of objects with arrays', [{ a: [33, 44] }], ['33', '44']],
    ['an object of nested arrays', { x: [[55], [66]] }, ['55', '66']],
  ];

  function stateWith(overrides: Partial<FlowState>): FlowState {
    return {
      flowConfig: {},
      attributes: [],
      activeStepExecutions: [],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
      ...overrides,
    };
  }

  it.each(cases)('keeps %s in a Channel message', (_name, value, numbers) => {
    const markup = renderToStaticMarkup(
      <FlowOverview
        summary={summary}
        events={[]}
        state={stateWith({
          pendingChannelMessages: {
            nested: { messages: [{ channelName: 'nested', messageId: `nested-${numbers[0]}`, value: jsonValue(value) }] },
          },
        })}
      />,
    );
    expect(markup).not.toContain('Empty object');
    for (const number of numbers) expect(markup).toContain(number);
  });

  it.each(cases)('keeps %s in an Attribute value', (_name, value, numbers) => {
    const markup = renderToStaticMarkup(
      <FlowOverview
        summary={summary}
        events={[]}
        state={stateWith({ attributes: [{ key: `nested-${numbers[0]}`, value: jsonValue(value) }] })}
      />,
    );
    expect(markup).not.toContain('Empty object');
    for (const number of numbers) expect(markup).toContain(number);
  });

  it('renders each inner array of an array of arrays as its own list', () => {
    const markup = renderToStaticMarkup(<StructuredValue value={jsonValue([[11, 22]])} />);
    expect(markup).toContain('>11<');
    expect(markup).toContain('>22<');
  });
});

describe('grouped Channel rows', () => {
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
  const jsonValue = (value: unknown) => ({ objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } });
  const longMessageId = '01a1bec0-7632-7009-a392-f5ea2af2a0cd';
  const state: FlowState = {
    flowConfig: {},
    attributes: [],
    activeStepExecutions: [],
    queuedSteps: [],
    pendingChannelMessages: {
      test: {
        messages: [
          { channelName: 'test', messageId: longMessageId, value: jsonValue(true) },
          { channelName: 'test', messageId: 'group-m-2', value: { intValue: 123 } },
        ],
      },
      'updates/2': { messages: [{ channelName: 'updates/2', messageId: 'group-m-3', value: jsonValue({ step: 2 }) }] },
    },
    completedSteps: [],
  };

  it('groups messages under each Channel with one view toggle and no duplicate list', () => {
    render(<FlowOverview summary={summary} events={[]} state={state} onDeleteChannelMessage={async () => {}} />);
    const test = screen.getByRole('region', { name: 'Channel test' });
    expect(test.textContent).toContain('2 messages');
    expect(test.textContent).toContain('true');
    expect(test.textContent).toContain('123');
    expect(test.textContent).not.toContain('objValue');
    expect(screen.getByRole('region', { name: 'Channel updates / 2' }).textContent).toContain('1 message');
    expect(screen.getAllByRole('group', { name: 'Channels view' })).toHaveLength(1);
    expect(screen.queryByText('All pending messages by Channel')).toBeNull();
    expect(screen.getByText('01a1bec0…a0cd').getAttribute('title')).toBe(longMessageId);
    expect(screen.getByRole('button', { name: `Copy message ID ${longMessageId}` })).toBeTruthy();
  });

  it('deletes one message through its icon button', () => {
    const onDelete = vi.fn(async () => {});
    render(<FlowOverview summary={summary} events={[]} state={state} onDeleteChannelMessage={onDelete} />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete message group-m-2 from test' }));
    expect(onDelete).toHaveBeenCalledWith('test', 'group-m-2');
  });

  it('switches the whole section to Raw JSON', () => {
    render(<FlowOverview summary={summary} events={[]} state={state} />);
    const toggle = screen.getByRole('group', { name: 'Channels view' });
    fireEvent.click(within(toggle).getByRole('button', { name: 'Raw JSON' }));
    expect(document.body.textContent).toContain('dHJ1ZQ==');
    expect(screen.queryByRole('region', { name: 'Channel test' })).toBeNull();
  });
});

describe('Active steps controls', () => {
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
  const activeStep = (id: string) => ({
    stepExecutionId: id,
    fromStepExecutionId: '',
    stepType: 'ReviewStep',
    phase: 'Waiting' as const,
    stepExecutionLocals: [],
    timers: [],
    waitForPullRouting: { servedByAppWorkerId: 'worker-a', stickyAppWorkerId: 'worker-a', source: 1 },
    executePullRouting: {},
  });
  const state = (steps: ReturnType<typeof activeStep>[]): FlowState => ({
    flowConfig: { workerTarget: { isPullMode: true } },
    attributes: [],
    activeStepExecutions: steps,
    queuedSteps: [],
    pendingChannelMessages: {},
    completedSteps: [],
  });

  it('toggles every active Step with a text button', () => {
    render(<FlowOverview summary={summary} events={[]} state={state([activeStep('Review-1'), activeStep('Review-2')])} />);
    const toggle = screen.getByRole('button', { name: /Collapse all/ });
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(toggle);
    expect(screen.getByRole('button', { name: /Expand all/ }).getAttribute('aria-pressed')).toBe('false');
    expect(document.querySelectorAll('details.active-step-card[open]')).toHaveLength(0);
  });

  it('hides routing groups and fields that hold no value', () => {
    const markup = renderToStaticMarkup(
      <FlowOverview summary={summary} events={[]} state={state([activeStep('Review-1')])} />,
    );
    const activeStepMarkup = markup.slice(markup.indexOf('<details class="active-step-card"'), markup.indexOf('</details>'));
    expect(activeStepMarkup).toContain('WaitFor routing');
    expect(activeStepMarkup).not.toContain('Execute routing');
    expect(activeStepMarkup).not.toContain('>From<');
    expect(activeStepMarkup).not.toContain('>—<');
  });
});

describe('Details and Raw JSON switches', () => {
  it('are toggle buttons, not tabs', () => {
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
    render(
      <FlowOverview
        summary={summary}
        events={[]}
        state={{
          flowConfig: {},
          attributes: [{ key: 'status', value: 'ready' }],
          activeStepExecutions: [],
          queuedSteps: [],
          pendingChannelMessages: {},
          completedSteps: [],
        }}
      />,
    );
    expect(screen.queryByRole('tab')).toBeNull();
    const group = screen.getByRole('group', { name: 'Attributes view' });
    const raw = within(group).getByRole('button', { name: 'Raw JSON' });
    expect(within(group).getByRole('button', { name: 'Details' }).getAttribute('aria-pressed')).toBe('true');
    fireEvent.click(raw);
    expect(raw.getAttribute('aria-pressed')).toBe('true');
  });
});
