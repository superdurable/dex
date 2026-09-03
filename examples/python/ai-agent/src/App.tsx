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

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';

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
  created_at: string;
}

interface QueuedMessage {
  message_id: string;
  value: {
    content: string;
    plan_mode: boolean;
  };
  optimistic?: boolean;
  submittedAfterSequence?: number;
  knownMessageIdsAtSubmit?: string[];
}

interface MessageQueue {
  queued: QueuedMessage[];
  steered: QueuedMessage[];
}

const hasSameQueuedValue = (left: QueuedMessage, right: QueuedMessage): boolean => (
  left.value.content === right.value.content
  && left.value.plan_mode === right.value.plan_mode
);

const reconcileOptimisticMessages = (
  current: QueuedMessage[],
  canonical: QueuedMessage[],
  history: SequencedMessage[],
): QueuedMessage[] => {
  const matchedCanonicalIds = new Set<string>();
  const matchedHistorySequences = new Set<number>();
  const optimistic = current.filter((message) => message.optimistic).filter((message) => {
    const canonicalMatch = canonical.find((candidate) => (
      !matchedCanonicalIds.has(candidate.message_id)
      && !(message.knownMessageIdsAtSubmit ?? []).includes(candidate.message_id)
      && hasSameQueuedValue(message, candidate)
    ));
    if (canonicalMatch) {
      matchedCanonicalIds.add(canonicalMatch.message_id);
      return false;
    }
    const historyMatch = history.find((candidate) => (
      !matchedHistorySequences.has(candidate.sequence)
      && candidate.sequence > (message.submittedAfterSequence ?? 0)
      && candidate.message.role === 'user'
      && candidate.message.content === message.value.content
    ));
    if (historyMatch) {
      matchedHistorySequences.add(historyMatch.sequence);
      return false;
    }
    return true;
  });
  return [...canonical, ...optimistic];
};

interface ThinkingEntry {
  source: string;
  content: string;
  createdTime: string;
  isStreaming: boolean;
  isExpanded: boolean;
  isManuallyExpanded: boolean;
}

interface ActivityEntry {
  source: string;
  createdTime: string;
  event: AgentEvent;
}

interface LiveResponseEntry {
  source: string;
  content: string;
  createdTime: string;
}

type TimelineEntry =
  | { kind: 'message'; id: string; createdTime: string; sequence: number; message: AgentMessage }
  | { kind: 'thinking'; id: string; createdTime: string; entry: ThinkingEntry; index: number }
  | { kind: 'response'; id: string; createdTime: string; entry: LiveResponseEntry }
  | { kind: 'activity'; id: string; createdTime: string; entry: ActivityEntry; index: number };

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
  pending_user_input_choices: string[];
  plan: AgentPlan | null;
  plan_execution_requested: boolean;
  pending_queued_message_count: number;
  pending_steered_message_count: number;
  available_mcp_servers: string[];
  available_tools: string[];
}

interface AgentEvent {
  kind: string;
  message: string;
  call_id: string | null;
  tool_name: string | null;
}

interface AgentFlowStatus {
  status: string;
  run_id: string;
  error_type: string | null;
  error_message: string | null;
}

