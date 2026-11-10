// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  BUILTIN_TOOL_NAMES,
  LEGACY_TOOL_NAME_ALIASES,
  type CanUseTool,
  type HookCallback,
  type HookInput,
  type HookJSONOutput,
  type HookPermissionDecision,
  type PermissionResult,
  type SDKDeferredToolUse,
  type SdkMcpToolDefinition,
} from "@anthropic-ai/claude-agent-sdk";
import {
  describeToolCallOutcome,
  isFinalToolCallStatus,
  toolEffectFromMcpToolAnnotations,
  type AgentToolCall,
  type AgentTurnContext,
  type DeferredToolCall,
  type ResolvedToolPolicy,
  type ToolEffect,
} from "@superdurable/dex-agents";

import type { ListedSourceTool } from "./claude-tool-sources.js";

type CustomToolResult = Awaited<ReturnType<SdkMcpToolDefinition["handler"]>>;

/**
 * A custom tool definition as `tool()` from `@anthropic-ai/claude-agent-sdk` creates it.
 *
 * Any input schema is accepted, so tools with different argument types fit in one array.
 */
export type ClaudeCustomTool = Omit<SdkMcpToolDefinition, "handler"> & {
  /** The tool's implementation, called with arguments parsed by its input schema. */
  // tool() returns a schema-specific handler; any accepts every argument type.
  readonly handler: (toolArguments: any, extra: unknown) => Promise<CustomToolResult>;
};

/** Names of the tools that run inside the Claude Code CLI, which no custom or source tool may use. */
export const cliToolNames: ReadonlySet<string> = new Set([
  ...BUILTIN_TOOL_NAMES,
  ...Object.keys(LEGACY_TOOL_NAME_ALIASES),
  ...Object.values(LEGACY_TOOL_NAME_ALIASES),
]);

const soloDeferralRequiredMessage = (toolName: string): string =>
  `${toolName} did not run. It must run outside this turn, which works only when it is the only ` +
  `tool call in its message. Call ${toolName} again by itself.`;
const subagentDeferralRefusedMessage = (toolName: string): string =>
  `${toolName} did not run. It must run outside this turn, which a subagent cannot do. Call it ` +
  "from the main conversation instead.";
const unpermittedToolCallMessage = (toolName: string): string =>
  `${toolName} did not run: the session's tool policies do not permit this call.`;
const orderedWriteDeferralMessage = (toolName: string): string =>
  `${toolName} did not run. Writes run one at a time in your order, and an earlier call of this reply ` +
  `runs first. Read its result, then call ${toolName} again by itself if it should still run.`;
const unlistedSourceToolMessage = (toolName: string): string =>
  `${toolName} did not run: its tool source does not offer this tool now.`;

/** Names and classifies every tool a Claude agent can call in one turn segment. */
export class ToolCatalog {
  private readonly customToolPrefix: string;
  private readonly customToolsByName: ReadonlyMap<string, ClaudeCustomTool>;
  private readonly sourceToolsByName: ReadonlyMap<string, ListedSourceTool>;
  private readonly replayOnlyToolNameSet: ReadonlySet<string>;

  // replayOnlyToolNames: recorded source tools this listing lacks, served only to replay their records.
  public constructor(
    customTools: readonly ClaudeCustomTool[],
    public readonly customToolServerName: string,
    sourceTools: ReadonlyMap<string, ListedSourceTool> = new Map(),
    replayOnlyToolNames: Iterable<string> = [],
  ) {
    this.customToolPrefix = `mcp__${customToolServerName}__`;
    this.customToolsByName = new Map(customTools.map((customTool) => [customTool.name, customTool]));
    if (this.customToolsByName.size !== customTools.length) {
      throw new TypeError("custom tool names must be unique");
    }
    // Policies and records name custom tools bare, so a bare name must not mean a CLI tool too.
    for (const customToolName of this.customToolsByName.keys()) {
      if (cliToolNames.has(customToolName) || customToolName.startsWith("mcp__")) {
        throw new TypeError(
          `custom tool ${JSON.stringify(customToolName)} collides with a Claude Code tool name; rename it`,
        );
      }
    }
    this.sourceToolsByName = sourceTools;
    this.replayOnlyToolNameSet = new Set(
      [...replayOnlyToolNames].filter(
        (toolName) => !this.customToolsByName.has(toolName) && !sourceTools.has(toolName),
      ),
    );
  }

