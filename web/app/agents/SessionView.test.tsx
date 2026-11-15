// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useState } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SESSION_COPY } from './copy';
import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import type { SessionView as SessionReadView, ToolCallView } from './sessionModel';
import type { LoadedAgentSession } from './useAgentSession';
import { messageTextParts, SessionView } from './SessionView';

const reload = vi.fn();
let loaded: LoadedAgentSession;

vi.mock('./useAgentSession', () => ({
  useAgentSession: () => ({
    held: { value: loaded, liveness: 'ok', reason: null },
    reload,
    includeChunks: false,
    loadEarlierMessages: vi.fn(),
  }),
}));
vi.mock('../catalog/WebCatalogProvider', () => ({ useWebCatalog: () => ({ revisionFor: () => '' }) }));

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  delete (Element.prototype as Partial<Element>).scrollIntoView;
});

const pastCall = (toolCallId: string, toolName: string, argumentsText: string): ToolCallView => ({
  toolCallId, toolName, turnId: 'turn-1', argumentsText, effect: null, requiresApproval: null,
  state: 'returned', source: 'messages', output: 'ok',
});

function sessionOf(view: Partial<SessionReadView>): LoadedAgentSession {
  return {
    summary: {
      flowId: 'session-1', runId: 'run-1', firstRunId: 'run-1', requestId: 'request-1', flowType: 'CodingAgent',
      flowStatus: 'Running', flowStatusCode: 1, startTime: null, closeTime: null,
    },
    view: {
      state: { status: 'waiting_for_user_message', turnCount: 1, activeTurnId: null, lastTurnError: null, acceptsTurnControl: true },
      usage: { modelCalls: 4, inputTokens: 18780, outputTokens: 1331, cachedInputTokens: 17065, reasoningTokens: 0 },
      sessionSettings: null,
      messageIndex: null,
      pendingInterrupts: [],
      items: [],
      waitingCalls: [],
      missing: [],
      hasCallsFromMessagesOnly: false,
      chunkKeys: [],
      ...view,
    },
    activeSteps: [],
    hasHistory: false,
    unreadableChunks: 0,
    readAt: 0,
  };
}

const schema = { language: 'python', hasQuestions: true, rpcNames: ['send_user_message', 'update_session_settings'] };

