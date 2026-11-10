// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
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
vi.mock('../catalog/WebCatalogProvider', () => ({ useWebCatalog: () => ({ catalog: null }) }));

afterEach(cleanup);

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

const schema = { language: 'python', hasQuestions: true };

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

  it('asks for Working as once, at the top of the tab', () => {
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

    expect(screen.getAllByText(SESSION_COPY.chooseWorkingAs('coding.operate'))).toHaveLength(1);
    expect(screen.getByRole('button', { name: SESSION_COPY.workAs('coding.operate') })).toBeTruthy();
    expect((screen.getByRole('textbox', { name: SESSION_COPY.steerLabel }) as HTMLTextAreaElement).disabled).toBe(true);
  });
});
