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
import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import { SESSION_COPY } from './copy';
import { NewSessionDialog, newSessionSettings, sessionIdProblem } from './NewSessionDialog';
import { SessionComposer } from './SessionComposer';
import type { SessionActionInvoker } from './InterruptControls';
import { MAX_SESSION_SETTINGS_BYTES, parseSessionSettings, sessionRouteBlockedReason } from './sessionRequests';
import type { AgentStateRecord } from './sessionSchema';
import { SessionSettingsPanel, sessionSettingsSummary } from './SessionSettingsPanel';
import type { LoadedAgentSession } from './useAgentSession';

const operate = (rpcName: string): FlowV2Action => ({
  rpcName, label: rpcName, requiredPermission: 'agent.operate', input: { kind: 'object', fields: [] },
});
const schema = {
  language: 'python',
  hasQuestions: true,
  steerUserMessage: operate('steer_user_message'),
  cancelTurn: operate('cancel_turn'),
  resolveInterrupt: operate('resolve_interrupt'),
  canStartSession: true,
  rpcNames: ['send_user_message', 'update_session_settings'],
};
const context = { isFlowOpen: true, workQueuePermissions: ['agent.operate'] };
const routeContext = {
  flowType: 'SupportAgent', flowId: 'session-1', workQueuePermissions: ['agent.operate'], definitionRevision: 'sha256:r',
};

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn(async () => new Response(JSON.stringify({ invoked: true, output: true }), { status: 200 }));
  vi.stubGlobal('fetch', fetchMock);
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value(this: HTMLDialogElement) { this.open = true; },
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function sessionWith(state: Partial<AgentStateRecord>): LoadedAgentSession {
  return {
    summary: {
      flowId: 'session-1', runId: 'run-1', firstRunId: 'run-1', requestId: 'r', flowType: 'SupportAgent',
      flowStatus: 'Running', flowStatusCode: 1, startTime: null, closeTime: null,
    },
    view: {
      state: {
        status: 'waiting_for_user_message', turnCount: 1, activeTurnId: null, lastTurnError: null, acceptsTurnControl: true,
        ...state,
      },
      usage: null, sessionSettings: null, messageIndex: null, pendingInterrupts: [], items: [], waitingCalls: [],
      missing: [], hasCallsFromMessagesOnly: false, chunkKeys: [],
    },
    activeSteps: [], hasHistory: true, unreadableChunks: 0, readAt: 0,
  };
}

function renderComposer(session: LoadedAgentSession, invoke: SessionActionInvoker = vi.fn(async () => ({ kind: 'done' as const }))) {
  const onActed = vi.fn();
  function Host() {
    let text = 'Look up order 7';
    const setText = (next: string) => { text = next; };
    return (
      <SessionComposer
        context={context}
        hintShownAbove={null}
        invoke={invoke}
        onActed={onActed}
        routeContext={routeContext}
        schema={schema}
        session={session}
        setText={setText}
        text={text}
        textRef={{ current: null }}
      />
    );
  }
  render(<Host />);
  return { onActed, invoke };
}

function postedBody(callIndex = 0): Record<string, unknown> {
  const [, init] = fetchMock.mock.calls[callIndex] as [string, RequestInit];
  return JSON.parse(init.body as string) as Record<string, unknown>;
}

