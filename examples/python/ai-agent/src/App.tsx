/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import React, { useCallback, useEffect, useRef, useState } from 'react';

const API_BASE = '/products/ai-agent';
const TURN_END_EVENT_TYPES = ['turn_completed', 'turn_failed', 'turn_steered', 'turn_cancelled'];
// A model call's Step writes usage_recorded when it ends; its text is then in the snapshot.
const MODEL_CALL_END_EVENTS = new Set(['usage_recorded', 'tool_call_deferred']);
const REFRESH_EVENTS = new Set(TURN_END_EVENT_TYPES.concat([
  'turn_started',
  'tool_call_deferred',
  'tool_call_finished',
  'interrupt_raised',
  'interrupt_resolved',
  'timer_started',
  'plan_updated',
  'plan_execution_started',
  'compaction_finished',
  'usage_recorded',
]));

interface AgentState {
  status: string;
  turn_count: number;
  active_turn_id: string | null;
  last_turn_error: string | null;
  interaction_mode: string | null;
}

interface AgentMessage {
  sequence: number;
  turn_id: string;
  role: 'user' | 'assistant' | 'tool';
  text: string | null;
  tool_call_id: string | null;
  tool_name: string | null;
  tool_arguments: string | null;
  tool_output: string | null;
  created_at: string;
}

interface AgentInterrupt {
  interrupt_id: string;
  kind: 'tool_approval' | 'question';
  tool_name: string;
  arguments: string;
  question: string | null;
  choices: string[];
  allows_free_text: boolean;
}

interface AgentToolCall {
  tool_call_id: string;
  tool_name: string;
  status: string;
  wait_until: string | null;
}

interface PendingUserMessage {
  message_id: string;
  message: { text: string; plan_mode: boolean | null };
}

interface AgentPlan {
  revision: number;
  status: 'draft' | 'active' | 'completed' | 'cleared';
  tasks: { content: string; status: string }[];
}

interface AgentSnapshot {
  state: AgentState | null;
  messages: AgentMessage[];
  pending_interrupts: AgentInterrupt[];
  tool_calls: AgentToolCall[];
  usage: { model_calls: number; input_tokens: number; output_tokens: number };
  plan: AgentPlan | null;
  queued_messages: PendingUserMessage[];
  steered_messages: PendingUserMessage[];
  context_summary: { generation: number; last_sequence: number } | null;
}

interface AgentEvent {
  type: string;
  turn_id: string | null;
  attempt: number;
  step_execution_id: string;
  payload: string;
}

interface WorkerSettings {
  model: string;
  system_prompt: string;
  context_window_tokens: number;
  mcp_servers: string[];
  mcp_tools: { name: string; effect: string }[];
  built_in_tools: string[];
}

interface LiveOutput {
  stepExecutionId: string;
  turnId: string | null;
  attempt: number;
  text: string;
  reasoning: string;
}

