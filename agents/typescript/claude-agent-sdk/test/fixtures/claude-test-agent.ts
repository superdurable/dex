// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { tool, type HookCallbackMatcher, type PermissionMode } from "@anthropic-ai/claude-agent-sdk";
import type {
  AgentSessionFlow,
  AgentToolCall,
  AgentTurnContext,
  DeferredToolCall,
  DeferredToolExecution,
  ToolCallStepOptions,
  ToolExecutionResult,
  ToolPolicy,
  ToolTaskPoll,
  ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import type { SourceToolDefinition, ToolSource } from "@superdurable/dex-agents/stepwise";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import {
  createClaudeAgentSessionFlow,
  currentDeferredToolExecution,
  type ClaudeAgentOptionsContext,
  type ClaudeAgentQueryOptions,
  type ClaudeAgentSessionOptions,
  type ClaudeCustomTool,
} from "../../src/index.js";
import {
  fakeClaudeInvocationLogName,
  fakeClaudeStateDirectoryVariable,
} from "./fake-claude-cli-environment.js";
import {
  CreatePayoutInputSchema,
  CreatePayoutOperation,
  FakeLedgerProvider,
  GetBalanceInputSchema,
  GetBalanceOperation,
  PostNoteInputSchema,
  PostNoteOperation,
  fakeLedgerConnection,
  fakeLedgerManifest,
} from "./fake-ledger.js";

/** Flow type of the Claude agent under test. */
export const claudeTestAgentFlowType = "ScriptedClaudeAgent";
/** Gate at which `slow_write` waits. */
export const slowWriteGateName = "slow_write";
/** The amount the application hook gives a `payout_checked` call whose memo is `raise`. */
export const raisedPayoutAmount = 500;
/** While this file exists in the state directory, `hiding_note`'s source lists nothing, or throws when it says `raise`. */
export const hideSourceToolsFileName = "hide-source-tools";
/** While this file exists in the state directory, an application hook denies every `publish` and `post_note`. */
export const denyFlagFileName = "deny-publish-and-post-note";
/** The reason that hook gives. */
export const applicationHookDenial = "the application hook denies this call";

const fakeClaudeCliPath = fileURLToPath(new URL("./fake-claude-cli.js", import.meta.url));

// Annotations classify lookup and charge; Bash keeps the default, which needs approval.
const testToolPolicies: Readonly<Record<string, ToolPolicy>> = {
  publish: { effect: "non_idempotent_write" },
  slow_write: { effect: "non_idempotent_write", requiresApproval: false },
  // The fake CLI's subagent tool, which then runs inside the turn.
  Agent: { effect: "read" },
  // Claims read; a connector tool takes only the approval from it.
  policy_note: { effect: "read", requiresApproval: true },
};

/** The test agent's CLI: the fake when `executablePath` is unset, else `environment` is the whole environment. */
export interface ClaudeTestAgentCli {
  readonly flowType: string;
  readonly executablePath?: string;
  readonly environment?: Readonly<Record<string, string>>;
  readonly model?: string;
  readonly permissionMode?: PermissionMode;
  readonly allowedTools?: readonly string[];
}

/** Creates the Claude agent Flow; tests and the Worker child build it identically. */
export function createClaudeTestAgentFlow(
  stateDirectory: string,
  cli?: ClaudeTestAgentCli,
  sessionOptions: ClaudeAgentSessionOptions = {},
): AgentSessionFlow {
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
    ...(cli?.allowedTools === undefined ? {} : { allowedTools: [...cli.allowedTools] }),
    settingSources: [],
    hooks: {
      PreToolUse: [
        reviewPublishedMessageHook,
        reviewBashCommandHook,
        raiseCheckedPayoutHook,
        createDenyWhileFlaggedHook(stateDirectory),
      ],
    },
  });
  return createClaudeAgentSessionFlow(
    {
      options: buildOptions,
      customTools: createTestCustomTools(stateDirectory),
      toolPolicies: testToolPolicies,
      toolSources: [createLedgerToolSource(stateDirectory), new HidingToolSource(stateDirectory)],
    },
    cli?.flowType ?? claudeTestAgentFlowType,
    sessionOptions,
  );
}

/**
 * The fake ledger Connector's operations as source tools: `get_balance` is a Query; `create_payout` is a
 * `retry: safe` Mutation without approval and `approved_payout` the same with the manifest's approval;
 * `payout_checked` needs approval above 50; `post_note` is a single-attempt Mutation and `policy_note` the same
 * under a session policy that asks for approval and claims `read`.
 */
