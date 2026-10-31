// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { tool, type HookCallbackMatcher, type PermissionMode } from "@anthropic-ai/claude-agent-sdk";
import type { AgentSessionFlow, ToolPolicy } from "@superdurable/dex-agents";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import {
  createClaudeAgentSessionFlow,
  currentDeferredToolExecution,
  type ClaudeAgentOptionsContext,
  type ClaudeAgentQueryOptions,
  type ClaudeCustomTool,
} from "../../src/index.js";
import {
  fakeClaudeInvocationLogName,
  fakeClaudeStateDirectoryVariable,
} from "./fake-claude-cli-environment.js";

/** Flow type of the Claude agent under test. */
export const claudeTestAgentFlowType = "ScriptedClaudeAgent";
/** Gate at which `slow_write` waits. */
export const slowWriteGateName = "slow_write";

const fakeClaudeCliPath = fileURLToPath(new URL("./fake-claude-cli.js", import.meta.url));

// Annotations classify lookup and charge; Bash keeps the default, which needs approval.
const testToolPolicies: Readonly<Record<string, ToolPolicy>> = {
  publish: { effect: "non_idempotent_write" },
  slow_write: { effect: "non_idempotent_write", requiresApproval: false },
};

/** The test agent's CLI: the fake when `executablePath` is unset, else `environment` is the whole environment. */
export interface ClaudeTestAgentCli {
  readonly flowType: string;
  readonly executablePath?: string;
  readonly environment?: Readonly<Record<string, string>>;
  readonly model?: string;
  readonly permissionMode?: PermissionMode;
}

/** Creates the Claude agent Flow; tests and the Worker child build it identically. */
export function createClaudeTestAgentFlow(stateDirectory: string, cli?: ClaudeTestAgentCli): AgentSessionFlow {
  mkdirSync(join(stateDirectory, "workspace"), { recursive: true });
  mkdirSync(join(stateDirectory, "claude-config"), { recursive: true });
  const environment =
    cli?.executablePath === undefined
      ? { ...process.env, [fakeClaudeStateDirectoryVariable]: stateDirectory, ...cli?.environment }
      : (cli.environment ?? {});
  const buildOptions = (context: ClaudeAgentOptionsContext): ClaudeAgentQueryOptions => ({
    pathToClaudeCodeExecutable: cli?.executablePath ?? fakeClaudeCliPath,
    cwd: join(stateDirectory, "workspace"),
    env: { ...environment, CLAUDE_CONFIG_DIR: join(stateDirectory, "claude-config") },
    ...(cli?.model === undefined ? {} : { model: cli.model }),
    ...(cli?.permissionMode === undefined ? {} : { permissionMode: cli.permissionMode }),
    systemPrompt: `You are scripted agent ${context.flowId}.`,
    tools: ["Bash"],
    settingSources: [],
    hooks: { PreToolUse: [reviewPublishedMessageHook] },
  });
  return createClaudeAgentSessionFlow(
    { options: buildOptions, customTools: createTestCustomTools(stateDirectory), toolPolicies: testToolPolicies },
    cli?.flowType ?? claudeTestAgentFlowType,
  );
}

/** An application hook: `publish` input `rewrite:<text>` becomes `reviewed <text>`, `rewrite-invalid` a number. */
export const reviewPublishedMessageHook: HookCallbackMatcher = {
  matcher: "mcp__tools__publish",
  hooks: [
    async (hookInput) => {
      const toolInput = hookInput.hook_event_name === "PreToolUse" ? (hookInput.tool_input as { message?: unknown }) : {};
      if (toolInput.message === "rewrite-invalid") {
        return { hookSpecificOutput: { hookEventName: "PreToolUse", updatedInput: { message: 5 } } };
      }
      if (typeof toolInput.message !== "string" || !toolInput.message.startsWith("rewrite:")) {
        return {};
      }
      return {
        hookSpecificOutput: {
          hookEventName: "PreToolUse",
          updatedInput: { message: `reviewed ${toolInput.message.slice("rewrite:".length)}` },
        },
      };
    },
  ],
};

function createTestCustomTools(stateDirectory: string): ClaudeCustomTool[] {
  const invocations = new InvocationLog(join(stateDirectory, fakeClaudeInvocationLogName));
  const lookup = tool(
    "lookup",
    "Look up a record",
    { query: z.string() },
    async (toolArguments) => {
      invocations.record("lookup");
      return textResult(`found ${toolArguments.query}`);
    },
    { annotations: { readOnlyHint: true } },
  );
  const publish = tool("publish", "Publish a message", { message: z.string() }, async (toolArguments) => {
    invocations.record("publish");
    return textResult(`published ${toolArguments.message}`);
  });
  const charge = tool(
    "charge",
    "Charge an amount",
    { amount: z.number().int(), fail_attempts: z.number().int().default(0) },
    async (toolArguments) => {
      invocations.record("charge");
      const execution = currentDeferredToolExecution();
      if (execution === undefined) {
        throw new Error("charge must run in a deferred tool Step");
      }
      if (execution.attempt <= toolArguments.fail_attempts) {
        throw new Error(`charge attempt ${execution.attempt} lost its connection`);
      }
      if (toolArguments.amount <= 0) {
        return textResult("amount must be positive", true);
      }
      return textResult(`charged ${toolArguments.amount} with key ${execution.idempotencyKey}`);
    },
    { annotations: { idempotentHint: true } },
  );
  const slowWrite = tool("slow_write", "Write slowly", { message: z.string() }, async (toolArguments) => {
    invocations.record("slow_write");
    const gate = new FileGate(stateDirectory, slowWriteGateName);
    gate.markReached();
    await gate.waitForRelease();
    return textResult(`wrote ${toolArguments.message}`);
  });
  return [lookup, publish, charge, slowWrite];
}

function textResult(text: string, isError = false): { content: { type: "text"; text: string }[]; isError: boolean } {
  return { content: [{ type: "text", text }], isError };
}
