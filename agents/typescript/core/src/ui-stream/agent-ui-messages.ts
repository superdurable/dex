// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Client } from "@superdurable/dex";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "../agent-framework-runner.js";
import type { AgentSessionFlow } from "../agent-session-flow.js";
import {
  describeToolCallOutcome,
  type AgentMessage,
  type AgentSnapshot,
  type AgentToolCallRecord,
} from "../agent-session-values.js";
import { createTurnReplayCursor, parseToolArguments } from "./agent-ui-message-stream.js";
import {
  agentAssistantUIMessageId,
  agentUserUIMessageId,
  type AgentDynamicToolUIPart,
  type AgentUIMessage,
} from "./agent-ui-message-types.js";
import { encodeAgentUIStreamCursor } from "./agent-ui-stream-cursor.js";

/** The stored conversation of a session as AI SDK messages, and the token that streams its active turn. */
export interface AgentSessionUIMessages {
  /** Messages from {@link convertAgentSnapshotToUIMessages}. */
  readonly messages: AgentUIMessage[];
  /**
   * UI stream resume token that replays the active turn from its first retained event, or `null` when no
   * turn is active or the Stream no longer holds the active turn. With `null` and an active turn, the
   * messages still show the turn's tool calls and approvals; poll this function until the turn ends.
   */
  readonly resumeToken: string | null;
}

/**
 * Converts a `get_agent_snapshot` result to AI SDK `UIMessage` objects for `useChat`.
 *
 * Each stored user message becomes a user message with ID {@link agentUserUIMessageId}. The stored
 * assistant and tool messages of one turn become one assistant message with ID
 * {@link agentAssistantUIMessageId}: a `step-start` part before each model call, a text part per model
 * text, and a dynamic tool part per tool call. A stored tool result shows the text the model saw, so a
 * failed or rejected call appears as `output-available` with that text. A turn-failure note is a stored
 * user message and appears as one.
 *
 * The session stores a turn's messages when the turn ends. For the active turn the function adds the tool
 * calls of `snapshot.toolCalls` to the turn's assistant message: approvals waiting for
 * `resolve_interrupt` are `approval-requested`, running calls are `input-available` or
 * `approval-responded`, and finished calls show their result. The active turn's live text exists only on
 * the Stream; a UI message stream started without a token replays it and replaces this message.
 *
 * @example
 * ```ts
 * const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
 * const messages = convertAgentSnapshotToUIMessages(snapshot);
 * ```
 * @param snapshot - Output of the `get_agent_snapshot` RPC; it holds the last 200 messages.
 * @returns New messages in conversation order. The function does not change `snapshot`.
 */
export function convertAgentSnapshotToUIMessages(snapshot: AgentSnapshot): AgentUIMessage[] {
  const uiMessages: AgentUIMessage[] = [];
  let assistantMessage: AgentAssistantUIMessageBuilder | undefined;
  for (const message of snapshot.messages) {
    if (message.role === "user") {
      assistantMessage = undefined;
      uiMessages.push({
        id: agentUserUIMessageId(message.sequence),
        role: "user",
        parts: [{ type: "text", text: message.text ?? "", state: "done" }],
      });
      continue;
    }
    if (assistantMessage?.turnId !== message.turnId) {
      assistantMessage = new AgentAssistantUIMessageBuilder(message.turnId);
      uiMessages.push(assistantMessage.message);
    }
    assistantMessage.appendStoredMessage(message);
  }
  const activeTurnId = snapshot.state?.activeTurnId ?? null;
  if (activeTurnId !== null && snapshot.toolCalls.length > 0) {
    if (assistantMessage?.turnId !== activeTurnId) {
      assistantMessage = new AgentAssistantUIMessageBuilder(activeTurnId);
      uiMessages.push(assistantMessage.message);
    }
    assistantMessage.appendActiveToolCalls(snapshot.toolCalls);
  }
  return uiMessages;
}

/**
 * Reads an agent session as AI SDK messages and the UI stream resume token of its active turn.
 *
 * Load it when a page opens. Pass `messages` to `useChat` as its initial messages, and when `resumeToken`
 * is not `null`, resume the stream with it, so the active turn's live text replays into the turn's
 * assistant message. The token stays valid after the turn ends; the replay then finishes at once. The
 * `get_agent_snapshot` RPC needs a running Worker.
 *
 * @example
 * ```ts
 * // GET /api/chat/:id
 * const { messages, resumeToken } = await readAgentSessionUIMessages({ client, flow, flowId: sessionId });
 * return Response.json({ messages, resumeToken });
 * ```
 * @param options - Client, Flow, and session ID.
 * @returns The messages and the resume token.
 * @throws {DexServiceError} When the RPC or a Stream read fails.
 */
