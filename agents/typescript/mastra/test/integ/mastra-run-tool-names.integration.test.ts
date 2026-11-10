// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Which tools Mastra adds itself may run inside RunModel once the runner has tool sources. The tools a model call
// offers are visible only inside runModelStep, so the tests call the runner directly with the scripted model's
// `tools` command, which answers with the offered tool names.

import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { Agent } from "@mastra/core/agent";
import {
  resolveToolPolicy,
  type AgentEventWriter,
  type AgentTurnContext,
  type SourceToolDefinition,
  type ToolExecutionResult,
  type ToolPolicy,
  type ToolSource,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

import { MastraAgentRunner, type MastraAgentRunnerOptions } from "../../src/index.js";
import { createLedgerToolSource } from "../fixtures/connector-mastra-agent.js";
import { ScriptedLanguageModel } from "../fixtures/scripted-language-model.js";

const markerDirectory = mkdtempSync(join(tmpdir(), "mastra-run-tool-names-"));
const payPolicies: Readonly<Record<string, ToolPolicy>> = { "agent-pay": { effect: "read" } };

/** A dynamic source that declares nothing and lists nothing at the moment, like a server that is down. */
class EmptyToolSource implements ToolSource {
  public readonly sourceName = "empty";

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return [];
  }

  public deferredToolCall(): never {
    throw new Error("the empty source lists no tools");
  }

  public async executeDeferredToolCall(): Promise<ToolExecutionResult> {
    throw new Error("the empty source lists no tools");
  }

  public async pollToolTask(): Promise<ToolTaskPollOutcome> {
    throw new Error("the empty source starts no tasks");
  }
}

const silentEvents: AgentEventWriter = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};

describe("tools Mastra runs itself next to tool sources", () => {
  test("a policy key does not let a same-named sub-agent run inline while a source lists nothing", async () => {
    const offered = await listOfferedToolNames({ toolPolicies: payPolicies, toolSources: [new EmptyToolSource()] });
    assert.equal(offered.includes("agent-pay"), false, offered.join(", "));
  });

  test("mastraRunToolNames lets the sub-agent run inline next to tool sources", async () => {
    const offered = await listOfferedToolNames({
      toolPolicies: payPolicies,
      toolSources: [new EmptyToolSource()],
      mastraRunToolNames: ["agent-pay"],
    });
    assert.equal(offered.includes("agent-pay"), true, offered.join(", "));
  });

  test("mastraRunToolNames still needs a read policy without approval", async () => {
    const offered = await listOfferedToolNames({
      toolPolicies: { "agent-pay": { effect: "read", requiresApproval: true } },
      mastraRunToolNames: ["agent-pay"],
    });
    assert.equal(offered.includes("agent-pay"), false, offered.join(", "));
  });

  test("without tool sources a read policy key still lets the sub-agent run inline", async () => {
    const offered = await listOfferedToolNames({ toolPolicies: payPolicies });
    assert.equal(offered.includes("agent-pay"), true, offered.join(", "));
  });

  test("a name in mastraRunToolNames that a source declares is refused", () => {
    assert.throws(
      () =>
        new MastraAgentRunner(createSupervisorAgent(), {
          toolSources: [createLedgerToolSource(markerDirectory)],
          mastraRunToolNames: ["get_balance"],
        }),
      { name: "TypeError", message: "tool get_balance of a tool source uses the name of a tool Mastra runs itself" },
    );
  });
});

async function listOfferedToolNames(options: MastraAgentRunnerOptions): Promise<string[]> {
  const runner = new MastraAgentRunner(createSupervisorAgent(), options);
  const policies = options.toolPolicies ?? {};
  const turn: AgentTurnContext = {
    flowId: "run-tool-names-session",
    turnId: "turn-1",
    attempt: 1,
    messages: [{ ...runner.buildUserMessage("tools"), sequence: 1, turnId: "turn-1", createdAt: new Date(0).toISOString() }],
    frameworkRunState: null,
    isResuming: false,
    toolCalls: [],
    events: silentEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    toolPolicies: policies,
    resolveToolPolicy: (toolName, frameworkEffect) => resolveToolPolicy(toolName, policies, "unclassified", frameworkEffect),
    recordedToolCall: () => undefined,
    heartbeat: async () => undefined,
  };
  const outcome = await runner.runModelStep(turn);
  assert.equal(outcome.kind, "completed");
  const text = outcome.kind === "completed" ? (outcome.newMessages.at(-1)?.text ?? "") : "";
  assert.match(text, /^tools: /);
  return text.slice("tools: ".length).split(", ");
}

function createSupervisorAgent(): Agent {
  const pay = new Agent({
    id: "pay",
    name: "pay",
    description: "Pays an invoice",
    instructions: "Answer with the scripted model.",
    model: new ScriptedLanguageModel(markerDirectory, "pay-model"),
  });
  return new Agent({
    id: "supervisor",
    name: "Supervisor",
    instructions: "Answer with the scripted model.",
    model: new ScriptedLanguageModel(markerDirectory),
    agents: { pay },
  });
}
