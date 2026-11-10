// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { FlowDefinitionEntry } from '@/lib/types';
import {
  EMPTY_RUN_QUERY,
  defaultOperator,
  emptyRunsText,
  filterableAttributes,
  hasAdvancedQuery,
  flowStatusQueryValue,
  isEmptyQuery,
  operatorsFor,
  searchedFlowTypes,
  statusOptions,
  stepTypeOptions,
  toFilterRows,
  toggleFlowType,
  toggleStepType,
  valueInputKind,
} from './runQuery';
import { SEARCH_COPY } from './searchCopy';

const indexed = (attributeKey: string, indexType: string, valueType: string) => ({
  attributeKey, indexKey: attributeKey, indexType, valueType, description: attributeKey,
}) as FlowV2Definition['indexedAttributes'][number];

const searchable = {
  indexedAttributes: [
    indexed('customer-email', 'fulltext', 'string'),
    indexed('refund-amount', 'double', 'double'),
    indexed('case-status', 'keyword', 'string'),
  ],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions: [],
} as FlowV2Definition;

/** The deterministic refund Flow: one keyword Attribute, nothing full-text. */
const keywordOnly = {
  ...searchable,
  indexedAttributes: [indexed('case-status', 'keyword', 'string')],
} as FlowV2Definition;

const NOW = Date.parse('2026-09-20T12:00:00.000Z');
const rows = (q: Partial<typeof EMPTY_RUN_QUERY>, definition = searchable) =>
  toFilterRows({ ...EMPTY_RUN_QUERY, ...q }, definition, NOW)
    .map((row) => `${row.field} ${row.operator} ${row.value}`);

describe('operatorsFor', () => {
  it('offers contains only where the index is full-text', () => {
    expect(operatorsFor('fulltext').map((o) => o.value)).toEqual(['contains', 'eq']);
    expect(operatorsFor('keyword').map((o) => o.value)).toEqual(['eq']);
  });

  it('offers ordering only where values order', () => {
    expect(operatorsFor('double').map((o) => o.value)).toEqual(['eq', 'gte', 'lte']);
    expect(operatorsFor('int').map((o) => o.value)).toEqual(['eq', 'gte', 'lte']);
    expect(operatorsFor('datetime').map((o) => o.value)).toEqual(['eq', 'gte', 'lte']);
  });

  it('never offers an operator the server rejects for that index', () => {
    expect(operatorsFor('keyword').map((o) => o.value)).not.toContain('contains');
    expect(operatorsFor('double').map((o) => o.value)).not.toContain('contains');
  });

  it('defaults to the comparison that index type is for', () => {
    expect(defaultOperator('fulltext')).toBe('contains');
    expect(defaultOperator('keyword')).toBe('eq');
    expect(defaultOperator('double')).toBe('eq');
  });
});

describe('valueInputKind', () => {
  it('types the value control from the Attribute, not the reader', () => {
    expect(valueInputKind('double')).toBe('number');
    expect(valueInputKind('int64')).toBe('number');
    expect(valueInputKind('datetime')).toBe('datetime');
    expect(valueInputKind('bool')).toBe('bool');
    expect(valueInputKind('string')).toBe('text');
    expect(valueInputKind(undefined)).toBe('text');
  });
});

describe('filterableAttributes', () => {
  it('offers every declared Indexed Attribute, full-text included', () => {
    expect(filterableAttributes(searchable).map((a) => a.attributeKey))
      .toEqual(['customer-email', 'refund-amount', 'case-status']);
  });
});