export async function readAgentSessionUIMessages(options: {
  readonly client: Client;
  readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  readonly flowId: string;
}): Promise<AgentSessionUIMessages> {
  const { client, flow, flowId } = options;
  const snapshot = await client.invokeRPC(flow.getAgentSnapshot, flowId);
  const messages = convertAgentSnapshotToUIMessages(snapshot);
  const activeTurnId = snapshot.state?.activeTurnId ?? null;
  if (activeTurnId === null) {
    return { messages, resumeToken: null };
  }
  const cursor = await createTurnReplayCursor(client, flow, flowId, activeTurnId, null);
  return { messages, resumeToken: cursor === undefined ? null : encodeAgentUIStreamCursor(cursor) };
}

/** Collects one turn's stored assistant and tool messages into one assistant message. */
class AgentAssistantUIMessageBuilder {
  public readonly message: AgentUIMessage;
  private isAfterToolResult = false;

  public constructor(public readonly turnId: string) {
    this.message = {
      id: agentAssistantUIMessageId(turnId),
      role: "assistant",
      parts: [{ type: "step-start" }],
    };
  }

  public appendStoredMessage(message: AgentMessage): void {
    if (message.role === "tool") {
      this.appendToolResult(message);
      this.isAfterToolResult = true;
      return;
    }
    // A model call follows the tool results it received.
    if (this.isAfterToolResult) {
      this.message.parts.push({ type: "step-start" });
      this.isAfterToolResult = false;
    }
    if (message.text !== null && message.text !== "") {
      this.message.parts.push({ type: "text", text: message.text, state: "done" });
    }
    if (message.toolCallId !== null) {
      this.message.parts.push({
        type: "dynamic-tool",
        toolName: message.toolName ?? "",
        toolCallId: message.toolCallId,
        state: "input-available",
        input: parseToolArguments(message.toolArguments),
      });
    }
  }

  public appendActiveToolCalls(toolCalls: readonly AgentToolCallRecord[]): void {
    const lastPart = this.message.parts.at(-1);
    if (lastPart?.type !== "step-start") {
      this.message.parts.push({ type: "step-start" });
    }
    for (const toolCall of toolCalls) {
      this.message.parts.push(convertToolCallRecordToUIPart(toolCall));
    }
  }

  private appendToolResult(message: AgentMessage): void {
    const toolCallId = message.toolCallId ?? "";
    const index = this.message.parts.findIndex(
      (part) => part.type === "dynamic-tool" && part.toolCallId === toolCallId,
    );
    const toolCallPart = this.message.parts[index];
    const requestedPart = toolCallPart?.type === "dynamic-tool" ? toolCallPart : undefined;
    const resultPart: AgentDynamicToolUIPart = {
      type: "dynamic-tool",
      toolName: message.toolName ?? requestedPart?.toolName ?? "",
      toolCallId,
      state: "output-available",
      input: requestedPart?.input,
      output: message.toolOutput ?? message.text ?? "",
    };
    if (requestedPart === undefined) {
      this.message.parts.push(resultPart);
    } else {
      this.message.parts[index] = resultPart;
    }
  }
}

function convertToolCallRecordToUIPart(toolCall: AgentToolCallRecord): AgentDynamicToolUIPart {
  const toolPart = {
    type: "dynamic-tool",
    toolName: toolCall.toolName,
    toolCallId: toolCall.toolCallId,
    input: parseToolArguments(toolCall.arguments),
  } as const;
  // A tool approval's interrupt ID is its tool call ID.
  const approvalId = toolCall.toolCallId;
  const grantedApproval = toolCall.requiresApproval
    ? { approval: { id: approvalId, approved: true as const } }
    : {};
  switch (toolCall.status) {
    case "awaiting_approval":
      return { ...toolPart, state: "approval-requested", approval: { id: approvalId } };
    case "approved":
      return toolCall.requiresApproval
        ? { ...toolPart, state: "approval-responded", approval: { id: approvalId, approved: true } }
        : { ...toolPart, state: "input-available" };
    case "awaiting_answer":
    case "waiting_for_timer":
      // A question or a durable wait holds the call; the browser shows it running, as for an approved call.
      return toolCall.requiresApproval
        ? { ...toolPart, state: "approval-responded", approval: { id: approvalId, approved: true } }
        : { ...toolPart, state: "input-available" };
    case "succeeded":
      return { ...toolPart, state: "output-available", output: toolCall.output ?? "", ...grantedApproval };
    case "failed":
    case "outcome_unknown":
      return {
        ...toolPart,
        state: "output-error",
        errorText: describeToolCallOutcome(toolCall),
        ...grantedApproval,
      };
    case "rejected":
      return {
        ...toolPart,
        state: "output-denied",
        approval: {
          id: approvalId,
          approved: false,
          ...(toolCall.rejectionMessage === null ? {} : { reason: toolCall.rejectionMessage }),
        },
      };
  }
}
