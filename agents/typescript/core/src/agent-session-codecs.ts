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
  AgentImageReference,
  AgentInterrupt,
  AgentInterruptDisplay,
  AgentInterruptKind,
  AgentInterruptResolution,
  AgentMessage,
  AgentMessageRole,
  AgentMessageChunk,
  AgentMessageChunkRange,
  AgentMessageIndex,
  AgentMessagesPage,
  AgentPlan,
  AgentPlanTask,
  AgentSessionStartInput,
  AgentSessionStatus,
  AgentSnapshot,
  AgentState,
  AgentToolCallRecord,
  AgentToolCallRecordStatus,
  AgentUsage,
  AgentUserMessage,
  AnswerQuestionRequest,
  CancelTurnRequest,
  DeleteQueuedMessageRequest,
  ExecutePlanRequest,
  InteractionMode,
  CompactContextInput,
  ContextSummary,
  ListAgentMessagesInput,
  PendingUserMessage,
  PlanExecutionRequest,
  PlanStatus,
  PlanTaskStatus,
  QuestionAnswer,
  QuestionResolution,
  ResolveInterruptInput,
  SteerUserMessageRequest,
  ToolCallCancellation,
  ToolCallCancellationReason,
  ToolCallCompleted,
  ToolEffect,
  TurnCancellationRequest,
  AcquireSessionTerminalLeaseRequest,
  ReleaseSessionTerminalLeaseRequest,
  SessionTerminalLease,
  SessionTerminalLeaseResult,
  TurnControlKind,
  TurnControlRequest,
  SessionSettings,
  TurnReplySequence,
  TurnSessionSettings,
  UpdateSessionSettingsRequest,
} from "./agent-session-values.js";

// Wire values are snake_case JSON shared with the Python core; TypeScript values are camelCase.

const agentSessionStatuses: readonly AgentSessionStatus[] = [
  "waiting_for_user_message",
  "running_model",
  "running_tools",
  "compacting_context",
  "failed",
];
const agentMessageRoles: readonly AgentMessageRole[] = ["user", "assistant", "tool"];
const toolEffects: readonly ToolEffect[] = [
  "read",
  "idempotent_write",
  "non_idempotent_write",
  "unclassified",
];
const agentToolCallStatuses: readonly AgentToolCallRecordStatus[] = [
  "awaiting_approval",
  "approved",
  "awaiting_answer",
  "waiting_for_timer",
  "waiting_for_task",
  "rejected",
  "succeeded",
  "failed",
  "outcome_unknown",
];
const agentInterruptKinds: readonly AgentInterruptKind[] = ["tool_approval", "question"];
const interactionModes: readonly InteractionMode[] = ["chat", "planning", "executing"];
const planStatuses: readonly PlanStatus[] = ["draft", "active", "completed", "cleared"];
const planTaskStatuses: readonly PlanTaskStatus[] = ["pending", "in_progress", "completed"];
const toolCallCancellationReasons: readonly ToolCallCancellationReason[] = ["steered", "turn_cancelled"];
const turnControlKinds: readonly TurnControlKind[] = ["steer", "cancel_turn"];
const questionResolutions: readonly QuestionResolution[] = ["answered", "declined", "deadline_passed"];

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

  public readBooleanOrDefault(field: string, defaultValue: boolean): boolean {
    const value = this.record[field];
    return value === undefined || value === null ? defaultValue : this.readBoolean(field);
  }

  public readStringArrayOrEmpty(field: string): readonly string[] {
    const value = this.record[field];
    if (value === undefined || value === null) {
      return [];
    }
    if (!Array.isArray(value) || !value.every((item) => typeof item === "string")) {
      throw new TypeError(`${this.typeName}.${field} must be an array of strings`);
    }
    return value;
  }

  public readArrayOrEmpty(field: string): readonly unknown[] {
    const value = this.record[field];
    return value === undefined || value === null ? [] : this.readArray(field);
  }

  public readBoolean(field: string): boolean {
    const value = this.record[field];
    if (typeof value !== "boolean") {
      throw new TypeError(`${this.typeName}.${field} must be a boolean`);
    }
    return value;
  }

  public readEnumOrDefault<Literal extends string>(
    field: string,
    allowed: readonly Literal[],
    defaultValue: Literal,
  ): Literal {
    const value = this.record[field];
    return value === undefined || value === null ? defaultValue : this.readEnum(field, allowed);
  }

  public readNullableEnum<Literal extends string>(field: string, allowed: readonly Literal[]): Literal | null {
    const value = this.record[field];
    return value === undefined || value === null ? null : this.readEnum(field, allowed);
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
  return {
    initial_message: value.initialMessage,
    session_settings: value.sessionSettings ?? null,
    start_environment: value.startEnvironment ?? null,
  };
}

