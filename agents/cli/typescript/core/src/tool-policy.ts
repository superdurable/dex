// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentTurnContext, ResolvedToolPolicy, ToolEffect } from "@superdurable/dex-agents";

import type { TurnConfig } from "./cli-config.js";

/** Effects from the least to the most conservative. */
const effectsBySafety: readonly ToolEffect[] = ["read", "idempotent_write", "non_idempotent_write", "unclassified"];

/**
 * Returns a tool call's policy from the tool's own effect and approval, an explicit session policy, and the
 * turn's `approval.always`.
 *
 * The tool's effect is a floor: an explicit `ToolPolicy` for the tool may raise it but never lowers it, so
 * the call gets the more conservative effect, ranked `read`, `idempotent_write`, `non_idempotent_write`, then
 * `unclassified`, as `McpToolSource` does for declared effects. Approval is a floor too: the call waits when the
 * tool asks for approval, when the explicit policy sets `requiresApproval: true`, when the policy leaves
 * `requiresApproval` out and the combined effect needs approval by default (`non_idempotent_write` or
 * `unclassified`), or when the turn's `approval.always` lists the tool. An explicit `requiresApproval: false`
 * cannot turn a tool's own approval off.
 *
 * A harness that replaces local tool groups uses it for its own tools, with the local tool's effect and
 * approval, so its policy matches the CLI core's local tools.
 *
 * @example
 * ```ts
 * const turn = context.currentTurn();
 * const policy = resolveTurnToolPolicy(turnContext, "Bash", "non_idempotent_write",
 *   isShellCommandApprovalRequired(turn, command), turn);
 * ```
 * @param turn - The `RunModel` context.
 * @param toolName - The tool name as the agent sees it.
 * @param effect - The tool's effect.
 * @param requiresApproval - Whether the tool itself asks for approval.
 * @param turnConfig - The turn's configuration.
 * @returns The policy for `DeferredToolCall.policy`.
 * @since 0.5.0
 */
export function resolveTurnToolPolicy(
  turn: AgentTurnContext,
  toolName: string,
  effect: ToolEffect,
  requiresApproval: boolean,
  turnConfig: TurnConfig,
): ResolvedToolPolicy {
  const explicit =
    turn.toolPolicies !== undefined && Object.hasOwn(turn.toolPolicies, toolName) ? turn.toolPolicies[toolName] : undefined;
  if (explicit === undefined) {
    return { effect, requiresApproval: requiresApproval || turnConfig.approval.always.includes(toolName) };
  }
  const combinedEffect =
    effectsBySafety.indexOf(explicit.effect) > effectsBySafety.indexOf(effect) ? explicit.effect : effect;
  const isApprovalRequiredByEffect =
    (explicit.requiresApproval === undefined || explicit.requiresApproval === null) &&
    (combinedEffect === "non_idempotent_write" || combinedEffect === "unclassified");
  const isApprovalRequired = requiresApproval || explicit.requiresApproval === true || isApprovalRequiredByEffect;
  return {
    effect: combinedEffect,
    requiresApproval: isApprovalRequired || turnConfig.approval.always.includes(toolName),
  };
}
