// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentTurnContext, ResolvedToolPolicy, ToolEffect } from "@superdurable/dex-agents";

import type { TurnConfig } from "./cli-config.js";

/**
 * Returns a tool call's policy: the tool's own effect and approval, tightened by an explicit session policy and
 * by the turn's `approval.always`. Nothing here can remove an approval.
 *
 * @param turn - The `RunModel` context.
 * @param toolName - The tool name as the agent sees it.
 * @param effect - The tool's effect.
 * @param requiresApproval - Whether the tool itself asks for approval.
 * @param turnConfig - The turn's configuration.
 * @returns The policy for `DeferredToolCall.policy`.
 */
export function resolveTurnToolPolicy(
  turn: AgentTurnContext,
  toolName: string,
  effect: ToolEffect,
  requiresApproval: boolean,
  turnConfig: TurnConfig,
): ResolvedToolPolicy {
  const hasExplicitPolicy = turn.toolPolicies !== undefined && Object.hasOwn(turn.toolPolicies, toolName);
  const explicit = hasExplicitPolicy ? turn.resolveToolPolicy(toolName, effect) : undefined;
  return {
    effect: explicit?.effect ?? effect,
    requiresApproval:
      requiresApproval || explicit?.requiresApproval === true || turnConfig.approval.always.includes(toolName),
  };
}
