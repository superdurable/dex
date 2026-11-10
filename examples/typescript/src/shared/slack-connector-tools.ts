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

import { SecretString, StaticCredentialProvider, type ConnectionRef } from "@superdurable/connectors-sdk";
import manifest from "@superdurable/connector-slack/dex-connector.json" with { type: "json" };
import {
  PostThreadReplyInputSchema,
  ProviderID,
  SlackClient,
  newConnection,
  type Connection,
  type Credentials,
} from "@superdurable/connector-slack";
import type { ToolSource } from "@superdurable/dex-agents";
import { ConnectorToolSource, connectorTool } from "@superdurable/dex-agents/connectors";

/** The logical Slack connection the agent examples post with. Flow state stores only this reference. */
export const slackConnectionReference: ConnectionRef = { provider: ProviderID, name: "support-slack" };

/**
 * Opens the Slack connection with tokens from the environment; they never enter Flow state.
 *
 * @returns The connection, or `undefined` after logging one line when `SLACK_BOT_TOKEN` is unset.
 */
export function openSlackConnectionFromEnvironment(): Connection | undefined {
  const botToken = process.env.SLACK_BOT_TOKEN ?? "";
  if (botToken === "") {
    console.warn("SLACK_BOT_TOKEN is not set, so the agent examples offer no Slack connector tools");
    return undefined;
  }
  const credentials: Credentials = {
    botToken: new SecretString(botToken),
    // Optional: confirms a post after an interrupted attempt; without it that post is outcome_unknown.
    userToken: new SecretString(process.env.SLACK_USER_TOKEN ?? ""),
  };
  const credentialProvider = new StaticCredentialProvider([[slackConnectionReference, credentials]]);
  return newConnection(new SlackClient({}, credentialProvider), slackConnectionReference);
}

/**
 * Offers the Slack operation `postThreadReply` as the tool `slack_post_thread_reply`.
 *
 * @param slack - The Slack connection, or `undefined` to offer no Slack tools.
 * @returns The tool sources to pass as `toolSources`; empty without a connection.
 */
export function createSlackConnectorToolSources(slack: Connection | undefined): ToolSource[] {
  if (slack === undefined) {
    return [];
  }
  return [
    new ConnectorToolSource([
      connectorTool(slack.operations.postThreadReply(), slack.reference, {
        manifest,
        inputValidator: PostThreadReplyInputSchema,
        // The manifest does not ask for approval; this agent asks before every post.
        requiresApproval: true,
      }),
    ]),
  ];
}
