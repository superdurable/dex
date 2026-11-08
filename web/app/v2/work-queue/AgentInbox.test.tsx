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
import { readFileSync } from 'node:fs';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../../providers';
import { WebCatalogProvider } from '../WebCatalogProvider';
import { SESSION_COPY } from '../agents/copy';
import { AGENT_INBOX_COPY } from './agentCopy';
import { WorkQueueWorkspace } from './WorkQueueWorkspace';

/** The graph Dex Agents publishes, with the Flow type and permission filled in as the renderer does. */
function sessionGraph(flowType: string, permission: string) {
  // jsdom gives import.meta.url an http scheme, so the path starts at the web module.
  const path = `${process.cwd()}/../agents/conformance/session_graph.json`;
  const graph = JSON.parse(readFileSync(path, 'utf8').replaceAll('<action-permission>', permission));
  graph.flow.name = flowType;
  return graph;
}

function graphEntry(flowType: string, permission: string) {
  return {
    id: flowType, file: `${flowType}.json`, schemaVersion: '2.0', flowName: flowType, sourceLanguage: 'python',
    sourcePath: 'session_flow.py', valid: true, graph: sessionGraph(flowType, permission),
  };
}

const view = { rpcName: 'GetDexSummary', fields: [] };
const soon = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();

interface Session {
  flowType: string;
  interrupts: Record<string, unknown>[];
  calls: Record<string, unknown>[];
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
  vi.stubGlobal('fetch', vi.fn(handle));
  async function handle(url: string, init?: RequestInit) {
    const path = new URL(url, 'http://dex.test');
    const flowId = path.searchParams.get('flowId') ?? '';
    if (url === '/api/flow-definitions') {
      return json({
        configured: true, definitionRevision: 'r1', definitionCount: 3,
        definitions: [graphEntry('SupportAgent', 'support.operate'), graphEntry('FinanceAgent', 'finance.operate')],
      });
    }
    if (url === '/api/v2/catalog') {
      return json({
        enabled: true, definitionRevision: 'r1',
        flows: [
          { flowType: 'FinanceAgent', definition: sessionGraph('FinanceAgent', 'finance.operate').v2 },
          { flowType: 'OrderFlow', definition: { indexedAttributes: [], summary: view, display: view, actions: [] } },
          { flowType: 'SupportAgent', definition: sessionGraph('SupportAgent', 'support.operate').v2 },
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
    if (path.pathname === '/api/flows/summary') return json({ flowId, runId: `run-${flowId}`, flowType: sessions[flowId].flowType, flowStatus: 'Running', flowStatusCode: 1, runAttempt: 1 });
    if (path.pathname === '/api/flows/state') {
      return json({
        activeStepExecutions: [],
        attributes: [
          { key: 'AgentState', value: { status: 'running_tools', turn_count: 0, active_turn_id: 'turn-1', accepts_turn_control: true } },
          { key: 'PendingInterrupts', value: sessions[flowId].interrupts },
          { key: 'AgentMessages/000000000001', value: { sequence: 1, turn_id: 'turn-1', role: 'user', text: `Please help with ${flowId}`, created_at: soon(-6) } },
          ...sessions[flowId].calls.map((call) => ({
            key: `AgentToolCalls/${String(call.tool_call_id)}`,
            value: { turn_id: 'turn-1', effect: 'non_idempotent_write', requires_approval: true, ...call },
          })),
        ],
      });
    }
    // Without history the panel shows message text only, which these tests do not need.
    if (path.pathname === '/api/flows/history' || path.pathname === '/api/flows/wait') {
      return json({ error: 'history unavailable' }, 503);
    }
    if (url === '/api/v2/actions') {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      actions.push(body);
      return actionReply(body);
    }
    return new Response('{}', { status: 404 });
  }
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

function call(id: string, toolName: string, args: string, status: string) {
  return { tool_call_id: id, tool_name: toolName, arguments: args, status };
}

function threeSessions(): Record<string, Session> {
  const late = approval('call-a', 'Close ticket 77', soon(40));
  const early = approval('call-b', 'Refund order 5521', soon(10));
  return {
    'ses-late': { flowType: 'SupportAgent', interrupts: [late], calls: [call('call-a', 'publish_note', late.arguments, 'awaiting_approval')] },
    'ses-soon': { flowType: 'SupportAgent', interrupts: [early], calls: [call('call-b', 'publish_note', early.arguments, 'awaiting_approval')] },
    'ses-question': {
      flowType: 'FinanceAgent',
      interrupts: [{
        interrupt_id: 'call-q', kind: 'question', tool_call_id: 'call-q', tool_name: 'ask_user', arguments: '{}',
        created_at: soon(-180), deadline: null, question: 'Which account should I use?', choices: ['checking', 'savings'], allows_free_text: true,
      }],
      calls: [call('call-q', 'ask_user', '{}', 'awaiting_answer')],
    },
  };
}

/** The session's state after an answer committed, whoever sent it. */
function answer(session: Session) {
  session.interrupts = [];
  session.calls = session.calls.map((entry) => ({ ...entry, status: 'approved' }));
}

const ok = (output: unknown) => new Response(JSON.stringify({ invoked: true, output }), { status: 200 });
const failure = (grpcCode: number) => new Response(JSON.stringify({ error: 'resolve_interrupt failed', grpcCode }), { status: 502 });

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

    const card = await screen.findByTestId('approval-call-b');
    expect(within(card).getByText(SESSION_COPY.chooseWorkingAs('support.operate'))).toBeTruthy();
    expect((within(card).getByRole('button', { name: SESSION_COPY.approve }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('link', { name: AGENT_INBOX_COPY.openSession }).getAttribute('href'))
      .toBe('/v2/run/SupportAgent/ses-soon?view=session');
  });

  it('approves from the side panel, and the row leaves the list', async () => {
    const sessions = threeSessions();
    const { actions, searches } = stubServer(sessions, () => {
      answer(sessions['ses-soon']);
      return ok(true);
    });
    renderInbox();
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    await waitFor(() => expect(searches[searches.length - 1].workQueuePermissions).toEqual(['support.operate']));
    fireEvent.click(await screen.findByRole('button', { name: /Refund order 5521/ }));

    const card = await screen.findByTestId('approval-call-b');
    expect(screen.getByText('Please help with ses-soon')).toBeTruthy();
    expect(screen.getByLabelText('location').textContent).toBe('/v2/work-queue/SupportAgent/ses-soon?view=agents');
    fireEvent.click(within(card).getByRole('button', { name: SESSION_COPY.approve }));

    await waitFor(() => expect(screen.queryByRole('button', { name: /Refund order 5521/ })).toBeNull());
    expect(actions).toEqual([expect.objectContaining({
      flowType: 'SupportAgent', flowId: 'ses-soon', rpcName: 'resolve_interrupt', workQueuePermissions: ['support.operate'],
      input: { interrupt_id: 'call-b', approved: true },
    })]);
    expect(await screen.findByText(AGENT_INBOX_COPY.nothingPending)).toBeTruthy();
  });

  it('says already handled when another operator answered first', async () => {
    const sessions = threeSessions();
    stubServer(sessions, () => {
      // Another operator's answer committed just before this one.
      answer(sessions['ses-late']);
      return ok(false);
    });
    renderInbox('/v2/work-queue/SupportAgent/ses-late?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    const card = await screen.findByTestId('approval-call-a');
    fireEvent.click(within(card).getByRole('button', { name: SESSION_COPY.reject }));
    fireEvent.change(within(card).getByLabelText(SESSION_COPY.rejectReasonLabel), { target: { value: 'Not this one' } });
    fireEvent.click(within(card).getByRole('button', { name: SESSION_COPY.confirmReject }));

    expect(await screen.findByText(SESSION_COPY.alreadyHandled)).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole('button', { name: /Close ticket 77/ })).toBeNull());
  });

  it('retries a lock conflict, then says the session is busy and keeps the call answerable', async () => {
    const sessions = threeSessions();
    const { actions } = stubServer(sessions, () => failure(10));
    renderInbox('/v2/work-queue/SupportAgent/ses-late?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    fireEvent.click(within(await screen.findByTestId('approval-call-a')).getByRole('button', { name: SESSION_COPY.approve }));

    expect(await screen.findByText(SESSION_COPY.busy, undefined, { timeout: 4000 })).toBeTruthy();
    expect(actions).toHaveLength(4);
    expect(screen.queryByText(SESSION_COPY.alreadyHandled)).toBeNull();
    const approve = within(screen.getByTestId('approval-call-a')).getByRole('button', { name: SESSION_COPY.approve }) as HTMLButtonElement;
    expect(approve.disabled).toBe(false);
    expect(screen.getByRole('button', { name: /Close ticket 77/ })).toBeTruthy();
  });

  it('says no Worker is connected when the RPC finds none', async () => {
    stubServer(threeSessions(), () => failure(14));
    renderInbox('/v2/work-queue/SupportAgent/ses-late?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'support.operate' } });
    fireEvent.click(within(await screen.findByTestId('approval-call-a')).getByRole('button', { name: SESSION_COPY.approve }));
    expect(await screen.findByText(SESSION_COPY.noWorker)).toBeTruthy();
  });

  it('answers a question with one of its choices', async () => {
    const sessions = threeSessions();
    const { actions } = stubServer(sessions, () => {
      answer(sessions['ses-question']);
      return ok(true);
    });
    renderInbox('/v2/work-queue/FinanceAgent/ses-question?view=agents');
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'finance.operate' } });
    const card = await screen.findByTestId('question-call-q');
    await waitFor(() => expect((within(card).getByRole('button', { name: 'savings' }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(within(card).getByRole('button', { name: 'savings' }));

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