export function decodeAgentSessionStartInput(value: unknown): AgentSessionStartInput {
  if (value === null || value === undefined) {
    return { initialMessage: null, sessionSettings: null, startEnvironment: null };
  }
  const reader = new WireRecordReader(value, "AgentSessionStartInput");
  const sessionSettings = reader.readObject("session_settings");
  const startEnvironment = reader.readObject("start_environment");
  return {
    initialMessage: reader.readNullableString("initial_message"),
    // Kept as sent: StartAgentSession refuses a value that is not an object, failing the session at once.
    sessionSettings: sessionSettings == null ? null : (sessionSettings as SessionSettings),
    // Kept as sent: the session never reads it.
    startEnvironment: startEnvironment == null ? null : (startEnvironment as Record<string, string>),
  };
}

export function encodeSessionSettings(value: SessionSettings): unknown {
  return value;
}

export function decodeSessionSettings(value: unknown): SessionSettings {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError("SessionSettings must be a JSON object");
  }
  return value as SessionSettings;
}

export function encodeUpdateSessionSettingsRequest(value: UpdateSessionSettingsRequest): unknown {
  return { session_settings: value.sessionSettings };
}

export function decodeUpdateSessionSettingsRequest(value: unknown): UpdateSessionSettingsRequest {
  const reader = new WireRecordReader(value, "UpdateSessionSettingsRequest");
  const sessionSettings = reader.readObject("session_settings");
  // Kept as sent: the RPC returns false for null or a value that is not an object.
  return { sessionSettings: sessionSettings == null ? null : (sessionSettings as SessionSettings) };
}

export function encodeTurnSessionSettings(value: TurnSessionSettings): unknown {
  return { turn_id: value.turnId, session_settings: value.sessionSettings };
}

export function decodeTurnSessionSettings(value: unknown): TurnSessionSettings {
  const reader = new WireRecordReader(value, "TurnSessionSettings");
  return {
    turnId: reader.readString("turn_id"),
    sessionSettings: decodeSessionSettings(reader.readObject("session_settings")),
  };
}

export function encodeAgentState(value: AgentState): unknown {
  return {
    status: value.status,
    turn_count: value.turnCount,
    active_turn_id: value.activeTurnId,
    next_message_sequence: value.nextMessageSequence,
    last_turn_error: value.lastTurnError,
    interaction_mode: value.interactionMode ?? "chat",
    accepts_turn_control: value.acceptsTurnControl ?? false,
  };
}

export function decodeAgentState(value: unknown): AgentState {
  const reader = new WireRecordReader(value, "AgentState");
  const interactionMode = reader.readEnumOrDefault("interaction_mode", interactionModes, "chat");
  const acceptsTurnControl = reader.readBooleanOrDefault("accepts_turn_control", false);
  return {
    status: reader.readEnum("status", agentSessionStatuses),
    turnCount: reader.readInteger("turn_count"),
    activeTurnId: reader.readNullableString("active_turn_id"),
    nextMessageSequence: reader.readInteger("next_message_sequence"),
    lastTurnError: reader.readNullableString("last_turn_error"),
    // Fields that plans added are omitted at their defaults, so a session without plans decodes to its 0.1.0 shape.
    ...(interactionMode === "chat" ? {} : { interactionMode }),
    ...(acceptsTurnControl ? { acceptsTurnControl } : {}),
  };
}

export function encodeAgentImageReference(value: AgentImageReference): unknown {
  return {
    type: value.type,
    uri: value.uri,
    mime_type: value.mimeType,
    sha256: value.sha256,
    size_bytes: value.sizeBytes,
  };
}

export function decodeAgentImageReference(value: unknown): AgentImageReference {
  const reader = new WireRecordReader(value, "AgentImageReference");
  return {
    // Another type decodes as it is, so validateImageReference refuses it as Python does.
    type: (reader.readNullableString("type") ?? "image") as "image",
    uri: reader.readString("uri"),
    mimeType: reader.readString("mime_type"),
    sha256: reader.readString("sha256"),
    sizeBytes: reader.readInteger("size_bytes"),
  };
}

