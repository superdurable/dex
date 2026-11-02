// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * UI stream adapters for agent sessions: an AI SDK UI message stream, a plain Server-Sent Events stream,
 * and session snapshots, all read from the session's `AgentEvents` Stream and resumable after a reconnect.
 *
 * @packageDocumentation
 */

export { convertAgentEventToJson, convertAgentSnapshotToJson } from "./agent-event-json.js";
export {
  agentEventStreamHeaders,
  convertAgentEventStreamSnapshotToJson,
  createAgentEventSseResponse,
  formatAgentEventSseFrame,
  readAgentEventStreamSnapshot,
  streamAgentEventSseFrames,
  type AgentEventSseOptions,
  type AgentEventStreamSnapshot,
  type AgentEventStreamSnapshotJson,
} from "./agent-event-sse.js";
export { defaultAgentEventPollTimeoutMs } from "./agent-event-stream-positions.js";
export {
  streamAgentUIMessageChunks,
  type AgentUIMessageStreamOptions,
} from "./agent-ui-message-stream.js";
export {
  agentResumeTokenHeaderName,
  createAgentUIMessageStreamResponse,
  readAgentToolApprovalResponses,
  resolveAgentToolApproval,
  resolveAgentToolApprovalsWithUIMessageStream,
  sendAgentUserMessageWithUIMessageStream,
  type AgentToolApprovalOptions,
  type AgentToolApprovalResponse,
  type AgentToolApprovalsUIStreamOptions,
  type AgentUIMessageStreamResponseOptions,
  type AgentUserMessageUIStreamOptions,
} from "./agent-ui-message-stream-responses.js";
export {
  agentAssistantUIMessageId,
  agentEventDataPartType,
  agentResumeTokenDataPartType,
  agentUIMessageStreamHeaders,
  agentUserUIMessageId,
  type AgentDynamicToolUIPart,
  type AgentEventJson,
  type AgentUIMessage,
  type AgentUIMessageChunk,
  type AgentUIMessageFinishReason,
  type AgentUIMessagePart,
} from "./agent-ui-message-types.js";
export {
  convertAgentSnapshotToUIMessages,
  readAgentSessionUIMessages,
  type AgentSessionUIMessages,
} from "./agent-ui-messages.js";
export { maximumAgentUIStreamResumeTokenLength } from "./agent-ui-stream-cursor.js";
