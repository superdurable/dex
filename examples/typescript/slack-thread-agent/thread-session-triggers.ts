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

// Slack Trigger targets that drive one agent session per thread. A new thread starts the
// session; each reply in the thread becomes the session's next user message.

import {
  newDexFlowTriggerTarget,
  newDexRPCTriggerTarget,
  type TriggerEvent,
  type TriggerOptions,
  type TriggerTarget,
} from "@superdurable/connectors-sdk";
import {
  newMessageTriggerRunner,
  type Connection,
  type MessageEvent,
  type MessageTriggerRunner,
} from "@superdurable/connector-slack";
import type { Client } from "@superdurable/dex";
import type { AgentSessionStartInput, AgentUserMessage } from "@superdurable/dex-agents";

import type { ThreadAgentFlow } from "./thread-agent-flow.ts";

// The wire name of the session's user-message RPC. It keys each reply's request ID.
const sendUserMessageRPCName = "send_user_message";

/** The session ID of a thread. A root message and its replies resolve to the same ID. */
export function resolveThreadSessionId(event: TriggerEvent<MessageEvent>): string {
  const { teamId, channelId, threadTimestamp } = event.payload;
  return `slack-thread-${teamId}-${channelId}-${threadTimestamp}`;
}

/** A session refuses blank text, so the targets skip messages without any. */
function hasText(event: TriggerEvent<MessageEvent>): boolean {
  return event.payload.text.trim() !== "";
}

/** Starts one session per new thread. The event ID is the start's request ID. */
export function newThreadSessionStartTarget(
  client: Client,
  flow: ThreadAgentFlow,
  options?: TriggerOptions,
): TriggerTarget<MessageEvent> {
  return newDexFlowTriggerTarget(
    client,
    flow,
    hasText,
    resolveThreadSessionId,
    (event): AgentSessionStartInput => ({ initialMessage: event.payload.text }),
    options,
  );
}

/** Sends each thread reply to the session once. Every redelivery keeps one request ID. */
export function newThreadReplyTarget(
  client: Client,
  flow: ThreadAgentFlow,
  options?: TriggerOptions,
): TriggerTarget<MessageEvent> {
  return newDexRPCTriggerTarget(
    client,
    flow.sendUserMessage,
    hasText,
    resolveThreadSessionId,
    (event): AgentUserMessage => ({ text: event.payload.text }),
    { ...options, rpcName: sendUserMessageRPCName },
  );
}

/** The Slack channel whose threads drive sessions, and the members who may post to them. */
export interface ThreadSessionChannel {
  /** Slack connection, such as one from newProjectConnection. */
  connection: Connection;
  /** Slack channel ID. */
  channelId: string;
  /** Slack user IDs whose roots and replies reach a session. Slack requires at least one for replies. */
  memberUserIds: string[];
}

/** Runs both Triggers of one channel on one runner, so a root is delivered before its replies. */
export function newThreadSessionTriggerRunner(
  channel: ThreadSessionChannel,
  client: Client,
  flow: ThreadAgentFlow,
): MessageTriggerRunner {
  const matcher = { posterUserIds: channel.memberUserIds };
  return newMessageTriggerRunner({
    connection: channel.connection,
    channelThreadCreatedRoutes: [
      {
        bindingName: "thread-session-start",
        configuration: { channelId: channel.channelId, threadTriggerMatcher: matcher },
        target: newThreadSessionStartTarget(client, flow),
      },
    ],
    threadReplyCreatedRoutes: [
      {
        bindingName: "thread-session-reply",
        configuration: { channelId: channel.channelId, threadReplyMatcher: matcher },
        target: newThreadReplyTarget(client, flow),
      },
    ],
  });
}