const postJson = async (path: string, body: Record<string, unknown>): Promise<{ accepted?: boolean }> => {
  const response = await fetch(`${API_BASE}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
};

const describeEvent = (event: AgentEvent): string => {
  const payload = JSON.parse(event.payload || '{}');
  switch (event.type) {
    case 'tool_call_deferred':
      return `${payload.tool_name} (${payload.effect}${payload.requires_approval ? ', needs approval' : ''})`;
    case 'tool_call_started':
      return `${payload.tool_name} started`;
    case 'tool_call_finished':
      return `${payload.tool_name} ${payload.status}`;
    case 'interrupt_raised':
      return payload.kind === 'question' ? `question: ${payload.question}` : `approval for ${payload.tool_name}`;
    case 'timer_started':
      return `waiting until ${payload.wait_until}: ${payload.reason}`;
    case 'plan_updated':
      return `plan revision ${payload.revision} is ${payload.status}`;
    case 'mcp.tool_progress':
      return `${payload.tool_name}: ${payload.message ?? payload.progress}`;
    case 'turn_failed':
      return payload.error;
    default:
      return '';
  }
};

const App: React.FC = () => {
  const [sessionId, setSessionId] = useState(
    () => new URLSearchParams(window.location.search).get('session_id') ?? '',
  );
  const [settings, setSettings] = useState<WorkerSettings | null>(null);
  const [snapshot, setSnapshot] = useState<AgentSnapshot | null>(null);
  const [resumeToken, setResumeToken] = useState<string | null>(null);
  const [liveOutputs, setLiveOutputs] = useState<LiveOutput[]>([]);
  const [activity, setActivity] = useState<{ key: string; type: string; detail: string }[]>([]);
  const [input, setInput] = useState('');
  const [planMode, setPlanMode] = useState(false);
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [isBusy, setIsBusy] = useState(false);
  const [olderMessages, setOlderMessages] = useState<AgentMessage[]>([]);
  const refreshTimer = useRef<number | null>(null);
  const endedModelCalls = useRef(new Set<string>());
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    void fetch(`${API_BASE}/settings`)
      .then(async (response) => {
        if (!response.ok) throw new Error(await response.text());
        setSettings(await response.json());
      })
      .catch((reason) => setError(String(reason)));
  }, []);

  const loadSnapshot = useCallback(async () => {
    if (!sessionId) return;
    const response = await fetch(`${API_BASE}/snapshot?session_id=${encodeURIComponent(sessionId)}`);
    if (!response.ok) throw new Error(await response.text());
    const payload = await response.json() as { snapshot: AgentSnapshot; resume_token: string };
    setSnapshot(payload.snapshot);
    setResumeToken((current) => current ?? payload.resume_token);
    const storedTexts = new Set(payload.snapshot.messages.map((message) => message.text));
    // Keep live text until the stored message that replaces it is on screen.
    setLiveOutputs((current) => (payload.snapshot.state?.active_turn_id
      ? current.filter((output) => !(endedModelCalls.current.has(output.stepExecutionId)
        && (!output.text || storedTexts.has(output.text))))
      : []));
  }, [sessionId]);

  const requestRefresh = useCallback(() => {
    if (refreshTimer.current !== null) return;
    refreshTimer.current = window.setTimeout(() => {
      refreshTimer.current = null;
      void loadSnapshot().catch((reason) => setError(String(reason)));
    }, 250);
  }, [loadSnapshot]);

  useEffect(() => {
    if (!sessionId) return;
    void loadSnapshot().catch((reason) => setError(String(reason)));
    const interval = window.setInterval(requestRefresh, 8000);
    window.addEventListener('focus', requestRefresh);
    return () => {
      window.clearInterval(interval);
      window.removeEventListener('focus', requestRefresh);
    };
  }, [sessionId, loadSnapshot, requestRefresh]);

  useEffect(() => {
    if (!sessionId || resumeToken === null) return;
    const query = new URLSearchParams({ session_id: sessionId, resume_token: resumeToken });
    const events = new EventSource(`${API_BASE}/events?${query}`);
    events.onmessage = (message) => {
      const event = JSON.parse(message.data) as AgentEvent;
      if (event.type === 'text_delta' || event.type === 'reasoning_delta') {
        const { text } = JSON.parse(event.payload) as { text: string };
        setLiveOutputs((current) => {
          const existing = current.find((item) => item.stepExecutionId === event.step_execution_id);
          if (existing && existing.attempt > event.attempt) return current;
          // A retried model call replaces the partial output of its earlier attempt.
          const base = existing && existing.attempt === event.attempt
            ? existing
            : { stepExecutionId: event.step_execution_id, turnId: event.turn_id, attempt: event.attempt, text: '', reasoning: '' };
          const updated = event.type === 'text_delta'
            ? { ...base, text: base.text + text }
            : { ...base, reasoning: base.reasoning + text };
          return [...current.filter((item) => item !== existing), updated];
        });
        return;
      }
      if (MODEL_CALL_END_EVENTS.has(event.type)) endedModelCalls.current.add(event.step_execution_id);
      setActivity((current) => [
        ...current,
        { key: message.lastEventId, type: event.type, detail: describeEvent(event) },
      ].slice(-30));
      if (REFRESH_EVENTS.has(event.type)) requestRefresh();
    };
    events.onerror = () => {
      // A rejected resume token closes the stream; reload the snapshot and reopen from its token.
      if (events.readyState === EventSource.CLOSED) setResumeToken(null);
    };
    return () => events.close();
  }, [sessionId, resumeToken, requestRefresh]);

  useEffect(() => {
    if (sessionId && resumeToken === null && snapshot !== null) {
      void loadSnapshot().catch((reason) => setError(String(reason)));
    }
  }, [sessionId, resumeToken, snapshot, loadSnapshot]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [snapshot?.messages.length, liveOutputs]);

  const storedMessages = snapshot?.messages ?? [];
  const firstStoredSequence = storedMessages[0]?.sequence ?? Number.MAX_SAFE_INTEGER;
  const conversation = olderMessages
    .filter((message) => message.sequence < firstStoredSequence)
    .concat(storedMessages);

  const run = async (action: () => Promise<{ accepted?: boolean }>, rejection: string) => {
    setIsBusy(true);
    setError('');
    try {
      const result = await action();
      if (result.accepted === false) setError(rejection);
      await loadSnapshot();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setIsBusy(false);
    }
  };

  const loadOlderMessages = async () => {
    const before = conversation[0]?.sequence;
    if (!before) return;
    const query = new URLSearchParams({ session_id: sessionId, before_sequence: String(before) });
    const response = await fetch(`${API_BASE}/history?${query}`);
    if (!response.ok) {
      setError(await response.text());
      return;
    }
    const page = await response.json() as { messages: AgentMessage[] };
    setOlderMessages((current) => [...page.messages, ...current]);
  };

  const startSession = async () => {
    const newSessionId = crypto.randomUUID();
    await run(async () => {
      await postJson('/sessions', { session_id: newSessionId });
      window.history.replaceState({}, '', `${window.location.pathname}?session_id=${newSessionId}`);
      setSessionId(newSessionId);
      return {};
    }, '');
  };

  const sendMessage = (steer: boolean) => {
    const text = input.trim();
    if (!text) return;
    setInput('');
    void run(
      () => (steer
        ? postJson('/messages/steer', { session_id: sessionId, text })
        : postJson('/messages', { session_id: sessionId, text, plan_mode: planMode })),
      'The message was not accepted.',
    );
  };

  if (!sessionId) {
    return (
      <main style={styles.page}>
        <section style={styles.card}>
          <p style={styles.eyebrow}>Dex Agents</p>
          <h1 style={styles.title}>AI Agent</h1>
          <p style={styles.muted}>
            The model, system prompt, context window, and MCP servers are Worker configuration.
            Change them in <code>examples/.env</code> and the MCP file, then restart the Worker.
          </p>
          {settings && (
            <dl style={styles.settings}>
              <dt>Model</dt>
              <dd><code>{settings.model}</code></dd>
              <dt>System prompt</dt>
              <dd>{settings.system_prompt}</dd>
              <dt>Context window</dt>
              <dd>{settings.context_window_tokens.toLocaleString()} tokens, compacted at 85 percent</dd>
              <dt>MCP servers</dt>
              <dd>{settings.mcp_servers.length ? settings.mcp_servers.join(', ') : 'None configured'}</dd>
              <dt>MCP tools</dt>
              <dd style={styles.pills}>
                {settings.mcp_tools.map((tool) => (
                  <span key={tool.name} style={styles.pill}>
                    {tool.name} <em style={styles.muted}>{tool.effect === 'read' || tool.effect === 'idempotent_write' ? tool.effect : 'needs approval'}</em>
                  </span>
                ))}
              </dd>
              <dt>Built-in tools</dt>
              <dd style={styles.pills}>
                {settings.built_in_tools.map((tool) => <span key={tool} style={styles.pill}>{tool}</span>)}
              </dd>
            </dl>
          )}
          <button style={styles.primaryButton} disabled={isBusy} onClick={startSession}>
            {isBusy ? 'Starting…' : 'Start AI Agent'}
          </button>
          {error && <p style={styles.error}>{error}</p>}
        </section>
      </main>
    );
  }

  const state = snapshot?.state ?? null;
  const isTurnActive = Boolean(state?.active_turn_id);
  const plan = snapshot?.plan?.status === 'cleared' ? null : snapshot?.plan ?? null;
  const waitingTimers = (snapshot?.tool_calls ?? []).filter((call) => call.status === 'waiting_for_timer');

  return (
    <main style={styles.page}>
      <header style={styles.header}>
        <div>
          <p style={styles.eyebrow}>AIAgentSession</p>
          <h1 style={styles.sessionTitle}>{sessionId}</h1>
        </div>
        <div style={styles.headerActions}>
          <span style={styles.status}>{state ? state.status.split('_').join(' ') : 'starting'}</span>
          {snapshot && (
            <span style={styles.muted}>
              {snapshot.usage.model_calls} model calls · {snapshot.usage.input_tokens + snapshot.usage.output_tokens} tokens
              {snapshot.context_summary ? ` · summary ${snapshot.context_summary.generation}` : ''}
            </span>
          )}
          {isTurnActive && (
            <button
              style={styles.stopButton}
              disabled={isBusy}
              onClick={() => void run(
                () => postJson('/turns/cancel', { session_id: sessionId, turn_id: state?.active_turn_id }),
                'The turn ended before it could be stopped.',
              )}
            >
              Stop
            </button>
          )}
        </div>
      </header>

      {state?.last_turn_error && <p style={styles.error}>The last turn failed: {state.last_turn_error}</p>}

      {plan && (
        <section style={styles.card}>
          <h2 style={styles.sectionTitle}>Plan · revision {plan.revision} · {plan.status}</h2>
          <ol style={styles.tasks}>
            {plan.tasks.map((task, index) => (
              <li key={index}><span style={styles.taskStatus}>{task.status.replace('_', ' ')}</span> {task.content}</li>
            ))}
          </ol>
          {(plan.status === 'draft' || plan.status === 'active') && (
            <button
              style={styles.primaryButton}
              disabled={isBusy || isTurnActive}
              onClick={() => void run(
                () => postJson('/plans/execute', { session_id: sessionId, revision: plan.revision }),
                'The plan changed or the agent is busy; review the latest revision.',
              )}
            >
              {plan.status === 'draft' ? 'Execute plan' : 'Continue plan'}
            </button>
          )}
        </section>
      )}

      <section style={styles.conversation}>
        {(conversation[0]?.sequence ?? 1) > 1 && (
          <button style={styles.linkButton} disabled={isBusy} onClick={() => void loadOlderMessages()}>
            Load older messages
          </button>
        )}
        {conversation.map((message) => (
          <article key={message.sequence} style={styles[message.role]}>
            {message.role === 'tool' ? (
              <>
                <strong>{message.tool_name} result</strong>
                <pre style={styles.pre}>{message.tool_output}</pre>
              </>
            ) : message.tool_name ? (
              <code>→ {message.tool_name}({message.tool_arguments})</code>
            ) : (
              <p style={styles.messageText}>{message.text}</p>
            )}
          </article>
        ))}
        {liveOutputs.map((output) => (
          <article key={output.stepExecutionId} style={styles.assistant}>
            {output.reasoning && (
              <details open={!output.text}>
                <summary style={styles.muted}>Thinking</summary>
                <p style={styles.muted}>{output.reasoning}</p>
              </details>
            )}
            <p style={styles.messageText}>{output.text || '…'}</p>
          </article>
        ))}
        {waitingTimers.map((call) => (
          <p key={call.tool_call_id} style={styles.muted}>Durable wait until {call.wait_until}. Steer to interrupt it.</p>
        ))}
        <div ref={bottomRef} />
      </section>

      {(snapshot?.pending_interrupts ?? []).map((interrupt) => (
        <section key={interrupt.interrupt_id} style={styles.interrupt}>
          {interrupt.kind === 'tool_approval' ? (
            <>
              <strong>Approve {interrupt.tool_name}?</strong>
              <pre style={styles.pre}>{interrupt.arguments}</pre>
              <div style={styles.row}>
                <button
                  style={styles.primaryButton}
                  disabled={isBusy}
                  onClick={() => void run(
                    () => postJson('/interrupts/resolve', { session_id: sessionId, interrupt_id: interrupt.interrupt_id, approved: true }),
                    'The approval is no longer pending.',
                  )}
                >
                  Approve
                </button>
                <button
                  style={styles.secondaryButton}
                  disabled={isBusy}
                  onClick={() => void run(
                    () => postJson('/interrupts/resolve', { session_id: sessionId, interrupt_id: interrupt.interrupt_id, approved: false }),
                    'The approval is no longer pending.',
                  )}
                >
                  Reject
                </button>
              </div>
            </>
          ) : (
            <>
              <strong>{interrupt.question}</strong>
              <div style={styles.row}>
                {interrupt.choices.map((choice) => (
                  <button
                    key={choice}
                    style={styles.secondaryButton}
                    disabled={isBusy}
                    onClick={() => void run(
                      () => postJson('/questions/answer', { session_id: sessionId, interrupt_id: interrupt.interrupt_id, answer: choice }),
                      'The question is no longer pending.',
                    )}
                  >
                    {choice}
                  </button>
                ))}
              </div>
              {interrupt.allows_free_text && (
                <div style={styles.row}>
                  <input
                    style={styles.input}
                    value={answers[interrupt.interrupt_id] ?? ''}
                    onChange={(event) => setAnswers({ ...answers, [interrupt.interrupt_id]: event.target.value })}
                    placeholder="Your answer"
                  />
                  <button
                    style={styles.primaryButton}
                    disabled={isBusy || !(answers[interrupt.interrupt_id] ?? '').trim()}
                    onClick={() => void run(
                      () => postJson('/questions/answer', { session_id: sessionId, interrupt_id: interrupt.interrupt_id, answer: answers[interrupt.interrupt_id] }),
                      'The question is no longer pending.',
                    )}
                  >
                    Answer
                  </button>
                </div>
              )}
              <button
                style={styles.linkButton}
                disabled={isBusy}
                onClick={() => void run(
                  () => postJson('/interrupts/resolve', { session_id: sessionId, interrupt_id: interrupt.interrupt_id, approved: false }),
                  'The question is no longer pending.',
                )}
              >
                Decline to answer
              </button>
            </>
          )}
        </section>
      ))}

      {Boolean(snapshot?.queued_messages.length || snapshot?.steered_messages.length) && (
        <section style={styles.card}>
          <h2 style={styles.sectionTitle}>Pending messages</h2>
          {snapshot?.steered_messages.map((item) => (
            <div key={item.message_id} style={styles.queueRow}>
              <span><em style={styles.muted}>Steering</em> {item.message.text}</span>
            </div>
          ))}
          {snapshot?.queued_messages.map((item) => (
            <div key={item.message_id} style={styles.queueRow}>
              <span><em style={styles.muted}>Queued{item.message.plan_mode ? ' · plan' : ''}</em> {item.message.text}</span>
              <span style={styles.row}>
                <button
                  style={styles.secondaryButton}
                  disabled={isBusy}
                  onClick={() => void run(
                    () => postJson('/messages/steer', { session_id: sessionId, message_id: item.message_id }),
                    'The message is no longer queued.',
                  )}
                >
                  Steer
                </button>
                <button
                  style={styles.linkButton}
                  disabled={isBusy}
                  onClick={() => void run(
                    () => postJson('/messages/delete', { session_id: sessionId, message_id: item.message_id }),
                    'The message is no longer queued.',
                  )}
                >
                  Delete
                </button>
              </span>
            </div>
          ))}
        </section>
      )}

      <section style={styles.composer}>
        <textarea
          style={{ ...styles.input, minHeight: 72 }}
          value={input}
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && (event.metaKey || event.ctrlKey || event.altKey)) {
              event.preventDefault();
              sendMessage(false);
            }
          }}
          placeholder="Message the agent. Try /ask, /choose, /wait, or /tool with the local demo model."
        />
        <div style={styles.row}>
          <label style={styles.muted}>
            <input type="checkbox" checked={planMode} onChange={(event) => setPlanMode(event.target.checked)} /> Plan mode
          </label>
          <button style={styles.primaryButton} disabled={isBusy || !input.trim()} onClick={() => sendMessage(false)}>
            {isTurnActive ? 'Queue' : 'Send'}
          </button>
          {isTurnActive && (
            <button style={styles.secondaryButton} disabled={isBusy || !input.trim()} onClick={() => sendMessage(true)}>
              Steer now
            </button>
          )}
        </div>
        {error && <p style={styles.error}>{error}</p>}
      </section>

      <section style={styles.card}>
        <h2 style={styles.sectionTitle}>Agent activity</h2>
        {activity.length === 0 && <p style={styles.muted}>Events appear here while the agent works.</p>}
        {activity.map((item) => (
          <div key={item.key} style={styles.activity}>
            <code>{item.type}</code> {item.detail}
          </div>
        ))}
      </section>
    </main>
  );
};

const bubble: React.CSSProperties = {
  borderRadius: 12,
  padding: '10px 14px',
  maxWidth: '80%',
  overflowWrap: 'anywhere',
};

const button: React.CSSProperties = {
  borderRadius: 8,
  padding: '8px 14px',
  fontSize: 14,
  cursor: 'pointer',
};

const styles: Record<string, React.CSSProperties> = {
  page: {
    maxWidth: 920,
    margin: '0 auto',
    padding: '24px 16px 48px',
    fontFamily: 'Inter, system-ui, sans-serif',
    color: '#1d2433',
    display: 'flex',
    flexDirection: 'column',
    gap: 16,
  },
  card: { background: '#fff', border: '1px solid #e2e6f0', borderRadius: 14, padding: 18 },
  eyebrow: { margin: 0, fontSize: 12, letterSpacing: 1, textTransform: 'uppercase', color: '#5b6b8c' },
  title: { margin: '4px 0 8px', fontSize: 28 },
  sessionTitle: { margin: '4px 0 0', fontSize: 16, fontFamily: 'ui-monospace, monospace' },
  sectionTitle: { margin: '0 0 10px', fontSize: 16 },
  muted: { color: '#5b6b8c', fontSize: 14 },
  settings: { display: 'grid', gridTemplateColumns: '160px 1fr', rowGap: 10, margin: '18px 0' },
  pills: { display: 'flex', flexWrap: 'wrap', gap: 6, margin: 0 },
  pill: { background: '#eef1f8', borderRadius: 999, padding: '3px 10px', fontSize: 13 },
  header: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, flexWrap: 'wrap' },
  headerActions: { display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' },
  status: { background: '#e8f0ff', color: '#2448a8', borderRadius: 999, padding: '4px 12px', fontSize: 13 },
  conversation: { display: 'flex', flexDirection: 'column', gap: 10 },
  user: { ...bubble, alignSelf: 'flex-end', background: '#2f5bd3', color: '#fff' },
  assistant: { ...bubble, alignSelf: 'flex-start', background: '#fff', border: '1px solid #e2e6f0' },
  tool: { ...bubble, alignSelf: 'flex-start', background: '#f4f6fb', fontSize: 13 },
  messageText: { margin: 0, whiteSpace: 'pre-wrap' },
  pre: { margin: '6px 0 0', whiteSpace: 'pre-wrap', fontSize: 12 },
  interrupt: { background: '#fff8e6', border: '1px solid #f1d58a', borderRadius: 14, padding: 16, display: 'flex', flexDirection: 'column', gap: 10 },
  tasks: { margin: '0 0 12px', paddingLeft: 20, lineHeight: 1.7 },
  taskStatus: { fontSize: 12, color: '#5b6b8c', marginRight: 6 },
  queueRow: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, padding: '6px 0' },
  composer: { display: 'flex', flexDirection: 'column', gap: 10 },
  row: { display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' },
  input: { flex: 1, border: '1px solid #cdd4e4', borderRadius: 10, padding: 10, fontSize: 15, fontFamily: 'inherit' },
  primaryButton: { ...button, background: '#2f5bd3', color: '#fff', border: 'none' },
  secondaryButton: { ...button, background: '#fff', color: '#2f5bd3', border: '1px solid #2f5bd3' },
  stopButton: { ...button, background: '#c0392b', color: '#fff', border: 'none' },
  linkButton: { ...button, background: 'none', border: 'none', color: '#5b6b8c', alignSelf: 'flex-start' },
  activity: { fontSize: 13, padding: '3px 0', borderBottom: '1px solid #f0f2f7' },
  error: { color: '#c0392b' },
};

export default App;
