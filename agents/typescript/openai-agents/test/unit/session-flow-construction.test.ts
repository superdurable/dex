// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { Agent, applyPatchTool, tool, toolSearchTool, type Editor, type MCPServer } from "@openai/agents";
import { SandboxAgent } from "@openai/agents/sandbox";
import { z } from "zod";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";

const unusedEditor: Editor = {
  createFile: async () => undefined,
  updateFile: async () => undefined,
  deleteFile: async () => undefined,
};

function createLookupTool(): ReturnType<typeof tool> {
  return tool({
    name: "lookup",
    description: "Look something up.",
    parameters: z.object({ key: z.string() }),
    errorFunction: null,
    execute: ({ key }) => key,
  });
}

function rejectsWithTypeError(pattern: RegExp): (error: unknown) => boolean {
  return (error: unknown) => error instanceof TypeError && pattern.test(error.message);
}

describe("createOpenAIAgentsSessionFlow construction", () => {
  test("rejects a local tool that would need a Dex Step and accepts it as a read without approval", () => {
    const agent = new Agent({ name: "Editor", tools: [applyPatchTool({ editor: unusedEditor })] });
    assert.throws(
      () => createOpenAIAgentsSessionFlow(agent, "EditorAgent"),
      rejectsWithTypeError(/local apply_patch tool apply_patch/),
    );
    const flow = createOpenAIAgentsSessionFlow(agent, "EditorAgent", {
      toolPolicies: { apply_patch: { effect: "read" } },
    });
    assert.equal(flow.getFlowType(), "EditorAgent");
  });

  test("rejects two different function tools with one name across handoffs", () => {
    const specialist = new Agent({ name: "Specialist", tools: [createLookupTool()] });
    const triage = new Agent({ name: "Triage", tools: [createLookupTool()], handoffs: [specialist] });
    assert.throws(() => createOpenAIAgentsSessionFlow(triage, "TriageAgent"), rejectsWithTypeError(/named lookup/));
  });

  test("rejects an agent used as a tool, whose inner tools would skip their own Steps and approvals", () => {
    const inner = new Agent({ name: "Inner", tools: [createLookupTool()] });
    const outer = new Agent({
      name: "Outer",
      tools: [inner.asTool({ toolName: "delegate", toolDescription: "Delegate to the inner agent." })],
    });
    assert.throws(
      () => createOpenAIAgentsSessionFlow(outer, "OuterAgent", { toolPolicies: { delegate: { effect: "read" } } }),
      rejectsWithTypeError(/uses an agent as tool delegate/),
    );
  });

  test("rejects a client tool search with an execute function and accepts the built-in loader", () => {
    const hiddenWrite = createLookupTool();
    const searchingAgent = new Agent({
      name: "Searching",
      tools: [toolSearchTool({ execution: "client", execute: () => [hiddenWrite] })],
    });
    assert.throws(
      () => createOpenAIAgentsSessionFlow(searchingAgent, "SearchingAgent"),
      rejectsWithTypeError(/client tool search with an execute function/),
    );
    const deferredLookup = tool({
      name: "deferred_lookup",
      description: "Look something up after tool search loads it.",
      parameters: z.object({ key: z.string() }),
      deferLoading: true,
      errorFunction: null,
      execute: ({ key }) => key,
    });
    const loadingAgent = new Agent({
      name: "Loading",
      tools: [deferredLookup, toolSearchTool({ execution: "client" })],
    });
    assert.equal(createOpenAIAgentsSessionFlow(loadingAgent, "LoadingAgent").getFlowType(), "LoadingAgent");
  });

  test("rejects a SandboxAgent, whose sandbox tools appear only at run time", () => {
    const sandboxAgent = new SandboxAgent({ name: "Sandbox" });
    const triage = new Agent({ name: "Triage", handoffs: [sandboxAgent] });
    assert.throws(() => createOpenAIAgentsSessionFlow(triage, "TriageAgent"), rejectsWithTypeError(/is a SandboxAgent/));
  });

  test("rejects MCP servers unless their tools are accepted as running inline", () => {
    const unconnectedServer = { name: "unconnected" } as unknown as MCPServer;
    const agent = new Agent({ name: "Mcp", mcpServers: [unconnectedServer] });
    assert.throws(() => createOpenAIAgentsSessionFlow(agent, "McpAgent"), rejectsWithTypeError(/has MCP servers/));
    const flow = createOpenAIAgentsSessionFlow(agent, "McpAgent", { shouldRunMcpServerToolsInline: true });
    assert.equal(flow.getFlowType(), "McpAgent");
  });

  test("rejects a value that is not an Agent and a blank Flow type", () => {
    assert.throws(() => createOpenAIAgentsSessionFlow({} as Agent, "NotAnAgent"), rejectsWithTypeError(/requires an Agent/));
    assert.throws(() => createOpenAIAgentsSessionFlow(new Agent({ name: "Blank" }), " "), TypeError);
  });
});