describe('SessionView', () => {
  it('reads past calls as compact rows under one retention notice', () => {
    const calls = [
      pastCall('toolu_01glob', 'Glob', '{"pattern":"*.py","path":"/tmp/work/team-3"}'),
      pastCall('toolu_02read', 'Read', '{"file_path":"/tmp/work/team-3/slug.py"}'),
      pastCall('toolu_03write', 'Write', '{"file_path":"/tmp/work/team-3/test_slug.py","content":"def test(): ..."}'),
    ];
    loaded = sessionOf({
      hasCallsFromMessagesOnly: true,
      items: [
        { kind: 'message', key: 'm1', message: {
          sequence: 1, turnId: 'turn-1', role: 'user', text: 'Implement slugify.', toolCallId: null,
          toolName: null, toolArguments: null, toolOutput: null, createdAt: null,
        } },
        ...calls.map((call) => ({ kind: 'tool' as const, key: call.toolCallId, call })),
      ],
    });
    render(<SessionView flowId="session-1" flowType="CodingAgent" reloadKey={0} schema={schema} workQueuePermissions={[]} />);

    expect(screen.getAllByText(SESSION_COPY.pastCallsNote)).toHaveLength(1);
    expect(screen.getByText(SESSION_COPY.sessionStatus.waiting_for_user_message)).toBeTruthy();
    expect(screen.getByRole('button', { name: SESSION_COPY.reload })).toBeTruthy();
    const rows = calls.map((call) => screen.getByTestId(`tool-call-${call.toolCallId}`).querySelector('summary') as HTMLElement);
    expect(rows.map((row) => row.textContent)).toEqual(['Glob*.py', 'Read…/team-3/slug.py', 'Write…/team-3/test_slug.py']);
    for (const call of calls) expect(rows.some((row) => row.textContent?.includes(call.toolCallId))).toBe(false);
  });

  it('shows the name a sender declared on their message', () => {
    loaded = sessionOf({
      items: [
        { kind: 'message', key: 'm1', message: {
          sequence: 1, turnId: 'turn-1', role: 'user', text: 'Hi from my phone.', toolCallId: null,
          toolName: null, toolArguments: null, toolOutput: null, createdAt: null, senderName: 'Alice',
        } },
        { kind: 'message', key: 'm2', message: {
          sequence: 2, turnId: 'turn-1', role: 'user', text: 'And from the terminal.', toolCallId: null,
          toolName: null, toolArguments: null, toolOutput: null, createdAt: null, senderName: null,
        } },
      ],
    });
    render(<SessionView flowId="session-1" flowType="CodingAgent" reloadKey={0} schema={schema} workQueuePermissions={[]} />);

    const named = screen.getByText('Alice', { exact: false, selector: '.ses-who' });
    expect(named.getAttribute('title')).toBe(SESSION_COPY.senderNameUnverified);
    expect(named.textContent).toBe(`Alice · ${SESSION_COPY.senderNameUnverifiedShort}`);
    expect(screen.getAllByText(SESSION_COPY.user)).toHaveLength(1);
  });

  it('draws bold and inline code in message text without reading it as HTML', () => {
    const { container } = render(<p>{messageTextParts('- **`slug.py`**: kept <b>as is</b>, see `tests`')}</p>);
    expect(container.querySelector('strong code')?.textContent).toBe('slug.py');
    expect(container.querySelectorAll('code')).toHaveLength(2);
    expect(container.querySelector('b')).toBeNull();
    expect(container.textContent).toBe('- slug.py: kept <b>as is</b>, see tests');
  });

  it('puts a pending approval in a warm status chip', () => {
    const waiting: ToolCallView = {
      ...pastCall('call-1', 'create_refund', '{"amount":24000}'),
      state: 'awaiting_approval', source: 'live', effect: 'non_idempotent_write', requiresApproval: true,
    };
    loaded = sessionOf({
      state: { status: 'running_tools', turnCount: 1, activeTurnId: 'turn-1', lastTurnError: null, acceptsTurnControl: true },
      items: [{ kind: 'tool', key: 'call-1', call: waiting }],
      waitingCalls: [waiting],
    });
    render(<SessionView flowId="session-1" flowType="CodingAgent" reloadKey={0} schema={schema} workQueuePermissions={[]} />);

    const chip = screen.getByText(SESSION_COPY.waitingForApproval(1));
    expect(chip.closest('.ses-status')?.getAttribute('data-tone')).toBe('attention');
    expect(screen.getByTestId('tool-call-call-1').dataset.tone).toBe('attention');
  });

  it('asks for Working as only where it blocks, with no banner at the top', () => {
    const operate = (rpcName: string): FlowV2Action => ({
      rpcName, label: rpcName, requiredPermission: 'coding.operate', input: { kind: 'object', fields: [] },
    });
    const actionSchema = { ...schema, steerUserMessage: operate('steer_user_message'), cancelTurn: operate('cancel_turn') };
    loaded = sessionOf({
      state: { status: 'running_model', turnCount: 1, activeTurnId: 'turn-1', lastTurnError: null, acceptsTurnControl: true },
    });
    render(
      <SessionView
        flowId="session-1"
        flowType="CodingAgent"
        onChooseWorkingAs={vi.fn()}
        reloadKey={0}
        schema={actionSchema}
        workQueuePermissions={[]}
      />,
    );

    expect(document.querySelector('.ses-permission')).toBeNull();
    // Each blocked control offers Work as where it is blocked: the composer in place of Send.
    const workAs = screen.getAllByRole('button', { name: SESSION_COPY.workAs('coding.operate') });
    expect(workAs.filter((button) => button.closest('.ses-controls') !== null)).toHaveLength(1);
    expect(screen.queryByText(SESSION_COPY.chooseWorkingAs('coding.operate'))).toBeNull();
    const composer = screen.getByRole('textbox', { name: SESSION_COPY.steerLabel }) as HTMLTextAreaElement;
    expect(composer.disabled).toBe(true);
    expect(composer.placeholder).toBe(SESSION_COPY.composerWorkingAsPlaceholder('coding.operate'));
  });

  it('offers in the composer the permission that sending checks', () => {
    const action = (rpcName: string, permission: string): FlowV2Action => ({
      rpcName, label: rpcName, requiredPermission: permission, input: { kind: 'object', fields: [] },
    });
    const mixedSchema = {
      ...schema,
      answerQuestion: action('answer_question', 'answers.operate'),
      steerUserMessage: action('steer_user_message', 'steering.operate'),
    };
    loaded = sessionOf({});
    const onChooseWorkingAs = vi.fn();
    render(
      <SessionView
        flowId="session-1"
        flowType="CodingAgent"
        onChooseWorkingAs={onChooseWorkingAs}
        reloadKey={0}
        schema={mixedSchema}
        workQueuePermissions={[]}
      />,
    );

    const composerWorkAs = screen.getAllByRole('button', { name: SESSION_COPY.workAs('steering.operate') })
      .find((button) => button.closest('.ses-controls') !== null)!;
    expect(composerWorkAs).toBeTruthy();
    fireEvent.click(composerWorkAs);
    expect(onChooseWorkingAs).toHaveBeenCalledWith('steering.operate');
  });

  it('lets a reader choose Working as from the composer, then send to a session they did not start', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ invoked: true, output: true }), { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    const operate = (rpcName: string): FlowV2Action => ({
      rpcName, label: rpcName, requiredPermission: 'dex-agents-cli.operate', input: { kind: 'object', fields: [] },
    });
    const cliSchema = {
      ...schema,
      resolveInterrupt: operate('resolve_interrupt'),
      answerQuestion: operate('answer_question'),
      steerUserMessage: operate('steer_user_message'),
      cancelTurn: operate('cancel_turn'),
    };
    loaded = sessionOf({});
    function RunPage() {
      const [permissions, setPermissions] = useState<string[]>([]);
      return (
        <SessionView
          flowId="session-1"
          flowType="DexAgentsClaudeSdkCliSession-abc123"
          onChooseWorkingAs={(permission) => setPermissions([permission])}
          reloadKey={0}
          schema={cliSchema}
          workQueuePermissions={permissions}
        />
      );
    }
    render(<RunPage />);

    expect(screen.queryByRole('button', { name: SESSION_COPY.send })).toBeNull();
    const composerWorkAs = screen.getAllByRole('button', { name: SESSION_COPY.workAs('dex-agents-cli.operate') })
      .find((button) => button.closest('.ses-controls') !== null);
    fireEvent.click(composerWorkAs as HTMLElement);

    const composer = screen.getByRole('textbox', { name: SESSION_COPY.steerLabel }) as HTMLTextAreaElement;
    expect(composer.disabled).toBe(false);
    await waitFor(() => expect(document.activeElement).toBe(composer));
    expect(screen.queryByText(SESSION_COPY.chooseWorkingAs('dex-agents-cli.operate'))).toBeNull();
    fireEvent.change(composer, { target: { value: 'Please also check the tests' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.send }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v2/agent-sessions/messages');
    expect(JSON.parse(init.body as string)).toEqual({
      flowType: 'DexAgentsClaudeSdkCliSession-abc123',
      flowId: 'session-1',
      workQueuePermissions: ['dex-agents-cli.operate'],
      text: 'Please also check the tests',
    });
    expect(await screen.findByText(SESSION_COPY.steered)).toBeTruthy();
  });

  it('names Working as by the composer once it is set, and Change offers every choice', () => {
    const operate = (rpcName: string): FlowV2Action => ({
      rpcName, label: rpcName, requiredPermission: 'coding.operate', input: { kind: 'object', fields: [] },
    });
    const actionSchema = { ...schema, steerUserMessage: operate('steer_user_message'), cancelTurn: operate('cancel_turn') };
    loaded = sessionOf({});
    const onChooseWorkingAs = vi.fn();
    render(
      <SessionView
        flowId="session-1"
        flowType="CodingAgent"
        onChooseWorkingAs={onChooseWorkingAs}
        reloadKey={0}
        schema={actionSchema}
        workingAsOptions={['coding.operate', 'coding.review']}
        workingAsPermission="coding.operate"
        workQueuePermissions={['coding.operate']}
      />,
    );
    const line = screen.getByText(SESSION_COPY.actingAs('coding.operate')).closest('p')!;
    expect(line.closest('.ses-controls')).not.toBeNull();
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.changeWorkingAs }));
    const select = screen.getByRole('combobox', { name: SESSION_COPY.workingAsLabel }) as HTMLSelectElement;
    expect([...select.options].map((option) => option.textContent)).toEqual(['Anyone', 'coding.operate', 'coding.review']);
    fireEvent.change(select, { target: { value: '' } });
    expect(onChooseWorkingAs).toHaveBeenCalledWith('');
  });
});

