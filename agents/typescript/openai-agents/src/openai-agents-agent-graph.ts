// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Agent, Handoff, getClientToolSearchExecutor, tool, type FunctionTool, type Tool } from "@openai/agents";
import { SandboxAgent } from "@openai/agents/sandbox";
import { getFunctionToolDisplayName } from "@openai/agents/utils";
import {
  describeToolCallOutcome,
  isFinalToolCallStatus,
  resolveToolPolicy,
  type ResolvedToolPolicy,
  type ToolEffect,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import { runningModelStepScope } from "./openai-agents-tool-scopes.js";

type AnyAgent = Agent<any, any>;
type AnyFunctionTool = FunctionTool<any, any, any>;
type FunctionToolCallDetails = Parameters<AnyFunctionTool["invoke"]>[2];

/** One function tool of the graph: the original tool, its first original owner, and the owning clones. */
export interface GraphFunctionTool {
  readonly originalTool: AnyFunctionTool;
  readonly originalOwnerAgent: AnyAgent;
  readonly clonedOwnerAgents: ReadonlySet<AnyAgent>;
}

/**
 * A copy of the application's agent graph in which every function tool stops the run for Dex.
 *
 * Every agent reachable through handoffs is cloned once, so handoffs between clones keep the
 * original graph's shape and agent names.
 */
export interface WrappedAgentGraph {
  /** Clone of the application's starting agent. */
  readonly startingAgent: AnyAgent;
  /** Function tools by qualified tool name. */
  readonly functionTools: ReadonlyMap<string, GraphFunctionTool>;
}

/** Tool policies, the session default, and the MCP opt-in, as resolved by the factory. */
export interface AgentGraphConfiguration {
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  readonly defaultToolEffect: ToolEffect;
  readonly shouldRunMcpServerToolsInline: boolean;
}

// tool() registers a boolean needsApproval as a static policy, which the SDK resumes without re-evaluating.
const approvalRequiredPolicy = tool({
  name: "dex_approval_required",
  description: "Static approval policy source.",
  parameters: { type: "object", properties: {}, required: [], additionalProperties: false },
  needsApproval: true,
  execute: () => "",
}).needsApproval;

/**
 * Clones an agent graph and gates every function tool, so each run stops after one model call.
 *
 * @param startingAgent - The application's starting agent.
 * @param configuration - Tool policies, the session default, and the MCP opt-in.
 * @returns The wrapped graph and its function tools by qualified name.
 * @throws {TypeError} When the graph has a sandbox agent, an agent used as a tool, a client tool
 * search with an execute function, MCP servers without the opt-in, a local non-function tool that would need a Dex Step, or two different function tools
 * with one qualified name.
 */
export function wrapAgentGraph(
  startingAgent: AnyAgent,
  configuration: AgentGraphConfiguration,
): WrappedAgentGraph {
  if (!(startingAgent instanceof Agent)) {
    throw new TypeError("createOpenAIAgentsSessionFlow requires an Agent from @openai/agents");
  }
  const originalAgents = collectAgentGraph(startingAgent);
  const functionTools = new Map<string, GraphFunctionTool & { clonedOwnerAgents: Set<AnyAgent> }>();
  const clonedAgents = new Map<AnyAgent, AnyAgent>();
  for (const originalAgent of originalAgents) {
    requireSupportedAgent(originalAgent, configuration);
    const gatedTools: Tool<any>[] = [];
    const ownedToolNames: string[] = [];
    for (const originalTool of originalAgent.tools) {
      if (originalTool.type !== "function") {
        requireUngatedToolAllowed(originalAgent, originalTool, configuration);
        gatedTools.push(originalTool);
        continue;
      }
      requireOrdinaryFunctionTool(originalAgent, originalTool);
      const toolName = readQualifiedToolName(originalTool);
      const registered = functionTools.get(toolName);
      if (registered !== undefined && registered.originalTool !== originalTool) {
        throw new TypeError(
          `agents ${registered.originalOwnerAgent.name} and ${originalAgent.name} have different function ` +
            `tools named ${toolName}; a tool call is resolved by its qualified name, so names must be unique`,
        );
      }
      if (registered === undefined) {
        functionTools.set(toolName, {
          originalTool,
          originalOwnerAgent: originalAgent,
          clonedOwnerAgents: new Set(),
        });
      }
      ownedToolNames.push(toolName);
      gatedTools.push(gateFunctionTool(originalTool, toolName));
    }
    const clonedAgent = originalAgent.clone({ tools: gatedTools, handoffs: [] });
    clonedAgents.set(originalAgent, clonedAgent);
    for (const toolName of ownedToolNames) {
      functionTools.get(toolName)?.clonedOwnerAgents.add(clonedAgent);
    }
  }
  for (const originalAgent of originalAgents) {
    const clonedAgent = requireClonedAgent(clonedAgents, originalAgent);
    for (const originalHandoff of originalAgent.handoffs) {
      clonedAgent.handoffs.push(
        originalHandoff instanceof Handoff
          ? originalHandoff.clone({ agent: requireClonedAgent(clonedAgents, originalHandoff.agent) })
          : requireClonedAgent(clonedAgents, originalHandoff),
      );
    }
  }
  return { startingAgent: requireClonedAgent(clonedAgents, startingAgent), functionTools };
}

/**
 * Returns the name that tool policies, records, and events use for a function tool.
 *
 * @param functionTool - A function tool, possibly inside a `toolNamespace`.
 * @returns `<namespace>.<name>` for a namespaced tool, otherwise the tool name.
 */
export function readQualifiedToolName(functionTool: AnyFunctionTool): string {
  return getFunctionToolDisplayName(functionTool) ?? functionTool.name;
}

function collectAgentGraph(startingAgent: AnyAgent): AnyAgent[] {
  const agents: AnyAgent[] = [];
  const pendingAgents: AnyAgent[] = [startingAgent];
  const seenAgents = new Set<AnyAgent>();
  while (pendingAgents.length > 0) {
    const agent = pendingAgents.shift();
    if (agent === undefined || seenAgents.has(agent)) {
      continue;
    }
    seenAgents.add(agent);
    agents.push(agent);
    for (const handoff of agent.handoffs) {
      pendingAgents.push(handoff instanceof Handoff ? handoff.agent : handoff);
    }
  }
  return agents;
}

// Sandbox and MCP tools appear only at run time, so the adapter cannot gate them.
function requireSupportedAgent(agent: AnyAgent, configuration: AgentGraphConfiguration): void {
  if (agent instanceof SandboxAgent) {
    throw new TypeError(
      `agent ${agent.name} is a SandboxAgent, whose sandbox tools run inside the SDK where Dex cannot ` +
        "give them their own Steps; use an Agent with function tools instead",
    );
  }
  if (agent.mcpServers.length > 0 && !configuration.shouldRunMcpServerToolsInline) {
    throw new TypeError(
      `agent ${agent.name} has MCP servers, whose tools run inside RunModel and repeat when it reruns; ` +
        "set shouldRunMcpServerToolsInline to accept that, or call the server from a function tool",
    );
  }
}

// An agent used as a tool runs its own tools in one call and silently skips their approvals.
function requireOrdinaryFunctionTool(agent: AnyAgent, functionTool: AnyFunctionTool): void {
  if (typeof (functionTool as { on?: unknown }).on === "function") {
    throw new TypeError(
      `agent ${agent.name} uses an agent as tool ${functionTool.name}; its tools would run inside one ` +
        "Dex Step without their own Steps or approvals, so hand off to that agent instead",
    );
  }
}

// Non-function tools are never gated, so only provider-side ones and local reads may stay.
function requireUngatedToolAllowed(
  agent: AnyAgent,
  candidate: Tool<any>,
  configuration: AgentGraphConfiguration,
): void {
  if (candidate.type === "hosted_tool" && getClientToolSearchExecutor(candidate) !== undefined) {
    throw new TypeError(
      `agent ${agent.name} has a client tool search with an execute function, which can return function ` +
        "tools at run time that never get Dex Steps; list those tools on the agent with deferLoading instead",
    );
  }
  const isLocalShell = candidate.type === "shell" && candidate.shell !== undefined;
  if (candidate.type !== "computer" && candidate.type !== "apply_patch" && !isLocalShell) {
    return;
  }
  const policy = resolveConfiguredToolPolicy(candidate.name, configuration);
  if (policy.effect !== "read" || policy.requiresApproval) {
    throw new TypeError(
      `agent ${agent.name} has a local ${candidate.type} tool ${candidate.name}, which runs inside ` +
        "RunModel; give it a read policy without approval or expose it as a function tool",
    );
  }
}

/**
 * Returns the effective policy of one tool from the factory configuration.
 *
 * @param toolName - Qualified tool name.
 * @param configuration - Tool policies and the session default.
 * @returns The resolved policy.
 */
export function resolveConfiguredToolPolicy(
  toolName: string,
  configuration: Pick<AgentGraphConfiguration, "toolPolicies" | "defaultToolEffect">,
): ResolvedToolPolicy {
  return resolveToolPolicy(toolName, configuration.toolPolicies, configuration.defaultToolEffect);
}

function gateFunctionTool(originalTool: AnyFunctionTool, toolName: string): AnyFunctionTool {
  // Input guardrails run before the real call in executeDeferredToolCall, not on the recorded result.
  return cloneFunctionTool(originalTool, {
    needsApproval: approvalRequiredPolicy,
    inputGuardrails: [],
    invoke: async (_runContext, _input, details) => describeRecordedToolCall(toolName, details),
  });
}

function describeRecordedToolCall(toolName: string, details: FunctionToolCallDetails): string {
  const modelStep = runningModelStepScope.getStore();
  if (modelStep === undefined) {
    throw new Error(`tool ${toolName} of a Dex agent session ran outside RunModel`);
  }
  const toolCallId = details?.toolCall?.callId;
  // The SDK auto-approves a later call that reuses an approved call ID; that call never ran.
  if (toolCallId === undefined || !modelStep.unreturnedResumedToolCallIds.delete(toolCallId)) {
    throw new Error(
      `tool ${toolName} call ${String(toolCallId)} was not resumed in this RunModel; a model call that ` +
        "reuses an earlier tool call ID cannot run in a Dex Step",
    );
  }
  const record = modelStep.turn.recordedToolCall(toolCallId);
  if (record === undefined || !isFinalToolCallStatus(record.status)) {
    throw new Error(`tool ${toolName} call ${toolCallId} resumed without a final Dex record`);
  }
  return describeToolCallOutcome(record);
}

function requireClonedAgent(clonedAgents: ReadonlyMap<AnyAgent, AnyAgent>, agent: AnyAgent): AnyAgent {
  const clonedAgent = clonedAgents.get(agent);
  if (clonedAgent === undefined) {
    throw new Error(`agent ${agent.name} is missing from the cloned agent graph`);
  }
  return clonedAgent;
}

// Copies property descriptors, including SDK symbol keys such as tool namespaces.
function cloneFunctionTool(
  originalTool: AnyFunctionTool,
  overrides: Partial<AnyFunctionTool>,
): AnyFunctionTool {
  const descriptors = Object.getOwnPropertyDescriptors(originalTool);
  for (const key of Reflect.ownKeys(overrides)) {
    Reflect.deleteProperty(descriptors, key);
  }
  const clonedTool = Object.create(Object.getPrototypeOf(originalTool), descriptors) as AnyFunctionTool;
  return Object.assign(clonedTool, overrides);
}