function encodeImages(images: readonly AgentImageReference[] | undefined): unknown[] {
  return (images ?? []).map(encodeAgentImageReference);
}

/** Decodes an `images` field; decoded values omit it when there are none, as they do `planMode`. */
function decodeImages(reader: WireRecordReader): { readonly images?: readonly AgentImageReference[] } {
  const images = reader.readArrayOrEmpty("images").map(decodeAgentImageReference);
  return images.length === 0 ? {} : { images };
}

export function encodeAgentInterruptDisplay(value: AgentInterruptDisplay | null | undefined): unknown {
  return value === null || value === undefined
    ? null
    : { target: value.target, summary: value.summary, preview: value.preview ?? null };
}

export function decodeAgentInterruptDisplay(value: unknown): AgentInterruptDisplay | null {
  if (value === null || value === undefined) {
    return null;
  }
  const reader = new WireRecordReader(value, "AgentInterruptDisplay");
  return {
    target: reader.readString("target"),
    summary: reader.readString("summary"),
    preview: reader.readNullableString("preview"),
  };
}

/** Decoded values omit a display field when there is none, as they do `images`. */
function omitNullDisplay<Name extends "display" | "approvalDisplay">(
  name: Name,
  display: AgentInterruptDisplay | null,
): Partial<Record<Name, AgentInterruptDisplay>> {
  return (display === null ? {} : { [name]: display }) as Partial<Record<Name, AgentInterruptDisplay>>;
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
    images: encodeImages(value.images),
    origin: value.origin ?? null,
    ...encodeSenderName(value.senderName),
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
    ...decodeImages(reader),
    ...decodeAgentMessageOrigin(reader),
    ...decodeSenderName(reader),
  };
}

/** Encoding omits an absent sender name, so values without one keep their released bytes. */
function encodeSenderName(senderName: string | undefined): { sender_name?: string } {
  return senderName === undefined ? {} : { sender_name: senderName };
}

function decodeSenderName(reader: WireRecordReader): { senderName?: string } {
  const senderName = reader.readNullableString("sender_name");
  return senderName === null ? {} : { senderName };
}

/** Keeps an origin this release does not know, so a client still tells the message apart from user text. */
function decodeAgentMessageOrigin(reader: WireRecordReader): Pick<AgentMessage, "origin"> {
  const origin = reader.readNullableString("origin");
  return origin === null ? {} : { origin };
}

export function encodeAgentToolCall(value: AgentToolCallRecord): unknown {
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
    wait_until: value.waitUntil,
    input_continuation: value.inputContinuation,
    input_answers: value.inputAnswers.map(encodeQuestionAnswer),
    input_question: value.inputQuestion,
    input_choices: [...value.inputChoices],
    input_allows_free_text: value.inputAllowsFreeText,
    cancellation_reason: value.cancellationReason ?? null,
    task_id: value.taskId ?? null,
    task_poll_interval_seconds: value.taskPollIntervalSeconds ?? null,
    task_answer_pending: value.taskAnswerPending ?? false,
    ordered_write: value.orderedWrite ?? false,
    write_queued: value.writeQueued ?? false,
    skipped: value.skipped ?? false,
    truncated_output_bytes: value.truncatedOutputBytes ?? null,
    images: encodeImages(value.images),
    runner_tool_state: value.runnerToolState ?? null,
    approval_display: encodeAgentInterruptDisplay(value.approvalDisplay),
  };
}

