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

import { propagation, type TextMapPropagator } from "@opentelemetry/api";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { claudeTestAgentFlowType, type ClaudeTestAgentCli } from "../fixtures/claude-test-agent.js";
import {
  fakeClaudeEnvironmentLogName,
  fakeClaudeStateDirectoryVariable,
} from "../fixtures/fake-claude-cli-environment.js";

const secureStorageVariable = "CLAUDE_SECURESTORAGE_CONFIG_DIR";
const applicationSecureStorageDirectory = "/srv/claude/secure-storage";
const mockApiKey = "sk-ant-mock-not-a-real-key";
const inheritedTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01";
const inheritedTracestate = "vendor=inherited";
const injectedTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
// The in-process Worker and the SDK read these from this process.
const isolatedVariables = [
  "CLAUDE_CONFIG_DIR",
  secureStorageVariable,
  "HOME",
  "ANTHROPIC_API_KEY",
  "CLAUDE_CODE_ENTRYPOINT",
  "TRACEPARENT",
  "TRACESTATE",
  fakeClaudeStateDirectoryVariable,
] as const;
const loginVariables = ["kind", "CLAUDE_CONFIG_DIR", secureStorageVariable] as const;

/** Injects a fixed `traceparent`, as a propagator does for an active span. */
const injectingPropagator: TextMapPropagator = {
  inject(_context, carrier, setter) {
    setter.set(carrier, "traceparent", injectedTraceparent);
  },
  extract(context) {
    return context;
  },
  fields() {
    return ["traceparent"];
  },
};

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
    for (const name of [secureStorageVariable, "CLAUDE_CODE_ENTRYPOINT", "TRACEPARENT", "TRACESTATE"]) {
      delete process.env[name];
    }
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
    propagation.disable();
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

  async function startHarness(cli?: ClaudeTestAgentCli): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, true, cli === undefined ? undefined : () => cli);
    return harness;
  }

  async function runTwoTurns(agent: ClaudeAgentTestHarness): Promise<[CliEnvironmentRecord, CliEnvironmentRecord]> {
    const sessionId = agent.newSessionId("login");
    await agent.sessions.startSession(sessionId, "say one");
    await agent.waitForTurns(sessionId, 1);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say two"), true);
    const second = await agent.waitForTurns(sessionId, 2);
    assert.equal(second.messages.at(-1)?.text, "said: two");
    const records = readFileSync(join(agent.stateDirectory, fakeClaudeEnvironmentLogName), "utf8")
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

  /** Starts an agent without `env`, whose fake CLI inherits this process's environment. */
  async function startAgentWithoutEnvironment(): Promise<ClaudeAgentTestHarness> {
    const agent = await startHarness({ flowType: claudeTestAgentFlowType, isEnvironmentUnset: true });
    process.env[fakeClaudeStateDirectoryVariable] = agent.stateDirectory;
    return agent;
  }

  function assertSdkVariablesArrive(record: CliEnvironmentRecord): void {
    assert.equal(record["CLAUDE_CODE_ENTRYPOINT"], "sdk-ts");
    assert.notEqual(record["CLAUDE_AGENT_SDK_VERSION"], null);
  }

  test("a resumed CLI keys its secure storage by the original config directory", async () => {
    const agent = await startHarness();
    const [newCli, resumedCli] = await runTwoTurns(agent);

    const configDirectory = join(agent.stateDirectory, "claude-config");
    assert.deepEqual(pickLoginVariables(newCli), {
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
    const agent = await startHarness({
      flowType: claudeTestAgentFlowType,
      environment: { HOME: home, ANTHROPIC_API_KEY: mockApiKey },
      hasConfigDirectory: false,
    });
    const [newCli, resumedCli] = await runTwoTurns(agent);

    assert.deepEqual(pickLoginVariables(newCli), { kind: "new", CLAUDE_CONFIG_DIR: null, [secureStorageVariable]: "" });
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], null);
    assert.equal(resumedCli[secureStorageVariable], "");
  });

  test("the application's secure-storage directory reaches every CLI", async () => {
    const agent = await startHarness({
      flowType: claudeTestAgentFlowType,
      environment: { [secureStorageVariable]: applicationSecureStorageDirectory },
    });
    const [newCli, resumedCli] = await runTwoTurns(agent);

    assert.equal(newCli[secureStorageVariable], applicationSecureStorageDirectory);
    assert.equal(resumedCli[secureStorageVariable], applicationSecureStorageDirectory);
  });

  test("without env the CLI inherits the Worker's config directory and trace context", async () => {
    const agent = await startAgentWithoutEnvironment();
    const configDirectory = join(agent.stateDirectory, "claude-config");
    process.env["CLAUDE_CONFIG_DIR"] = configDirectory;
    process.env["TRACEPARENT"] = inheritedTraceparent;
    process.env["TRACESTATE"] = inheritedTracestate;
    const [newCli, resumedCli] = await runTwoTurns(agent);

    assert.deepEqual(pickLoginVariables(newCli), {
      kind: "new",
      CLAUDE_CONFIG_DIR: configDirectory,
      [secureStorageVariable]: configDirectory,
    });
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], configDirectory);
    assert.equal(resumedCli[secureStorageVariable], configDirectory);
    for (const record of [newCli, resumedCli]) {
      assertSdkVariablesArrive(record);
      // Without an injected trace context the SDK keeps the inherited one.
      assert.equal(record["TRACEPARENT"], inheritedTraceparent);
      assert.equal(record["TRACESTATE"], inheritedTracestate);
    }
  });

  test("without env or a config directory the CLI gets the default login and the injected trace context", async () => {
    // Keeps the SDK and the CLI away from the host's Claude config and keychain.
    const home = mkdtempSync(join(engine.stateDirectory, "home-"));
    process.env["HOME"] = home;
    process.env["ANTHROPIC_API_KEY"] = mockApiKey;
    delete process.env["CLAUDE_CONFIG_DIR"];
    process.env["TRACEPARENT"] = inheritedTraceparent;
    process.env["TRACESTATE"] = inheritedTracestate;
    assert.equal(propagation.setGlobalPropagator(injectingPropagator), true);
    const agent = await startAgentWithoutEnvironment();
    const [newCli, resumedCli] = await runTwoTurns(agent);

    assert.deepEqual(pickLoginVariables(newCli), { kind: "new", CLAUDE_CONFIG_DIR: null, [secureStorageVariable]: "" });
    assert.notEqual(resumedCli["CLAUDE_CONFIG_DIR"], null);
    assert.equal(resumedCli[secureStorageVariable], "");
    for (const record of [newCli, resumedCli]) {
      assertSdkVariablesArrive(record);
      // The SDK replaces the inherited trace context with the injected one.
      assert.equal(record["TRACEPARENT"], injectedTraceparent);
      assert.equal(record["TRACESTATE"], null);
    }
  });
});

function pickLoginVariables(record: CliEnvironmentRecord): CliEnvironmentRecord {
  return Object.fromEntries(loginVariables.map((name) => [name, record[name] ?? null]));
}