  /** Returns this catalog's custom tools with the tools that the sources listed for one segment. */
  public withSourceTools(
    sourceTools: ReadonlyMap<string, ListedSourceTool>,
    replayOnlyToolNames: Iterable<string>,
  ): ToolCatalog {
    return new ToolCatalog(this.customTools, this.customToolServerName, sourceTools, replayOnlyToolNames);
  }

  public get customTools(): ClaudeCustomTool[] {
    return [...this.customToolsByName.values()];
  }

  public get sourceTools(): ListedSourceTool[] {
    return [...this.sourceToolsByName.values()];
  }

  public get replayOnlyToolNames(): string[] {
    return [...this.replayOnlyToolNameSet].sort();
  }

  public get hasServedTools(): boolean {
    return this.customToolsByName.size > 0 || this.sourceToolsByName.size > 0 || this.replayOnlyToolNameSet.size > 0;
  }

  public customTool(policyToolName: string): ClaudeCustomTool | undefined {
    return this.customToolsByName.get(policyToolName);
  }

  public sourceTool(policyToolName: string): ListedSourceTool | undefined {
    return this.sourceToolsByName.get(policyToolName);
  }

  public isReplayOnlyTool(policyToolName: string): boolean {
    return this.replayOnlyToolNameSet.has(policyToolName);
  }

  /** Whether the in-process server serves a tool of this bare name: custom, source, or replay-only. */
  public isServedToolName(toolName: string): boolean {
    return (
      this.customToolsByName.has(toolName) ||
      this.sourceToolsByName.has(toolName) ||
      this.replayOnlyToolNameSet.has(toolName)
    );
  }

  /** Returns the bare name of a tool the in-process server serves, classified by its CLI name only. */
  public servedToolName(cliToolName: string): string | undefined {
    if (!cliToolName.startsWith(this.customToolPrefix)) {
      return undefined;
    }
    const toolName = cliToolName.slice(this.customToolPrefix.length);
    return this.isServedToolName(toolName) ? toolName : undefined;
  }

  /** Returns the name that tool policies and records use for a CLI tool name. */
  public policyToolName(cliToolName: string): string {
    return this.servedToolName(cliToolName) ?? cliToolName;
  }

  public frameworkEffect(policyToolName: string): ToolEffect | undefined {
    const annotations = this.customToolsByName.get(policyToolName)?.annotations;
    if (annotations === undefined) {
      return undefined;
    }
    return toolEffectFromMcpToolAnnotations({
      ...(annotations.readOnlyHint === undefined ? {} : { readOnlyHint: annotations.readOnlyHint }),
      ...(annotations.idempotentHint === undefined ? {} : { idempotentHint: annotations.idempotentHint }),
      ...(annotations.destructiveHint === undefined ? {} : { destructiveHint: annotations.destructiveHint }),
    });
  }

  // The adapter cannot record a tool inside the CLI, so such a tool defers only for approval.
  public requiresDeferral(policyToolName: string, policy: ResolvedToolPolicy | undefined): boolean {
    // A source tool has no policy here and always runs in the tool Step its source needs.
    if (policy === undefined) {
      return true;
    }
    if (this.customToolsByName.has(policyToolName)) {
      return policy.effect !== "read" || policy.requiresApproval;
    }
    return policy.requiresApproval;
  }
}

interface CustomToolCallPlan {
  readonly toolUseId: string;
  readonly recordedToolCall: AgentToolCall | undefined;
}

/** Decides every tool call of one `RunModel` Step and serves custom and source tool calls. */
export class ToolCallGate {
  private deferralRequest: DeferredToolCall | undefined;
  private deferredSourceTool: ListedSourceTool | undefined;
  private readonly permittedToolUseIds = new Set<string>();
  private readonly runningToolNames = new Map<string, string>();
  private readonly customToolCallPlans = new Map<string, CustomToolCallPlan>();

  public constructor(
    private readonly turn: AgentTurnContext,
    private readonly toolCatalog: ToolCatalog,
  ) {}