interface PortalProvider {
  id: string;
  label: string;
  prefix: string;
  defaultModel: string;
  environmentVariable: string | null;
  isConfigured: boolean;
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
  const [hasLoadedConversation, setHasLoadedConversation] = useState(false);
  const [messageQueue, setMessageQueue] = useState<MessageQueue>({ queued: [], steered: [] });
  const [description, setDescription] = useState<AgentDescription | null>(null);
  const [flowStatus, setFlowStatus] = useState<AgentFlowStatus | null>(null);
  const [input, setInput] = useState('');
  const [planMode, setPlanMode] = useState(false);
  const [userInputAnswer, setUserInputAnswer] = useState('');
  const [pressedInputChoice, setPressedInputChoice] = useState<string | null>(null);
  const [selectedInputChoice, setSelectedInputChoice] = useState<string | null>(null);
  const [isInputSubmitPressed, setIsInputSubmitPressed] = useState(false);
  const [thinkingEntries, setThinkingEntries] = useState<ThinkingEntry[]>([]);
  const [liveResponse, setLiveResponse] = useState<LiveResponseEntry | null>(null);
  const [activity, setActivity] = useState<ActivityEntry[]>([]);
  const [isBusy, setIsBusy] = useState(false);
  const [queueMutation, setQueueMutation] = useState('');
  const [pressedQueueAction, setPressedQueueAction] = useState('');
  const [error, setError] = useState('');
  const messageInputRef = useRef<HTMLTextAreaElement>(null);
  const conversationScrollRef = useRef<HTMLDivElement>(null);
  const timelineEntryRefs = useRef(new Map<string, HTMLDivElement>());
  const currentTurnSequenceRef = useRef<number | null>(null);
  const hasInitializedConversationViewportRef = useRef(false);
  const isFollowingTimelineRef = useRef(true);
  const isProgrammaticScrollRef = useRef(false);
  const userInputRef = useRef<HTMLTextAreaElement>(null);
  const stateFetchSequenceRef = useRef(0);
  const descriptionStatusRef = useRef('');
  const eventRefreshTimerRef = useRef<number | null>(null);
  const completedThinkingSourcesRef = useRef(new Set<string>());

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
    setHasLoadedConversation(false);
    hasInitializedConversationViewportRef.current = false;
    currentTurnSequenceRef.current = null;
    isFollowingTimelineRef.current = true;
  }, [workflowId]);

  useEffect(() => {
    if (description?.pending_user_input_prompt) {
      setUserInputAnswer('');
      setPressedInputChoice(null);
      setSelectedInputChoice(null);
      setIsInputSubmitPressed(false);
      window.requestAnimationFrame(() => {
        userInputRef.current?.focus();
      });
    }
  }, [description?.pending_user_input_call_id]);

  const fetchState = useCallback(async () => {
    if (!workflowId) return;
    const fetchSequence = ++stateFetchSequenceRef.current;
    const query = new URLSearchParams({ workflowId, limit: '200' });
    const [historyResponse, describeResponse, queueResponse, statusResponse] = await Promise.all([
      fetch(`${API_BASE}/history?${query}`),
      fetch(`${API_BASE}/describe?workflowId=${encodeURIComponent(workflowId)}`),
      fetch(`${API_BASE}/message-queue?workflowId=${encodeURIComponent(workflowId)}`),
      fetch(`${API_BASE}/status?workflowId=${encodeURIComponent(workflowId)}`),
    ]);
    if (!statusResponse.ok) throw new Error(await statusResponse.text());
    const nextFlowStatus = await statusResponse.json() as AgentFlowStatus;
    if (fetchSequence !== stateFetchSequenceRef.current) return;
    setFlowStatus(nextFlowStatus);
    if (!historyResponse.ok) throw new Error(await historyResponse.text());
    if (!describeResponse.ok) throw new Error(await describeResponse.text());
    if (!queueResponse.ok) throw new Error(await queueResponse.text());
    const history = await historyResponse.json();
    const nextDescription = await describeResponse.json();
    const nextQueue = await queueResponse.json() as MessageQueue;
    if (fetchSequence !== stateFetchSequenceRef.current) return;
    setMessages(history.messages);
    setHasLoadedConversation(true);
    setDescription(nextDescription);
    descriptionStatusRef.current = nextDescription.status;
    setMessageQueue((current) => ({
      queued: reconcileOptimisticMessages(current.queued, nextQueue.queued, history.messages),
      steered: nextQueue.steered,
    }));
    if (
      nextDescription.status === 'waiting_for_message'
      && history.messages.at(-1)?.message.role === 'assistant'
    ) {
      setLiveResponse(null);
    }
  }, [workflowId]);

  useEffect(() => {
    if (!workflowId) return;
    const refresh = () => void fetchState().catch((reason) => setError(String(reason)));
    const refreshAfterVisibilityChange = () => {
      if (document.visibilityState === 'visible') refresh();
    };
    const fallbackRefresh = () => {
      const isAgentActive = descriptionStatusRef.current !== ''
        && descriptionStatusRef.current !== 'waiting_for_message';
      if (document.visibilityState === 'visible' || isAgentActive) refresh();
    };
    refresh();
    const interval = window.setInterval(fallbackRefresh, 8000);
    window.addEventListener('focus', refresh);
    window.addEventListener('online', refresh);
    document.addEventListener('visibilitychange', refreshAfterVisibilityChange);
    return () => {
      window.clearInterval(interval);
      window.removeEventListener('focus', refresh);
      window.removeEventListener('online', refresh);
      document.removeEventListener('visibilitychange', refreshAfterVisibilityChange);
    };
  }, [workflowId, fetchState]);

  useEffect(() => {
    if (!workflowId) return;
    const controller = new AbortController();
    let reasoningSource = '';
    let assistantSource = '';
    const finishThinking = (source: string) => {
      if (!source) return;
      completedThinkingSourcesRef.current.add(source);
      setThinkingEntries((current) => current.map((entry) => (
        entry.source === source
          ? {
            ...entry,
            isStreaming: false,
            isExpanded: entry.isManuallyExpanded,
          }
          : entry
      )));
    };
    const requestEventRefresh = () => {
      if (eventRefreshTimerRef.current !== null) return;
      eventRefreshTimerRef.current = window.setTimeout(() => {
        eventRefreshTimerRef.current = null;
        void fetchState().catch((reason) => setError(String(reason)));
      }, 250);
    };
    const readStream = async (
      stream: 'reasoning' | 'assistant' | 'activity',
      receive: (payload: { value: unknown; source: string; created_time: string }) => void,
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
          if (controller.signal.aborted) return;
          setError(String(reason));
          await new Promise<void>((resolve) => window.setTimeout(resolve, 1000));
        }
      }
    };
    void readStream('reasoning', (payload) => {
      const content = String(payload.value);
      if (!content) return;
      if (payload.source !== reasoningSource) {
        finishThinking(reasoningSource);
        reasoningSource = payload.source;
        const isStreaming = !completedThinkingSourcesRef.current.has(payload.source);
        setThinkingEntries((current) => [
          ...current,
          {
            source: payload.source,
            content,
            createdTime: payload.created_time,
            isStreaming,
            isExpanded: isStreaming,
            isManuallyExpanded: false,
          },
        ]);
        return;
      }
      setThinkingEntries((current) => current.map((entry) => (
        entry.source === payload.source
          ? {
            ...entry,
            content: entry.content + content,
            isStreaming: !completedThinkingSourcesRef.current.has(payload.source),
            isExpanded: completedThinkingSourcesRef.current.has(payload.source)
              ? entry.isExpanded
              : true,
          }
          : entry
      )));
    });
    void readStream('assistant', (payload) => {
      if (payload.source !== assistantSource) {
        assistantSource = payload.source;
        setLiveResponse({
          source: payload.source,
          content: String(payload.value),
          createdTime: payload.created_time,
        });
        return;
      }
      setLiveResponse((current) => current && current.source === payload.source
        ? { ...current, content: current.content + String(payload.value) }
        : current);
    });
    void readStream('activity', (payload) => {
      const event = payload.value as AgentEvent;
      if (event.kind === 'model_completed') {
        finishThinking(payload.source);
        setLiveResponse((current) => current?.source === payload.source ? null : current);
      }
      setActivity((current) => [
        ...current,
        { source: payload.source, createdTime: payload.created_time, event },
      ].slice(-30));
      requestEventRefresh();
    });
    return () => {
      controller.abort();
      if (eventRefreshTimerRef.current !== null) {
        window.clearTimeout(eventRefreshTimerRef.current);
        eventRefreshTimerRef.current = null;
      }
    };
  }, [workflowId, fetchState]);

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
    stateFetchSequenceRef.current += 1;
    descriptionStatusRef.current = '';
    window.history.replaceState({}, '', window.location.pathname);
    setWorkflowId('');
    setDescription(null);
    setMessages([]);
    setHasLoadedConversation(false);
    setMessageQueue({ queued: [], steered: [] });
    setThinkingEntries([]);
    completedThinkingSourcesRef.current.clear();
    setLiveResponse(null);
    setActivity([]);
    setError('');
    currentTurnSequenceRef.current = null;
    isFollowingTimelineRef.current = true;
  };

  const sendMessage = async () => {
    const content = input.trim();
    if (!content || !workflowId) return;
    setIsBusy(true);
    setError('');
    setInput('');
    setLiveResponse(null);
    const requestedPlanMode = planMode;
    const optimisticId = `optimistic-${crypto.randomUUID()}`;
    setMessageQueue((current) => ({
      ...current,
      queued: [
        ...current.queued,
        {
          message_id: optimisticId,
          value: { content, plan_mode: requestedPlanMode },
          optimistic: true,
          submittedAfterSequence: Math.max(
            description?.last_sequence ?? 0,
            messages.at(-1)?.sequence ?? 0,
          ),
          knownMessageIdsAtSubmit: current.queued
            .filter((message) => !message.optimistic)
            .map((message) => message.message_id),
        },
      ],
    }));
    setPlanMode(false);
    try {
      const response = await fetch(`${API_BASE}/messages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, content, planMode: requestedPlanMode }),
      });
      if (!response.ok) throw new Error(await response.text());
    } catch (reason) {
      setMessageQueue((current) => ({
        ...current,
        queued: current.queued.filter((message) => message.message_id !== optimisticId),
      }));
      setError(String(reason));
      setInput(content);
      setPlanMode(requestedPlanMode);
      setIsBusy(false);
      return;
    }
    await fetchState().catch((reason) => {
      setError(`Message accepted; queue refresh failed: ${String(reason)}`);
    });
    setIsBusy(false);
  };

  const mutateQueuedMessage = async (
    message: QueuedMessage,
    action: 'delete' | 'steer' | 'edit',
  ) => {
    if (!workflowId || message.optimistic) return;
    const mutationKey = `${action}:${message.message_id}`;
    setQueueMutation(mutationKey);
    setError('');
    try {
      const response = await fetch(
        `${API_BASE}/message-queue/${action === 'edit' ? 'delete' : action}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ workflowId, messageId: message.message_id }),
        },
      );
      if (!response.ok) throw new Error(await response.text());
    } catch (reason) {
      setError(String(reason));
      await fetchState().catch(() => undefined);
      setQueueMutation('');
      return;
    }
    if (action !== 'steer') {
      setMessageQueue((current) => ({
        ...current,
        queued: current.queued.filter(
          (queuedMessage) => queuedMessage.message_id !== message.message_id,
        ),
      }));
    }
    if (action === 'edit') {
      setInput(message.value.content);
      setPlanMode(message.value.plan_mode);
      window.requestAnimationFrame(() => messageInputRef.current?.focus());
    }
    await fetchState().catch((reason) => {
      setError(`Queue updated; refresh failed: ${String(reason)}`);
    });
    setQueueMutation('');
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

  const answerUserInput = async (answer: string) => {
    const content = answer.trim();
    if (!content || !workflowId) return;
    if ((description?.pending_user_input_choices ?? []).includes(content)) {
      setSelectedInputChoice(content);
    }
    setIsBusy(true);
    setError('');
    try {
      const response = await fetch(`${API_BASE}/messages`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workflowId, content, planMode: false }),
      });
      if (!response.ok) throw new Error(await response.text());
      setUserInputAnswer('');
      await fetchState();
    } catch (reason) {
      setSelectedInputChoice(null);
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

  const isModelRunning = description?.status === 'calling_model';
  useEffect(() => {
    if (isModelRunning) return;
    setThinkingEntries((current) => current.map((entry) => (
      entry.isStreaming
        ? {
          ...entry,
          isStreaming: false,
          isExpanded: entry.isManuallyExpanded,
        }
        : entry
    )));
  }, [isModelRunning]);

  const toggleThinking = (source: string) => {
    setThinkingEntries((current) => current.map((entry) => {
      if (entry.source !== source) return entry;
      const isExpanded = !entry.isExpanded;
      return {
        ...entry,
        isExpanded,
        isManuallyExpanded: isExpanded,
      };
    }));
  };
  const timeline = useMemo(
    () => buildTimeline(messages, thinkingEntries, liveResponse, activity),
    [messages, thinkingEntries, liveResponse, activity],
  );
  useEffect(() => {
    if (!workflowId || !hasLoadedConversation) return;
    const latestUserMessage = [...timeline].reverse().find(
      (entry) => entry.kind === 'message' && entry.message.role === 'user',
    );
    if (!hasInitializedConversationViewportRef.current) {
      hasInitializedConversationViewportRef.current = true;
      currentTurnSequenceRef.current = latestUserMessage?.kind === 'message'
        ? latestUserMessage.sequence
        : null;
      isFollowingTimelineRef.current = true;
      const frame = window.requestAnimationFrame(() => {
        const container = conversationScrollRef.current;
        if (container) container.scrollTo({ top: container.scrollHeight, behavior: 'auto' });
      });
      return () => window.cancelAnimationFrame(frame);
    }
    if (timeline.length === 0) return;
    const isNewTurn = latestUserMessage?.kind === 'message'
      && latestUserMessage.sequence !== currentTurnSequenceRef.current;
    if (!isNewTurn && !isFollowingTimelineRef.current) return;

    const frame = window.requestAnimationFrame(() => {
      const container = conversationScrollRef.current;
      const targetEntry = isNewTurn ? latestUserMessage : timeline.at(-1);
      if (!container || !targetEntry) return;
      const target = timelineEntryRefs.current.get(targetEntry.id);
      if (!target) return;
      const containerRect = container.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      const targetTop = container.scrollTop + targetRect.top - containerRect.top;
      const targetScrollTop = isNewTurn
        ? targetTop
        : Math.max(container.scrollTop, targetTop + targetRect.height - container.clientHeight + 24);
      isProgrammaticScrollRef.current = true;
      container.scrollTo({ top: targetScrollTop, behavior: isNewTurn ? 'smooth' : 'auto' });
      window.requestAnimationFrame(() => {
        isProgrammaticScrollRef.current = false;
      });
      if (isNewTurn) {
        currentTurnSequenceRef.current = latestUserMessage.sequence;
        isFollowingTimelineRef.current = true;
      }
    });
    return () => window.cancelAnimationFrame(frame);
  }, [workflowId, hasLoadedConversation, timeline]);

  const stopFollowingTimeline = () => {
    if (!isProgrammaticScrollRef.current) isFollowingTimelineRef.current = false;
  };

  const setTimelineEntryRef = (entryId: string, element: HTMLDivElement | null) => {
    if (element) timelineEntryRefs.current.set(entryId, element);
    else timelineEntryRefs.current.delete(entryId);
  };
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
            <nav style={styles.portalSteps} aria-label="AI Agent setup progress">
              <span style={{ ...styles.portalStep, ...styles.activeStep }} aria-current="step">
                <span style={{ ...styles.portalStepNumber, ...styles.activeStepNumber }}>1</span>
                Configure
              </span>
              <span style={styles.portalStepDivider} aria-hidden="true" />
              <span style={styles.portalStep}>
                <span style={styles.portalStepNumber}>2</span>
                Chat
              </span>
            </nav>
          </header>

          <div style={styles.portalFooter}>
            <div style={styles.portalFooterCopy}>
              <strong>Ready to start</strong>
              <p style={styles.sectionCopy}>You can create plans and approve write tools from the Agent page.</p>
            </div>
            <button
              style={styles.launchButton}
              disabled={isBusy || !portalConfig || !selectedProvider?.isConfigured || !model.trim() || !systemPrompt.trim() || !hasValidToolSelection || !hasValidMcpSelection}
              onClick={startAgent}
            >
              {isBusy ? 'Starting Agent…' : 'Start AI Agent →'}
            </button>
          </div>
          {!hasValidToolSelection && <p style={styles.error}>Select at least one available MCP tool, or disable MCP.</p>}
          {!hasValidMcpSelection && <p style={styles.error}>Select at least one MCP server, or disable MCP.</p>}
          {error && <p style={styles.error}>{error}</p>}

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
                {(portalConfig?.providers ?? [{ id: 'mock', label: 'Local mock', isConfigured: true } as PortalProvider]).map((item) => (
                  <option key={item.id} value={item.id} disabled={!item.isConfigured}>
                    {item.label}{item.isConfigured ? '' : ' — not configured'}
                  </option>
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
              <p style={styles.securityNote}>
                {selectedProvider?.environmentVariable
                  ? `${selectedProvider.label} uses ${selectedProvider.environmentVariable} from the Worker environment.`
                  : 'Local mock does not require credentials.'}
              </p>
              {(portalConfig?.providers ?? []).some((item) => !item.isConfigured) && (
                <div style={styles.providerSetupNote}>
                  <strong>Enable another provider</strong>
                  <p>Add its key to <code>examples/.env</code>, then restart the Python examples:</p>
                  {(portalConfig?.providers ?? []).filter((item) => !item.isConfigured).map((item) => (
                    <code key={item.id}>{item.environmentVariable}=...</code>
                  ))}
                </div>
              )}
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

        </section>
      </main>
    );
  }

  const hasPendingMessages = messageQueue.queued.length > 0 || messageQueue.steered.length > 0;

  return (
    <main style={{ ...styles.page, ...styles.chatPage }}>
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

      {flowStatus && ['failed', 'canceled', 'terminated', 'server_side_timeout_internal_only'].includes(flowStatus.status) && (
        <section style={styles.flowFailureCard}>
          <strong>Flow {flowStatus.status.split('_').join(' ')}</strong>
          <p style={styles.flowFailureText}>
            {flowStatus.error_message || 'The Agent run ended before it could complete.'}
          </p>
        </section>
      )}

      <section style={styles.chatCard}>
        <div
          ref={conversationScrollRef}
          style={styles.conversationScroll}
          onWheel={stopFollowingTimeline}
          onTouchStart={stopFollowingTimeline}
        >
          <div style={styles.messages}>
            {timeline.length === 0 && <p style={styles.empty}>Send a message to begin.</p>}
            {timeline.map((entry) => (
              <div
                key={entry.id}
                ref={(element) => setTimelineEntryRef(entry.id, element)}
                style={styles.timelineEntry}
              >
                <TimelineEntryCard
                  entry={entry}
                  model={description?.model ?? ''}
                  onToggleThinking={toggleThinking}
                />
              </div>
            ))}
          </div>

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
            <small>Queue a message, then choose Steer to interrupt this wait.</small>
          </div>
          )}

        </div>

        <div style={styles.composerArea}>
          <section
            style={{
              ...styles.queueArea,
              ...(!hasPendingMessages ? styles.queueAreaCollapsed : {}),
            }}
            aria-label="Pending user messages"
          >
            <div
              style={{
                ...styles.queueHeader,
                ...(!hasPendingMessages ? styles.queueHeaderCollapsed : {}),
              }}
            >
              <div>
                <strong>Message queue</strong>
                {hasPendingMessages && (
                  <small style={styles.queueHint}>
                    Queued messages wait for the current Agent loop. Steer applies one at the next safe boundary.
                  </small>
                )}
              </div>
              <span>{messageQueue.queued.length} queued · {messageQueue.steered.length} steered</span>
            </div>
            {[...messageQueue.steered, ...messageQueue.queued].map((message) => {
              const isSteered = messageQueue.steered.some(
                (item) => item.message_id === message.message_id,
              );
              return (
                <div
                  key={message.message_id}
                  style={{ ...styles.queueItem, ...(isSteered ? styles.steeringItem : {}) }}
                >
                  <div style={styles.queueContent}>
                    <span style={isSteered ? styles.steeringBadge : styles.queuedBadge}>
                      {isSteered ? 'Steering' : message.optimistic ? 'Submitting' : 'Queued'}
                    </span>
                    <p style={styles.queueMessage}>{message.value.content}</p>
                    {message.value.plan_mode && <small>Plan mode</small>}
                  </div>
                  {!isSteered && (
                    <div style={styles.queueActions}>
                      {(['edit', 'delete', 'steer'] as const).map((action) => {
                        const actionKey = `${action}:${message.message_id}`;
                        const isMutating = queueMutation === actionKey;
                        const isPressed = pressedQueueAction === actionKey;
                        return (
                          <button
                            key={action}
                            style={{
                              ...styles.queueButton,
                              ...(action === 'steer' ? styles.steerButton : {}),
                              ...(isPressed ? styles.queueButtonPressed : {}),
                            }}
                            disabled={Boolean(queueMutation) || Boolean(message.optimistic)}
                            onPointerDown={() => setPressedQueueAction(actionKey)}
                            onPointerUp={() => setPressedQueueAction('')}
                            onPointerCancel={() => setPressedQueueAction('')}
                            onPointerLeave={() => setPressedQueueAction('')}
                            onClick={() => void mutateQueuedMessage(message, action)}
                          >
                            {isMutating ? `${action}…` : action[0]!.toUpperCase() + action.slice(1)}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </section>

          {description?.pending_user_input_prompt ? (
            <section
              style={{ ...styles.inputCard, ...styles.composerInputCard }}
              role="group"
              aria-labelledby="agent-input-title"
            >
              <div style={styles.inputCardHeader}>
                <span style={styles.inputIndicator}>?</span>
                <div>
                  <p style={styles.eyebrow}>Agent needs your input</p>
                  <h2 id="agent-input-title" style={styles.inputPrompt}>
                    {description.pending_user_input_prompt}
                  </h2>
                </div>
              </div>
              {(description.pending_user_input_choices ?? []).length > 0 ? (
                <div style={styles.inputChoices}>
                  {description.pending_user_input_choices.map((choice) => {
                    const isPressed = pressedInputChoice === choice;
                    const isSelected = selectedInputChoice === choice;
                    return (
                      <button
                        key={choice}
                        style={{
                          ...styles.inputChoice,
                          ...(isPressed ? styles.inputChoicePressed : {}),
                          ...(isSelected ? styles.inputChoiceSelected : {}),
                        }}
                        disabled={isBusy}
                        aria-pressed={isSelected}
                        onPointerDown={() => setPressedInputChoice(choice)}
                        onPointerUp={() => setPressedInputChoice(null)}
                        onPointerCancel={() => setPressedInputChoice(null)}
                        onPointerLeave={() => setPressedInputChoice(null)}
                        onClick={() => void answerUserInput(choice)}
                      >
                        <span>{isSelected ? '✓' : '→'}</span>
                        <span>{choice}</span>
                      </button>
                    );
                  })}
                </div>
              ) : (
                <div style={styles.inputAnswerComposer}>
                  <textarea
                    ref={userInputRef}
                    style={{ ...styles.input, minHeight: 96, resize: 'vertical' }}
                    value={userInputAnswer}
                    placeholder="Type your answer…"
                    onChange={(event) => setUserInputAnswer(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' && (event.metaKey || event.ctrlKey || event.altKey)) {
                        event.preventDefault();
                        void answerUserInput(userInputAnswer);
                      }
                    }}
                  />
                  <button
                    style={{
                      ...styles.primaryButton,
                      ...styles.inputSubmitButton,
                      ...(isInputSubmitPressed ? styles.inputSubmitButtonPressed : {}),
                    }}
                    disabled={isBusy || !userInputAnswer.trim()}
                    onPointerDown={() => setIsInputSubmitPressed(true)}
                    onPointerUp={() => setIsInputSubmitPressed(false)}
                    onPointerCancel={() => setIsInputSubmitPressed(false)}
                    onPointerLeave={() => setIsInputSubmitPressed(false)}
                    onClick={() => void answerUserInput(userInputAnswer)}
                  >
                    {isBusy ? 'Sending…' : 'Submit answer'}
                  </button>
                </div>
              )}
              <small style={styles.inputHint}>
                Your answer is delivered through a durable Channel and resumes the Agent.
              </small>
            </section>
          ) : (
            <>
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
                  placeholder={planMode
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
                <button
                  style={styles.primaryButton}
                  disabled={isBusy || !input.trim()}
                  onClick={sendMessage}
                >
                  {planMode ? 'Create plan' : 'Send'}
                </button>
              </div>
              <small style={styles.shortcutHint}>Send with ⌘/Ctrl + Enter or Alt + Enter · Enter adds a new line</small>
            </>
          )}
          {error && <p style={styles.error}>{error}</p>}
        </div>
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

const TimelineEntryCard: React.FC<{
  entry: TimelineEntry;
  model: string;
  onToggleThinking: (source: string) => void;
}> = ({ entry, model, onToggleThinking }) => {
  if (entry.kind === 'message') {
    return <MessageCard sequence={entry.sequence} message={entry.message} />;
  }
  if (entry.kind === 'thinking') {
    const thinking = entry.entry;
    return (
      <section style={styles.thinkingCard} aria-live="polite">
        <button
          type="button"
          style={styles.thinkingHeader}
          aria-expanded={thinking.isExpanded}
          aria-controls={`thinking-${entry.index}`}
          onClick={() => onToggleThinking(thinking.source)}
        >
          <span style={thinking.isStreaming ? styles.thinkingIndicator : styles.thinkingCompleteIndicator} />
          <strong>Thinking</strong>
          <small>{thinking.isStreaming ? 'Streaming reasoning summary…' : 'Reasoning summary'}</small>
          <span style={styles.thinkingChevron}>{thinking.isExpanded ? '▾' : '▸'}</span>
        </button>
        {thinking.isExpanded && (
          <p id={`thinking-${entry.index}`} style={styles.streamText}>{thinking.content}</p>
        )}
      </section>
    );
  }
  if (entry.kind === 'response') {
    return (
      <section style={styles.liveModelCard} aria-live="polite">
        <div style={styles.streamHeader}>
          <span style={styles.liveIndicator} />
          <strong>Response</strong>
          <small>{model}</small>
        </div>
        <p style={styles.streamText}>{entry.entry.content}</p>
      </section>
    );
  }
  const activityEvent = entry.entry.event;
  return (
    <details style={styles.activity}>
      <summary>
        <strong>Agent activity</strong> · {activityEvent.kind}
        {activityEvent.tool_name ? ` · ${activityEvent.tool_name}` : ''}
      </summary>
      <p>{activityEvent.message}</p>
    </details>
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

const buildTimeline = (
  messages: SequencedMessage[],
  thinkingEntries: ThinkingEntry[],
  liveResponse: LiveResponseEntry | null,
  activity: ActivityEntry[],
): TimelineEntry[] => {
  const timeline: TimelineEntry[] = messages
    .filter(({ message }) => !isAgentPlumbing(message))
    .map(({ sequence, message, created_at: createdAt }) => ({
      kind: 'message' as const,
      id: `message-${sequence}`,
      createdTime: createdAt,
      sequence,
      message,
    }));
  timeline.push(...thinkingEntries.map((entry, index) => ({
    kind: 'thinking' as const,
    id: `thinking-${entry.source}`,
    createdTime: entry.createdTime,
    entry,
    index,
  })));
  if (liveResponse) {
    timeline.push({
      kind: 'response',
      id: `response-${liveResponse.source}`,
      createdTime: liveResponse.createdTime,
      entry: liveResponse,
    });
  }
  timeline.push(...activity.map((entry, index) => ({
    kind: 'activity' as const,
    id: `activity-${entry.source}-${entry.createdTime}-${index}`,
    createdTime: entry.createdTime,
    entry,
    index,
  })));
  return timeline.sort(compareTimelineEntries);
};

const compareTimelineEntries = (left: TimelineEntry, right: TimelineEntry): number => {
  const leftTime = timelineTime(left.createdTime);
  const rightTime = timelineTime(right.createdTime);
  if (leftTime.milliseconds !== rightTime.milliseconds) {
    return leftTime.milliseconds - rightTime.milliseconds;
  }
  const rankDifference = timelineRank(left) - timelineRank(right);
  if (rankDifference !== 0) return rankDifference;
  return leftTime.subMilliseconds - rightTime.subMilliseconds;
};

const timelineRank = (entry: TimelineEntry): number => {
  if (entry.kind === 'message') return 0;
  if (entry.kind === 'activity' && entry.entry.event.kind.endsWith('_started')) return 1;
  if (entry.kind === 'thinking') return 2;
  if (entry.kind === 'response') return 3;
  return 4;
};

const timelineTime = (createdTime: string): { milliseconds: number; subMilliseconds: number } => {
  if (!createdTime) return { milliseconds: 0, subMilliseconds: 0 };
  const milliseconds = Date.parse(createdTime);
  const fraction = /\.(\d+)/.exec(createdTime)?.[1] ?? '';
  return {
    milliseconds: Number.isNaN(milliseconds) ? 0 : milliseconds,
    subMilliseconds: Number(fraction.padEnd(9, '0').slice(3, 9)),
  };
};

const planTaskIcon = (status: PlanTask['status']): string => {
  if (status === 'completed') return '✓';
  if (status === 'in_progress') return '●';
  return '○';
};

const styles: Record<string, React.CSSProperties> = {
  page: { minHeight: '100vh', background: 'linear-gradient(145deg, #f7f8fc 0%, #eef1fb 100%)', color: '#172033', padding: '32px 18px', fontFamily: 'Inter, system-ui, sans-serif' },
  chatPage: { boxSizing: 'border-box', height: '100dvh', minHeight: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column', padding: '20px 18px 12px' },
  portalShell: { maxWidth: 1120, margin: '0 auto', paddingBottom: 40 },
  portalHero: { display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 24, padding: '22px 4px 30px' },
  portalSteps: { display: 'flex', alignItems: 'center', gap: 9, flex: '0 0 auto', padding: '7px 9px', border: '1px solid #dce1eb', borderRadius: 14, background: 'rgba(255,255,255,.8)', color: '#7b8495', boxShadow: '0 6px 18px rgba(24,39,75,.06)', fontSize: 13, fontWeight: 750 },
  portalStep: { display: 'flex', alignItems: 'center', gap: 7, minHeight: 34, padding: '0 8px', borderRadius: 9 },
  activeStep: { background: '#eef0ff', color: '#3730a3' },
  portalStepNumber: { display: 'grid', placeItems: 'center', width: 21, height: 21, borderRadius: 999, background: '#e1e5ee', color: '#667085', fontSize: 11, fontWeight: 850 },
  activeStepNumber: { background: '#4f46e5', color: '#fff' },
  portalStepDivider: { width: 20, height: 1, background: '#cfd6e4' },
  portalGrid: { display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)', gap: 18, marginTop: 18 },
  portalCard: { padding: 26, borderRadius: 20, background: 'rgba(255,255,255,.94)', border: '1px solid rgba(207,214,228,.8)', boxShadow: '0 16px 48px rgba(24, 39, 75, 0.08)' },
  sectionHeading: { display: 'flex', alignItems: 'center', gap: 12 },
  sectionNumber: { display: 'grid', placeItems: 'center', width: 34, height: 34, flex: '0 0 34px', borderRadius: 11, background: '#4f46e5', color: '#fff', fontWeight: 800 },
  sectionTitle: { margin: 0, fontSize: 19 },
  sectionCopy: { margin: '4px 0 0', color: '#667085', lineHeight: 1.45 },
  securityNote: { margin: '10px 0 0', padding: '10px 12px', borderRadius: 10, background: '#eef8f4', color: '#17634a', fontSize: 13, lineHeight: 1.45 },
  providerSetupNote: { display: 'grid', gap: 6, marginTop: 10, padding: '12px 14px', borderRadius: 10, background: '#fff7e8', color: '#744b12', fontSize: 13, lineHeight: 1.45 },
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
  portalFooter: { display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', gap: 18, marginTop: 18, padding: 24, borderRadius: 18, background: '#172033', color: '#fff' },
  portalFooterCopy: { maxWidth: 620 },
  launchButton: { width: '100%', minHeight: 54, border: '1px solid rgba(255,255,255,.16)', borderRadius: 12, padding: '15px 24px', background: '#7668ff', color: '#fff', boxShadow: '0 10px 24px rgba(80,70,229,.35)', fontWeight: 850, cursor: 'pointer', fontSize: 16, letterSpacing: '.01em' },
  header: { width: '100%', maxWidth: 960, flex: '0 0 auto', margin: '0 auto 14px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' },
  headerButton: { border: '1px solid #cfd6e4', borderRadius: 10, padding: '9px 13px', background: '#fff', color: '#27334a', fontWeight: 700, cursor: 'pointer' },
  flowFailureCard: { maxWidth: 960, boxSizing: 'border-box', margin: '0 auto 18px', padding: '16px 18px', borderRadius: 14, border: '1px solid #ef9a9a', background: '#fff0f0', color: '#8f1d2c' },
  flowFailureText: { margin: '7px 0 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.5 },
  title: { margin: '4px 0 10px', fontSize: 44 },
  eyebrow: { margin: 0, color: '#5c6ac4', fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.08em', fontSize: 12 },
  subtitle: { color: '#596579', lineHeight: 1.6 },
  label: { display: 'block', fontWeight: 700, margin: '18px 0 7px' },
  input: { boxSizing: 'border-box', width: '100%', border: '1px solid #cfd6e4', borderRadius: 10, padding: '11px 13px', font: 'inherit' },
  grid: { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 },
  primaryButton: { border: 0, borderRadius: 10, padding: '11px 18px', background: '#4f46e5', color: '#fff', fontWeight: 700, cursor: 'pointer', marginTop: 18 },
  secondaryButton: { border: '1px solid #cfd6e4', borderRadius: 10, padding: '11px 18px', background: '#fff', color: '#27334a', fontWeight: 700, cursor: 'pointer', marginTop: 18 },
  status: { display: 'flex', flexDirection: 'column', alignItems: 'flex-end', padding: '9px 14px', borderRadius: 12, background: '#e9ecff', color: '#3730a3' },
  chatCard: { width: '100%', maxWidth: 960, minHeight: 0, flex: '1 1 auto', margin: '0 auto', display: 'flex', flexDirection: 'column', overflow: 'hidden', background: '#fff', borderRadius: 20, boxShadow: '0 18px 60px rgba(24, 39, 75, 0.08)' },
  conversationScroll: { minHeight: 0, flex: '1 1 auto', display: 'flex', flexDirection: 'column', overflowY: 'auto', overscrollBehavior: 'contain', padding: 22, scrollPaddingTop: 22 },
  messages: { display: 'flex', flexDirection: 'column', gap: 14, minHeight: 320, marginTop: 'auto' },
  timelineEntry: { display: 'flex', flexDirection: 'column', flex: '0 0 auto' },
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
  thinkingCard: { marginTop: 18, padding: 18, borderRadius: 14, background: '#f4f1ff', color: '#3d3168', border: '1px solid #cfc4ff' },
  thinkingHeader: { display: 'flex', alignItems: 'center', gap: 9, width: '100%', padding: 0, border: 0, background: 'transparent', color: 'inherit', font: 'inherit', textAlign: 'left', cursor: 'pointer' },
  thinkingChevron: { marginLeft: 'auto', fontSize: 18, lineHeight: 1 },
  streamHeader: { display: 'flex', alignItems: 'center', gap: 9, color: 'inherit' },
  thinkingIndicator: { width: 9, height: 9, borderRadius: '50%', background: '#9b72e8', boxShadow: '0 0 0 5px rgba(155,114,232,.15)' },
  thinkingCompleteIndicator: { width: 9, height: 9, borderRadius: '50%', background: '#9ca3af' },
  liveIndicator: { width: 9, height: 9, borderRadius: '50%', background: '#7c72ff', boxShadow: '0 0 0 5px rgba(124,114,255,.15)' },
  streamText: { margin: '13px 0 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.6, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' },
  inputCard: { marginTop: 18, padding: 20, borderRadius: 16, background: '#f5f7ff', border: '1px solid #aebdf2', boxShadow: '0 12px 28px rgba(79, 70, 229, .10)' },
  composerInputCard: { maxHeight: '45dvh', marginTop: 0, overflowY: 'auto', boxShadow: 'none' },
  inputCardHeader: { display: 'flex', alignItems: 'flex-start', gap: 13 },
  inputIndicator: { display: 'grid', placeItems: 'center', width: 32, height: 32, flex: '0 0 32px', borderRadius: 10, background: '#4f46e5', color: '#fff', fontWeight: 900 },
  inputPrompt: { margin: '5px 0 17px', fontSize: 20, lineHeight: 1.45 },
  inputChoices: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 10 },
  inputChoice: { display: 'flex', alignItems: 'center', gap: 10, width: '100%', padding: '13px 15px', borderRadius: 11, border: '1px solid #bec8e1', background: '#fff', color: '#172033', font: 'inherit', fontWeight: 700, textAlign: 'left', cursor: 'pointer', boxShadow: '0 3px 8px rgba(30, 41, 59, .07)', transition: 'transform 80ms ease, background 80ms ease, box-shadow 80ms ease' },
  inputChoicePressed: { transform: 'translateY(2px) scale(.99)', background: '#e3e7ff', boxShadow: 'inset 0 2px 5px rgba(50, 46, 129, .18)' },
  inputChoiceSelected: { background: '#4f46e5', borderColor: '#4f46e5', color: '#fff', transform: 'translateY(1px)', boxShadow: 'inset 0 2px 5px rgba(30, 27, 75, .25)' },
  inputAnswerComposer: { display: 'grid', gridTemplateColumns: '1fr auto', gap: 12, alignItems: 'end' },
  inputSubmitButton: { marginTop: 0, minHeight: 45, transition: 'transform 80ms ease, box-shadow 80ms ease' },
  inputSubmitButtonPressed: { transform: 'translateY(2px) scale(.98)', boxShadow: 'inset 0 2px 5px rgba(30, 27, 75, .32)' },
  inputHint: { display: 'block', marginTop: 13, color: '#667085', lineHeight: 1.45 },
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
  queueArea: { marginBottom: 14, padding: 16, borderRadius: 14, border: '1px solid #d8deea', background: '#fafbfe' },
  queueAreaCollapsed: { padding: '10px 14px' },
  queueHeader: { display: 'flex', justifyContent: 'space-between', gap: 16, alignItems: 'start', marginBottom: 10 },
  queueHeaderCollapsed: { alignItems: 'center', marginBottom: 0 },
  queueHint: { display: 'block', marginTop: 4, color: '#667085', lineHeight: 1.4 },
  queueItem: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 14, padding: 13, marginTop: 9, borderRadius: 11, border: '1px solid #dce1eb', background: '#fff' },
  steeringItem: { borderColor: '#aebdf2', background: '#f0f4ff' },
  queueContent: { minWidth: 0, flex: 1 },
  queueMessage: { margin: '7px 0 0', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', lineHeight: 1.45 },
  queuedBadge: { display: 'inline-block', padding: '3px 8px', borderRadius: 999, background: '#e8edf5', color: '#445066', fontSize: 11, fontWeight: 800, textTransform: 'uppercase' },
  steeringBadge: { display: 'inline-block', padding: '3px 8px', borderRadius: 999, background: '#4f46e5', color: '#fff', fontSize: 11, fontWeight: 800, textTransform: 'uppercase' },
  queueActions: { display: 'flex', flexWrap: 'wrap', justifyContent: 'flex-end', gap: 7 },
  queueButton: { border: '1px solid #cfd6e4', borderRadius: 8, padding: '7px 10px', background: '#fff', color: '#27334a', fontWeight: 700, cursor: 'pointer', transition: 'transform 80ms ease, box-shadow 80ms ease' },
  steerButton: { borderColor: '#4f46e5', background: '#4f46e5', color: '#fff' },
  queueButtonPressed: { transform: 'translateY(2px) scale(.97)', boxShadow: 'inset 0 2px 4px rgba(30, 27, 75, .25)' },
  composerArea: { flex: '0 0 auto', padding: '16px 22px 18px', borderTop: '1px solid #e6e9ef', background: 'rgba(255,255,255,.98)', boxShadow: '0 -12px 28px rgba(24,39,75,.06)' },
  planModeToggle: { display: 'flex', gap: 8, alignItems: 'center', fontWeight: 700, marginBottom: 10 },
  planModeHint: { color: '#667085', fontWeight: 400 },
  composer: { display: 'grid', gridTemplateColumns: '1fr auto', gap: 12, alignItems: 'end' },
  shortcutHint: { display: 'block', marginTop: 8, color: '#7b8495' },
  error: { padding: 12, borderRadius: 9, background: '#ffeded', color: '#a11d2b' },
  empty: { textAlign: 'center', color: '#7b8495', margin: '100px 0' },
  footer: { width: '100%', maxWidth: 960, flex: '0 0 auto', margin: '8px auto 0', color: '#6b7280', fontSize: 13 },
};

export default App;
