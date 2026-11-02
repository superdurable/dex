// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { jsonCodec, type Codec } from "@superdurable/dex";

import type {
  AgentEvent,
  AgentInterrupt,
  AgentInterruptResolution,
  AgentMessage,
  AgentMessageRole,
  AgentMessagesPage,
  AgentSessionStartInput,
  AgentSessionStatus,
  AgentSnapshot,
  AgentState,
  AgentToolCall,
  AgentToolCallStatus,
  AgentUsage,
  AgentUserMessage,
  ListAgentMessagesInput,
  ResolveInterruptInput,
  ToolCallCompleted,
  ToolEffect,
} from "./agent-session-values.js";

// Wire values are snake_case JSON shared with the Python core; TypeScript values are camelCase.

const agentSessionStatuses: readonly AgentSessionStatus[] = [
  "waiting_for_user_message",
  "running_model",
  "running_tools",
];
const agentMessageRoles: readonly AgentMessageRole[] = ["user", "assistant", "tool"];
const toolEffects: readonly ToolEffect[] = [
  "read",
  "idempotent_write",
  "non_idempotent_write",
  "unclassified",
];
const agentToolCallStatuses: readonly AgentToolCallStatus[] = [
  "awaiting_approval",
  "approved",
  "rejected",
  "succeeded",
  "failed",
  "outcome_unknown",
];

class WireRecordReader {
  private readonly record: Readonly<Record<string, unknown>>;

  public constructor(
    value: unknown,
    private readonly typeName: string,
  ) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      throw new TypeError(`${typeName} must be a JSON object`);
    }
    this.record = value as Readonly<Record<string, unknown>>;
  }

  public readString(field: string): string {
    const value = this.record[field];
    if (typeof value !== "string") {
      throw new TypeError(`${this.typeName}.${field} must be a string`);
    }
    return value;
  }

  public readNullableString(field: string): string | null {
    const value = this.record[field];
    if (value === undefined || value === null) {
      return null;
    }
    if (typeof value !== "string") {
      throw new TypeError(`${this.typeName}.${field} must be a string or null`);
    }
    return value;
  }

  public readInteger(field: string): number {
    const value = this.record[field];
    if (typeof value !== "number" || !Number.isSafeInteger(value)) {
      throw new TypeError(`${this.typeName}.${field} must be an integer`);
    }
    return value;
  }

  public readIntegerOrDefault(field: string, defaultValue: number): number {
    const value = this.record[field];
    return value === undefined || value === null ? defaultValue : this.readInteger(field);
  }

  public readNullableInteger(field: string): number | null {
    const value = this.record[field];
    if (value === undefined || value === null) {
      return null;
    }
    return this.readInteger(field);
  }

  public readBoolean(field: string): boolean {
    const value = this.record[field];
    if (typeof value !== "boolean") {
      throw new TypeError(`${this.typeName}.${field} must be a boolean`);
    }
    return value;
  }

  public readEnum<Literal extends string>(field: string, allowed: readonly Literal[]): Literal {
    const value = this.readString(field);
    if (!(allowed as readonly string[]).includes(value)) {
      throw new TypeError(`${this.typeName}.${field} has unknown value ${value}`);
    }
    return value as Literal;
  }

  public readArray(field: string): readonly unknown[] {
    const value = this.record[field];
    if (!Array.isArray(value)) {
      throw new TypeError(`${this.typeName}.${field} must be an array`);
    }
    return value;
  }

  public readObject(field: string): unknown {
    return this.record[field];
  }
}

export function encodeAgentSessionStartInput(value: AgentSessionStartInput): unknown {
  return { initial_message: value.initialMessage };
}

export function decodeAgentSessionStartInput(value: unknown): AgentSessionStartInput {
  if (value === null || value === undefined) {
    return { initialMessage: null };
  }
  const reader = new WireRecordReader(value, "AgentSessionStartInput");
  return { initialMessage: reader.readNullableString("initial_message") };
}

