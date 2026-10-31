// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ToolsInput } from "@mastra/core/agent";
import { isProviderDefinedTool } from "@mastra/core/tools";
import {
  toolEffectFromMcpToolAnnotations,
  type McpToolAnnotations,
  type ResolvedToolPolicy,
  type ToolEffect,
  type ToolPolicy,
} from "@superdurable/dex-agents";

/** Mastra renames other keys for the model, which would break lookups by name. */
const mastraToolNamePattern = /^[a-zA-Z_][a-zA-Z0-9_-]{0,62}$/;

export type ResolveToolPolicy = (toolName: string, frameworkEffect?: ToolEffect) => ResolvedToolPolicy;

export interface MastraModelStepToolPlan {
  /** Mastra strips `execute` from client tools, so the model call ends at their calls. */
  readonly clientTools: ToolsInput;
  readonly activeToolNames: readonly string[];
  readonly clientToolPolicies: ReadonlyMap<string, ResolvedToolPolicy>;
  readonly mastraRunToolNames: ReadonlySet<string>;
}

/** Every agent tool runs in a session Step; tools Mastra runs itself need an explicit inline `read` policy. */
export function planMastraModelStepTools(
  agentTools: ToolsInput,
  toolPolicies: Readonly<Record<string, ToolPolicy>>,
  resolveToolPolicy: ResolveToolPolicy,
  shouldTrustMcpServerToolAnnotations: boolean,
): MastraModelStepToolPlan {
  const clientTools: ToolsInput = {};
  const activeToolNames: string[] = [];
  const clientToolPolicies = new Map<string, ResolvedToolPolicy>();
  const mastraRunToolNames = new Set<string>();
  const isAllowedToRunInsideModelStep = (toolName: string): boolean => {
    const policy = resolveToolPolicy(toolName);
    return Object.hasOwn(toolPolicies, toolName) && policy.effect === "read" && !policy.requiresApproval;
  };
  for (const [toolName, tool] of Object.entries(agentTools)) {
    if (!mastraToolNamePattern.test(toolName)) {
      throw new TypeError(
        `Mastra tool name "${toolName}" must match ${mastraToolNamePattern.source}; ` +
          "rename its key in the agent's tools",
      );
    }
    // A provider runs these itself, so the session cannot run them in a Step.
    if (isProviderDefinedTool(tool)) {
      if (isAllowedToRunInsideModelStep(toolName)) {
        activeToolNames.push(toolName);
        mastraRunToolNames.add(toolName);
      }
      continue;
    }
    clientTools[toolName] = tool;
    activeToolNames.push(toolName);
    clientToolPolicies.set(
      toolName,
      resolveMastraToolPolicy(toolName, tool, resolveToolPolicy, shouldTrustMcpServerToolAnnotations),
    );
  }
  for (const toolName of Object.keys(toolPolicies)) {
    if (!Object.hasOwn(agentTools, toolName) && isAllowedToRunInsideModelStep(toolName)) {
      activeToolNames.push(toolName);
      mastraRunToolNames.add(toolName);
    }
  }
  return { clientTools, activeToolNames, clientToolPolicies, mastraRunToolNames };
}

function resolveMastraToolPolicy(
  toolName: string,
  tool: unknown,
  resolveToolPolicy: ResolveToolPolicy,
  shouldTrustMcpServerToolAnnotations: boolean,
): ResolvedToolPolicy {
  const policy = resolveToolPolicy(toolName, readMastraToolEffect(tool, shouldTrustMcpServerToolAnnotations));
  return !policy.requiresApproval && hasMastraToolApproval(tool) ? { ...policy, requiresApproval: true } : policy;
}

function readMastraToolEffect(tool: unknown, shouldTrustMcpServerToolAnnotations: boolean): ToolEffect | undefined {
  if (!isRecord(tool)) {
    return undefined;
  }
  const isFromMcpServer = tool["mcpMetadata"] !== undefined && tool["mcpMetadata"] !== null;
  if (isFromMcpServer && !shouldTrustMcpServerToolAnnotations) {
    return undefined;
  }
  const mcpProperties = tool["mcp"];
  if (!isRecord(mcpProperties) || !isRecord(mcpProperties["annotations"])) {
    return undefined;
  }
  return toolEffectFromMcpToolAnnotations(mcpProperties["annotations"] as McpToolAnnotations);
}

// The application asked Mastra to gate the tool, so the session keeps that approval.
function hasMastraToolApproval(tool: unknown): boolean {
  if (!isRecord(tool)) {
    return false;
  }
  return (
    Boolean(tool["requireApproval"]) ||
    typeof tool["needsApprovalFn"] === "function" ||
    Boolean(tool["needsApproval"])
  );
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}
