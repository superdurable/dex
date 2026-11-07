// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../../providers';
import { WebCatalogProvider } from '../WebCatalogProvider';
import { AGENT_INBOX_COPY } from './agentCopy';
import { WorkQueueWorkspace } from './WorkQueueWorkspace';

const view = (rpcName: string) => ({ rpcName, fields: [] });
const field = (fieldName: string, valueType: string, required: boolean) => ({
  fieldName, valueType, source: 'user', required, description: fieldName,
});

function agentContract(permission: string) {
  return {
    indexedAttributes: [],
    summary: view('GetDexSummary'),
    display: view('GetDexDisplay'),
    actions: [
      {
        rpcName: 'resolve_interrupt', label: 'Resolve interrupt', requiredPermission: permission,
        input: { kind: 'object', fields: [field('interrupt_id', 'string', true), field('approved', 'bool', true), field('rejection_message', 'string', false)] },
      },
      {
        rpcName: 'answer_question', label: 'Answer question', requiredPermission: permission,
        input: { kind: 'object', fields: [field('interrupt_id', 'string', true), field('answer', 'string', true)] },
      },
    ],
  };
}

function graphEntry(flowName: string, steps: string[]) {
  return {
    id: flowName, file: `${flowName}.json`, schemaVersion: '2.0', flowName, sourceLanguage: 'python',
    sourcePath: 'session_flow.py', valid: true,
    graph: { nodes: steps.map((name) => ({ id: `step:${name}`, kind: 'step', name })), edges: [] },
  };
}

const SESSION_STEPS = ['AwaitUserMessage', 'AwaitToolApproval', 'AwaitQuestionAnswer'];
const soon = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

interface Session {
  flowType: string;
  interrupts: Record<string, unknown>[];
}

function approval(id: string, message: string, deadline: string | null) {
  return {
    interrupt_id: id, kind: 'tool_approval', tool_call_id: id, tool_name: 'publish_note',
    arguments: JSON.stringify({ message }), created_at: soon(-5), deadline, question: null, choices: [], allows_free_text: true,
  };
}

function stubServer(sessions: Record<string, Session>, actionReply: (body: Record<string, unknown>) => Response) {
  const searches: Record<string, unknown>[] = [];
  const actions: Record<string, unknown>[] = [];
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const path = new URL(url, 'http://dex.test');
    const flowId = path.searchParams.get('flowId') ?? '';
    if (url === '/api/flow-definitions') {
      return json({
        configured: true, definitionRevision: 'r1', definitionCount: 3,
        definitions: [graphEntry('SupportAgent', SESSION_STEPS), graphEntry('FinanceAgent', SESSION_STEPS), graphEntry('OrderFlow', ['AwaitPacking'])],
      });
    }
    if (url === '/api/v2/catalog') {
      return json({
        enabled: true, definitionRevision: 'r1',
        flows: [
          { flowType: 'FinanceAgent', definition: agentContract('finance.operate') },
          { flowType: 'OrderFlow', definition: { ...agentContract('order.manage'), actions: [] } },
          { flowType: 'SupportAgent', definition: agentContract('support.operate') },
        ],
      });
    }
    if (url === '/api/v2/search') {
      searches.push(JSON.parse(String(init?.body)));
      return json({
        flows: Object.entries(sessions).filter(([, session]) => session.interrupts.length > 0).map(([id, session]) => ({
          flowId: id, flowType: session.flowType, flowStatus: 'Running', flowStatusCode: 1,
          startTime: soon(-10), closeTime: null, indexedAttributes: {},
        })),
        nextPageToken: '',
      });
    }
    if (path.pathname === '/api/flows/summary') return json({ runId: `run-${flowId}`, flowStatusCode: 1 });
    if (path.pathname === '/api/flows/state') {
      return json({
        attributes: [
          { key: 'PendingInterrupts', value: sessions[flowId].interrupts },
          { key: 'AgentMessages/000000000001', value: { sequence: 1, role: 'user', text: `Please help with ${flowId}`, created_at: soon(-6) } },
          ...sessions[flowId].interrupts.map((pending) => ({
            key: `AgentToolCalls/${String(pending.tool_call_id)}`,
            value: { tool_call_id: pending.tool_call_id, tool_name: pending.tool_name, effect: 'non_idempotent_write', status: 'awaiting_approval' },
          })),
        ],
      });
    }
    if (url === '/api/v2/actions') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      actions.push(body);
      return actionReply(body);
    }
    return new Response('{}', { status: 404 });
  }));
  return { searches, actions };
}