describe('toFilterRows', () => {
  it('compiles status to an exact execution-status filter', () => {
    expect(rows({ status: 'Running' })).toEqual(['executionStatus eq Running']);
  });

  it('sends the query value, never the display label, for a timed-out status', () => {
    expect(rows({ status: 'TimedOut' })).toEqual(['executionStatus eq TimedOut']);
    expect(rows({ status: 'Timed out' })).toEqual(['executionStatus eq TimedOut']);
  });

  it('compiles a relative window to an absolute start time', () => {
    expect(rows({ since: '24h' })).toEqual(['startTime gte 2026-09-19T12:00:00.000Z']);
  });

  it('looks a Flow up by exact Flow ID', () => {
    expect(rows({ flowId: ' adv-1 ' })).toEqual(['flowId eq adv-1']);
  });

  it('carries the chosen operator through', () => {
    expect(rows({ attributeKey: 'refund-amount', attributeOperator: 'gte', attributeValue: '450' }))
      .toEqual(['refund-amount gte 450']);
    expect(rows({ attributeKey: 'customer-email', attributeOperator: 'contains', attributeValue: 'acme' }))
      .toEqual(['customer-email contains acme']);
  });

  it('substitutes the default when the operator is illegal for the index', () => {
    expect(rows({ attributeKey: 'case-status', attributeOperator: 'contains', attributeValue: 'open' }))
      .toEqual(['case-status eq open']);
  });

  it('ignores an attribute with no value, and a value with no attribute', () => {
    expect(rows({ attributeKey: 'refund-amount', attributeValue: '  ' })).toEqual([]);
    expect(rows({ attributeValue: '450' })).toEqual([]);
  });

  it('ignores an attribute the Flow does not declare', () => {
    expect(rows({ attributeKey: 'refund-amount', attributeValue: '450' }, keywordOnly)).toEqual([]);
  });

  it('combines every control that is set', () => {
    expect(rows({
      status: 'Running',
      since: '1h',
      flowId: 'adv-1',
      attributeKey: 'refund-amount',
      attributeOperator: 'lte',
      attributeValue: '450',
    })).toEqual([
      'executionStatus eq Running',
      'startTime gte 2026-09-20T11:00:00.000Z',
      'flowId eq adv-1',
      'refund-amount lte 450',
    ]);
  });
});

describe('statusOptions', () => {
  it('is a real enum with nothing unspecified in it', () => {
    const labels = statusOptions().map((option) => option.label);
    expect(labels).toContain('Running');
    expect(labels).not.toContain('Unspecified');
  });

  it('pairs each display label with the value both Dex Servers accept', () => {
    expect(statusOptions()).toEqual([
      { value: 'Running', label: 'Running' },
      { value: 'Completed', label: 'Completed' },
      { value: 'Failed', label: 'Failed' },
      { value: 'TimedOut', label: 'Timed out' },
      { value: 'Terminated', label: 'Terminated' },
      { value: 'Canceled', label: 'Canceled' },
    ]);
  });

  it('maps labels to query values and keeps query values unchanged', () => {
    expect(flowStatusQueryValue('Timed out')).toBe('TimedOut');
    expect(flowStatusQueryValue('TimedOut')).toBe('TimedOut');
    expect(flowStatusQueryValue('constructor')).toBe('constructor');
  });
});

describe('query emptiness', () => {
  it('knows when nothing is narrowing the list', () => {
    expect(isEmptyQuery(EMPTY_RUN_QUERY)).toBe(true);
    expect(isEmptyQuery({ ...EMPTY_RUN_QUERY, status: 'Running' })).toBe(false);
    expect(isEmptyQuery({ ...EMPTY_RUN_QUERY, flowId: '  ' })).toBe(true);
  });

  it('opens the disclosure when something inside it is set', () => {
    expect(hasAdvancedQuery(EMPTY_RUN_QUERY)).toBe(false);
    expect(hasAdvancedQuery({ ...EMPTY_RUN_QUERY, status: 'Running' })).toBe(false);
    expect(hasAdvancedQuery({ ...EMPTY_RUN_QUERY, flowId: 'adv-1' })).toBe(true);
    expect(hasAdvancedQuery({
      ...EMPTY_RUN_QUERY, attributeKey: 'refund-amount', attributeValue: '450',
    })).toBe(true);
  });
});

describe('active Step filter', () => {
  it('sends the chosen Step types as exact values, so a comma stays part of a name', () => {
    const [row] = toFilterRows(
      { ...EMPTY_RUN_QUERY, activeStepTypes: ['AwaitApproval', 'Pick[a,b]'] },
      searchable,
      NOW,
    );

    expect(row).toMatchObject({
      field: 'activeStepTypes',
      operator: 'in',
      values: ['AwaitApproval', 'Pick[a,b]'],
    });
  });

  it('adds no filter when no Step is chosen', () => {
    expect(rows({})).toEqual([]);
  });

  it('counts as an active, advanced filter', () => {
    const query = { ...EMPTY_RUN_QUERY, activeStepTypes: ['AwaitApproval'] };

    expect(isEmptyQuery(query)).toBe(false);
    expect(hasAdvancedQuery(query)).toBe(true);
  });

  it('toggles a Step type in sorted order', () => {
    expect(toggleStepType(['B'], 'A')).toEqual(['A', 'B']);
    expect(toggleStepType(['A', 'B'], 'A')).toEqual(['B']);
  });
});

