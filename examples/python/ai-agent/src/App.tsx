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

import React, { useEffect, useMemo, useState } from 'react';

const API_BASE = '/products/ai-agent';

interface ToolCall {
  id: string;
  name: string;
  arguments_json: string;
}

interface AgentMessage {
  role: string;
  content: string;
  tool_calls: ToolCall[];
  tool_call_id: string | null;
  tool_name: string | null;
}

interface SequencedMessage {
  sequence: number;
  message: AgentMessage;
}

interface AgentDescription {
  status: string;
  model: string;
  system_prompt: string;
  first_retained_sequence: number;
  last_sequence: number;
  summarized_through_sequence: number;
  pending_approval_call_id: string | null;
  pending_approval_tool_name: string | null;
  pending_approval_arguments_json: string | null;
  pending_timer_call_id: string | null;
  pending_timer_duration_seconds: number | null;
  pending_timer_reason: string | null;
  available_mcp_servers: string[];
  available_tools: string[];
}

interface AgentEvent {
  kind: string;
  message: string;
  call_id: string | null;
  tool_name: string | null;
}

const generateWorkflowId = (): string => crypto.randomUUID();

const App: React.FC = () => {
  const queryWorkflowId = useMemo(
    () => new URLSearchParams(window.location.search).get('workflowId') ?? '',
    [],
  );
  const [workflowId, setWorkflowId] = useState(queryWorkflowId);
  const [model, setModel] = useState('mock/dex');
  const [systemPrompt, setSystemPrompt] = useState(
    'You are a helpful durable AI agent. Use tools when they help and report tool outcomes accurately.',
  );
  const [maxContextTokens, setMaxContextTokens] = useState(32000);
  const [messageRetentionLimit, setMessageRetentionLimit] = useState(2000);
  const [mcpServers, setMcpServers] = useState('');
  const [enabledTools, setEnabledTools] = useState('');
  const [messages, setMessages] = useState<SequencedMessage[]>([]);
  const [description, setDescription] = useState<AgentDescription | null>(null);
  const [input, setInput] = useState('');
  const [liveText, setLiveText] = useState('');
  const [activity, setActivity] = useState<AgentEvent[]>([]);
  const [isBusy, setIsBusy] = useState(false);
  const [error, setError] = useState('');

  const fetchState = async () => {
    if (!workflowId) return;
    const query = new URLSearchParams({ workflowId, limit: '200' });
    const [historyResponse, describeResponse] = await Promise.all([
      fetch(`${API_BASE}/history?${query}`),
      fetch(`${API_BASE}/describe?workflowId=${encodeURIComponent(workflowId)}`),
    ]);
    if (!historyResponse.ok) throw new Error(await historyResponse.text());
    if (!describeResponse.ok) throw new Error(await describeResponse.text());
    const history = await historyResponse.json();
    const nextDescription = await describeResponse.json();
    setMessages(history.messages);
    setDescription(nextDescription);
    if (
      nextDescription.status === 'waiting_for_message'
      && history.messages.at(-1)?.message.role === 'assistant'
    ) {
      setLiveText('');
    }
  };

  useEffect(() => {
    if (!workflowId) return;
    void fetchState().catch((reason) => setError(String(reason)));
    const interval = window.setInterval(
      () => void fetchState().catch((reason) => setError(String(reason))),
      1500,
    );
    return () => window.clearInterval(interval);
  }, [workflowId]);

  useEffect(() => {
    if (!workflowId) return;
    const controller = new AbortController();
    let assistantSource = '';
    const readStream = async (
      stream: 'assistant' | 'activity',
      receive: (payload: { value: unknown; source: string }) => void,
    ) => {
      let resumeToken = '';
      while (!controller.signal.aborted) {
        try {
          const query = new URLSearchParams({ workflowId, resumeToken, stream });
          const response = await fetch(`${API_BASE}/events?${query}`, {
            signal: controller.signal,
          });
          if (response.status === 504) continue;
          if (!response.ok) throw new Error(await response.text());
          const payload = await response.json();
          resumeToken = payload.resume_token;
          receive(payload);
        } catch (reason) {
          if (!controller.signal.aborted) setError(String(reason));
          return;
        }
      }
    };
    void readStream('assistant', (payload) => {
      if (payload.source !== assistantSource) {
        assistantSource = payload.source;
        setLiveText(String(payload.value));
        return;
      }
      setLiveText((current) => current + String(payload.value));
    });
    void readStream('activity', (payload) => {
      setActivity((current) => [...current, payload.value as AgentEvent].slice(-30));
    });
    return () => controller.abort();
  }, [workflowId]);

  const startAgent = async () => {
    setIsBusy(true);
    setError('');
    try {
      const newWorkflowId = generateWorkflowId();
      const response = await fetch(`${API_BASE}/start`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          workflowId: newWorkflowId,
          model,
          systemPrompt,
          maxContextTokens,
          messageRetentionLimit,
          enabledMcpServers: splitList(mcpServers),
          enabledTools: splitList(enabledTools),
        }),
      });
      if (!response.ok) throw new Error(await response.text());
      window.history.replaceState({}, '', `${window.location.pathname}?workflowId=${newWorkflowId}`);
      setWorkflowId(newWorkflowId);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setIsBusy(false);
    }
  };

  const sendMessage = async () => {
    const content = input.trim();
    if (!content || !workflowId) return;
    setIsBusy(true);
    setError('');
    setInput('');
    setLiveText('');
    try {
      const response = await fetch(`${API_BASE}/messages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, content }),
      });
      if (!response.ok) throw new Error(await response.text());
      await fetchState();
    } catch (reason) {
      setError(String(reason));
      setInput(content);
    } finally {
      setIsBusy(false);
    }
  };

  const decideTool = async (callId: string, approved: boolean) => {
    setIsBusy(true);
    try {
      const response = await fetch(`${API_BASE}/tool-approvals`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, callId, approved }),
      });
      if (!response.ok) throw new Error(await response.text());
      await fetchState();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setIsBusy(false);
    }
  };

  if (!workflowId) {
    return (
      <main style={styles.page}>
        <section style={styles.startCard}>
          <p style={styles.eyebrow}>Dex durable application</p>
          <h1 style={styles.title}>AI Agent</h1>
          <p style={styles.subtitle}>
            A long-running agent with durable conversation state, MCP tools, approvals, and timers.
          </p>
          <label style={styles.label}>Model</label>
          <input style={styles.input} value={model} onChange={(event) => setModel(event.target.value)} />
          <label style={styles.label}>System prompt</label>
          <textarea
            style={{ ...styles.input, minHeight: 110 }}
            value={systemPrompt}
            onChange={(event) => setSystemPrompt(event.target.value)}
          />
          <div style={styles.grid}>
            <div>
              <label style={styles.label}>Context tokens</label>
              <input
                style={styles.input}
                type="number"
                value={maxContextTokens}
                onChange={(event) => setMaxContextTokens(Number(event.target.value))}
              />
            </div>
            <div>
              <label style={styles.label}>Retained messages</label>
              <input
                style={styles.input}
                type="number"
                value={messageRetentionLimit}
                onChange={(event) => setMessageRetentionLimit(Number(event.target.value))}
              />
            </div>
          </div>
          <label style={styles.label}>MCP servers (comma separated, empty means all)</label>
          <input style={styles.input} value={mcpServers} onChange={(event) => setMcpServers(event.target.value)} />
          <label style={styles.label}>Tools (comma separated, empty means all)</label>
          <input style={styles.input} value={enabledTools} onChange={(event) => setEnabledTools(event.target.value)} />
          <button style={styles.primaryButton} disabled={isBusy} onClick={startAgent}>
            {isBusy ? 'Starting…' : 'Start AI Agent'}
          </button>
          {error && <p style={styles.error}>{error}</p>}
        </section>
      </main>
    );
  }

  return (
    <main style={styles.page}>
      <header style={styles.header}>
        <div>
          <p style={styles.eyebrow}>Durable conversation</p>
          <h1 style={{ ...styles.title, fontSize: 30 }}>AI Agent</h1>
        </div>
        <div style={styles.status}>
          <strong>{description?.status ?? 'loading'}</strong>
          <span>{description?.model ?? ''}</span>
        </div>
      </header>

      <section style={styles.chatCard}>
        <div style={styles.messages}>
          {messages.length === 0 && <p style={styles.empty}>Send a message to begin.</p>}
          {messages.map(({ sequence, message }) => (
            <MessageCard key={sequence} sequence={sequence} message={message} />
          ))}
          {liveText && (
            <div style={{ ...styles.message, ...styles.assistantMessage }}>
              <strong>Assistant · streaming</strong>
              <p style={styles.messageText}>{liveText}</p>
            </div>
          )}
        </div>

        {description?.pending_approval_call_id && (
          <div style={styles.approvalCard}>
            <strong>Approve tool: {description.pending_approval_tool_name}</strong>
            <pre style={styles.pre}>{description.pending_approval_arguments_json}</pre>
            <p>This operation may change an external system or have an unknown effect.</p>
            <div style={styles.actions}>
              <button
                style={styles.primaryButton}
                disabled={isBusy}
                onClick={() => void decideTool(description.pending_approval_call_id!, true)}
              >
                Approve
              </button>
              <button
                style={styles.secondaryButton}
                disabled={isBusy}
                onClick={() => void decideTool(description.pending_approval_call_id!, false)}
              >
                Reject
              </button>
            </div>
          </div>
        )}

        {description?.pending_timer_call_id && (
          <div style={styles.timerCard}>
            <strong>Durable timer · {description.pending_timer_duration_seconds}s</strong>
            <p>{description.pending_timer_reason}</p>
            <small>Send a new message to interrupt this wait.</small>
          </div>
        )}

        {activity.length > 0 && (
          <details style={styles.activity}>
            <summary>Agent activity ({activity.length})</summary>
            {activity.map((event, index) => (
              <p key={`${event.kind}-${index}`}>
                <strong>{event.kind}</strong> {event.tool_name ? `· ${event.tool_name}` : ''}: {event.message}
              </p>
            ))}
          </details>
        )}

        <div style={styles.composer}>
          <textarea
            style={{ ...styles.input, minHeight: 90, margin: 0 }}
            value={input}
            placeholder="Message the AI Agent. Try /wait 5 demo when using mock/dex."
            onChange={(event) => setInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                void sendMessage();
              }
            }}
          />
          <button style={styles.primaryButton} disabled={isBusy || !input.trim()} onClick={sendMessage}>
            Send
          </button>
        </div>
        {error && <p style={styles.error}>{error}</p>}
      </section>

      <footer style={styles.footer}>
        Flow ID: <code>{workflowId}</code>
        {description && <> · summarized through {description.summarized_through_sequence}</>}
      </footer>
    </main>
  );
};

const MessageCard: React.FC<{ sequence: number; message: AgentMessage }> = ({ sequence, message }) => {
  const messageStyle = message.role === 'user'
    ? styles.userMessage
    : message.role === 'tool'
      ? styles.toolMessage
      : styles.assistantMessage;
  return (
    <div style={{ ...styles.message, ...messageStyle }}>
      <strong>{message.role === 'user' ? 'You' : message.role === 'tool' ? `Tool · ${message.tool_name}` : 'Assistant'}</strong>
      <small style={styles.sequence}>#{sequence}</small>
      {message.content && <p style={styles.messageText}>{message.content}</p>}
      {message.tool_calls.map((call) => (
        <div key={call.id} style={styles.toolCall}>
          <strong>Tool request · {call.name}</strong>
          <pre style={styles.pre}>{call.arguments_json}</pre>
        </div>
      ))}
    </div>
  );
};

const splitList = (value: string): string[] => value
  .split(',')
  .map((item) => item.trim())
  .filter(Boolean);

const styles: Record<string, React.CSSProperties> = {
  page: { minHeight: '100vh', background: '#f5f7fb', color: '#172033', padding: '32px 18px', fontFamily: 'Inter, system-ui, sans-serif' },
  startCard: { maxWidth: 720, margin: '40px auto', padding: 36, borderRadius: 20, background: '#fff', boxShadow: '0 18px 60px rgba(24, 39, 75, 0.10)' },
  header: { maxWidth: 960, margin: '0 auto 18px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' },
  title: { margin: '4px 0 10px', fontSize: 44 },
  eyebrow: { margin: 0, color: '#5c6ac4', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 12 },
  subtitle: { color: '#596579', lineHeight: 1.6 },
  label: { display: 'block', fontWeight: 700, margin: '18px 0 7px' },
  input: { boxSizing: 'border-box', width: '100%', border: '1px solid #cfd6e4', borderRadius: 10, padding: '11px 13px', font: 'inherit' },
  grid: { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 },
  primaryButton: { border: 0, borderRadius: 10, padding: '11px 18px', background: '#4f46e5', color: '#fff', fontWeight: 700, cursor: 'pointer', marginTop: 18 },
  secondaryButton: { border: '1px solid #cfd6e4', borderRadius: 10, padding: '11px 18px', background: '#fff', color: '#27334a', fontWeight: 700, cursor: 'pointer', marginTop: 18 },
  status: { display: 'flex', flexDirection: 'column', alignItems: 'flex-end', padding: '9px 14px', borderRadius: 12, background: '#e9ecff', color: '#3730a3' },
  chatCard: { maxWidth: 960, margin: '0 auto', padding: 22, background: '#fff', borderRadius: 20, boxShadow: '0 18px 60px rgba(24, 39, 75, 0.08)' },
  messages: { display: 'flex', flexDirection: 'column', gap: 14, minHeight: 320 },
  message: { maxWidth: '82%', borderRadius: 15, padding: '13px 16px', position: 'relative' },
  userMessage: { alignSelf: 'flex-end', background: '#4f46e5', color: '#fff' },
  assistantMessage: { alignSelf: 'flex-start', background: '#eef1f7' },
  toolMessage: { alignSelf: 'stretch', maxWidth: '100%', background: '#fff7df', border: '1px solid #f4d98b' },
  messageText: { margin: '8px 0 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.55 },
  sequence: { marginLeft: 8, opacity: 0.65 },
  toolCall: { marginTop: 10, padding: 10, background: 'rgba(255,255,255,.7)', borderRadius: 9 },
  pre: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', margin: '8px 0', fontSize: 12 },
  approvalCard: { marginTop: 18, padding: 18, borderRadius: 14, background: '#fff3e5', border: '1px solid #f4bb77' },
  timerCard: { marginTop: 18, padding: 18, borderRadius: 14, background: '#e9f8f2', border: '1px solid #8dd7bd' },
  actions: { display: 'flex', gap: 10 },
  activity: { marginTop: 18, padding: 14, borderRadius: 12, background: '#f7f8fb', color: '#4b5568' },
  composer: { display: 'grid', gridTemplateColumns: '1fr auto', gap: 12, alignItems: 'end', marginTop: 22, paddingTop: 18, borderTop: '1px solid #e6e9ef' },
  error: { padding: 12, borderRadius: 9, background: '#ffeded', color: '#a11d2b' },
  empty: { textAlign: 'center', color: '#7b8495', margin: '100px 0' },
  footer: { maxWidth: 960, margin: '14px auto', color: '#6b7280', fontSize: 13 },
};

export default App;