export function decodeAgentToolCall(value: unknown): AgentToolCallRecord {
  const reader = new WireRecordReader(value, "AgentToolCall");
  const traceContext = reader.readNullableString("trace_context");
  const toolCall: AgentToolCallRecord = {
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
    waitUntil: reader.readNullableString("wait_until"),
    inputContinuation: reader.readNullableString("input_continuation"),
    inputAnswers: reader.readArrayOrEmpty("input_answers").map(decodeQuestionAnswer),
    inputQuestion: reader.readNullableString("input_question"),
    inputChoices: reader.readStringArrayOrEmpty("input_choices"),
    inputAllowsFreeText: reader.readBooleanOrDefault("input_allows_free_text", true),
    cancellationReason: reader.readNullableEnum("cancellation_reason", toolCallCancellationReasons),
    taskId: reader.readNullableString("task_id"),
    taskPollIntervalSeconds: reader.readNullableInteger("task_poll_interval_seconds"),
    taskAnswerPending: reader.readBooleanOrDefault("task_answer_pending", false),
    orderedWrite: reader.readBooleanOrDefault("ordered_write", false),
    writeQueued: reader.readBooleanOrDefault("write_queued", false),
    skipped: reader.readBooleanOrDefault("skipped", false),
    truncatedOutputBytes: reader.readNullableInteger("truncated_output_bytes"),
    runnerToolState: reader.readNullableString("runner_tool_state"),
    ...(traceContext === null ? {} : { traceContext }),
    ...decodeImages(reader),
    ...omitNullDisplay("approvalDisplay", decodeAgentInterruptDisplay(reader.readObject("approval_display"))),
  };
  const toolSourceReference = reader.readNullableString("tool_source_reference");
  return toolSourceReference === null ? toolCall : { ...toolCall, toolSourceReference };
}

export function encodeQuestionAnswer(value: QuestionAnswer): unknown {
  return {
    interrupt_id: value.interruptId,
    question: value.question,
    answer: value.answer,
    resolution: value.resolution ?? "answered",
  };
}

export function decodeQuestionAnswer(value: unknown): QuestionAnswer {
  const reader = new WireRecordReader(value, "QuestionAnswer");
  return {
    interruptId: reader.readString("interrupt_id"),
    question: reader.readString("question"),
    answer: reader.readString("answer"),
    resolution: reader.readNullableString("resolution") === null ? "answered" : reader.readEnum("resolution", questionResolutions),
  };
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
    question: value.question ?? null,
    choices: [...(value.choices ?? [])],
    allows_free_text: value.allowsFreeText ?? true,
    display: encodeAgentInterruptDisplay(value.display),
  };
}

export function decodeAgentInterrupt(value: unknown): AgentInterrupt {
  const reader = new WireRecordReader(value, "AgentInterrupt");
  return {
    interruptId: reader.readString("interrupt_id"),
    kind: reader.readEnum("kind", agentInterruptKinds),
    toolCallId: reader.readString("tool_call_id"),
    toolName: reader.readString("tool_name"),
    arguments: reader.readString("arguments"),
    createdAt: reader.readString("created_at"),
    deadline: reader.readNullableString("deadline"),
    question: reader.readNullableString("question"),
    choices: reader.readStringArrayOrEmpty("choices"),
    allowsFreeText: reader.readBooleanOrDefault("allows_free_text", true),
    ...omitNullDisplay("display", decodeAgentInterruptDisplay(reader.readObject("display"))),
  };
}

export function encodeAgentInterruptResolution(value: AgentInterruptResolution): unknown {
  return { approved: value.approved, rejection_message: value.rejectionMessage, answer: value.answer ?? null };
}

export function decodeAgentInterruptResolution(value: unknown): AgentInterruptResolution {
  const reader = new WireRecordReader(value, "AgentInterruptResolution");
  return {
    approved: reader.readBoolean("approved"),
    rejectionMessage: reader.readNullableString("rejection_message"),
    answer: reader.readNullableString("answer"),
  };
}

export function encodeAnswerQuestionRequest(value: AnswerQuestionRequest): unknown {
  return { interrupt_id: value.interruptId, answer: value.answer };
}

export function decodeAnswerQuestionRequest(value: unknown): AnswerQuestionRequest {
  const reader = new WireRecordReader(value, "AnswerQuestionRequest");
  return { interruptId: reader.readString("interrupt_id"), answer: reader.readString("answer") };
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
  return {
    text: value.text,
    plan_mode: value.planMode ?? false,
    images: encodeImages(value.images),
    ...encodeSenderName(value.senderName),
  };
}

export function decodeAgentUserMessage(value: unknown): AgentUserMessage {
  const reader = new WireRecordReader(value, "AgentUserMessage");
  const planMode = reader.readBooleanOrDefault("plan_mode", false);
  return {
    text: reader.readString("text"),
    ...(planMode ? { planMode } : {}),
    ...decodeImages(reader),
    ...decodeSenderName(reader),
  };
}

export function encodeAgentPlan(value: AgentPlan): unknown {
  return {
    revision: value.revision,
    status: value.status,
    tasks: value.tasks.map(encodeAgentPlanTask),
    updated_at: value.updatedAt,
  };
}

