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

import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { after, before, describe, it } from "node:test";

import {
  createSlackConnectorToolSources,
  openSlackConnectionFromProject,
} from "../../src/shared/slack-connector-tools.js";

describe("Slack connector tools", () => {
  let configurationDirectory: string;

  before(async () => {
    configurationDirectory = await mkdtemp(join(tmpdir(), "dex-typescript-examples-slack-"));
    await chmod(configurationDirectory, 0o700);
  });

  after(async () => {
    await rm(configurationDirectory, { recursive: true, force: true });
  });

  it("offers no tool without a project configuration", async () => {
    assert.equal(await openSlackConnectionFromProject({}), undefined);
    assert.deepEqual(createSlackConnectorToolSources(undefined), []);
  });

  it("fails on an incomplete hosted project configuration", async () => {
    await assert.rejects(openSlackConnectionFromProject({ DEX_PROJECT_SCOPE: "live" }));
  });

  it("offers no tool when the project has no support-slack connection", async () => {
    const path = await writeLocalConnectionsFile("other-slack.json", "other-slack");
    assert.equal(await openSlackConnectionFromProject({ DEX_CONNECTOR_CONFIG_FILE: path }), undefined);
  });

  it("opens support-slack from the local connections file and offers one tool source", async () => {
    const path = await writeLocalConnectionsFile("support-slack.json", "support-slack");
    const slack = await openSlackConnectionFromProject({ DEX_CONNECTOR_CONFIG_FILE: path });
    assert.deepEqual(slack?.reference, { provider: "slack", name: "support-slack" });
    assert.equal(createSlackConnectorToolSources(slack).length, 1);
  });

  async function writeLocalConnectionsFile(fileName: string, connectionName: string): Promise<string> {
    const path = join(configurationDirectory, fileName);
    const file = {
      schemaVersion: "connectors.dex.dev/local-connections/v1alpha1",
      connections: [
        {
          connectorId: "slack",
          connectionName,
          modulePath: "github.com/superdurable/dex-connectors-library/connectors/slack/go",
          provider: "slack",
          configuration: {},
          credentials: { bot_token: "xoxb-not-a-real-token", user_token: "xoxp-not-a-real-token" },
        },
      ],
    };
    await writeFile(path, JSON.stringify(file), { mode: 0o600 });
    return path;
  }
});