function LocationProbe() {
  const location = useLocation();
  return <p aria-label="location">{`${location.pathname}${location.search}`}</p>;
}

function renderInbox(entry = '/v2/work-queue?view=agents') {
  render(
    <PreferencesProvider>
      <MemoryRouter initialEntries={[entry]}>
        <WebCatalogProvider>
          <Routes>
            <Route path="/v2/work-queue" element={<WorkQueueWorkspace />} />
            <Route path="/v2/work-queue/:flowType" element={<WorkQueueWorkspace />} />
            <Route path="/v2/work-queue/:flowType/:flowId" element={<WorkQueueWorkspace />} />
          </Routes>
          <LocationProbe />
        </WebCatalogProvider>
      </MemoryRouter>
    </PreferencesProvider>,
  );
}

function threeSessions(): Record<string, Session> {
  return {
    'ses-late': { flowType: 'SupportAgent', interrupts: [approval('call-a', 'Close ticket 77', soon(40))] },
    'ses-soon': { flowType: 'SupportAgent', interrupts: [approval('call-b', 'Refund order 5521', soon(10))] },
    'ses-question': {
      flowType: 'FinanceAgent',
      interrupts: [{
        interrupt_id: 'call-q', kind: 'question', tool_call_id: 'call-q', tool_name: 'ask_user', arguments: '{}',
        created_at: soon(-180), deadline: null, question: 'Which account should I use?', choices: ['checking', 'savings'], allows_free_text: true,
      }],
    },
  };
}

