// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The gate refuses calls a defective or changed CLI could send: MCP calls and deferrals it never permitted.

import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { tool, type HookInput } from "@anthropic-ai/claude-agent-sdk";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import type { AgentToolCall, AgentTurnContext, ToolEffect } from "@superdurable/dex-agents";

import { ToolCallGate, ToolCatalog, type ClaudeCustomTool } from "../../src/claude-tool-call-gate.js";
import { createClaudeToolServer } from "../../src/claude-tool-server.js";
import { ToolSourceRouter } from "../../src/claude-tool-sources.js";
import { createLedgerToolSource } from "../fixtures/claude-test-agent.js";

interface GateFixture {
  readonly catalog: ToolCatalog;
  readonly gate: ToolCallGate;
  readonly handlerCalls: string[];
}

async function createGateFixture(records: readonly AgentToolCall[] = []): Promise<GateFixture> {
  const handlerCalls: string[] = [];
  const customTools = [
    tool(
      "read_note",
      "Read a note",
      {},
      async () => {
        handlerCalls.push("read_note");
        return { content: [{ type: "text" as const, text: "note" }] };
      },
      { annotations: { readOnlyHint: true } },
    ),
    tool("write_note", "Write a note", {}, async () => {
      handlerCalls.push("write_note");
      return { content: [{ type: "text" as const, text: "written" }] };
    }),
  ] as unknown as ClaudeCustomTool[];
  const source = createLedgerToolSource(mkdtempSync(join(tmpdir(), "claude-gate-")));
  const sourceTools = await new ToolSourceRouter([source], 5_000, []).listSourceTools();
  const catalog = new ToolCatalog(customTools, "tools").withSourceTools(sourceTools, []);
  const turn = {
    flowId: "gate-test",
    turnId: "turn-1",
    toolPolicies: {},
    recordedToolCall: (toolCallId: string) => records.find((record) => record.toolCallId === toolCallId),
    resolveToolPolicy: (_toolName: string, frameworkEffect?: ToolEffect) => ({
      effect: frameworkEffect ?? "non_idempotent_write",
      requiresApproval: frameworkEffect !== "read",
    }),
    events: { writeToolCallStarted: () => undefined, writeToolCallFinished: () => undefined },
  } as unknown as AgentTurnContext;
  return { catalog, gate: new ToolCallGate(turn, catalog), handlerCalls };
}

function preToolUse(cliToolName: string, toolUseId: string, toolInput: Record<string, unknown> = {}): HookInput {
  return {
    hook_event_name: "PreToolUse",
    session_id: "session",
    transcript_path: "/dev/null",
    cwd: "/",
    tool_name: cliToolName,
    tool_input: toolInput,
    tool_use_id: toolUseId,
  } as HookInput;
}

async function connectClient(fixture: GateFixture): Promise<Client> {
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await createClaudeToolServer(fixture.catalog, fixture.gate).instance.connect(serverTransport);
  const client = new Client({ name: "gate-test", version: "1.0.0" });
  await client.connect(clientTransport);
  return client;
}

async function callTool(client: Client, toolName: string, toolUseId: string): Promise<{ readonly isError: unknown }> {
  const result = await client.callTool({ name: toolName, arguments: {}, _meta: { "claudecode/toolUseId": toolUseId } });
  return { ...result, isError: result["isError"] };
}

describe("the Claude tool call gate", () => {
  test("an MCP call runs only the tool that PreToolUse permitted under its tool use ID", async () => {
    const fixture = await createGateFixture();
    const client = await connectClient(fixture);
    await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-1"), "call-1");
    await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-2"), "call-2");

    const mismatched = await callTool(client, "write_note", "call-1");
    assert.equal(mismatched.isError, true);
    // The mismatched call consumed call-1; the permitted name still runs under call-2.
    const permitted = await callTool(client, "read_note", "call-2");
    assert.notEqual(permitted.isError, true, JSON.stringify(permitted));
    assert.deepEqual(fixture.handlerCalls, ["read_note"]);
    await client.close();
  });

  test("a replayed record answers only an MCP call of its own tool", async () => {
    const record: AgentToolCall = {
      toolCallId: "call-1",
      turnId: "turn-1",
      toolName: "read_note",
      arguments: "{}",
      effect: "read",
      requiresApproval: true,
      status: "succeeded",
      output: "recorded note",
      error: null,
      rejectionMessage: null,
      deferralOrder: 0,
    };
    const fixture = await createGateFixture([record]);
    const client = await connectClient(fixture);
    await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-1"), "call-1");

    assert.equal((await callTool(client, "write_note", "call-1")).isError, true);
    assert.deepEqual(fixture.handlerCalls, []);
    await client.close();
  });

  test("a deferral that PreToolUse never deferred fails the segment", async () => {
    const fixture = await createGateFixture();
    assert.throws(
      () => fixture.gate.deferredToolCall({ id: "call-1", name: "mcp__tools__post_note", input: { accountId: "a-1" } }),
      /did not defer/,
    );
  });

  test("a deferral whose ID or tool differs from the deferred call fails the segment", async () => {
    const fixture = await createGateFixture();
    const input = { accountId: "a-1", text: "hello" };
    const decision = await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__post_note", "call-1", input), "call-1");
    const specificOutput = "hookSpecificOutput" in decision ? decision.hookSpecificOutput : undefined;
    assert.equal(specificOutput?.hookEventName === "PreToolUse" ? specificOutput.permissionDecision : undefined, "defer");

    assert.throws(
      () => fixture.gate.deferredToolCall({ id: "call-9", name: "mcp__tools__post_note", input }),
      /but the adapter deferred post_note call call-1/,
    );
    assert.throws(
      () => fixture.gate.deferredToolCall({ id: "call-1", name: "mcp__tools__create_payout", input }),
      /but the adapter deferred post_note call call-1/,
    );
    const deferred = fixture.gate.deferredToolCall({ id: "call-1", name: "mcp__tools__post_note", input });
    assert.deepEqual([deferred.toolCallId, deferred.toolName], ["call-1", "post_note"]);
  });
});