export function encodeAgentState(value: AgentState): unknown {
  return {
    status: value.status,
    turn_count: value.turnCount,
    active_turn_id: value.activeTurnId,
    next_message_sequence: value.nextMessageSequence,
    last_turn_error: value.lastTurnError,
  };
}

export function decodeAgentState(value: unknown): AgentState {
  const reader = new WireRecordReader(value, "AgentState");
  return {
    status: reader.readEnum("status", agentSessionStatuses),
    turnCount: reader.readInteger("turn_count"),
    activeTurnId: reader.readNullableString("active_turn_id"),
    nextMessageSequence: reader.readInteger("next_message_sequence"),
    lastTurnError: reader.readNullableString("last_turn_error"),
  };
}

export function encodeAgentMessage(value: AgentMessage): unknown {
  return {
    sequence: value.sequence,
    turn_id: value.turnId,
    role: value.role,
    text: value.text,
    tool_call_id: value.toolCallId,
    tool_name: value.toolName,
    tool_arguments: value.toolArguments,
    tool_output: value.toolOutput,
    framework_item: value.frameworkItem,
    created_at: value.createdAt,
  };
}

export function decodeAgentMessage(value: unknown): AgentMessage {
  const reader = new WireRecordReader(value, "AgentMessage");
  return {
    sequence: reader.readInteger("sequence"),
    turnId: reader.readString("turn_id"),
    role: reader.readEnum("role", agentMessageRoles),
    text: reader.readNullableString("text"),
    toolCallId: reader.readNullableString("tool_call_id"),
    toolName: reader.readNullableString("tool_name"),
    toolArguments: reader.readNullableString("tool_arguments"),
    toolOutput: reader.readNullableString("tool_output"),
    frameworkItem: reader.readString("framework_item"),
    createdAt: reader.readString("created_at"),
  };
}

export function encodeAgentToolCall(value: AgentToolCall): unknown {
  return {
    tool_call_id: value.toolCallId,
    turn_id: value.turnId,
    tool_name: value.toolName,
    arguments: value.arguments,
    effect: value.effect,
    requires_approval: value.requiresApproval,
    status: value.status,
    output: value.output,
    error: value.error,
    rejection_message: value.rejectionMessage,
    deferral_order: value.deferralOrder,
    tool_source_reference: value.toolSourceReference ?? null,
    trace_context: value.traceContext ?? null,
  };
}

export function decodeAgentToolCall(value: unknown): AgentToolCall {
  const reader = new WireRecordReader(value, "AgentToolCall");
  const traceContext = reader.readNullableString("trace_context");
  const toolCall: AgentToolCall = {
    toolCallId: reader.readString("tool_call_id"),
    turnId: reader.readString("turn_id"),
    toolName: reader.readString("tool_name"),
    arguments: reader.readString("arguments"),
    effect: reader.readEnum("effect", toolEffects),
    requiresApproval: reader.readBoolean("requires_approval"),
    status: reader.readEnum("status", agentToolCallStatuses),
    output: reader.readNullableString("output"),
    error: reader.readNullableString("error"),
    rejectionMessage: reader.readNullableString("rejection_message"),
    deferralOrder: reader.readIntegerOrDefault("deferral_order", 0),
    ...(traceContext === null ? {} : { traceContext }),
  };
  const toolSourceReference = reader.readNullableString("tool_source_reference");
  return toolSourceReference === null ? toolCall : { ...toolCall, toolSourceReference };
}

export function encodeAgentInterrupt(value: AgentInterrupt): unknown {
  return {
    interrupt_id: value.interruptId,
    kind: value.kind,
    tool_call_id: value.toolCallId,
    tool_name: value.toolName,
    arguments: value.arguments,
    created_at: value.createdAt,
    deadline: value.deadline,
  };
}

export function decodeAgentInterrupt(value: unknown): AgentInterrupt {
  const reader = new WireRecordReader(value, "AgentInterrupt");
  return {
    interruptId: reader.readString("interrupt_id"),
    kind: reader.readEnum("kind", ["tool_approval"] as const),
    toolCallId: reader.readString("tool_call_id"),
    toolName: reader.readString("tool_name"),
    arguments: reader.readString("arguments"),
    createdAt: reader.readString("created_at"),
    deadline: reader.readNullableString("deadline"),
  };
}