export function createLedgerToolSource(stateDirectory: string): ConnectorToolSource {
  const provider = new FakeLedgerProvider(stateDirectory);
  const payouts = new CreatePayoutOperation(provider);
  const notes = new PostNoteOperation(provider);
  const manifest = structuredClone(fakeLedgerManifest) as ConnectorPackageManifest;
  return new ConnectorToolSource([
    connectorTool(new GetBalanceOperation(provider), fakeLedgerConnection, {
      manifest,
      inputValidator: GetBalanceInputSchema,
      name: "get_balance",
    }),
    connectorTool(payouts, fakeLedgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "create_payout",
      requiresApproval: false,
    }),
    connectorTool(payouts, fakeLedgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "approved_payout",
    }),
    connectorTool(payouts, fakeLedgerConnection, {
      manifest,
      inputValidator: CreatePayoutInputSchema,
      name: "payout_checked",
      requiresApproval: (toolArguments) => Number(toolArguments["amount"]) > 50,
    }),
    connectorTool(notes, fakeLedgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "post_note" }),
    connectorTool(notes, fakeLedgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "policy_note" }),
  ]);
}

/** A dynamic source of `hiding_note`, the single-attempt `postNote`, whose listing a test can empty or break. */
export class HidingToolSource implements ToolSource {
  public readonly sourceName = "hiding-ledger";
  private readonly inner: ConnectorToolSource;

  public constructor(private readonly stateDirectory: string) {
    const notes = new PostNoteOperation(new FakeLedgerProvider(stateDirectory));
    const manifest = structuredClone(fakeLedgerManifest) as ConnectorPackageManifest;
    this.inner = new ConnectorToolSource(
      [connectorTool(notes, fakeLedgerConnection, { manifest, inputValidator: PostNoteInputSchema, name: "hiding_note" })],
      { sourceName: this.sourceName },
    );
  }

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    const hideFile = join(this.stateDirectory, hideSourceToolsFileName);
    if (existsSync(hideFile)) {
      if (readFileSync(hideFile, "utf8") === "raise") {
        throw new Error("the hiding source is down");
      }
      return [];
    }
    return this.inner.listTools();
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall {
    return this.inner.deferredToolCall(turn, call);
  }

  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    return this.inner.toolCallStepOptions(toolCall);
  }

  public executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    return this.inner.executeDeferredToolCall(call);
  }

  public pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return this.inner.pollToolTask(poll);
  }
}

/** An application hook: a `Bash` command `rewrite:<text>` becomes `echo reviewed <text>`. */
export const reviewBashCommandHook: HookCallbackMatcher = {
  matcher: "Bash",
  hooks: [
    async (hookInput) => {
      const toolInput = hookInput.hook_event_name === "PreToolUse" ? (hookInput.tool_input as { command?: unknown }) : {};
      if (typeof toolInput.command !== "string" || !toolInput.command.startsWith("rewrite:")) {
        return {};
      }
      return {
        hookSpecificOutput: {
          hookEventName: "PreToolUse",
          updatedInput: { command: `echo reviewed ${toolInput.command.slice("rewrite:".length)}` },
        },
      };
    },
  ],
};

/** An application hook: a `payout_checked` call whose memo is `raise` gets the amount {@link raisedPayoutAmount}. */
export const raiseCheckedPayoutHook: HookCallbackMatcher = {
  matcher: "mcp__tools__payout_checked",
  hooks: [
    async (hookInput) => {
      const toolInput = hookInput.hook_event_name === "PreToolUse" ? (hookInput.tool_input as Record<string, unknown>) : {};
      if (toolInput["memo"] !== "raise") {
        return {};
      }
      return {
        hookSpecificOutput: { hookEventName: "PreToolUse", updatedInput: { ...toolInput, amount: raisedPayoutAmount } },
      };
    },
  ],
};

/** An application hook that denies every `publish` and `post_note` while {@link denyFlagFileName} exists. */
function createDenyWhileFlaggedHook(stateDirectory: string): HookCallbackMatcher {
  return {
    matcher: "mcp__tools__(publish|post_note)",
    hooks: [
      async () => {
        if (!existsSync(join(stateDirectory, denyFlagFileName))) {
          return {};
        }
        return {
          hookSpecificOutput: {
            hookEventName: "PreToolUse",
            permissionDecision: "deny",
            permissionDecisionReason: applicationHookDenial,
          },
        };
      },
    ],
  };
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