  /** Returns the policy of a custom or CLI tool, or `undefined` for a source or replay-only tool. */
  public resolvePolicy(cliToolName: string): ResolvedToolPolicy | undefined {
    const servedToolName = this.toolCatalog.servedToolName(cliToolName);
    if (
      servedToolName !== undefined &&
      (this.toolCatalog.sourceTool(servedToolName) !== undefined || this.toolCatalog.isReplayOnlyTool(servedToolName))
    ) {
      return undefined;
    }
    const policyToolName = servedToolName ?? cliToolName;
    return this.turn.resolveToolPolicy(policyToolName, this.toolCatalog.frameworkEffect(policyToolName));
  }

  /** Wraps an application PreToolUse callback so it never sees a call with a final record. */
  public skipHookOnReplay(callback: HookCallback): HookCallback {
    return async (hookInput, toolUseId, options) => {
      const callId = hookInput.hook_event_name === "PreToolUse" ? hookInput.tool_use_id || toolUseId || "" : "";
      const record = callId === "" ? undefined : this.turn.recordedToolCall(callId);
      if (record !== undefined && isFinalToolCallStatus(record.status)) {
        return {};
      }
      return callback(hookInput, toolUseId, options);
    };
  }

  /** Answers the CLI's PreToolUse hook with allow, deny, or defer. */
  public readonly decidePreToolUse = async (
    hookInput: HookInput,
    toolUseId: string | undefined,
  ): Promise<HookJSONOutput> => {
    if (hookInput.hook_event_name !== "PreToolUse") {
      return {};
    }
    const cliToolName = hookInput.tool_name;
    const toolInput = toToolInputRecord(hookInput.tool_input);
    const callId = hookInput.tool_use_id || toolUseId || "";
    if (callId === "") {
      throw new Error(`the CLI sent PreToolUse for ${cliToolName} without a tool_use_id`);
    }
    const servedToolName = this.toolCatalog.servedToolName(cliToolName);
    const policyToolName = servedToolName ?? cliToolName;
    const record = this.turn.recordedToolCall(callId);
    if (record !== undefined) {
      return this.replayRecordedToolCall(record, servedToolName);
    }
    if (servedToolName !== undefined && this.toolCatalog.isReplayOnlyTool(servedToolName)) {
      // A new call would run under a new Call ID; only the source's listing permits that.
      return preToolUseDecision("deny", unlistedSourceToolMessage(policyToolName));
    }
    const sourceTool = servedToolName === undefined ? undefined : this.toolCatalog.sourceTool(servedToolName);
    const policy = this.resolvePolicy(cliToolName);
    if (!this.toolCatalog.requiresDeferral(policyToolName, policy)) {
      this.permitInlineToolCall(callId, policyToolName);
      return preToolUseDecision("allow");
    }
    if (hookInput.agent_id !== undefined && hookInput.agent_id !== "") {
      return preToolUseDecision("deny", subagentDeferralRefusedMessage(policyToolName));
    }
    const toolArguments = JSON.stringify(toolInput);
    const deferral =
      sourceTool === undefined
        ? { toolCallId: callId, toolName: policyToolName, arguments: toolArguments, policy: requirePolicy(policy) }
        : sourceTool.buildDeferredToolCall(this.turn, callId, toolArguments);
    // The result names one deferred call, and the CLI checks siblings per content block, not per reply.
    if (this.deferralRequest !== undefined && this.deferralRequest.toolCallId !== callId) {
      const message = deferral.ordered === true ? orderedWriteDeferralMessage : soloDeferralRequiredMessage;
      return preToolUseDecision("deny", message(policyToolName));
    }
    this.deferralRequest = deferral;
    this.deferredSourceTool = sourceTool;
    return preToolUseDecision("defer", "Dex runs this call in its own Step.");
  };

