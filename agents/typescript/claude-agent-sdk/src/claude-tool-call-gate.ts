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

const cliToolUseIdMetaKey = "claudecode/toolUseId";
const cliToolNames: ReadonlySet<string> = new Set([
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

/** Names and classifies every tool a Claude agent can call. */
export class ToolCatalog {
  private readonly customToolPrefix: string;
  private readonly customToolsByName: ReadonlyMap<string, ClaudeCustomTool>;

  public constructor(
    customTools: readonly ClaudeCustomTool[],
    public readonly customToolServerName: string,
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
  }

  public get customTools(): ClaudeCustomTool[] {
    return [...this.customToolsByName.values()];
  }

  public customTool(policyToolName: string): ClaudeCustomTool | undefined {
    return this.customToolsByName.get(policyToolName);
  }

  /** Returns the name that tool policies and records use for a CLI tool name. */
  public policyToolName(cliToolName: string): string {
    if (cliToolName.startsWith(this.customToolPrefix)) {
      const customToolName = cliToolName.slice(this.customToolPrefix.length);
      if (this.customToolsByName.has(customToolName)) {
        return customToolName;
      }
    }
    return cliToolName;
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
  public requiresDeferral(policyToolName: string, policy: ResolvedToolPolicy): boolean {
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

/** Decides every tool call of one `RunModel` Step and serves custom tool calls. */
export class ToolCallGate {
  private deferralRequest: DeferredToolCall | undefined;
  private readonly permittedToolUseIds = new Set<string>();
  private readonly runningToolNames = new Map<string, string>();
  private readonly customToolCallPlans = new Map<string, CustomToolCallPlan>();

  public constructor(
    private readonly turn: AgentTurnContext,
    private readonly toolCatalog: ToolCatalog,
  ) {}

  public resolvePolicy(policyToolName: string): ResolvedToolPolicy {
    return this.turn.resolveToolPolicy(policyToolName, this.toolCatalog.frameworkEffect(policyToolName));
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
    const policyToolName = this.toolCatalog.policyToolName(cliToolName);
    const record = this.turn.recordedToolCall(callId);
    if (record !== undefined) {
      return this.replayRecordedToolCall(record, policyToolName);
    }
    const policy = this.resolvePolicy(policyToolName);
    if (!this.toolCatalog.requiresDeferral(policyToolName, policy)) {
      this.permitInlineToolCall(callId, policyToolName);
      return preToolUseDecision("allow");
    }
    if (hookInput.agent_id !== undefined && hookInput.agent_id !== "") {
      return preToolUseDecision("deny", subagentDeferralRefusedMessage(policyToolName));
    }
    // The result names one deferred call, and the CLI checks siblings per content block, not per reply.
    if (this.deferralRequest !== undefined && this.deferralRequest.toolCallId !== callId) {
      return preToolUseDecision("deny", soloDeferralRequiredMessage(policyToolName));
    }
    this.deferralRequest = {
      toolCallId: callId,
      toolName: policyToolName,
      arguments: JSON.stringify(toolInput),
      policy,
    };
    return preToolUseDecision("defer", "Dex runs this call in its own Step.");
  };

  /** Answers a permission request the CLI sends after the hook did not settle a call. */
  public readonly decideToolPermission: CanUseTool = async (cliToolName, toolInput, options) => {
    const policyToolName = this.toolCatalog.policyToolName(cliToolName);
    const callId = options.toolUseID;
    const record = this.turn.recordedToolCall(callId);
    // A resumed call can skip the hook when the CLI starts it before the hooks are registered.
    if (record !== undefined && !this.permittedToolUseIds.has(callId)) {
      return toPermissionResult(this.replayRecordedToolCall(record, policyToolName), toolInput, callId);
    }
    // The CLI ignores defer for a call with siblings and asks for permission instead.
    if (this.deferralRequest?.toolCallId === callId) {
      this.deferralRequest = undefined;
      return denyPermission(soloDeferralRequiredMessage(policyToolName), callId);
    }
    if (this.permittedToolUseIds.has(callId)) {
      return { behavior: "allow", updatedInput: toolInput, toolUseID: callId };
    }
    return denyPermission(unpermittedToolCallMessage(policyToolName), callId);
  };

  /** Returns a copy of a custom tool whose handler serves only calls this gate permitted. */
  public gatedCustomTool(customTool: ClaudeCustomTool): ClaudeCustomTool {
    const policyToolName = customTool.name;
    const handleGatedCustomToolCall = async (
      toolArguments: Record<string, unknown>,
      extra: unknown,
    ): Promise<CustomToolResult> => {
      const plan = this.takeCustomToolCallPlan(cliToolUseIdOf(extra));
      if (plan === undefined) {
        return this.refuseUnplannedCustomToolCall(policyToolName);
      }
      if (plan.recordedToolCall !== undefined) {
        return recordedToolCallResult(plan.recordedToolCall);
      }
      return customTool.handler(toolArguments, extra);
    };
    return { ...customTool, handler: handleGatedCustomToolCall };
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
    const requested = this.deferralRequest;
    if (requested !== undefined) {
      if (requested.toolCallId !== deferredToolUse.id) {
        throw new Error(
          `the Claude Code CLI deferred ${deferredToolUse.id}, but the adapter deferred ${requested.toolCallId}`,
        );
      }
      return { ...requested, arguments: JSON.stringify(deferredToolUse.input) };
    }
    const policyToolName = this.toolCatalog.policyToolName(deferredToolUse.name);
    return {
      toolCallId: deferredToolUse.id,
      toolName: policyToolName,
      arguments: JSON.stringify(deferredToolUse.input),
      policy: this.resolvePolicy(policyToolName),
    };
  }

  private replayRecordedToolCall(record: AgentToolCall, policyToolName: string): HookJSONOutput {
    if (!isFinalToolCallStatus(record.status)) {
      throw new Error(`tool call ${record.toolCallId} resumed before it was final`);
    }
    // The handler returns a custom tool's rejection, so the model sees the text without a hook prefix.
    if (this.toolCatalog.customTool(policyToolName) !== undefined) {
      this.planCustomToolCall(record.toolCallId, record);
      this.permittedToolUseIds.add(record.toolCallId);
      return preToolUseDecision("allow");
    }
    if (record.status === "succeeded") {
      this.permitInlineToolCall(record.toolCallId, policyToolName);
      return preToolUseDecision("allow");
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

  private refuseUnplannedCustomToolCall(policyToolName: string): CustomToolResult {
    const policy = this.resolvePolicy(policyToolName);
    const message = this.toolCatalog.requiresDeferral(policyToolName, policy)
      ? soloDeferralRequiredMessage(policyToolName)
      : unpermittedToolCallMessage(policyToolName);
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

function preToolUseDecision(decision: HookPermissionDecision, reason?: string): HookJSONOutput {
  return {
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: decision,
      ...(reason === undefined ? {} : { permissionDecisionReason: reason }),
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
    return { behavior: "allow", updatedInput: toolInput, toolUseID: toolUseId };
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

function cliToolUseIdOf(extra: unknown): string | undefined {
  if (typeof extra !== "object" || extra === null || !("_meta" in extra)) {
    return undefined;
  }
  const meta = (extra as { readonly _meta?: unknown })._meta;
  if (typeof meta !== "object" || meta === null) {
    return undefined;
  }
  const toolUseId = (meta as Record<string, unknown>)[cliToolUseIdMetaKey];
  return typeof toolUseId === "string" && toolUseId !== "" ? toolUseId : undefined;
}