export function encodeAgentInterruptResolution(value: AgentInterruptResolution): unknown {
  return { approved: value.approved, rejection_message: value.rejectionMessage };
}

export function decodeAgentInterruptResolution(value: unknown): AgentInterruptResolution {
  const reader = new WireRecordReader(value, "AgentInterruptResolution");
  return {
    approved: reader.readBoolean("approved"),
    rejectionMessage: reader.readNullableString("rejection_message"),
  };
}

export function encodeAgentUsage(value: AgentUsage): unknown {
  return {
    model_calls: value.modelCalls,
    input_tokens: value.inputTokens,
    output_tokens: value.outputTokens,
    cached_input_tokens: value.cachedInputTokens,
    reasoning_tokens: value.reasoningTokens,
  };
}

export function decodeAgentUsage(value: unknown): AgentUsage {
  const reader = new WireRecordReader(value, "AgentUsage");
  return {
    modelCalls: reader.readInteger("model_calls"),
    inputTokens: reader.readInteger("input_tokens"),
    outputTokens: reader.readInteger("output_tokens"),
    cachedInputTokens: reader.readInteger("cached_input_tokens"),
    reasoningTokens: reader.readInteger("reasoning_tokens"),
  };
}

export function encodeAgentUserMessage(value: AgentUserMessage): unknown {
  return { text: value.text };
}

export function decodeAgentUserMessage(value: unknown): AgentUserMessage {
  const reader = new WireRecordReader(value, "AgentUserMessage");
  return { text: reader.readString("text") };
}

export function encodeToolCallCompleted(value: ToolCallCompleted): unknown {
  return { tool_call_id: value.toolCallId };
}

export function decodeToolCallCompleted(value: unknown): ToolCallCompleted {
  const reader = new WireRecordReader(value, "ToolCallCompleted");
  return { toolCallId: reader.readString("tool_call_id") };
}

export function encodeAgentEvent(value: AgentEvent): unknown {
  return {
    type: value.type,
    turn_id: value.turnId,
    attempt: value.attempt,
    step_execution_id: value.stepExecutionId,
    payload: value.payload,
  };
}

export function decodeAgentEvent(value: unknown): AgentEvent {
  const reader = new WireRecordReader(value, "AgentEvent");
  return {
    type: reader.readString("type"),
    turnId: reader.readNullableString("turn_id"),
    attempt: reader.readInteger("attempt"),
    stepExecutionId: reader.readNullableString("step_execution_id") ?? "",
    payload: reader.readString("payload"),
  };
}

export function encodeResolveInterruptInput(value: ResolveInterruptInput): unknown {
  return {
    interrupt_id: value.interruptId,
    approved: value.approved,
    rejection_message: value.rejectionMessage,
  };
}

export function decodeResolveInterruptInput(value: unknown): ResolveInterruptInput {
  const reader = new WireRecordReader(value, "ResolveInterruptInput");
  return {
    interruptId: reader.readString("interrupt_id"),
    approved: reader.readBoolean("approved"),
    rejectionMessage: reader.readNullableString("rejection_message"),
  };
}

export function encodeAgentSnapshot(value: AgentSnapshot): unknown {
  return {
    state: value.state === null ? null : encodeAgentState(value.state),
    messages: value.messages.map(encodeAgentMessage),
    pending_interrupts: value.pendingInterrupts.map(encodeAgentInterrupt),
    tool_calls: value.toolCalls.map(encodeAgentToolCall),
    usage: encodeAgentUsage(value.usage),
    queued_message_count: value.queuedMessageCount,
  };
}

export function decodeAgentSnapshot(value: unknown): AgentSnapshot {
  const reader = new WireRecordReader(value, "AgentSnapshot");
  return {
    state: reader.readObject("state") == null ? null : decodeAgentState(reader.readObject("state")),
    messages: reader.readArray("messages").map(decodeAgentMessage),
    pendingInterrupts: reader.readArray("pending_interrupts").map(decodeAgentInterrupt),
    toolCalls: reader.readArray("tool_calls").map(decodeAgentToolCall),
    usage: decodeAgentUsage(reader.readObject("usage")),
    queuedMessageCount: reader.readIntegerOrDefault("queued_message_count", 0),
  };
}