export function decodeAgentPlan(value: unknown): AgentPlan {
  const reader = new WireRecordReader(value, "AgentPlan");
  return {
    revision: reader.readInteger("revision"),
    status: reader.readEnum("status", planStatuses),
    tasks: reader.readArrayOrEmpty("tasks").map(decodeAgentPlanTask),
    updatedAt: reader.readString("updated_at"),
  };
}

export function encodeAgentPlanTask(value: AgentPlanTask): unknown {
  return { content: value.content, status: value.status };
}

export function decodeAgentPlanTask(value: unknown): AgentPlanTask {
  const reader = new WireRecordReader(value, "AgentPlanTask");
  return { content: reader.readString("content"), status: reader.readEnum("status", planTaskStatuses) };
}

export function encodePlanRevisionRequest(value: ExecutePlanRequest | PlanExecutionRequest): unknown {
  return { revision: value.revision };
}

export function decodePlanRevisionRequest(value: unknown): PlanExecutionRequest {
  const reader = new WireRecordReader(value, "PlanExecutionRequest");
  return { revision: reader.readInteger("revision") };
}

export function encodePendingUserMessage(value: PendingUserMessage): unknown {
  return { message_id: value.messageId, message: encodeAgentUserMessage(value.message) };
}

export function decodePendingUserMessage(value: unknown): PendingUserMessage {
  const reader = new WireRecordReader(value, "PendingUserMessage");
  return { messageId: reader.readString("message_id"), message: decodeAgentUserMessage(reader.readObject("message")) };
}

export function encodeSteerUserMessageRequest(value: SteerUserMessageRequest): unknown {
  return { message_id: value.messageId, text: value.text, ...encodeSenderName(value.senderName) };
}

export function decodeSteerUserMessageRequest(value: unknown): SteerUserMessageRequest {
  const reader = new WireRecordReader(value, "SteerUserMessageRequest");
  return {
    messageId: reader.readNullableString("message_id"),
    text: reader.readNullableString("text"),
    ...decodeSenderName(reader),
  };
}

export function encodeDeleteQueuedMessageRequest(value: DeleteQueuedMessageRequest): unknown {
  return { message_id: value.messageId };
}

export function decodeDeleteQueuedMessageRequest(value: unknown): DeleteQueuedMessageRequest {
  const reader = new WireRecordReader(value, "DeleteQueuedMessageRequest");
  return { messageId: reader.readString("message_id") };
}

export function encodeCancelTurnRequest(value: CancelTurnRequest): unknown {
  return { turn_id: value.turnId };
}

export function decodeCancelTurnRequest(value: unknown): CancelTurnRequest {
  if (value === null || value === undefined) {
    return { turnId: null };
  }
  const reader = new WireRecordReader(value, "CancelTurnRequest");
  return { turnId: reader.readNullableString("turn_id") };
}

export function encodeTurnControlRequest(value: TurnControlRequest): unknown {
  return { kind: value.kind };
}

export function decodeTurnControlRequest(value: unknown): TurnControlRequest {
  const reader = new WireRecordReader(value, "TurnControlRequest");
  return { kind: reader.readEnum("kind", turnControlKinds) };
}

export function encodeTurnCancellationRequest(value: TurnCancellationRequest): unknown {
  return { turn_id: value.turnId, requested_at: value.requestedAt };
}

export function decodeTurnCancellationRequest(value: unknown): TurnCancellationRequest {
  const reader = new WireRecordReader(value, "TurnCancellationRequest");
  return { turnId: reader.readString("turn_id"), requestedAt: reader.readString("requested_at") };
}

export function encodeSessionTerminalLease(value: SessionTerminalLease): unknown {
  return {
    holder_id: value.holderId,
    host_name: value.hostName,
    process_id: value.processId,
    opened_at: value.openedAt,
    expires_at: value.expiresAt,
  };
}

export function decodeSessionTerminalLease(value: unknown): SessionTerminalLease {
  const reader = new WireRecordReader(value, "SessionTerminalLease");
  return {
    holderId: reader.readString("holder_id"),
    hostName: reader.readString("host_name"),
    processId: reader.readInteger("process_id"),
    openedAt: reader.readString("opened_at"),
    expiresAt: reader.readString("expires_at"),
  };
}

