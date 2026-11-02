// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * HTTP headers of an AI SDK UI message stream response, protocol version `v1`.
 *
 * {@link createAgentUIMessageStreamResponse} and the other Response helpers set them. Use them when you
 * write the Server-Sent Events of {@link streamAgentUIMessageChunks} to a response yourself.
 */
export const agentUIMessageStreamHeaders: Readonly<Record<string, string>> = Object.freeze({
  "content-type": "text/event-stream",
  "cache-control": "no-cache",
  connection: "keep-alive",
  "x-vercel-ai-ui-message-stream": "v1",
  "x-accel-buffering": "no",
});

/**
 * Type of the transient data part that carries the UI stream resume token after each translated event.
 *
 * The AI SDK passes transient data parts to the `onData` callback of `useChat` and never stores them in
 * a message. Keep the latest `data` in memory and send it back as the `resumeToken` of a reconnect, so the
 * stream continues after the last event the browser received.
 */
export const agentResumeTokenDataPartType = "data-agent-resume-token";

/**
 * Type of the transient data part that carries an `AgentEvent` with no other UI message mapping.
 *
 * It carries `usage_recorded`, `tool_call_started`, text written by a tool Step, and adapter-specific
 * events such as `openai_agents.handoff`. Read it in the `onData` callback of `useChat`.
 */
export const agentEventDataPartType = "data-agent-event";

/**
 * One `AgentEvent` in its snake_case wire format, the same JSON in the TypeScript and Python cores.
 *
 * `payload` stays JSON object text, as on the `AgentEvents` Stream. Parse it with `JSON.parse`.
 */
export interface AgentEventJson {
  /** An `AgentEventType` or an adapter-specific type. */
  readonly type: string;
  /** Turn the event belongs to, or `null`. */
  readonly turn_id: string | null;
  /** One-based attempt of the Step that wrote the event. */
  readonly attempt: number;
  /** Dex Step execution ID, `StepType-Number`, of the Step that wrote the event; `""` when absent. */
  readonly step_execution_id: string;
  /** Event payload as JSON object text. */
  readonly payload: string;
}

/**
 * Reason in the `finish` part of an agent UI message stream.
 *
 * - `stop`: the turn completed.
 * - `tool-calls`: the turn waits only for tool approvals; the stream ends so `useChat` can send them.
 * - `error`: the turn failed; an `error` part with the failure text comes first.
 * - `other`: the turn ended without its end event on the Stream, or the session Flow closed.
 */
export type AgentUIMessageFinishReason = "stop" | "tool-calls" | "error" | "other";

/**
 * One part of an AI SDK UI message stream (protocol `v1`) that the agent UI stream adapters emit.
 *
 * Each member is a structural subset of the AI SDK `UIMessageChunk` type, so a stream of these parts can be
 * passed to AI SDK functions such as `createUIMessageStreamResponse` and `readUIMessageStream`.
 *
 * - `start` opens the turn's assistant message; its `messageId` is {@link agentAssistantUIMessageId}.
 * - `start-step`, `finish-step`, and `reset-step` frame one `RunModel` Step execution. `reset-step` removes
 *   the parts of an interrupted attempt when a new attempt of the same Step execution starts.
 * - `text-*` and `reasoning-*` carry the model call's live text.
 * - `tool-*` parts follow each tool call as a dynamic tool: requested, waiting for approval, approved or
 *   rejected, and finished.
 * - Transient data parts carry the resume token and unmapped events.
 * - `error` and `finish` end the stream.
 */
export type AgentUIMessageChunk =
  | { readonly type: "start"; readonly messageId: string }
  | { readonly type: "start-step" }
  | { readonly type: "finish-step" }
  | { readonly type: "reset-step" }
  | { readonly type: "text-start"; readonly id: string }
  | { readonly type: "text-delta"; readonly id: string; readonly delta: string }
  | { readonly type: "text-end"; readonly id: string }
  | { readonly type: "reasoning-start"; readonly id: string }
  | { readonly type: "reasoning-delta"; readonly id: string; readonly delta: string }
  | { readonly type: "reasoning-end"; readonly id: string }
  | {
      readonly type: "tool-input-available";
      readonly toolCallId: string;
      readonly toolName: string;
      readonly input: unknown;
      readonly dynamic: true;
    }
  | { readonly type: "tool-approval-request"; readonly approvalId: string; readonly toolCallId: string }
  | {
      readonly type: "tool-approval-response";
      readonly approvalId: string;
      readonly approved: boolean;
      readonly reason?: string;
    }
  | {
      readonly type: "tool-output-available";
      readonly toolCallId: string;
      readonly output: unknown;
      readonly dynamic: true;
    }
  | {
      readonly type: "tool-output-error";
      readonly toolCallId: string;
      readonly errorText: string;
      readonly dynamic: true;
    }
  | { readonly type: "tool-output-denied"; readonly toolCallId: string }
  | { readonly type: typeof agentResumeTokenDataPartType; readonly data: string; readonly transient: true }
  | { readonly type: typeof agentEventDataPartType; readonly data: AgentEventJson; readonly transient: true }
  | { readonly type: "error"; readonly errorText: string }
  | { readonly type: "finish"; readonly finishReason: AgentUIMessageFinishReason };

