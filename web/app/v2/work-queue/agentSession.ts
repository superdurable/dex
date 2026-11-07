// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action, FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { hydrateBlobs } from '@/lib/blobs';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionEntry, V2CatalogEntry } from '@/lib/types';

/**
 * Names the Dex Agents session Flow publishes in its conformance suite (agents/conformance/names.json).
 *
 * Dex Web reads them and nothing else: no agent logic lives here.
 */
export const AGENT_SESSION = {
  approvalStepType: 'AwaitToolApproval',
  questionStepType: 'AwaitQuestionAnswer',
  resolveRPC: 'resolve_interrupt',
  answerRPC: 'answer_question',
  pendingInterrupts: 'PendingInterrupts',
  toolCalls: 'AgentToolCalls',
  messages: 'AgentMessages',
} as const;

/** The Step types where a session waits for a person. */
export const AGENT_WAITING_STEP_TYPES = [AGENT_SESSION.approvalStepType, AGENT_SESSION.questionStepType];

export type AgentInterruptKind = 'tool_approval' | 'question';

export interface AgentInterrupt {
  interruptId: string;
  kind: AgentInterruptKind;
  toolCallId: string;
  toolName: string;
  /** JSON text, exactly as the model wrote it. */
  arguments: string;
  createdAt: string;
  deadline: string | null;
  question: string | null;
  choices: string[];
  allowsFreeText: boolean;
}

export interface AgentToolCall {
  toolCallId: string;
  toolName: string;
  effect: string;
  status: string;
}

export interface AgentMessage {
  sequence: number;
  role: string;
  text: string;
  createdAt: string;
}

export interface AgentSessionRead {
  interrupts: AgentInterrupt[];
  toolCalls: Record<string, AgentToolCall>;
  /** The newest message with text, which says what the session is about. */
  lastMessage: AgentMessage | null;
  isActive: boolean;
}

/** The Flow types whose graph is a Dex Agents session: its waiting Steps and its answering Actions. */
export function agentSessionFlowTypes(
  flows: readonly V2CatalogEntry[],
  definitions: readonly FlowDefinitionEntry[],
): string[] {
  return flows
    .filter((entry) => isAgentSessionDefinition(entry.definition)
      && definitions.some((graph) => graph.flowName === entry.flowType && hasStep(graph, AGENT_SESSION.approvalStepType)
        && hasStep(graph, AGENT_SESSION.questionStepType)))
    .map((entry) => entry.flowType)
    .sort();
}

export function isAgentSessionDefinition(definition: FlowV2Definition): boolean {
  return answeringAction(definition, AGENT_SESSION.resolveRPC) !== undefined
    && answeringAction(definition, AGENT_SESSION.answerRPC) !== undefined;
}

export function answeringAction(definition: FlowV2Definition, rpcName: string): FlowV2Action | undefined {
  return definition.actions.find((action) => action.rpcName === rpcName);
}

function hasStep(graph: FlowDefinitionEntry, stepType: string): boolean {
  return graph.schemaVersion === '2.0' && graph.valid
    && graph.graph.nodes.some((node) => node.kind === 'step' && node.name === stepType);
}

interface StoredAttribute {
  key: string;
  value: unknown;
}

const LAST_MESSAGES_READ = 6;

/**
 * Reads what a session waits on from its stored Attributes, so it works while Workers are down.
 *
 * The state read returns large values as blob references, so only the values needed are loaded.
 */
export async function readAgentSession(flowId: string, signal?: AbortSignal): Promise<AgentSessionRead> {
  const summary = await readResponseJSON<{ runId: string; flowStatusCode: number }>(
    await fetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, { cache: 'no-store', signal }),
  );
  const state = await readResponseJSON<{ attributes: StoredAttribute[] }>(await fetch(
    `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(summary.runId)}`,
    { cache: 'no-store', signal },
  ));
  const stored = new Map((state.attributes ?? []).map((attribute) => [attribute.key, attribute.value]));
  const messageKeys = [...stored.keys()]
    .filter((key) => key.startsWith(`${AGENT_SESSION.messages}/`))
    .sort()
    .slice(-LAST_MESSAGES_READ);
  const interruptsOnly = await hydrateBlobs(flowId, stored.get(AGENT_SESSION.pendingInterrupts) ?? [], new Map(), signal);
  if (interruptsOnly.error) throw new Error(`${AGENT_SESSION.pendingInterrupts}: ${interruptsOnly.error}`);
  const interrupts = parseInterrupts(interruptsOnly.value);
  const toolCallKeys = [...new Set(interrupts.map((interrupt) => `${AGENT_SESSION.toolCalls}/${interrupt.toolCallId}`))];
  const picked = Object.fromEntries([...toolCallKeys, ...messageKeys]
    .filter((key) => stored.has(key))
    .map((key) => [key, stored.get(key)]));
  // A tool call or message that cannot be loaded leaves its detail out; the interrupt still shows.
  const hydrated = (await hydrateBlobs(flowId, picked, new Map(), signal)).value as Record<string, unknown>;
  const toolCalls: Record<string, AgentToolCall> = {};
  for (const key of toolCallKeys) {
    const call = parseToolCall(hydrated[key]);
    if (call !== null) toolCalls[call.toolCallId] = call;
  }
  const messages = messageKeys.map((key) => parseMessage(hydrated[key])).filter((message) => message !== null);
  return {
    interrupts,
    toolCalls,
    lastMessage: messages.reverse().find((message) => message.text !== '') ?? null,
    isActive: summary.flowStatusCode === 1,
  };
}

export function parseInterrupts(value: unknown): AgentInterrupt[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((entry) => {
    if (!isRecord(entry)) return [];
    const kind = entry.kind;
    if (kind !== 'tool_approval' && kind !== 'question') return [];
    const interruptId = text(entry.interrupt_id);
    if (interruptId === '') return [];
    return [{
      interruptId,
      kind,
      toolCallId: text(entry.tool_call_id) || interruptId,
      toolName: text(entry.tool_name),
      arguments: text(entry.arguments),
      createdAt: text(entry.created_at),
      deadline: typeof entry.deadline === 'string' ? entry.deadline : null,
      question: typeof entry.question === 'string' ? entry.question : null,
      choices: Array.isArray(entry.choices) ? entry.choices.filter((choice) => typeof choice === 'string') : [],
      // Absent in sessions written by dex-agents 0.1.0, which allowed free text.
      allowsFreeText: entry.allows_free_text !== false,
    }];
  });
}

function parseToolCall(value: unknown): AgentToolCall | null {
  if (!isRecord(value) || text(value.tool_call_id) === '') return null;
  return {
    toolCallId: text(value.tool_call_id),
    toolName: text(value.tool_name),
    effect: text(value.effect),
    status: text(value.status),
  };
}

function parseMessage(value: unknown): AgentMessage | null {
  if (!isRecord(value) || typeof value.sequence !== 'number') return null;
  return {
    sequence: value.sequence,
    role: text(value.role),
    text: text(value.text),
    createdAt: text(value.created_at),
  };
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
