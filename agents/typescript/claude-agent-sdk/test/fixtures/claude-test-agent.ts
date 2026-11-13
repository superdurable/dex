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
import { LocalDirectoryMediaStore, ToolExecutionError } from "@superdurable/dex-agents";
import type {
  AgentSessionFlow,
  AgentToolCall,
  AgentTurnContext,
  DeferredToolCall,
  DeferredToolExecution,
  DeferredToolOutcome,
  MediaStore,
  ToolCallStepOptions,
  ToolExecutionResult,
  ToolPolicy,
  ToolTaskCancellation,
  ToolTaskPoll,
  ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import { ConnectorToolSource, connectorTool, type ConnectorPackageManifest } from "@superdurable/dex-agents/connectors";
import type { SourceToolDefinition, ToolSource } from "@superdurable/dex-agents/builtin-loop";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import {
  ClaudeCodeToolDenial,
  createClaudeAgentSessionFlow,
  type ClaudeAgentRunner,
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

/** A `Bash` command the Claude Code tool permission runs without approval. */
export const inlineBashCommand = "echo inline";
/** `Bash` commands with this prefix are denied by the Claude Code tool permission. */
export const deniedBashCommandPrefix = "forbidden";
/** The reason the Claude Code tool permission gives for a denied command. */
export const deniedBashCommandReason = "forbidden commands never run";
/** A `Bash` command for which the Claude Code tool permission throws. */
export const failingPermissionBashCommand = "break the permission check";

/**
 * Runs {@link inlineBashCommand} without approval, denies commands that start with {@link deniedBashCommandPrefix},
 * throws for {@link failingPermissionBashCommand}, and leaves every other call to the session's policies.
 */
function decideClaudeCodeToolPermission(
  _turn: AgentTurnContext,
  toolName: string,
  toolInput: Readonly<Record<string, unknown>>,
): ToolPolicy | ClaudeCodeToolDenial | undefined {
  const command = toolName === "Bash" && typeof toolInput["command"] === "string" ? toolInput["command"] : "";
  if (command === inlineBashCommand) {
    return { effect: "non_idempotent_write", requiresApproval: false };
  }
  if (command.startsWith(deniedBashCommandPrefix)) {
    return new ClaudeCodeToolDenial(deniedBashCommandReason);
  }
  if (command === failingPermissionBashCommand) {
    throw new Error("the permission check broke");
  }
  return undefined;
}

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

/** Worker child variable: `"1"` gives the agent a media store, {@link createMediaStore}. */
export const claudeTestAgentMediaStoreVariable = "CLAUDE_TEST_AGENT_MEDIA_STORE";
/** Worker child variable: a JSON {@link ClaudeTestAgentCli}, so a child runs the same CLI as the test's Flow. */
export const claudeTestAgentCliVariable = "CLAUDE_TEST_AGENT_CLI";

const fakeClaudeCliPath = fileURLToPath(new URL("./fake-claude-cli.js", import.meta.url));
const conformanceImagesPath = fileURLToPath(new URL("../../../../../conformance/images.json", import.meta.url));

/** One image of the cores' shared conformance data. */
export interface ConformanceImage {
  readonly name: string;
  readonly base64: string;
  readonly mime_type: string;
  readonly sha256: string;
}

/** The conformance images by name: `png`, `jpeg`, `gif`, and `webp`. */
export const conformanceImages: ReadonlyMap<string, ConformanceImage> = new Map(
  (JSON.parse(readFileSync(conformanceImagesPath, "utf8")) as { readonly images: readonly ConformanceImage[] }).images.map(
    (image) => [image.name, image],
  ),
);

/** Returns one conformance image. */
export function conformanceImage(name: string): ConformanceImage {
  const image = conformanceImages.get(name);
  if (image === undefined) {
    throw new Error(`no conformance image ${name}`);
  }
  return image;
}

/** The media store a test shares with its Workers: a local directory under the state directory. */
export function createMediaStore(stateDirectory: string): LocalDirectoryMediaStore {
  return new LocalDirectoryMediaStore(join(stateDirectory, "media"));
}

// Annotations classify lookup and charge; Bash keeps the default, which needs approval.
const testToolPolicies: Readonly<Record<string, ToolPolicy>> = {
  publish: { effect: "non_idempotent_write" },
  slow_write: { effect: "non_idempotent_write", requiresApproval: false },
  // The fake CLI's subagent tool, which then runs inside the turn.
  Agent: { effect: "read" },
  // Claims read; a connector tool takes only the approval from it.
  policy_note: { effect: "read", requiresApproval: true },
  screenshot: { effect: "read", requiresApproval: true },
  draw: { effect: "non_idempotent_write", requiresApproval: false },
  report: { effect: "read", requiresApproval: false },
  choose_account: { effect: "read", requiresApproval: false },
};

/**
 * The test agent's CLI: the fake when `executablePath` is unset, else `environment` is the whole environment.
 * `hasConfigDirectory: false` leaves `CLAUDE_CONFIG_DIR` out of the CLI environment, and `isEnvironmentUnset: true`
 * passes no `env`, so the fake CLI inherits `process.env`.
 */
export interface ClaudeTestAgentCli {
  readonly flowType: string;
  readonly executablePath?: string;
  readonly environment?: Readonly<Record<string, string>>;
  readonly hasConfigDirectory?: boolean;
  readonly isEnvironmentUnset?: boolean;
  readonly model?: string;
  readonly permissionMode?: PermissionMode;
  readonly allowedTools?: readonly string[];
}

/**
 * Creates the Claude agent Flow; tests and the Worker child build it identically. With `hasMediaStore`, the agent
 * takes images from {@link createMediaStore} and adds `screenshot`, a source tool that needs approval and returns the
 * GIF conformance image as a reference, and `draw`, a deferred custom tool that returns the JPEG one as MCP content.
 */
export function createClaudeTestAgentFlow(
  stateDirectory: string,
  cli?: ClaudeTestAgentCli,
  sessionOptions: ClaudeAgentSessionOptions = {},
  hasMediaStore = false,
): AgentSessionFlow<ClaudeAgentRunner> {
  mkdirSync(join(stateDirectory, "workspace"), { recursive: true });
  mkdirSync(join(stateDirectory, "claude-config"), { recursive: true });
  const environment =
    cli?.executablePath === undefined
      ? { ...process.env, [fakeClaudeStateDirectoryVariable]: stateDirectory, ...cli?.environment }
      : (cli.environment ?? {});
  const buildOptions = (context: ClaudeAgentOptionsContext): ClaudeAgentQueryOptions => ({
    pathToClaudeCodeExecutable: cli?.executablePath ?? fakeClaudeCliPath,
    cwd: join(stateDirectory, "workspace"),
    ...(cli?.isEnvironmentUnset === true
      ? {}
      : {
          env:
            cli?.hasConfigDirectory === false
              ? environment
              : { ...environment, CLAUDE_CONFIG_DIR: join(stateDirectory, "claude-config") },
        }),
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
  const mediaStore = hasMediaStore ? createMediaStore(stateDirectory) : undefined;
  const customTools = createTestCustomTools(stateDirectory);
  const toolSources: ToolSource[] = [
    createLedgerToolSource(stateDirectory),
    new HidingToolSource(stateDirectory),
    new ReportToolSource(stateDirectory),
  ];
  if (mediaStore !== undefined) {
    customTools.push(createDrawTool(stateDirectory));
    toolSources.push(new ScreenshotToolSource(stateDirectory, mediaStore));
  }
  return createClaudeAgentSessionFlow(
    {
      options: buildOptions,
      customTools,
      toolPolicies: testToolPolicies,
      claudeCodeToolPermission: decideClaudeCodeToolPermission,
      toolSources,
      ...(mediaStore === undefined ? {} : { mediaStore }),
    },
    cli?.flowType ?? claudeTestAgentFlowType,
    sessionOptions,
  );
}

/** Lists `screenshot`, which uploads the GIF conformance image and returns its reference. */
export class ScreenshotToolSource implements ToolSource {
  public readonly sourceName = "screenshots";
  private readonly invocations: InvocationLog;

  public constructor(
    stateDirectory: string,
    private readonly mediaStore: MediaStore,
  ) {
    this.invocations = new InvocationLog(join(stateDirectory, fakeClaudeInvocationLogName));
  }

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return [{ name: "screenshot", description: "Take a screenshot", inputSchema: { type: "object", properties: {} } }];
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return { ...call, policy: turn.resolveToolPolicy(call.toolName, "read") };
  }

  public async executeDeferredToolCall(): Promise<DeferredToolOutcome> {
    this.invocations.record("screenshot");
    const reference = await this.mediaStore.putImage(Buffer.from(conformanceImage("gif").base64, "base64"));
    return { output: "took a screenshot", images: [reference] };
  }

  public async pollToolTask(): Promise<ToolTaskPollOutcome> {
    throw new Error("screenshot starts no tool task");
  }
}

function createDrawTool(stateDirectory: string): ClaudeCustomTool {
  const invocations = new InvocationLog(join(stateDirectory, fakeClaudeInvocationLogName));
  return tool("draw", "Draw a picture", { subject: z.string() }, async (toolArguments) => {
    invocations.record("draw");
    return {
      content: [
        { type: "text", text: `drew ${toolArguments.subject}` },
        { type: "image", data: conformanceImage("jpeg").base64, mimeType: "image/jpeg" },
      ],
    };
  });
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

const reportSourceReference = JSON.stringify({ kind: "report" });

/**
 * Lists `report`, whose call starts a tool task that `pollToolTask` polls, and `choose_account`, whose call asks
 * the user which account to use. Both are reads without approval.
 *
 * A `report` call returns `ToolTaskStarted` with task ID `task-<tool call ID>`. Its arguments: `topic` names the
 * result `report on <topic>`; `polls` is the poll that finishes the task, counted from 1; `fail` makes that poll
 * throw `ToolExecutionError`; `hold` keeps the task pending. A `choose_account` call returns `account <answer>`
 * once the user answered. The invocation log records `start <task>`, `poll <task> <n>`,
 * `cancel-task <task> <status> <cancellationReason>`, and `choose-account <answer count>`.
 */
export class ReportToolSource implements ToolSource {
  public readonly sourceName = "reports";
  private readonly invocations: InvocationLog;

  public constructor(stateDirectory: string) {
    this.invocations = new InvocationLog(join(stateDirectory, fakeClaudeInvocationLogName));
  }

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return [
      { name: "report", description: "Write a report as a task", inputSchema: { type: "object", properties: {} } },
      { name: "choose_account", description: "Ask which account to use", inputSchema: { type: "object", properties: {} } },
    ];
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return {
      ...call,
      policy: turn.resolveToolPolicy(call.toolName, "read"),
      toolSourceReference: reportSourceReference,
    };
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const record = call.toolCall;
    if (record.toolSourceReference !== reportSourceReference) {
      throw new Error(`${record.toolName} got another source's reference`);
    }
    if (record.toolName === "choose_account") {
      const answers = call.inputAnswers ?? [];
      this.invocations.record(`choose-account ${answers.length}`);
      const answer = answers.at(-1)?.answer;
      return answer === undefined
        ? { kind: "tool_input_requested", question: "Which account?", continuation: null }
        : { output: `account ${answer}` };
    }
    if (call.supportsToolTasks !== true) {
      throw new Error("the runner does not poll tool tasks");
    }
    const taskId = `task-${record.toolCallId}`;
    this.invocations.record(`start ${taskId}`);
    return { kind: "tool_task_started", taskId, pollIntervalSeconds: 1 };
  }

  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    const record = poll.toolCall;
    if (record.toolSourceReference !== reportSourceReference) {
      throw new Error("report got another source's reference");
    }
    const taskId = record.taskId ?? "";
    const pollNumber = this.invocations.names().filter((name) => name.startsWith(`poll ${taskId} `)).length + 1;
    this.invocations.record(`poll ${taskId} ${pollNumber}`);
    const toolArguments = JSON.parse(record.arguments) as {
      readonly topic?: string;
      readonly polls?: number;
      readonly fail?: boolean;
      readonly hold?: boolean;
    };
    if (toolArguments.hold === true || pollNumber < (toolArguments.polls ?? 1)) {
      return { kind: "tool_task_pending", pollIntervalSeconds: 1 };
    }
    if (toolArguments.fail === true) {
      throw new ToolExecutionError(`the report on ${toolArguments.topic} failed`);
    }
    return { output: `report on ${toolArguments.topic}` };
  }

  public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    const record = cancellation.toolCall;
    this.invocations.record(`cancel-task ${record.taskId} ${record.status} ${record.cancellationReason ?? "none"}`);
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
