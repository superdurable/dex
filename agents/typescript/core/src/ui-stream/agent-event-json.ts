// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { encodeAgentSnapshot } from "../agent-session-codecs.js";
import type { AgentEvent, AgentSnapshot } from "../agent-session-values.js";
import type { AgentEventJson } from "./agent-ui-message-types.js";

/**
 * Converts an `AgentEvent` to its snake_case wire JSON, the format of the plain Server-Sent Events stream.
 *
 * @example
 * ```ts
 * const message = await client.readStream(sessionId, flow.agentEvents, resumeToken);
 * response.write(`data: ${JSON.stringify(convertAgentEventToJson(message.value))}\n\n`);
 * ```
 * @param event - Event read from the `AgentEvents` Stream.
 * @returns A new object with `type`, `turn_id`, `attempt`, `step_execution_id`, and `payload` text.
 */
export function convertAgentEventToJson(event: AgentEvent): AgentEventJson {
  return {
    type: event.type,
    turn_id: event.turnId,
    attempt: event.attempt,
    step_execution_id: event.stepExecutionId,
    payload: event.payload,
  };
}

// Replay, routing, tracing, and tool resume state that only the session's Steps read.
const workerOnlyMessageFieldNames: ReadonlySet<string> = new Set(["framework_item"]);
const workerOnlyToolCallFieldNames: ReadonlySet<string> = new Set([
  "tool_source_reference",
  "trace_context",
  "input_continuation",
  "input_answers",
]);

/**
 * Converts an `AgentSnapshot` to the snake_case JSON a browser reads, the same JSON the Python core produces.
 *
 * Use it when a browser shared with Python backends reads the snapshot. It is the `get_agent_snapshot` wire
 * format without the fields only the session's Steps use. Messages leave out `framework_item`, the
 * framework's replay data, which can hold the agent's instructions. Tool calls leave out
 * `tool_source_reference`, `trace_context`, `input_continuation`, and `input_answers`.
 *
 * @param snapshot - Output of the `get_agent_snapshot` RPC.
 * @returns A new JSON object with `state`, `messages`, `pending_interrupts`, `tool_calls`, `usage`, and
 * `queued_message_count`.
 */
export function convertAgentSnapshotToJson(snapshot: AgentSnapshot): Readonly<Record<string, unknown>> {
  const snapshotJson = encodeAgentSnapshot(snapshot) as Record<string, unknown>;
  return {
    ...snapshotJson,
    messages: (snapshotJson["messages"] as Record<string, unknown>[]).map((message) =>
      omitJsonFields(message, workerOnlyMessageFieldNames),
    ),
    tool_calls: (snapshotJson["tool_calls"] as Record<string, unknown>[]).map((toolCall) =>
      omitJsonFields(toolCall, workerOnlyToolCallFieldNames),
    ),
  };
}

function omitJsonFields(
  value: Record<string, unknown>,
  fieldNames: ReadonlySet<string>,
): Record<string, unknown> {
  return Object.fromEntries(Object.entries(value).filter(([fieldName]) => !fieldNames.has(fieldName)));
}