describe('Flow type scope', () => {
  it('searches the page Flow type until the reader widens it', () => {
    expect(searchedFlowTypes([], 'RefundFlow')).toEqual(['RefundFlow']);
    expect(searchedFlowTypes(['OrderFlow', 'RefundFlow'], 'RefundFlow')).toEqual(['OrderFlow', 'RefundFlow']);
    expect(searchedFlowTypes([], '')).toEqual([]);
  });

  it('ignores a widening that does not cover the page', () => {
    expect(searchedFlowTypes(['OrderFlow', 'PayFlow'], 'RefundFlow')).toEqual(['RefundFlow']);
  });

  it('widens from the page Flow type and collapses back to it', () => {
    const widened = toggleFlowType([], 'RefundFlow', 'OrderFlow');

    expect(widened).toEqual(['OrderFlow', 'RefundFlow']);
    expect(toggleFlowType(widened, 'RefundFlow', 'OrderFlow')).toEqual([]);
  });

  it('never drops the page Flow type', () => {
    expect(toggleFlowType(['OrderFlow', 'RefundFlow'], 'RefundFlow', 'RefundFlow'))
      .toEqual(['OrderFlow', 'RefundFlow']);
  });

  it('leaves an Attribute filter out of a search of several Flow types', () => {
    const query = {
      attributeKey: 'case-status',
      attributeOperator: 'eq' as const,
      attributeValue: 'review',
    };

    expect(rows(query)).toEqual(['case-status eq review']);
    expect(rows({ ...query, flowTypes: ['OrderFlow', 'RefundFlow'] })).toEqual([]);
  });
});

describe('stepTypeOptions', () => {
  const entry = (flowName: string, steps: string[], overrides: Partial<FlowDefinitionEntry> = {}) => ({
    flowName,
    schemaVersion: '2.0',
    valid: true,
    graph: {
      nodes: [
        ...steps.map((name) => ({ id: `step:${name}`, kind: 'step', name })),
        { id: 'channel:Approvals', kind: 'channel', name: 'Approvals' },
      ],
    },
    ...overrides,
  }) as unknown as FlowDefinitionEntry;

  it('lists the Steps of the searched Flow types only, once each', () => {
    const definitions = [
      entry('RefundFlow', ['AwaitApproval', 'Pay']),
      entry('OrderFlow', ['AwaitApproval', 'Ship']),
      entry('OtherFlow', ['Hidden']),
    ];

    expect(stepTypeOptions(definitions, ['RefundFlow', 'OrderFlow'], []))
      .toEqual(['AwaitApproval', 'Pay', 'Ship']);
  });

  it('skips definitions Version 2 cannot use', () => {
    const definitions = [
      entry('RefundFlow', ['Old'], { schemaVersion: '1.0' }),
      entry('RefundFlow', ['Broken'], { valid: false }),
    ];

    expect(stepTypeOptions(definitions, ['RefundFlow'], [])).toEqual([]);
  });

  it('keeps a chosen Step listed after its Flow type leaves the search', () => {
    expect(stepTypeOptions([entry('RefundFlow', ['Pay'])], ['RefundFlow'], ['Ship']))
      .toEqual(['Pay', 'Ship']);
  });
});

describe('emptyRunsText', () => {
  const stepFilter = { id: '1', field: 'activeStepTypes', operator: 'in', value: 'A', values: ['A'] };

  it('names the opt-in when the Step filter found nothing', () => {
    const text = emptyRunsText([stepFilter], ['RefundFlow'], 'Nothing open on this page.');

    expect(text).toBe(SEARCH_COPY.noActiveStepMatch);
    expect(text).toContain('ActiveStepSearchMode');
  });

  it('does not speak for one Flow type when the search covered several', () => {
    expect(emptyRunsText([], ['OrderFlow', 'RefundFlow'], 'No runs of this Flow type.'))
      .toBe(SEARCH_COPY.noRunsAcrossFlowTypes);
  });

  it('keeps the view\'s own wording otherwise', () => {
    expect(emptyRunsText([], ['RefundFlow'], 'No runs of this Flow type.')).toBe('No runs of this Flow type.');
  });
});
