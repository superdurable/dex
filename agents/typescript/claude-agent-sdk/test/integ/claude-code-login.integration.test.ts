// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { mkdtempSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, beforeEach, describe, test } from "node:test";

import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { claudeTestAgentFlowType, type ClaudeTestAgentCli } from "../fixtures/claude-test-agent.js";
import { fakeClaudeEnvironmentLogName } from "../fixtures/fake-claude-cli-environment.js";

const secureStorageVariable = "CLAUDE_SECURESTORAGE_CONFIG_DIR";
const applicationSecureStorageDirectory = "/srv/claude/secure-storage";
const mockApiKey = "sk-ant-mock-not-a-real-key";
// The in-process Worker and the SDK read these from this process.
const isolatedVariables = ["CLAUDE_CONFIG_DIR", secureStorageVariable, "HOME"] as const;

type CliEnvironmentRecord = Readonly<Record<string, string | null>>;

describe("Claude agent CLI login across turns", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;
  let savedVariables: Map<string, string | undefined>;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  beforeEach(() => {
    savedVariables = new Map(isolatedVariables.map((name) => [name, process.env[name]]));
    delete process.env[secureStorageVariable];
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
    for (const [name, value] of savedVariables) {
      if (value === undefined) {
        delete process.env[name];
      } else {
        process.env[name] = value;
      }
    }
  });

  after(async () => {
    await engine?.stop();
  });

  async function runTwoTurns(cli?: ClaudeTestAgentCli): Promise<[CliEnvironmentRecord, CliEnvironmentRecord]> {
    harness = await ClaudeAgentTestHarness.start(engine, true, cli === undefined ? undefined : () => cli);
    const sessionId = harness.newSessionId("login");
    await harness.sessions.startSession(sessionId, "say one");
    await harness.waitForTurns(sessionId, 1);
    assert.equal(await harness.sessions.sendUserMessage(sessionId, "say two"), true);
    const second = await harness.waitForTurns(sessionId, 2);
    assert.equal(second.messages.at(-1)?.text, "said: two");
    const records = readFileSync(join(harness.stateDirectory, fakeClaudeEnvironmentLogName), "utf8")
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line) as CliEnvironmentRecord);
    assert.deepEqual(
      records.map((record) => record["kind"]),
      ["new", "resume"],
    );
    const [newCli, resumedCli] = records;
    assert.ok(newCli !== undefined && resumedCli !== undefined);
    return [newCli, resumedCli];
  }

  test("a resumed CLI keys its secure storage by the original config directory", async () => {
    const [newCli, resumedCli] = await runTwoTurns();

    assert.ok(harness !== undefined);
    const configDirectory = join(harness.stateDirectory, "claude-config");
    assert.deepEqual(newCli, {
      kind: "new",
      CLAUDE_CONFIG_DIR: configDirectory,
      [secureStorageVariable]: configDirectory,
    });
    // The SDK resumes from a temporary config directory.
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], null);
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], configDirectory);
    assert.equal(resumedCli[secureStorageVariable], configDirectory);
  });

  test("without a config directory a resumed CLI keys its secure storage by the default", async () => {
    // Keeps the SDK and the CLI away from the host's Claude config and keychain.
    const home = mkdtempSync(join(engine.stateDirectory, "home-"));
    process.env["HOME"] = home;
    delete process.env["CLAUDE_CONFIG_DIR"];
    const [newCli, resumedCli] = await runTwoTurns({
      flowType: claudeTestAgentFlowType,
      environment: { HOME: home, ANTHROPIC_API_KEY: mockApiKey },
      hasConfigDirectory: false,
    });

    assert.deepEqual(newCli, { kind: "new", CLAUDE_CONFIG_DIR: null, [secureStorageVariable]: "" });
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], null);
    assert.equal(resumedCli[secureStorageVariable], "");
  });

  test("the application's secure-storage directory reaches every CLI", async () => {
    const [newCli, resumedCli] = await runTwoTurns({
      flowType: claudeTestAgentFlowType,
      environment: { [secureStorageVariable]: applicationSecureStorageDirectory },
    });

    assert.equal(newCli[secureStorageVariable], applicationSecureStorageDirectory);
    assert.equal(resumedCli[secureStorageVariable], applicationSecureStorageDirectory);
  });
});