export function encodeAcquireSessionTerminalLeaseRequest(value: AcquireSessionTerminalLeaseRequest): unknown {
  return {
    holder_id: value.holderId,
    host_name: value.hostName,
    process_id: value.processId,
    ttl_seconds: value.ttlSeconds,
    ...(value.environment === undefined || value.environment === null ? {} : { environment: { ...value.environment } }),
  };
}

export function decodeAcquireSessionTerminalLeaseRequest(value: unknown): AcquireSessionTerminalLeaseRequest {
  const reader = new WireRecordReader(value, "AcquireSessionTerminalLeaseRequest");
  const environment = reader.readObject("environment");
  return {
    holderId: reader.readString("holder_id"),
    hostName: reader.readString("host_name"),
    processId: reader.readInteger("process_id"),
    ttlSeconds: reader.readInteger("ttl_seconds"),
    ...(environment === undefined || environment === null
      ? {}
      : { environment: decodeTextMap(environment, "AcquireSessionTerminalLeaseRequest.environment") }),
  };
}

export function encodeSessionTerminalEnvironment(value: Readonly<Record<string, string>>): unknown {
  return { ...value };
}

export function decodeSessionTerminalEnvironment(value: unknown): Readonly<Record<string, string>> {
  return decodeTextMap(value, "SessionTerminalEnvironment");
}

function decodeTextMap(value: unknown, typeName: string): Readonly<Record<string, string>> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new TypeError(`${typeName} must be a JSON object`);
  }
  const entries = Object.entries(value as Record<string, unknown>);
  for (const [key, entry] of entries) {
    if (typeof entry !== "string") {
      throw new TypeError(`${typeName}.${key} must be a string`);
    }
  }
  return Object.fromEntries(entries) as Record<string, string>;
}

export function encodeSessionTerminalLeaseResult(value: SessionTerminalLeaseResult): unknown {
  return {
    is_acquired: value.isAcquired,
    lease: value.lease === null ? null : encodeSessionTerminalLease(value.lease),
  };
}

export function decodeSessionTerminalLeaseResult(value: unknown): SessionTerminalLeaseResult {
  const reader = new WireRecordReader(value, "SessionTerminalLeaseResult");
  const lease = reader.readObject("lease");
  return {
    isAcquired: reader.readBoolean("is_acquired"),
    lease: lease == null ? null : decodeSessionTerminalLease(lease),
  };
}

export function encodeReleaseSessionTerminalLeaseRequest(value: ReleaseSessionTerminalLeaseRequest): unknown {
  return { holder_id: value.holderId };
}

export function decodeReleaseSessionTerminalLeaseRequest(value: unknown): ReleaseSessionTerminalLeaseRequest {
  const reader = new WireRecordReader(value, "ReleaseSessionTerminalLeaseRequest");
  return { holderId: reader.readString("holder_id") };
}

export function encodeToolCallCancellation(value: ToolCallCancellation): unknown {
  return { reason: value.reason };
}