  /** Answers a permission request the CLI sends after the hook did not settle a call. */
  public readonly decideToolPermission: CanUseTool = async (cliToolName, toolInput, options) => {
    const servedToolName = this.toolCatalog.servedToolName(cliToolName);
    const policyToolName = servedToolName ?? cliToolName;
    const callId = options.toolUseID;
    const record = this.turn.recordedToolCall(callId);
    // A resumed call can skip the hook when the CLI starts it before the hooks are registered.
    if (record !== undefined && !this.permittedToolUseIds.has(callId)) {
      return toPermissionResult(this.replayRecordedToolCall(record, servedToolName), toolInput, callId);
    }
    // The CLI ignores defer for a call with siblings and asks for permission instead.
    if (this.deferralRequest?.toolCallId === callId) {
      this.deferralRequest = undefined;
      this.deferredSourceTool = undefined;
      return denyPermission(soloDeferralRequiredMessage(policyToolName), callId);
    }
    if (this.permittedToolUseIds.has(callId)) {
      return { behavior: "allow", updatedInput: toolInput, toolUseID: callId };
    }
    return denyPermission(unpermittedToolCallMessage(policyToolName), callId);
  };

  /** Serves one MCP tool call: a resumed call's record, a permitted inline custom tool, or a refusal. */
  public async callServedTool(
    toolName: string,
    toolUseId: string | undefined,
    runCustomTool: (() => Promise<CustomToolResult>) | undefined,
  ): Promise<CustomToolResult> {
    const plan = this.takeCustomToolCallPlan(toolUseId);
    if (plan === undefined) {
      return this.refuseUnplannedToolCall(toolName);
    }
    if (plan.recordedToolCall !== undefined) {
      return recordedToolCallResult(plan.recordedToolCall);
    }
    // A source tool runs only in its tool Step; the server only replays its record.
    if (runCustomTool === undefined) {
      return textToolResult(unpermittedToolCallMessage(toolName), true);
    }
    return runCustomTool();
  }

  public recordToolResult(toolUseId: string, isError: boolean): void {
    const toolName = this.runningToolNames.get(toolUseId);
    if (toolName === undefined) {
      return;
    }
    this.runningToolNames.delete(toolUseId);
    this.turn.events.writeToolCallFinished(toolUseId, toolName, isError ? "failed" : "succeeded");
  }

  // The CLI's recorded input includes every hook's updatedInput; another call than ours is a defect.
  public deferredToolCall(deferredToolUse: SDKDeferredToolUse): DeferredToolCall {
    const toolArguments = JSON.stringify(deferredToolUse.input);
    const requested = this.deferralRequest;
    if (requested !== undefined) {
      if (requested.toolCallId !== deferredToolUse.id) {
        throw new Error(
          `the Claude Code CLI deferred ${deferredToolUse.id}, but the adapter deferred ${requested.toolCallId}`,
        );
      }
      // Rebuilt, so the source decides approval from the arguments its Step runs.
      if (this.deferredSourceTool !== undefined) {
        return this.deferredSourceTool.buildDeferredToolCall(this.turn, deferredToolUse.id, toolArguments);
      }
      return { ...requested, arguments: toolArguments };
    }
    const servedToolName = this.toolCatalog.servedToolName(deferredToolUse.name);
    const sourceTool = servedToolName === undefined ? undefined : this.toolCatalog.sourceTool(servedToolName);
    if (sourceTool !== undefined) {
      return sourceTool.buildDeferredToolCall(this.turn, deferredToolUse.id, toolArguments);
    }
    const policyToolName = servedToolName ?? deferredToolUse.name;
    const policy = this.resolvePolicy(deferredToolUse.name);
    if (policy === undefined) {
      throw new Error(`the Claude Code CLI deferred ${policyToolName}, which its tool source does not offer now`);
    }
    return { toolCallId: deferredToolUse.id, toolName: policyToolName, arguments: toolArguments, policy };
  }

  private replayRecordedToolCall(record: AgentToolCall, servedToolName: string | undefined): HookJSONOutput {
    if (!isFinalToolCallStatus(record.status)) {
      throw new Error(`tool call ${record.toolCallId} resumed before it was final`);
    }
    // The handler returns a custom or source tool's rejection, so the model sees the text without a hook prefix.
    if (servedToolName !== undefined) {
      this.planCustomToolCall(record.toolCallId, record);
      this.permittedToolUseIds.add(record.toolCallId);
      return preToolUseDecision("allow");
    }
    if (record.status === "succeeded") {
      this.permitInlineToolCall(record.toolCallId, record.toolName);
      // Application hooks are skipped on replay, so the CLI must run the approved input.
      return preToolUseDecision("allow", undefined, recordedToolInput(record));
    }
    return preToolUseDecision("deny", describeToolCallOutcome(record));
  }

