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

import { tool, type HookInput, type HookJSONOutput, type PermissionResult } from "@anthropic-ai/claude-agent-sdk";
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
    tool("inspect_command", "Inspect a command", {}, async () => {
      handlerCalls.push("inspect_command");
      return { content: [{ type: "text" as const, text: "inspected" }] };
    }),
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

function succeededRecord(toolCallId: string, toolName: string, toolArguments: Record<string, unknown>): AgentToolCall {
  return {
    toolCallId,
    turnId: "turn-1",
    toolName,
    arguments: JSON.stringify(toolArguments),
    effect: "non_idempotent_write",
    requiresApproval: true,
    status: "succeeded",
    output: "recorded",
    error: null,
    rejectionMessage: null,
    deferralOrder: 0,
  };
}

function decisionOf(output: HookJSONOutput): { readonly decision: unknown; readonly reason: unknown; readonly updatedInput: unknown } {
  const specificOutput = "hookSpecificOutput" in output ? output.hookSpecificOutput : undefined;
  return specificOutput?.hookEventName === "PreToolUse"
    ? {
        decision: specificOutput.permissionDecision,
        reason: specificOutput.permissionDecisionReason,
        updatedInput: specificOutput.updatedInput,
      }
    : { decision: undefined, reason: undefined, updatedInput: undefined };
}

