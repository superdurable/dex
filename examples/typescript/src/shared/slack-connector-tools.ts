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
export const slackConnectionReference: ConnectionRef = { provider: ProviderID, name: "agent-slack" };

/**
 * Opens the Slack connection from `SLACK_BOT_TOKEN` and `SLACK_USER_TOKEN`. The bot token posts; the user token
 * reads a reply back when a retry must confirm an earlier post. The tokens stay in the Worker's memory and never
 * enter Flow state.
 *
 * @returns The connection, or `undefined` after logging one line when either variable is unset.
 */
export function openSlackConnectionFromEnvironment(): Connection | undefined {
  const botToken = process.env.SLACK_BOT_TOKEN ?? "";
  const userToken = process.env.SLACK_USER_TOKEN ?? "";
  if (botToken === "" || userToken === "") {
    console.warn("SLACK_BOT_TOKEN or SLACK_USER_TOKEN is not set, so the agent examples offer no Slack connector tools");
    return undefined;
  }
  const credentials: Credentials = { botToken: new SecretString(botToken), userToken: new SecretString(userToken) };
  const credentialProvider = new StaticCredentialProvider([[slackConnectionReference, credentials]]);
  return newConnection(new SlackClient({}, credentialProvider), slackConnectionReference);
}

/**
 * Offers the reviewed Slack operation `postThreadReply` as the tool `slack_post_thread_reply`. Its manifest makes it
 * a `non_idempotent_write` that runs once in its own tool Step. The manifest does not ask for approval, so this
 * example requires it: a person approves every post before it reaches Slack.
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
        requiresApproval: true,
      }),
    ]),
  ];
}