const userMessage = (sequence: number, text: string, origin?: string | null) => ({
  kind: 'message' as const, key: `m${sequence}`, message: {
    sequence, turnId: 'turn-1', role: 'user', text, toolCallId: null, toolName: null,
    toolArguments: null, toolOutput: null, createdAt: null, origin,
  },
});

const waitingCall = (toolCallId: string): ToolCallView => ({
  ...pastCall(toolCallId, 'create_refund', '{"amount":24000}'),
  state: 'awaiting_approval', source: 'live', effect: 'non_idempotent_write', requiresApproval: true,
  interrupt: {
    interruptId: `interrupt-${toolCallId}`, kind: 'tool_approval', toolCallId, toolName: 'create_refund',
    arguments: '{"amount":24000}', createdAt: null, deadline: null, question: null, choices: [], allowsFreeText: true,
  },
});

function renderSession(reloadKey = 0) {
  return <SessionView flowId="session-1" flowType="CodingAgent" reloadKey={reloadKey} schema={schema} workQueuePermissions={[]} />;
}

describe('SessionView notes and long text', () => {
  it('shows a note the session wrote as a closed Dex note row, not as a user message', () => {
    const note = '[Dex] The user stopped the previous turn after these tool calls ran:\n- fetch {"url":"x"} -> succeeded: <html>…</html>\nDo not repeat these tool calls unless the user asks again.';
    loaded = sessionOf({ items: [userMessage(1, 'Fetch x.'), userMessage(2, note, 'turn_cancelled_note')] });
    render(renderSession());

    expect(screen.getAllByText(SESSION_COPY.user)).toHaveLength(1);
    const row = screen.getByText(SESSION_COPY.dexNoteSummary('turn_cancelled_note')).closest('details') as HTMLDetailsElement;
    expect(row.className).toBe('ses-dexnote');
    expect(row.open).toBe(false);
    expect(row.querySelector('summary')?.textContent).toContain(SESSION_COPY.dexNote);
    expect(row.querySelector('.ses-dexnote-text')?.textContent).toBe(note);
  });

  it('shows a note with an origin from a later release as a Dex note too', () => {
    loaded = sessionOf({ items: [userMessage(1, 'Something the session said.', 'origin_from_a_later_release')] });
    render(renderSession());

    expect(screen.queryByText(SESSION_COPY.user)).toBeNull();
    expect(screen.getByText(SESSION_COPY.dexNoteSummary('origin_from_a_later_release'))).toBeTruthy();
    expect(SESSION_COPY.dexNoteSummary('turn_steered_note')).toContain('steered');
  });

  it('shows the first lines of a long message until the reader asks for more', () => {
    const long = Array.from({ length: 40 }, (_, index) => `line ${index + 1}`).join('\n');
    loaded = sessionOf({ items: [userMessage(1, long), userMessage(2, 'short')] });
    render(renderSession());

    const texts = [...document.querySelectorAll('.ses-text')];
    expect(texts.map((text) => text.getAttribute('data-collapsed'))).toEqual(['true', null]);
    expect(screen.getAllByRole('button', { name: SESSION_COPY.showMore })).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.showMore }));
    expect(texts[0].getAttribute('data-collapsed')).toBeNull();
    expect(screen.getByRole('button', { name: SESSION_COPY.showLess }).getAttribute('aria-expanded')).toBe('true');
  });
});