export function decodeToolCallCancellation(value: unknown): ToolCallCancellation {
  const reader = new WireRecordReader(value, "ToolCallCancellation");
  return { reason: reader.readEnum("reason", toolCallCancellationReasons) };
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

export function encodeContextSummary(value: ContextSummary): unknown {
  return {
    generation: value.generation,
    first_sequence: value.firstSequence,
    last_sequence: value.lastSequence,
    text: value.text,
    created_at: value.createdAt,
  };
}

export function decodeContextSummary(value: unknown): ContextSummary {
  const reader = new WireRecordReader(value, "ContextSummary");
  return {
    generation: reader.readInteger("generation"),
    firstSequence: reader.readInteger("first_sequence"),
    lastSequence: reader.readInteger("last_sequence"),
    text: reader.readString("text"),
    createdAt: reader.readString("created_at"),
  };
}

export function encodeAgentMessageChunk(value: AgentMessageChunk): unknown {
  return { messages: value.messages.map(encodeAgentMessage) };
}

export function decodeAgentMessageChunk(value: unknown): AgentMessageChunk {
  const reader = new WireRecordReader(value, "AgentMessageChunk");
  return { messages: reader.readArray("messages").map(decodeAgentMessage) };
}

export function encodeAgentMessageChunkRange(value: AgentMessageChunkRange): unknown {
  return { first_sequence: value.firstSequence, last_sequence: value.lastSequence };
}

export function decodeAgentMessageChunkRange(value: unknown): AgentMessageChunkRange {
  const reader = new WireRecordReader(value, "AgentMessageChunkRange");
  return {
    firstSequence: reader.readInteger("first_sequence"),
    lastSequence: reader.readInteger("last_sequence"),
  };
}

export function encodeAgentMessageIndex(value: AgentMessageIndex): unknown {
  return {
    chunks: value.chunks.map(encodeAgentMessageChunkRange),
    first_sequence: value.firstSequence,
    window_first_sequence: value.windowFirstSequence,
  };
}

export function decodeAgentMessageIndex(value: unknown): AgentMessageIndex {
  const reader = new WireRecordReader(value, "AgentMessageIndex");
  return {
    chunks: reader.readArray("chunks").map(decodeAgentMessageChunkRange),
    firstSequence: reader.readInteger("first_sequence"),
    windowFirstSequence: reader.readInteger("window_first_sequence"),
  };
}

export function encodeTurnReplySequence(value: TurnReplySequence): unknown {
  return { turn_id: value.turnId, sequence: value.sequence };
}

export function decodeTurnReplySequence(value: unknown): TurnReplySequence {
  const reader = new WireRecordReader(value, "TurnReplySequence");
  return { turnId: reader.readString("turn_id"), sequence: reader.readInteger("sequence") };
}

export function encodeCompactContextInput(value: CompactContextInput): unknown {
  return { through_sequence: value.throughSequence };
}

export function decodeCompactContextInput(value: unknown): CompactContextInput {
  const reader = new WireRecordReader(value, "CompactContextInput");
  return { throughSequence: reader.readInteger("through_sequence") };
}

export function encodeAgentSnapshot(value: AgentSnapshot): unknown {
  return {
    state: value.state === null ? null : encodeAgentState(value.state),
    messages: value.messages.map(encodeAgentMessage),
    pending_interrupts: value.pendingInterrupts.map(encodeAgentInterrupt),
    tool_calls: value.toolCalls.map(encodeAgentToolCall),
    usage: encodeAgentUsage(value.usage),
    queued_message_count: value.queuedMessageCount,
    plan: value.plan == null ? null : encodeAgentPlan(value.plan),
    queued_messages: (value.queuedMessages ?? []).map(encodePendingUserMessage),
    steered_messages: (value.steeredMessages ?? []).map(encodePendingUserMessage),
    context_summary:
      value.contextSummary === undefined || value.contextSummary === null
        ? null
        : encodeContextSummary(value.contextSummary),
    session_settings: value.sessionSettings ?? null,
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
    ...(reader.readObject("plan") == null ? {} : { plan: decodeAgentPlan(reader.readObject("plan")) }),
    queuedMessages: reader.readArrayOrEmpty("queued_messages").map(decodePendingUserMessage),
    steeredMessages: reader.readArrayOrEmpty("steered_messages").map(decodePendingUserMessage),
    contextSummary:
      reader.readObject("context_summary") == null
        ? null
        : decodeContextSummary(reader.readObject("context_summary")),
    sessionSettings:
      reader.readObject("session_settings") == null
        ? null
        : decodeSessionSettings(reader.readObject("session_settings")),
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
    deleted_before_sequence: value.deletedBeforeSequence ?? null,
  };
}

export function decodeAgentMessagesPage(value: unknown): AgentMessagesPage {
  const reader = new WireRecordReader(value, "AgentMessagesPage");
  return {
    messages: reader.readArray("messages").map(decodeAgentMessage),
    nextBeforeSequence: reader.readNullableInteger("next_before_sequence"),
    deletedBeforeSequence: reader.readNullableInteger("deleted_before_sequence"),
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
export const agentPlanCodec = snakeCaseJsonCodec("AgentPlan", encodeAgentPlan, decodeAgentPlan);
export const planExecutionRequestCodec = snakeCaseJsonCodec<PlanExecutionRequest>(
  "PlanExecutionRequest",
  encodePlanRevisionRequest,
  decodePlanRevisionRequest,
);
export const executePlanRequestCodec = snakeCaseJsonCodec<ExecutePlanRequest>(
  "ExecutePlanRequest",
  encodePlanRevisionRequest,
  decodePlanRevisionRequest,
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
export const answerQuestionRequestCodec = snakeCaseJsonCodec(
  "AnswerQuestionRequest",
  encodeAnswerQuestionRequest,
  decodeAnswerQuestionRequest,
);
export const steerUserMessageRequestCodec = snakeCaseJsonCodec(
  "SteerUserMessageRequest",
  encodeSteerUserMessageRequest,
  decodeSteerUserMessageRequest,
);
export const deleteQueuedMessageRequestCodec = snakeCaseJsonCodec(
  "DeleteQueuedMessageRequest",
  encodeDeleteQueuedMessageRequest,
  decodeDeleteQueuedMessageRequest,
);
export const cancelTurnRequestCodec = snakeCaseJsonCodec(
  "CancelTurnRequest",
  encodeCancelTurnRequest,
  decodeCancelTurnRequest,
);
export const turnControlRequestCodec = snakeCaseJsonCodec(
  "TurnControlRequest",
  encodeTurnControlRequest,
  decodeTurnControlRequest,
);
export const turnCancellationRequestCodec = snakeCaseJsonCodec(
  "TurnCancellationRequest",
  encodeTurnCancellationRequest,
  decodeTurnCancellationRequest,
);
export const sessionTerminalLeaseCodec = snakeCaseJsonCodec(
  "SessionTerminalLease",
  encodeSessionTerminalLease,
  decodeSessionTerminalLease,
);
export const acquireSessionTerminalLeaseRequestCodec = snakeCaseJsonCodec(
  "AcquireSessionTerminalLeaseRequest",
  encodeAcquireSessionTerminalLeaseRequest,
  decodeAcquireSessionTerminalLeaseRequest,
);
export const sessionTerminalEnvironmentCodec = snakeCaseJsonCodec(
  "SessionTerminalEnvironment",
  encodeSessionTerminalEnvironment,
  decodeSessionTerminalEnvironment,
);
export const sessionTerminalLeaseResultCodec = snakeCaseJsonCodec(
  "SessionTerminalLeaseResult",
  encodeSessionTerminalLeaseResult,
  decodeSessionTerminalLeaseResult,
);
export const releaseSessionTerminalLeaseRequestCodec = snakeCaseJsonCodec(
  "ReleaseSessionTerminalLeaseRequest",
  encodeReleaseSessionTerminalLeaseRequest,
  decodeReleaseSessionTerminalLeaseRequest,
);
export const toolCallCancellationCodec = snakeCaseJsonCodec(
  "ToolCallCancellation",
  encodeToolCallCancellation,
  decodeToolCallCancellation,
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
export const sessionSettingsCodec = snakeCaseJsonCodec(
  "SessionSettings",
  encodeSessionSettings,
  decodeSessionSettings,
);
export const turnSessionSettingsCodec = snakeCaseJsonCodec(
  "TurnSessionSettings",
  encodeTurnSessionSettings,
  decodeTurnSessionSettings,
);
export const updateSessionSettingsRequestCodec = snakeCaseJsonCodec(
  "UpdateSessionSettingsRequest",
  encodeUpdateSessionSettingsRequest,
  decodeUpdateSessionSettingsRequest,
);
export const agentMessageChunkCodec = snakeCaseJsonCodec(
  "AgentMessageChunk",
  encodeAgentMessageChunk,
  decodeAgentMessageChunk,
);
export const agentMessageIndexCodec = snakeCaseJsonCodec(
  "AgentMessageIndex",
  encodeAgentMessageIndex,
  decodeAgentMessageIndex,
);
export const turnReplySequenceCodec = snakeCaseJsonCodec(
  "TurnReplySequence",
  encodeTurnReplySequence,
  decodeTurnReplySequence,
);
export const contextSummaryCodec = snakeCaseJsonCodec(
  "ContextSummary",
  encodeContextSummary,
  decodeContextSummary,
);

/** Input of `CompactContext` and `RecordTurnFailure`: a `CompactContextInput`, or `RunModel`'s JSON `null` as `undefined`. */
export const turnFailureRouteInputCodec: Codec<CompactContextInput | undefined> = {
  typeName: "TurnFailureRouteInput",
  wireKind: "json",
  encode: (value) => ({
    kind: "json",
    data: value === undefined ? "null" : JSON.stringify(encodeCompactContextInput(value)),
  }),
  decode: (value) => {
    if (value.kind !== "json") {
      throw new TypeError(`TurnFailureRouteInput requires JSON, got ${value.kind}`);
    }
    const parsed: unknown = JSON.parse(String(value.data));
    return parsed === null ? undefined : decodeCompactContextInput(parsed);
  },
};