async function askPermission(
  fixture: GateFixture,
  cliToolName: string,
  toolUseId: string,
  toolInput: Record<string, unknown> = {},
): Promise<PermissionResult> {
  const permission = await fixture.gate.decideToolPermission(cliToolName, toolInput, {
    signal: new AbortController().signal,
    toolUseID: toolUseId,
  } as Parameters<ToolCallGate["decideToolPermission"]>[2]);
  assert.ok(permission !== null);
  return permission;
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
    // The hook allow was the call's one confirmation; canUseTool allows no tool under that ID.
    for (const cliToolName of ["Bash", "read_note", "mcp__tools__read_note"]) {
      assert.equal((await askPermission(fixture, cliToolName, "call-2", { path: "/etc/passwd" })).behavior, "deny");
    }
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

  test("a custom or source record is never replayed as Bash", async () => {
    const records = [
      succeededRecord("call-1", "inspect_command", { command: "rm -rf build" }),
      succeededRecord("call-2", "post_note", { accountId: "a-1", text: "hello" }),
    ];
    for (const callId of ["call-1", "call-2"]) {
      const fixture = await createGateFixture(records);
      const hookDecision = decisionOf(
        await fixture.gate.decidePreToolUse(preToolUse("Bash", callId, { command: "ls" }), callId),
      );
      assert.equal(hookDecision.decision, "deny");
      assert.match(String(hookDecision.reason), new RegExp(`tool use ID ${callId} belongs to a recorded call`));
      assert.equal(hookDecision.updatedInput, undefined);

      const permission = await askPermission(await createGateFixture(records), "Bash", callId);
      assert.equal(permission.behavior, "deny");
      assert.equal("updatedInput" in permission ? permission.updatedInput : undefined, undefined);
    }
  });

  test("a source record replays only as its own tool", async () => {
    const fixture = await createGateFixture([succeededRecord("call-1", "post_note", { accountId: "a-1", text: "x" })]);
    const client = await connectClient(fixture);
    const mismatched = decisionOf(
      await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__create_payout", "call-1"), "call-1"),
    );
    assert.equal(mismatched.decision, "deny");
    assert.match(String(mismatched.reason), /belongs to a recorded call of post_note/);
    // The denied replay left no permission, so no MCP call of either tool answers under its ID.
    assert.equal((await callTool(client, "create_payout", "call-1")).isError, true);
    assert.equal((await callTool(client, "post_note", "call-1")).isError, true);
    await client.close();
  });

  test("a recorded call is replayed at most once per segment", async () => {
    const record = succeededRecord("call-1", "Bash", { command: "echo reviewed" });
    const hookFirst = await createGateFixture([record]);
    const first = decisionOf(await hookFirst.gate.decidePreToolUse(preToolUse("Bash", "call-1"), "call-1"));
    assert.deepEqual([first.decision, first.updatedInput], ["allow", { command: "echo reviewed" }]);
    // The hook allow authorized the one run; canUseTool afterwards, as after the tool result, is denied.
    assert.equal((await askPermission(hookFirst, "Bash", "call-1", { command: "echo reviewed" })).behavior, "deny");
    assert.equal((await askPermission(hookFirst, "Bash", "call-1", { command: "rm -rf build" })).behavior, "deny");
    const second = decisionOf(await hookFirst.gate.decidePreToolUse(preToolUse("Bash", "call-1"), "call-1"));
    assert.equal(second.decision, "deny");
    assert.match(String(second.reason), /was already resumed in this turn segment/);

    const permissionFirst = await createGateFixture([record]);
    const permission = await askPermission(permissionFirst, "Bash", "call-1", { command: "rm -rf build" });
    assert.deepEqual(permission.behavior === "allow" ? permission.updatedInput : undefined, { command: "echo reviewed" });
    // The canUseTool answer was the replay's one decision; a repeat cannot swap the input.
    assert.equal((await askPermission(permissionFirst, "Bash", "call-1", { command: "rm -rf build" })).behavior, "deny");
    const afterPermission = decisionOf(
      await permissionFirst.gate.decidePreToolUse(preToolUse("Bash", "call-1"), "call-1"),
    );
    assert.equal(afterPermission.decision, "deny");

    const rejected = await createGateFixture([{ ...record, status: "rejected", rejectionMessage: "no" }]);
    assert.equal(decisionOf(await rejected.gate.decidePreToolUse(preToolUse("Bash", "call-1"), "call-1")).decision, "deny");
    assert.equal((await askPermission(rejected, "Bash", "call-1")).behavior, "deny");
  });

  test("a served record replays with its recorded input and only under its own kind", async () => {
    const record = { ...succeededRecord("call-1", "read_note", { path: "notes/a" }), effect: "read" as const };
    const fixture = await createGateFixture([record]);
    const replay = decisionOf(
      await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-1", { path: "other" }), "call-1"),
    );
    assert.deepEqual([replay.decision, replay.updatedInput], ["allow", { path: "notes/a" }]);
    // The hook allow was the replay's one decision, whatever kind canUseTool names afterwards.
    assert.equal((await askPermission(fixture, "read_note", "call-1")).behavior, "deny");
    assert.equal((await askPermission(fixture, "mcp__tools__read_note", "call-1")).behavior, "deny");

    // With the hook skipped, canUseTool makes the one decision: the recorded input, only for the served kind.
    const hookSkipped = await createGateFixture([record]);
    assert.equal((await askPermission(hookSkipped, "read_note", "call-1")).behavior, "deny");
    const viaPermission = await createGateFixture([record]);
    assert.deepEqual(await askPermission(viaPermission, "mcp__tools__read_note", "call-1", { path: "other" }), {
      behavior: "allow",
      updatedInput: { path: "notes/a" },
      toolUseID: "call-1",
    });
    assert.equal((await askPermission(viaPermission, "mcp__tools__read_note", "call-1")).behavior, "deny");
  });

  test("a tool use ID gets one PreToolUse decision per segment, which a repeat never replaces", async () => {
    const fixture = await createGateFixture();
    const first = decisionOf(await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-1"), "call-1"));
    assert.equal(first.decision, "allow");
    assert.equal((await askPermission(fixture, "mcp__tools__read_note", "call-1")).behavior, "deny");
    const repeated = decisionOf(
      await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__read_note", "call-1"), "call-1"),
    );
    assert.equal(repeated.decision, "deny");
    assert.match(String(repeated.reason), /tool use ID call-1 was already decided in this turn segment/);
    assert.equal((await askPermission(fixture, "mcp__tools__read_note", "call-1")).behavior, "deny");

    // A repeat of a deferred call is denied too, and the deferral stays as the hook decided it.
    const input = { accountId: "a-1", text: "hello" };
    const deferred = decisionOf(
      await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__post_note", "call-2", input), "call-2"),
    );
    assert.equal(deferred.decision, "defer");
    assert.equal(
      decisionOf(await fixture.gate.decidePreToolUse(preToolUse("mcp__tools__post_note", "call-2", input), "call-2"))
        .decision,
      "deny",
    );
    assert.equal(fixture.gate.deferredToolCall({ id: "call-2", name: "mcp__tools__post_note", input }).toolCallId, "call-2");
  });
});
