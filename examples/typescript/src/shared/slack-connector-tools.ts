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

import { LocalConnectionsFileEnvironment, loadFromEnvironment } from "@superdurable/connectors-sdk/projectconfig";
import manifest from "@superdurable/connector-slack/dex-connector.json" with { type: "json" };
import {
  ConnectorID,
  PostThreadReplyInputSchema,
  newProjectConnection,
  type Connection,
} from "@superdurable/connector-slack";
import type { ToolSource } from "@superdurable/dex-agents";
import { ConnectorToolSource, connectorTool } from "@superdurable/dex-agents/connectors";

/** The static name of the project's Slack connection that the agent examples post with. */
export const slackConnectionName = "support-slack";

/**
 * Opens the Slack connection `support-slack` from the Dex project configuration. Credentials never enter Flow state.
 *
 * @param environment - Selects the project configuration: `DEX_CONNECTOR_CONFIG_FILE` or the `DEX_PROJECT_*` settings.
 * @returns The connection, or `undefined` after logging one line when the project has no such connection.
 * @throws When the selected project configuration is invalid, such as an incomplete `DEX_PROJECT_*` set.
 */
export async function openSlackConnectionFromProject(
  environment: NodeJS.ProcessEnv = process.env,
): Promise<Connection | undefined> {
  const isHostedConfigurationRequested = Object.keys(environment).some((name) => name.startsWith("DEX_PROJECT_"));
  if (!environment[LocalConnectionsFileEnvironment] && !isHostedConfigurationRequested) {
    console.warn("No Dex project configuration is set, so the agent examples offer no Slack connector tools");
    return undefined;
  }
  const project = await loadFromEnvironment(environment);
  const hasSlackConnection = project.configuration.connections.some(
    (connection) => connection.connectorId === ConnectorID && connection.connectionName === slackConnectionName,
  );
  if (!hasSlackConnection) {
    console.warn(`The Dex project has no Slack connection ${slackConnectionName}, so the agent examples offer no Slack connector tools`);
    return undefined;
  }
  return newProjectConnection(project, slackConnectionName);
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