describe('SessionView scrolling', () => {
  function stubScrollIntoView() {
    const scrolled: Element[] = [];
    Element.prototype.scrollIntoView = vi.fn(function scrollIntoView(this: Element) { scrolled.push(this); });
    return scrolled;
  }

  function placeLatestBelowTheView() {
    const below = { top: 5000, bottom: 5000, left: 0, right: 0, width: 0, height: 0, x: 0, y: 5000, toJSON: () => ({}) };
    const original = Element.prototype.getBoundingClientRect;
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function rect(this: Element) {
      return this.classList.contains('ses-latest') ? below as DOMRect : original.call(this);
    });
  }

  it('follows a new item while the reader is at the newest one, but not on the first read', () => {
    const scrolled = stubScrollIntoView();
    loaded = sessionOf({ items: [userMessage(1, 'First.')] });
    const { rerender } = render(renderSession());
    expect(scrolled).toHaveLength(0);

    loaded = sessionOf({ items: [userMessage(1, 'First.'), userMessage(2, 'Second.')] });
    rerender(renderSession(1));
    expect(scrolled.map((element) => element.className)).toEqual(['ses-latest']);
    expect(screen.queryByRole('button', { name: SESSION_COPY.jumpToLatest })).toBeNull();
  });

  it('offers Jump to latest instead of following when the reader scrolled up', () => {
    const scrolled = stubScrollIntoView();
    placeLatestBelowTheView();
    loaded = sessionOf({ items: [userMessage(1, 'First.')] });
    const { rerender } = render(renderSession());
    loaded = sessionOf({ items: [userMessage(1, 'First.'), userMessage(2, 'Second.')] });
    rerender(renderSession(1));
    expect(scrolled).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.jumpToLatest }));
    expect(scrolled.map((element) => element.className)).toEqual(['ses-latest']);
  });

  it('brings the controls of a call that starts waiting into view, wherever the reader is', () => {
    const scrolled = stubScrollIntoView();
    placeLatestBelowTheView();
    loaded = sessionOf({ items: [userMessage(1, 'Refund order 7.')] });
    const { rerender } = render(renderSession());

    const call = waitingCall('call-1');
    loaded = sessionOf({
      state: { status: 'running_tools', turnCount: 1, activeTurnId: 'turn-1', lastTurnError: null, acceptsTurnControl: true },
      items: [userMessage(1, 'Refund order 7.'), { kind: 'tool', key: 'call-1', call }],
      waitingCalls: [call],
    });
    rerender(renderSession(1));
    expect(scrolled).toEqual([screen.getByTestId('approval-interrupt-call-1')]);

    // A later read of the same waiting call does not scroll again.
    rerender(renderSession(2));
    expect(scrolled).toHaveLength(1);
  });

  it('jumps from the waiting chip to the call and focuses its first control', () => {
    const scrolled = stubScrollIntoView();
    const call = waitingCall('call-1');
    loaded = sessionOf({
      state: { status: 'running_tools', turnCount: 1, activeTurnId: 'turn-1', lastTurnError: null, acceptsTurnControl: true },
      items: [{ kind: 'tool', key: 'call-1', call }],
      waitingCalls: [call],
    });
    const resolveInterrupt: FlowV2Action = {
      rpcName: 'resolve_interrupt', label: 'resolve_interrupt', requiredPermission: 'coding.operate',
      input: { kind: 'object', fields: [] },
    };
    render(
      <SessionView
        flowId="session-1"
        flowType="CodingAgent"
        reloadKey={0}
        schema={{ ...schema, resolveInterrupt }}
        workQueuePermissions={['coding.operate']}
      />,
    );
    expect(scrolled).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.waitingForApproval(1) }));
    const controls = screen.getByTestId('approval-interrupt-call-1');
    expect(scrolled).toEqual([controls]);
    expect(document.activeElement).toBe(screen.getByRole('button', { name: SESSION_COPY.approve }));
  });
});