describe('SessionComposer', () => {
  it('sends a message through send_user_message between turns', async () => {
    const { onActed } = renderComposer(sessionWith({}));
    expect(screen.queryByRole('button', { name: SESSION_COPY.steer })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.send }));

    await waitFor(() => expect(onActed).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v2/agent-sessions/messages');
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toMatchObject({ 'X-Dex-Flow-Definition-Revision': 'sha256:r' });
    expect(postedBody()).toEqual({
      flowType: 'SupportAgent', flowId: 'session-1', workQueuePermissions: ['agent.operate'], text: 'Look up order 7',
    });
  });

  it('queues or steers while a turn runs', async () => {
    const { invoke, onActed } = renderComposer(sessionWith({ status: 'running_model', activeTurnId: 'turn-2' }));
    expect(screen.getByRole('button', { name: SESSION_COPY.queue })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.steer }));

    await waitFor(() => expect(onActed).toHaveBeenCalled());
    expect(invoke).toHaveBeenCalledWith(schema.steerUserMessage, { text: 'Look up order 7' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('only queues when the running turn does not accept steering', () => {
    renderComposer(sessionWith({ status: 'running_tools', activeTurnId: 'turn-2', acceptsTurnControl: false }));
    expect(screen.getByRole('button', { name: SESSION_COPY.queue })).toBeTruthy();
    expect(screen.queryByRole('button', { name: SESSION_COPY.steer })).toBeNull();
  });

  it('says why a refused message did not go in', async () => {
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ invoked: true, output: false }), { status: 200 }));
    renderComposer(sessionWith({}));
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.send }));
    expect(await screen.findByText(SESSION_COPY.sendRefused)).toBeTruthy();
  });

  it('blocks sending without the session permission or for a closed session', () => {
    const send = 'send_user_message';
    expect(sessionRouteBlockedReason(schema, send, { ...context, workQueuePermissions: [] }))
      .toBe(SESSION_COPY.chooseWorkingAs('agent.operate'));
    expect(sessionRouteBlockedReason(schema, send, { ...context, isFlowOpen: false })).toBe(SESSION_COPY.closed);
    expect(sessionRouteBlockedReason({ ...schema, steerUserMessage: undefined, cancelTurn: undefined, resolveInterrupt: undefined }, send, context))
      .toBe(SESSION_COPY.noSessionActions);
  });

  it('blocks sending and settings for a graph that is not a startable session or lacks the RPC', () => {
    expect(sessionRouteBlockedReason({ ...schema, canStartSession: false }, 'send_user_message', context))
      .toBe(SESSION_COPY.noSessionRPC('send_user_message'));
    expect(sessionRouteBlockedReason({ ...schema, rpcNames: ['send_user_message'] }, 'update_session_settings', context))
      .toBe(SESSION_COPY.noSessionRPC('update_session_settings'));
  });
});