export function encodeListAgentMessagesInput(value: ListAgentMessagesInput): unknown {
  return { before_sequence: value.beforeSequence, limit: value.limit };
}

export function decodeListAgentMessagesInput(value: unknown): ListAgentMessagesInput {
  const reader = new WireRecordReader(value, "ListAgentMessagesInput");
  return {
    beforeSequence: reader.readNullableInteger("before_sequence"),
    limit: reader.readIntegerOrDefault("limit", 50),
  };
}

export function encodeAgentMessagesPage(value: AgentMessagesPage): unknown {
  return {
    messages: value.messages.map(encodeAgentMessage),
    next_before_sequence: value.nextBeforeSequence,
  };
}

export function decodeAgentMessagesPage(value: unknown): AgentMessagesPage {
  const reader = new WireRecordReader(value, "AgentMessagesPage");
  return {
    messages: reader.readArray("messages").map(decodeAgentMessage),
    nextBeforeSequence: reader.readNullableInteger("next_before_sequence"),
  };
}

function snakeCaseJsonCodec<Value>(
  typeName: string,
  encode: (value: Value) => unknown,
  decode: (value: unknown) => Value,
): Codec<Value> {
  return jsonCodec<Value>({ typeName, encode, decode });
}

export const agentSessionStartInputCodec = snakeCaseJsonCodec(
  "AgentSessionStartInput",
  encodeAgentSessionStartInput,
  decodeAgentSessionStartInput,
);
export const agentStateCodec = snakeCaseJsonCodec("AgentState", encodeAgentState, decodeAgentState);
export const agentMessageCodec = snakeCaseJsonCodec(
  "AgentMessage",
  encodeAgentMessage,
  decodeAgentMessage,
);
export const agentToolCallCodec = snakeCaseJsonCodec(
  "AgentToolCall",
  encodeAgentToolCall,
  decodeAgentToolCall,
);
export const agentInterruptListCodec = snakeCaseJsonCodec<readonly AgentInterrupt[]>(
  "AgentInterruptList",
  (value) => value.map(encodeAgentInterrupt),
  (value) => {
    if (!Array.isArray(value)) {
      throw new TypeError("PendingInterrupts must be a JSON array");
    }
    return value.map(decodeAgentInterrupt);
  },
);
export const agentInterruptResolutionCodec = snakeCaseJsonCodec(
  "AgentInterruptResolution",
  encodeAgentInterruptResolution,
  decodeAgentInterruptResolution,
);
export const agentUsageCodec = snakeCaseJsonCodec("AgentUsage", encodeAgentUsage, decodeAgentUsage);
export const agentUserMessageCodec = snakeCaseJsonCodec(
  "AgentUserMessage",
  encodeAgentUserMessage,
  decodeAgentUserMessage,
);
export const toolCallCompletedCodec = snakeCaseJsonCodec(
  "ToolCallCompleted",
  encodeToolCallCompleted,
  decodeToolCallCompleted,
);
export const agentEventCodec = snakeCaseJsonCodec("AgentEvent", encodeAgentEvent, decodeAgentEvent);
export const resolveInterruptInputCodec = snakeCaseJsonCodec(
  "ResolveInterruptInput",
  encodeResolveInterruptInput,
  decodeResolveInterruptInput,
);
export const agentSnapshotCodec = snakeCaseJsonCodec(
  "AgentSnapshot",
  encodeAgentSnapshot,
  decodeAgentSnapshot,
);
export const listAgentMessagesInputCodec = snakeCaseJsonCodec(
  "ListAgentMessagesInput",
  encodeListAgentMessagesInput,
  decodeListAgentMessagesInput,
);
export const agentMessagesPageCodec = snakeCaseJsonCodec(
  "AgentMessagesPage",
  encodeAgentMessagesPage,
  decodeAgentMessagesPage,
);