const ok = (output: unknown) => new Response(JSON.stringify({ invoked: true, output }), { status: 200 });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Work Queue agent mode', () => {
  it('searches every agent Flow type at its waiting Steps and groups the sessions by agent', async () => {
    const { searches } = stubServer(threeSessions(), () => ok(true));
    renderInbox();

    const support = await screen.findByText('SupportAgent', { selector: '.ai-grouphead span' });
    const supportGroup = support.closest('section') as HTMLElement;
    await waitFor(() => expect(within(supportGroup).getAllByRole('button').map((button) => button.textContent)).toEqual([
      expect.stringContaining('Refund order 5521'),
      expect.stringContaining('Close ticket 77'),
    ]));
    expect(within(supportGroup).getByLabelText(AGENT_INBOX_COPY.waiting(2))).toBeTruthy();
    const financeGroup = screen.getByText('FinanceAgent', { selector: '.ai-grouphead span' }).closest('section') as HTMLElement;
    expect(within(financeGroup).getByRole('button').textContent).toContain('Which account should I use?');
    expect(within(financeGroup).getByRole('button').textContent).toContain('waiting 3 h');
    expect(screen.getByText(AGENT_INBOX_COPY.waiting(3))).toBeTruthy();

    expect(searches[0]).toMatchObject({
      flowTypes: ['FinanceAgent', 'SupportAgent'],
      workQueuePermissions: [],
      filters: [
        { field: 'activeStepTypes', operator: 'in', values: ['AwaitToolApproval', 'AwaitQuestionAnswer'] },
        { field: 'executionStatus', operator: 'eq', values: ['Running'] },
      ],
    });
  });

  it('asks for Working as before it offers an answer', async () => {
    stubServer(threeSessions(), () => ok(true));
    renderInbox('/v2/work-queue/SupportAgent/ses-soon?view=agents');

    const card = await screen.findByRole('article');
    expect(within(card).getByText(AGENT_INBOX_COPY.needsPermission('support.operate'))).toBeTruthy();
    expect((within(card).getByRole('button', { name: AGENT_INBOX_COPY.approve }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('approves from the side panel, and the row leaves the list', async () => {
    const sessions = threeSessions();
    const { actions, searches } = stubServer(sessions, () => {
      sessions['ses-soon'].interrupts = [];
      return ok(true);
    });
    renderInbox();
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    await waitFor(() => expect(searches[searches.length - 1].workQueuePermissions).toEqual(['support.operate']));
    fireEvent.click(await screen.findByRole('button', { name: /Refund order 5521/ }));

    const card = await screen.findByRole('article');
    expect(within(card).getByText('Write, not safe to repeat')).toBeTruthy();
    expect(screen.getByText('Please help with ses-soon')).toBeTruthy();
    expect(screen.getByLabelText('location').textContent).toBe('/v2/work-queue/SupportAgent/ses-soon?view=agents');
    fireEvent.click(within(card).getByRole('button', { name: AGENT_INBOX_COPY.approve }));

    await waitFor(() => expect(screen.queryByRole('button', { name: /Refund order 5521/ })).toBeNull());
    expect(actions).toEqual([expect.objectContaining({
      flowType: 'SupportAgent', flowId: 'ses-soon', rpcName: 'resolve_interrupt', workQueuePermissions: ['support.operate'],
      input: { interrupt_id: 'call-b', approved: true, rejection_message: null },
    })]);
    expect(await screen.findByText(AGENT_INBOX_COPY.done)).toBeTruthy();
    expect(screen.getByText(AGENT_INBOX_COPY.nothingPending)).toBeTruthy();
  });

  it('says already handled when another operator answered first', async () => {
    const sessions = threeSessions();
    stubServer(sessions, () => {
      // Another operator's answer committed just before this one.
      sessions['ses-late'].interrupts = [];
      return ok(false);
    });
    renderInbox('/v2/work-queue/SupportAgent/ses-late?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    const card = await screen.findByRole('article');
    fireEvent.click(within(card).getByRole('button', { name: AGENT_INBOX_COPY.reject }));
    fireEvent.change(within(card).getByLabelText(AGENT_INBOX_COPY.rejectReason), { target: { value: 'Not this one' } });
    fireEvent.click(within(card).getByRole('button', { name: AGENT_INBOX_COPY.reject }));

    const notice = await screen.findByRole('status');
    expect(within(notice).getByText(AGENT_INBOX_COPY.alreadyHandled)).toBeTruthy();
    expect(within(notice).getByText(AGENT_INBOX_COPY.alreadyHandledWhy)).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: /Close ticket 77/ })).toBeNull());
  });

  it('keeps the call when the RPC could not take its lock', async () => {
    stubServer(threeSessions(), () => new Response(JSON.stringify({ error: 'resolve_interrupt failed', grpcCode: 10 }), { status: 502 }));
    renderInbox('/v2/work-queue/SupportAgent/ses-late?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    const card = await screen.findByRole('article');
    fireEvent.click(within(card).getByRole('button', { name: AGENT_INBOX_COPY.approve }));

    const notice = await screen.findByRole('status');
    expect(within(notice).getByText(AGENT_INBOX_COPY.notApplied)).toBeTruthy();
    expect(within(notice).getByText(AGENT_INBOX_COPY.stillWaitingAfterLock)).toBeTruthy();
    expect(screen.getByRole('article')).toBeTruthy();
  });

  it('answers a question with one of its choices', async () => {
    const sessions = threeSessions();
    const { actions } = stubServer(sessions, () => {
      sessions['ses-question'].interrupts = [];
      return ok(true);
    });
    renderInbox('/v2/work-queue/FinanceAgent/ses-question?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'finance.operate' } });
    const card = await screen.findByRole('article');
    fireEvent.click(within(card).getByRole('button', { name: 'savings' }));
    fireEvent.click(within(card).getByRole('button', { name: AGENT_INBOX_COPY.answer }));

    await waitFor(() => expect(actions[0]).toMatchObject({
      rpcName: 'answer_question', input: { interrupt_id: 'call-q', answer: 'savings' },
    }));
  });

  it('offers the mode switch only when a Flow type is an agent session', async () => {
    stubServer(threeSessions(), () => ok(true));
    renderInbox('/v2/work-queue/OrderFlow');

    const modes = await screen.findByRole('navigation', { name: AGENT_INBOX_COPY.modeLabel });
    fireEvent.click(within(modes).getByRole('link', { name: AGENT_INBOX_COPY.agentsMode }));
    await waitFor(() => expect(screen.getByLabelText('location').textContent).toBe('/v2/work-queue?view=agents'));
  });
});