describe('SessionSettingsPanel', () => {
  const renderPanel = (settings: Record<string, unknown> | null) => {
    const onSaved = vi.fn();
    render(<SessionSettingsPanel blockedReason={null} routeContext={routeContext} settings={settings} onSaved={onSaved} />);
    return { onSaved, textbox: screen.getByRole('textbox', { name: SESSION_COPY.settingsLabel }) as HTMLTextAreaElement };
  };

  it('shows the stored settings, says they hold no secrets, and saves an edit', async () => {
    const { onSaved, textbox } = renderPanel({ model: 'mock/dex' });
    expect(screen.getByText('model: mock/dex')).toBeTruthy();
    expect(screen.getByText(SESSION_COPY.settingsNoSecrets)).toBeTruthy();
    expect(JSON.parse(textbox.value)).toEqual({ model: 'mock/dex' });

    fireEvent.change(textbox, { target: { value: '{"model": "mock/other", "system_prompt": "Be brief"}' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.settingsSave }));

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v2/agent-sessions/settings');
    expect(postedBody().sessionSettings).toEqual({ model: 'mock/other', system_prompt: 'Be brief' });
    expect(screen.getByText(SESSION_COPY.settingsSaved)).toBeTruthy();
  });

  it('refuses text that is not one JSON object before sending', () => {
    const { textbox } = renderPanel(null);
    fireEvent.change(textbox, { target: { value: '["model"]' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.settingsSave }));
    expect(screen.getByText(SESSION_COPY.settingsNotObject)).toBeTruthy();

    fireEvent.change(textbox, { target: { value: '{"model":' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.settingsSave }));
    expect(screen.getByRole('status').textContent).toMatch(/^Settings are not valid JSON/);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('shows the server error when the save fails', async () => {
    fetchMock.mockImplementation(async () => new Response(
      JSON.stringify({ error: 'Flow is not active', grpcCode: 9 }), { status: 409 },
    ));
    const { textbox } = renderPanel({});
    fireEvent.change(textbox, { target: { value: '{"model":"x"}' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.settingsSave }));
    expect(await screen.findByText('Flow is not active')).toBeTruthy();
  });

  it('measures the limit in UTF-8 bytes of compact JSON', () => {
    const atLimit = { p: 'x'.repeat(MAX_SESSION_SETTINGS_BYTES - '{"p":""}'.length) };
    expect(parseSessionSettings(JSON.stringify(atLimit, null, 2), false).ok).toBe(true);
    const overByOneMultibyte = { p: `${'x'.repeat(MAX_SESSION_SETTINGS_BYTES - '{"p":""}'.length - 1)}é` };
    expect(parseSessionSettings(JSON.stringify(overByOneMultibyte), false)).toEqual({
      ok: false, message: SESSION_COPY.settingsTooLarge(MAX_SESSION_SETTINGS_BYTES + 1, MAX_SESSION_SETTINGS_BYTES),
    });
    expect(sessionSettingsSummary(null)).toBe(SESSION_COPY.settingsNone);
  });
});

describe('NewSessionDialog', () => {
  it('starts a session with the first message, model, system prompt, and other settings', async () => {
    fetchMock.mockImplementation(async () => new Response(JSON.stringify({ flowId: 'my-session', runId: 'r' }), { status: 200 }));
    const onStarted = vi.fn();
    render(
      <NewSessionDialog
        definitionRevision="sha256:r"
        flowTypes={['SupportAgent', 'ResearchAgent']}
        initialFlowType="ResearchAgent"
        onClose={vi.fn()}
        onDefinitionChanged={() => false}
        onStarted={onStarted}
      />,
    );
    const sessionId = screen.getByRole('textbox', { name: /Session ID/ }) as HTMLInputElement;
    expect(sessionId.value).toMatch(/^session-[0-9a-f-]{36}$/);
    fireEvent.change(sessionId, { target: { value: 'my-session' } });
    fireEvent.change(screen.getByRole('textbox', { name: /First message/ }), { target: { value: 'Hello' } });
    fireEvent.change(screen.getByRole('textbox', { name: /^model/ }), { target: { value: ' mock/dex ' } });
    fireEvent.change(screen.getByRole('textbox', { name: /^system_prompt/ }), { target: { value: 'Be brief' } });
    fireEvent.change(screen.getByRole('textbox', { name: /Other settings/ }), { target: { value: '{"model":"old","max_turns":3}' } });
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.startSession }));

    await waitFor(() => expect(onStarted).toHaveBeenCalledWith('ResearchAgent', 'my-session'));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v2/agent-sessions/start');
    expect(postedBody()).toEqual({
      flowType: 'ResearchAgent',
      flowId: 'my-session',
      initialMessage: 'Hello',
      sessionSettings: { model: 'mock/dex', max_turns: 3, system_prompt: 'Be brief' },
    });
  });

  it('checks the session ID and the other settings before starting', () => {
    expect(sessionIdProblem(' ')).toBe(SESSION_COPY.sessionIdRequired);
    expect(sessionIdProblem('a/b')).toBe(SESSION_COPY.sessionIdReserved);
    const draft = { sessionId: 's', firstMessage: '', model: '', systemPrompt: '', otherSettings: '' };
    expect(newSessionSettings(draft)).toEqual({ ok: true, value: null });
    expect(newSessionSettings({ ...draft, otherSettings: '[]' })).toEqual({ ok: false, message: SESSION_COPY.settingsNotObject });
  });

  it('shows the server error and stays open', async () => {
    fetchMock.mockImplementation(async () => new Response(
      JSON.stringify({ error: 'Flow already started', grpcCode: 6 }), { status: 409 },
    ));
    const onStarted = vi.fn();
    render(
      <NewSessionDialog
        definitionRevision="sha256:r"
        flowTypes={['SupportAgent']}
        onClose={vi.fn()}
        onDefinitionChanged={() => false}
        onStarted={onStarted}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: SESSION_COPY.startSession }));
    expect(await screen.findByText('Flow already started')).toBeTruthy();
    expect(onStarted).not.toHaveBeenCalled();
    expect(postedBody()).toMatchObject({ flowType: 'SupportAgent', initialMessage: null, sessionSettings: null });
  });
});
