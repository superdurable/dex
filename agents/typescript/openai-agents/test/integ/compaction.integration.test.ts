// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  ContextWindowSessionScenario,
  PrivateDexEngine,
  defaultContextWindowSettings,
  type ContextWindowSettings,
} from "@superdurable/dex-agents/testing";

import {
  contextWindowStateDirectoryVariable,
  createContextWindowAgentFlow,
} from "../fixtures/context-window-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/context-window-agent-worker.js", import.meta.url));

describe("OpenAI Agents SDK agent sessions outlive the context window", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let scenario: ContextWindowSessionScenario | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createContextWindowAgentFlow(join(engine.stateDirectory, "client-unused"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  afterEach(async () => {
    await scenario?.close();
    scenario = undefined;
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  function createScenario(
    settings: ContextWindowSettings = defaultContextWindowSettings,
  ): ContextWindowSessionScenario {
    const stateDirectory = mkdtempSync(join(engine.stateDirectory, "context-window-"));
    scenario = new ContextWindowSessionScenario({
      serverAddress: engine.serverAddress,
      sessions,
      stateDirectory,
      settings,
      workerEntryModulePath,
      workerEnvironment: { [contextWindowStateDirectoryVariable]: stateDirectory },
    });
    return scenario;
  }

  test("a session keeps working past the context window", async () => {
    await createScenario().assertOutlivesTheWindow();
  });

  test("retention deletes only chunks behind the summary", async () => {
    const settings = { ...defaultContextWindowSettings, retainedMessageLimit: 8 };
    await createScenario(settings).assertRetentionFollowsTheSummary();
  });

  test("without a policy nothing changes and the window ends the session", async () => {
    const settings = { ...defaultContextWindowSettings, isCompactionEnabled: false };
    await createScenario(settings).assertFailsAtTheWindowWithoutAPolicy();
  });

  test("a session started without a policy compacts after an upgrade", async () => {
    await createScenario().assertStartsCompactingAfterAnUpgrade();
  });

  test("a Worker killed during CompactContext reruns only the summary", async () => {
    await createScenario().assertWorkerKillRerunsOnlyTheSummary();
  });
});
