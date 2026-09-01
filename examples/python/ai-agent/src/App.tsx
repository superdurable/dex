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

import React, { useEffect, useMemo, useRef, useState } from 'react';

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

interface PlanTask {
  content: string;
  status: 'pending' | 'in_progress' | 'completed';
}

interface AgentPlan {
  revision: number;
  status: 'draft' | 'active' | 'completed';
  tasks: PlanTask[];
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
  pending_user_input_call_id: string | null;
  pending_user_input_prompt: string | null;
  plan: AgentPlan | null;
  plan_execution_requested: boolean;
  available_mcp_servers: string[];
  available_tools: string[];
}

interface AgentEvent {
  kind: string;
  message: string;
  call_id: string | null;
  tool_name: string | null;
}

interface PortalProvider {
  id: string;
  label: string;
  prefix: string;
  defaultModel: string;
  environmentVariable: string | null;
}

interface PortalTool {
  name: string;
  description: string;
  requiresApproval: boolean;
  server: string | null;
}

interface PortalConfig {
  providers: PortalProvider[];
  mcpServers: string[];
  tools: PortalTool[];
  builtInTools: string[];
}

const generateWorkflowId = (): string => crypto.randomUUID();

const App: React.FC = () => {
  const queryWorkflowId = useMemo(
    () => new URLSearchParams(window.location.search).get('workflowId') ?? '',
    [],
  );
  const [workflowId, setWorkflowId] = useState(queryWorkflowId);
  const [provider, setProvider] = useState('mock');
  const [apiKey, setApiKey] = useState('');
  const [showApiKey, setShowApiKey] = useState(false);
  const [portalConfig, setPortalConfig] = useState<PortalConfig | null>(null);
  const [model, setModel] = useState('mock/dex');
  const [systemPrompt, setSystemPrompt] = useState(
    'You are a helpful durable AI agent. Use tools when they help and report tool outcomes accurately.',
  );
  const [maxContextTokens, setMaxContextTokens] = useState(32000);
  const [messageRetentionLimit, setMessageRetentionLimit] = useState(2000);
  const [mcpEnabled, setMcpEnabled] = useState(true);
  const [selectedMcpServers, setSelectedMcpServers] = useState<string[]>([]);
  const [selectedTools, setSelectedTools] = useState<string[]>([]);
  const [messages, setMessages] = useState<SequencedMessage[]>([]);
  const [description, setDescription] = useState<AgentDescription | null>(null);
  const [input, setInput] = useState('');
  const [planMode, setPlanMode] = useState(false);
  const [liveText, setLiveText] = useState('');
  const [activity, setActivity] = useState<AgentEvent[]>([]);
  const [isBusy, setIsBusy] = useState(false);
  const [error, setError] = useState('');
  const messageInputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (workflowId) return;
    void fetch(`${API_BASE}/portal`)
      .then(async (response) => {
        if (!response.ok) throw new Error(await response.text());
        return response.json() as Promise<PortalConfig>;
      })
      .then((configuration) => {
        setPortalConfig(configuration);
        setSelectedMcpServers(configuration.mcpServers);
        setSelectedTools(configuration.tools.map((tool) => tool.name));
      })
      .catch((reason) => setError(String(reason)));
  }, [workflowId]);

  useEffect(() => {
    if (description?.pending_user_input_prompt) {
      messageInputRef.current?.focus();
    }
  }, [description?.pending_user_input_prompt]);

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
          provider,
          apiKey: apiKey.trim() || null,
          model,
          systemPrompt,
          maxContextTokens,
          messageRetentionLimit,
          mcpEnabled,
          enabledMcpServers: mcpEnabled ? selectedMcpServers : [],
          enabledTools: mcpEnabled
            ? selectedTools.filter((toolName) => visibleTools.some((tool) => tool.name === toolName))
            : [],
        }),
      });
      if (!response.ok) throw new Error(await response.text());
      window.history.replaceState({}, '', `${window.location.pathname}?workflowId=${newWorkflowId}`);
      setApiKey('');
      setWorkflowId(newWorkflowId);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setIsBusy(false);
    }
  };

  const selectedProvider = portalConfig?.providers.find((item) => item.id === provider);
  const visibleTools = (portalConfig?.tools ?? []).filter(
    (tool) => tool.server === null || selectedMcpServers.includes(tool.server),
  );
  const hasValidToolSelection = !mcpEnabled
    || visibleTools.length === 0
    || visibleTools.some((tool) => selectedTools.includes(tool.name));
  const hasValidMcpSelection = !mcpEnabled
    || (portalConfig?.mcpServers.length ?? 0) === 0
    || selectedMcpServers.length > 0;

  const changeProvider = (providerId: string) => {
    const nextProvider = portalConfig?.providers.find((item) => item.id === providerId);
    setProvider(providerId);
    setModel(nextProvider?.defaultModel ?? '');
    setApiKey('');
  };

  const toggleSelection = (
    value: string,
    selected: string[],
    update: React.Dispatch<React.SetStateAction<string[]>>,
  ) => {
    update(selected.includes(value)
      ? selected.filter((item) => item !== value)
      : [...selected, value]);
  };

  const returnToPortal = () => {
    window.history.replaceState({}, '', window.location.pathname);
    setWorkflowId('');
    setDescription(null);
    setMessages([]);
    setLiveText('');
    setActivity([]);
    setError('');
  };

  const sendMessage = async () => {
    const content = input.trim();
    if (!content || !workflowId) return;
    setIsBusy(true);
    setError('');
    setInput('');
    setLiveText('');
    const requestedPlanMode = planMode;
    setPlanMode(false);
    try {
      const response = await fetch(`${API_BASE}/messages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, content, planMode: requestedPlanMode }),
      });
      if (!response.ok) throw new Error(await response.text());
      await fetchState();
    } catch (reason) {
      setError(String(reason));
      setInput(content);
      setPlanMode(requestedPlanMode);
    } finally {
      setIsBusy(false);
    }
  };

  const executePlan = async (revision: number) => {
    setIsBusy(true);
    setError('');
    try {
      const response = await fetch(`${API_BASE}/plans/execute`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, revision }),
      });
      if (!response.ok) throw new Error(await response.text());
      await fetchState();
    } catch (reason) {
      setError(String(reason));
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

  const isModelRunning = description?.status === 'calling_model'
    || description?.status === 'compacting_context'
    || description?.status === 'routing_tool'
    || description?.status === 'executing_tool';
  const latestModelEvent = [...activity]
    .reverse()
    .find((event) => event.kind.startsWith('model_'));

  if (!workflowId) {
    return (
      <main style={styles.page}>
        <section style={styles.portalShell}>
          <header style={styles.portalHero}>
            <div>
              <p style={styles.eyebrow}>Dex agent portal</p>
              <h1 style={styles.title}>Configure your AI Agent</h1>
              <p style={styles.subtitle}>
                Connect a model, choose trusted capabilities, then start a durable conversation.
              </p>
            </div>
            <div style={styles.portalSteps}>
              <span style={styles.activeStep}>1 · Configure</span>
              <span>2 · Chat</span>
            </div>
          </header>

          <div style={styles.portalGrid}>
            <section style={styles.portalCard}>
              <div style={styles.sectionHeading}>
                <span style={styles.sectionNumber}>1</span>
                <div>
                  <h2 style={styles.sectionTitle}>Model provider</h2>
                  <p style={styles.sectionCopy}>Used for this Agent session.</p>
                </div>
              </div>
              <label style={styles.label}>LLM provider</label>
              <select
                style={styles.input}
                value={provider}
                onChange={(event) => changeProvider(event.target.value)}
                disabled={!portalConfig}
              >
                {(portalConfig?.providers ?? [{ id: 'mock', label: 'Local mock' } as PortalProvider]).map((item) => (
                  <option key={item.id} value={item.id}>{item.label}</option>
                ))}
              </select>
              <label style={styles.label}>Model</label>
              <input
                style={styles.input}
                value={model}
                onChange={(event) => setModel(event.target.value)}
                disabled={provider === 'mock'}
                placeholder={provider === 'custom' ? 'provider/model-name' : 'Model name'}
              />
              <label style={styles.label}>API key</label>
              <div style={styles.secretInput}>
                <input
                  style={{ ...styles.input, paddingRight: 72 }}
                  type={showApiKey ? 'text' : 'password'}
                  value={apiKey}
                  onChange={(event) => setApiKey(event.target.value)}
                  disabled={provider === 'mock'}
                  autoComplete="off"
                  placeholder={provider === 'mock' ? 'Not needed for mock/dex' : 'Enter a key or use the Worker environment'}
                />
                {provider !== 'mock' && (
                  <button style={styles.revealButton} onClick={() => setShowApiKey((shown) => !shown)}>
                    {showApiKey ? 'Hide' : 'Show'}
                  </button>
                )}
              </div>
              <p style={styles.securityNote}>
                The key stays in this Worker process and is never written to Dex history.
                {selectedProvider?.environmentVariable && ` You may instead set ${selectedProvider.environmentVariable}.`}
              </p>
            </section>

            <section style={styles.portalCard}>
              <div style={styles.sectionHeading}>
                <span style={styles.sectionNumber}>2</span>
                <div>
                  <h2 style={styles.sectionTitle}>Agent behavior</h2>
                  <p style={styles.sectionCopy}>Set the durable session defaults.</p>
                </div>
              </div>
              <label style={styles.label}>System prompt</label>
              <textarea
                style={{ ...styles.input, minHeight: 132, resize: 'vertical' }}
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
            </section>
          </div>

          <section style={{ ...styles.portalCard, marginTop: 18 }}>
            <div style={styles.capabilityHeader}>
              <div style={styles.sectionHeading}>
                <span style={styles.sectionNumber}>3</span>
                <div>
                  <h2 style={styles.sectionTitle}>Tools and MCP</h2>
                  <p style={styles.sectionCopy}>Only capabilities registered by the Worker can be enabled.</p>
                </div>
              </div>
              <label style={styles.switchLabel}>
                <input
                  type="checkbox"
                  checked={mcpEnabled}
                  onChange={(event) => setMcpEnabled(event.target.checked)}
                />
                Enable MCP
              </label>
            </div>

            <div style={mcpEnabled ? undefined : styles.disabledSection}>
              <h3 style={styles.capabilityTitle}>MCP servers</h3>
              {portalConfig && portalConfig.mcpServers.length === 0 ? (
                <p style={styles.emptyCapability}>No MCP servers are registered. Set DEX_AGENT_MCP_CONFIG before starting the Worker.</p>
              ) : (
                <div style={styles.choiceGrid}>
                  {(portalConfig?.mcpServers ?? []).map((server) => (
                    <label key={server} style={styles.choiceCard}>
                      <input
                        type="checkbox"
                        checked={selectedMcpServers.includes(server)}
                        disabled={!mcpEnabled}
                        onChange={() => toggleSelection(server, selectedMcpServers, setSelectedMcpServers)}
                      />
                      <span><strong>{server}</strong><small style={styles.choiceMeta}>Trusted Worker configuration</small></span>
                    </label>
                  ))}
                </div>
              )}

              <h3 style={styles.capabilityTitle}>Available tools</h3>
              {visibleTools.length === 0 ? (
                <p style={styles.emptyCapability}>No MCP tools are available for the selected servers.</p>
              ) : (
                <div style={styles.toolGrid}>
                  {visibleTools.map((tool) => (
                    <label key={tool.name} style={styles.toolChoice}>
                      <input
                        type="checkbox"
                        checked={selectedTools.includes(tool.name)}
                        disabled={!mcpEnabled}
                        onChange={() => toggleSelection(tool.name, selectedTools, setSelectedTools)}
                      />
                      <span style={styles.toolCopy}>
                        <span><strong>{tool.name}</strong>{tool.requiresApproval && <em style={styles.approvalBadge}>approval</em>}</span>
                        <small>{tool.description}</small>
                      </span>
                    </label>
                  ))}
                </div>
              )}
            </div>

            <div style={styles.builtIns}>
              <strong>Built in</strong>
              {(portalConfig?.builtInTools ?? ['write_todos', 'request_user_input', 'durable_wait']).map((tool) => (
                <span key={tool} style={styles.toolPill}>{tool}</span>
              ))}
            </div>
          </section>

          <div style={styles.portalFooter}>
            <div>
              <strong>Ready to start</strong>
              <p style={styles.sectionCopy}>You can create plans and approve write tools from the Agent page.</p>
            </div>
            <button
              style={styles.launchButton}
              disabled={isBusy || !portalConfig || !model.trim() || !systemPrompt.trim() || !hasValidToolSelection || !hasValidMcpSelection}
              onClick={startAgent}
            >
              {isBusy ? 'Starting Agent…' : 'Enter AI Agent →'}
            </button>
          </div>
          {!hasValidToolSelection && <p style={styles.error}>Select at least one available MCP tool, or disable MCP.</p>}
          {!hasValidMcpSelection && <p style={styles.error}>Select at least one MCP server, or disable MCP.</p>}
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
        <button style={styles.headerButton} onClick={returnToPortal}>New Agent</button>
      </header>

      <section style={styles.chatCard}>
        <div style={styles.messages}>
          {messages.length === 0 && <p style={styles.empty}>Send a message to begin.</p>}
          {messages
            .filter(({ message }) => !isAgentPlumbing(message))
            .map(({ sequence, message }) => (
              <MessageCard key={sequence} sequence={sequence} message={message} />
            ))}
        </div>

        {(isModelRunning || liveText) && (
          <section style={styles.liveModelCard} aria-live="polite">
            <div style={styles.liveModelHeader}>
              <span style={styles.liveIndicator} />
              <strong>Live model output</strong>
              <small>{description?.model}</small>
            </div>
            <p style={styles.liveModelText}>
              {liveText || latestModelEvent?.message || 'Waiting for streamed model output…'}
            </p>
          </section>
        )}

        {description?.pending_user_input_prompt && (
          <section style={styles.userInputCard}>
            <p style={styles.eyebrow}>Agent needs your input</p>
            <strong style={styles.userInputPrompt}>{description.pending_user_input_prompt}</strong>
            <small>Your reply is delivered durably and execution resumes from this point.</small>
          </section>
        )}

        {description?.plan && (
          <PlanCard
            plan={description.plan}
            canExecute={
              description.status === 'waiting_for_message'
              && !description.plan_execution_requested
              && !description.pending_user_input_prompt
              && description.plan.status !== 'completed'
            }
            isBusy={isBusy || description.plan_execution_requested}
            onExecute={executePlan}
          />
        )}

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

        <div style={styles.composerArea}>
          <label style={styles.planModeToggle}>
            <input
              type="checkbox"
              checked={planMode}
              onChange={(event) => setPlanMode(event.target.checked)}
            />
            Plan mode
            <small style={styles.planModeHint}>Create or revise a plan without executing tools</small>
          </label>
          <div style={styles.composer}>
            <textarea
              ref={messageInputRef}
              style={{ ...styles.input, minHeight: 90, margin: 0 }}
              value={input}
              placeholder={description?.pending_user_input_prompt
                ? 'Answer the Agent’s question…'
                : planMode
                  ? 'Describe what you want the Agent to plan.'
                  : 'Message the AI Agent. Try /wait 5 demo when using mock/dex.'}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && (event.metaKey || event.ctrlKey || event.altKey)) {
                  event.preventDefault();
                  void sendMessage();
                }
              }}
            />
            <button style={styles.primaryButton} disabled={isBusy || !input.trim()} onClick={sendMessage}>
              {description?.pending_user_input_prompt ? 'Reply' : planMode ? 'Create plan' : 'Send'}
            </button>
          </div>
          <small style={styles.shortcutHint}>Send with ⌘/Ctrl + Enter or Alt + Enter · Enter adds a new line</small>
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

const PlanCard: React.FC<{
  plan: AgentPlan;
  canExecute: boolean;
  isBusy: boolean;
  onExecute: (revision: number) => Promise<void>;
}> = ({ plan, canExecute, isBusy, onExecute }) => {
  const completed = plan.tasks.filter((task) => task.status === 'completed').length;
  return (
    <section style={styles.planCard}>
      <div style={styles.planHeader}>
        <div>
          <strong>Plan · {plan.status}</strong>
          <small style={styles.planRevision}>revision {plan.revision}</small>
        </div>
        <span>{completed}/{plan.tasks.length} completed</span>
      </div>
      <ol style={styles.planList}>
        {plan.tasks.map((task, index) => (
          <li key={`${index}-${task.content}`} style={styles.planTask}>
            <span style={styles.planTaskIcon}>{planTaskIcon(task.status)}</span>
            <span>
              <strong>{task.status.replace('_', ' ')}</strong>
              <span style={task.status === 'completed' ? styles.completedTask : styles.planTaskContent}>
                {task.content}
              </span>
            </span>
          </li>
        ))}
      </ol>
      {canExecute && (
        <button
          style={styles.primaryButton}
          disabled={isBusy}
          onClick={() => void onExecute(plan.revision)}
        >
          {plan.status === 'draft' ? 'Execute plan' : 'Continue plan'}
        </button>
      )}
      {isBusy && plan.status !== 'completed' && <small>Execution request pending…</small>}
    </section>
  );
};

const INTERNAL_TOOLS = new Set(['write_todos', 'request_user_input']);

const isAgentPlumbing = (message: AgentMessage): boolean => (
  (message.tool_name !== null && INTERNAL_TOOLS.has(message.tool_name))
  || (message.tool_calls.length > 0 && message.tool_calls.every((call) => INTERNAL_TOOLS.has(call.name)))
);

const planTaskIcon = (status: PlanTask['status']): string => {
  if (status === 'completed') return '✓';
  if (status === 'in_progress') return '●';
  return '○';
};

const styles: Record<string, React.CSSProperties> = {
  page: { minHeight: '100vh', background: 'linear-gradient(145deg, #f7f8fc 0%, #eef1fb 100%)', color: '#172033', padding: '32px 18px', fontFamily: 'Inter, system-ui, sans-serif' },
  portalShell: { maxWidth: 1120, margin: '0 auto', paddingBottom: 40 },
  portalHero: { display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end', gap: 24, padding: '22px 4px 30px' },
  portalSteps: { display: 'flex', gap: 8, padding: 6, borderRadius: 999, background: '#e6e9f3', color: '#6b7280', fontSize: 13, fontWeight: 700 },
  activeStep: { padding: '8px 13px', borderRadius: 999, background: '#fff', color: '#3730a3', boxShadow: '0 2px 8px rgba(25, 33, 61, .08)' },
  portalGrid: { display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: 18 },
  portalCard: { padding: 26, borderRadius: 20, background: 'rgba(255,255,255,.94)', border: '1px solid rgba(207,214,228,.8)', boxShadow: '0 16px 48px rgba(24, 39, 75, 0.08)' },
  sectionHeading: { display: 'flex', alignItems: 'center', gap: 12 },
  sectionNumber: { display: 'grid', placeItems: 'center', width: 34, height: 34, flex: '0 0 34px', borderRadius: 11, background: '#4f46e5', color: '#fff', fontWeight: 800 },
  sectionTitle: { margin: 0, fontSize: 19 },
  sectionCopy: { margin: '4px 0 0', color: '#667085', lineHeight: 1.45 },
  secretInput: { position: 'relative' },
  revealButton: { position: 'absolute', right: 8, top: 7, border: 0, borderRadius: 7, padding: '5px 8px', background: '#eef1f7', color: '#39445a', fontWeight: 700, cursor: 'pointer' },
  securityNote: { margin: '10px 0 0', padding: '10px 12px', borderRadius: 10, background: '#eef8f4', color: '#17634a', fontSize: 13, lineHeight: 1.45 },
  capabilityHeader: { display: 'flex', justifyContent: 'space-between', gap: 18, alignItems: 'center' },
  switchLabel: { display: 'flex', alignItems: 'center', gap: 8, padding: '9px 12px', borderRadius: 10, background: '#f0f4ff', color: '#3730a3', fontWeight: 800 },
  disabledSection: { opacity: 0.45 },
  capabilityTitle: { margin: '24px 0 10px', fontSize: 14, textTransform: 'uppercase', letterSpacing: '.06em', color: '#596579' },
  choiceGrid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 10 },
  choiceCard: { display: 'flex', gap: 10, padding: 13, borderRadius: 12, border: '1px solid #dce1eb', background: '#fafbfe', cursor: 'pointer' },
  choiceMeta: { display: 'block', marginTop: 3, color: '#7b8495' },
  toolGrid: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))', gap: 10 },
  toolChoice: { display: 'flex', alignItems: 'flex-start', gap: 10, padding: 13, borderRadius: 12, border: '1px solid #dce1eb', background: '#fff', cursor: 'pointer' },
  toolCopy: { display: 'grid', gap: 4, minWidth: 0, overflowWrap: 'anywhere' },
  approvalBadge: { marginLeft: 8, padding: '2px 7px', borderRadius: 999, background: '#fff1d6', color: '#8a5514', fontSize: 11, fontStyle: 'normal' },
  emptyCapability: { margin: 0, padding: 14, borderRadius: 11, background: '#f7f8fb', color: '#667085' },
  builtIns: { display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginTop: 22, paddingTop: 18, borderTop: '1px solid #e6e9ef' },
  toolPill: { padding: '5px 9px', borderRadius: 999, background: '#e9ecff', color: '#3730a3', fontSize: 12, fontWeight: 700 },
  portalFooter: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 20, marginTop: 18, padding: '20px 24px', borderRadius: 18, background: '#172033', color: '#fff' },
  launchButton: { border: 0, borderRadius: 11, padding: '13px 20px', background: '#7c72ff', color: '#fff', fontWeight: 800, cursor: 'pointer', fontSize: 15 },
  header: { maxWidth: 960, margin: '0 auto 18px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' },
  headerButton: { border: '1px solid #cfd6e4', borderRadius: 10, padding: '9px 13px', background: '#fff', color: '#27334a', fontWeight: 700, cursor: 'pointer' },
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
  liveModelCard: { marginTop: 18, padding: 18, borderRadius: 14, background: '#161b2e', color: '#eef2ff', border: '1px solid #343b5f', boxShadow: '0 12px 30px rgba(20, 24, 45, .16)' },
  liveModelHeader: { display: 'flex', alignItems: 'center', gap: 9, color: '#c7d2fe' },
  liveIndicator: { width: 9, height: 9, borderRadius: '50%', background: '#7c72ff', boxShadow: '0 0 0 5px rgba(124,114,255,.15)' },
  liveModelText: { margin: '13px 0 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.6, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
  userInputCard: { display: 'grid', gap: 8, marginTop: 18, padding: 20, borderRadius: 14, background: '#fff8e8', border: '1px solid #f1ca74' },
  userInputPrompt: { fontSize: 18, lineHeight: 1.5 },
  planCard: { marginTop: 18, padding: 18, borderRadius: 14, background: '#f0f4ff', border: '1px solid #aebdf2' },
  planHeader: { display: 'flex', justifyContent: 'space-between', gap: 16, alignItems: 'center' },
  planRevision: { marginLeft: 8, color: '#667085' },
  planList: { listStyle: 'none', padding: 0, margin: '14px 0 0', display: 'grid', gap: 10 },
  planTask: { display: 'grid', gridTemplateColumns: '24px 1fr', gap: 8, alignItems: 'start' },
  planTaskIcon: { color: '#4f46e5', fontWeight: 700 },
  planTaskContent: { display: 'block', marginTop: 2 },
  completedTask: { display: 'block', marginTop: 2, textDecoration: 'line-through', opacity: 0.65 },
  actions: { display: 'flex', gap: 10 },
  activity: { marginTop: 18, padding: 14, borderRadius: 12, background: '#f7f8fb', color: '#4b5568' },
  composerArea: { marginTop: 22, paddingTop: 18, borderTop: '1px solid #e6e9ef' },
  planModeToggle: { display: 'flex', gap: 8, alignItems: 'center', fontWeight: 700, marginBottom: 10 },
  planModeHint: { color: '#667085', fontWeight: 400 },
  composer: { display: 'grid', gridTemplateColumns: '1fr auto', gap: 12, alignItems: 'end' },
  shortcutHint: { display: 'block', marginTop: 8, color: '#7b8495' },
  error: { padding: 12, borderRadius: 9, background: '#ffeded', color: '#a11d2b' },
  empty: { textAlign: 'center', color: '#7b8495', margin: '100px 0' },
  footer: { maxWidth: 960, margin: '14px auto', color: '#6b7280', fontSize: 13 },
};

export default App;