/**
 * One tool call of an agent session as an AI SDK dynamic tool part.
 *
 * `state` follows the AI SDK tool part states. Every state carries `input`, the parsed tool arguments, or
 * the arguments text when it is not JSON. `output-available` carries `output`, the tool output text.
 * `output-error` carries `errorText`, the text the model saw for a `failed` or `outcome_unknown` call.
 * `approval` appears on calls that needed approval. Its `id` is the `interrupt_id` for `resolve_interrupt`,
 * `approved` is the decision once made, and `reason` is the rejection message.
 */
export type AgentDynamicToolUIPart = {
  /** Always `dynamic-tool`: the browser needs no tool schema to render it. */
  readonly type: "dynamic-tool";
  /** Tool name the model called. */
  readonly toolName: string;
  /** Framework tool call ID; equal to the approval ID of a call that needs approval. */
  readonly toolCallId: string;
} & (
  | { readonly state: "input-available"; readonly input: unknown }
  | {
      readonly state: "approval-requested";
      readonly input: unknown;
      readonly approval: { readonly id: string };
    }
  | {
      readonly state: "approval-responded";
      readonly input: unknown;
      readonly approval: { readonly id: string; readonly approved: boolean };
    }
  | {
      readonly state: "output-available";
      readonly input: unknown;
      readonly output: unknown;
      readonly approval?: { readonly id: string; readonly approved: true };
    }
  | {
      readonly state: "output-error";
      readonly input: unknown;
      readonly errorText: string;
      readonly approval?: { readonly id: string; readonly approved: true };
    }
  | {
      readonly state: "output-denied";
      readonly input: unknown;
      readonly approval: { readonly id: string; readonly approved: false; readonly reason?: string };
    }
);

/**
 * One part of an {@link AgentUIMessage}.
 *
 * `step-start` opens one model call. `text` is one finished model text in `text`. A dynamic tool part is one
 * tool call; see {@link AgentDynamicToolUIPart}.
 */
export type AgentUIMessagePart =
  | { readonly type: "step-start" }
  | { readonly type: "text"; readonly text: string; readonly state: "done" }
  | AgentDynamicToolUIPart;

/**
 * One stored agent message as an AI SDK `UIMessage`.
 *
 * A user message holds one text part. An assistant message holds every model text and tool call of one
 * turn, with a `step-start` part before each model call. It is a structural subset of the AI SDK
 * `UIMessage` type, so the array can be passed to `useChat` as its initial `messages`.
 */
export interface AgentUIMessage {
  /** {@link agentUserUIMessageId} for a user message, {@link agentAssistantUIMessageId} for an assistant message. */
  readonly id: string;
  /** Message author. */
  readonly role: "user" | "assistant";
  /** Message parts in order. */
  parts: AgentUIMessagePart[];
}

/**
 * Returns the AI SDK message ID of a turn's assistant message.
 *
 * The live UI message stream and {@link convertAgentSnapshotToUIMessages} use the same ID, so a stored
 * message replaces the streamed one in place.
 *
 * @param turnId - Turn ID, such as `"turn-3"`.
 * @returns `<turnId>-assistant`, such as `"turn-3-assistant"`.
 */
export function agentAssistantUIMessageId(turnId: string): string {
  return `${turnId}-assistant`;
}

/**
 * Returns the AI SDK message ID of a stored user message.
 *
 * @param sequence - Stored message sequence.
 * @returns `message-<sequence>`, such as `"message-7"`.
 */
export function agentUserUIMessageId(sequence: number): string {
  return `message-${sequence}`;
}