  private permitInlineToolCall(toolUseId: string, policyToolName: string): void {
    this.permittedToolUseIds.add(toolUseId);
    if (this.toolCatalog.customTool(policyToolName) !== undefined) {
      this.planCustomToolCall(toolUseId, undefined);
    }
    this.runningToolNames.set(toolUseId, policyToolName);
    this.turn.events.writeToolCallStarted(toolUseId, policyToolName);
  }

  private planCustomToolCall(toolUseId: string, recordedToolCall: AgentToolCall | undefined): void {
    this.customToolCallPlans.set(toolUseId, { toolUseId, recordedToolCall });
  }

  // Hooks may rewrite the input, so only the tool use ID from _meta identifies a call.
  private takeCustomToolCallPlan(toolUseId: string | undefined): CustomToolCallPlan | undefined {
    if (toolUseId === undefined) {
      return undefined;
    }
    const plan = this.customToolCallPlans.get(toolUseId);
    this.customToolCallPlans.delete(toolUseId);
    return plan;
  }

  private refuseUnplannedToolCall(toolName: string): CustomToolResult {
    const policy =
      this.toolCatalog.customTool(toolName) === undefined
        ? undefined
        : this.turn.resolveToolPolicy(toolName, this.toolCatalog.frameworkEffect(toolName));
    const message = this.toolCatalog.requiresDeferral(toolName, policy)
      ? soloDeferralRequiredMessage(toolName)
      : unpermittedToolCallMessage(toolName);
    return textToolResult(message, true);
  }
}

/** Renders a final record as the custom tool result the model sees on resume. */
export function recordedToolCallResult(record: AgentToolCall): CustomToolResult {
  return textToolResult(describeToolCallOutcome(record), record.status !== "succeeded");
}

function textToolResult(text: string, isError: boolean): CustomToolResult {
  return { content: [{ type: "text", text }], isError };
}

function requirePolicy(policy: ResolvedToolPolicy | undefined): ResolvedToolPolicy {
  if (policy === undefined) {
    throw new Error("a custom or Claude Code tool call has no policy");
  }
  return policy;
}

function recordedToolInput(record: AgentToolCall): Record<string, unknown> | undefined {
  const recorded: unknown = JSON.parse(record.arguments);
  return typeof recorded === "object" && recorded !== null && !Array.isArray(recorded)
    ? (recorded as Record<string, unknown>)
    : undefined;
}

function preToolUseDecision(
  decision: HookPermissionDecision,
  reason?: string,
  updatedInput?: Record<string, unknown>,
): HookJSONOutput {
  return {
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: decision,
      ...(reason === undefined ? {} : { permissionDecisionReason: reason }),
      ...(updatedInput === undefined ? {} : { updatedInput }),
    },
  };
}

function toPermissionResult(
  hookOutput: HookJSONOutput,
  toolInput: Record<string, unknown>,
  toolUseId: string,
): PermissionResult {
  const specificOutput = "hookSpecificOutput" in hookOutput ? hookOutput.hookSpecificOutput : undefined;
  if (specificOutput?.hookEventName === "PreToolUse" && specificOutput.permissionDecision === "allow") {
    return { behavior: "allow", updatedInput: specificOutput.updatedInput ?? toolInput, toolUseID: toolUseId };
  }
  const reason =
    specificOutput?.hookEventName === "PreToolUse" ? specificOutput.permissionDecisionReason : undefined;
  return denyPermission(reason ?? "denied", toolUseId);
}

function denyPermission(message: string, toolUseId: string): PermissionResult {
  return { behavior: "deny", message, toolUseID: toolUseId };
}

function toToolInputRecord(toolInput: unknown): Record<string, unknown> {
  return typeof toolInput === "object" && toolInput !== null && !Array.isArray(toolInput)
    ? { ...(toolInput as Record<string, unknown>) }
    : {};
}
