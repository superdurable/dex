// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ToolEffect } from "../agent-session-values.js";

/**
 * Tool definition `_meta` key that declares the tool's effect class.
 *
 * Its value is `"read"`, `"idempotent_write"`, or `"non_idempotent_write"`. See the package README,
 * "Dex MCP conventions".
 * @since 0.5.0
 */
export const mcpToolEffectMetaKey = "io.superdurable.dex/effect";

/**
 * Tool definition `_meta` key that recommends approval of every call.
 *
 * Its only value is {@link mcpToolApprovalRecommended}. It applies only together with
 * {@link mcpToolEffectMetaKey}.
 * @since 0.5.0
 */
export const mcpToolApprovalMetaKey = "io.superdurable.dex/approval";

/**
 * Tool result `_meta` key, or JSON-RPC `error.data._meta` key, that reports the call's outcome.
 *
 * Its value is {@link mcpToolOutcomeUnknown} or {@link mcpToolOutcomeRejected}.
 * @since 0.5.0
 */
export const mcpToolOutcomeMetaKey = "io.superdurable.dex/outcome";

/**
 * {@link mcpToolApprovalMetaKey} value: a person approves each call by default.
 * @since 0.5.0
 */
export const mcpToolApprovalRecommended = "recommended";

/**
 * {@link mcpToolOutcomeMetaKey} value: the call may or may not have taken effect.
 *
 * The session records `outcome_unknown` and never sends the call again.
 * @since 0.5.0
 */
export const mcpToolOutcomeUnknown = "unknown";

/**
 * {@link mcpToolOutcomeMetaKey} value: the call definitely did not take effect.
 *
 * The session records `failed` with the result's text.
 * @since 0.5.0
 */
export const mcpToolOutcomeRejected = "rejected";

export const unknownOutcomeFallbackMessage = "the MCP server could not tell whether the tool took effect";
export const rejectedOutcomeFallbackMessage = "the MCP server rejected the call, so it did not take effect";

const declarableEffects: ReadonlySet<string> = new Set(["read", "idempotent_write", "non_idempotent_write"]);

/** What a tool definition's Dex `_meta` keys declare; `effect` is `undefined` for a value outside the convention. */
export interface DexToolDeclaration {
  readonly effect: ToolEffect | undefined;
  readonly isApprovalRecommended: boolean;
}

export type DexToolOutcome = "unknown" | "rejected";

/** Returns the Dex declaration of a tool, or `undefined` when it has no effect key. */
export function readDexToolDeclaration(
  meta: Readonly<Record<string, unknown>> | undefined,
  serverName: string,
  mcpToolName: string,
): DexToolDeclaration | undefined {
  if (meta === undefined || !Object.hasOwn(meta, mcpToolEffectMetaKey)) {
    return undefined;
  }
  const effectValue = meta[mcpToolEffectMetaKey];
  if (typeof effectValue !== "string" || !declarableEffects.has(effectValue)) {
    console.warn(
      `MCP tool ${serverName}/${mcpToolName} declares the unknown effect ${JSON.stringify(effectValue)}; it gets the session's default effect`,
    );
    return { effect: undefined, isApprovalRecommended: false };
  }
  const approval = Object.hasOwn(meta, mcpToolApprovalMetaKey) ? meta[mcpToolApprovalMetaKey] : undefined;
  const hasApproval = approval !== undefined && approval !== null;
  if (hasApproval && approval !== mcpToolApprovalRecommended) {
    // Fail closed: an approval value this core does not know still asks a person.
    console.warn(
      `MCP tool ${serverName}/${mcpToolName} declares the unknown approval ${JSON.stringify(approval)}; its calls wait for approval`,
    );
  }
  return { effect: effectValue as ToolEffect, isApprovalRecommended: hasApproval };
}

/** Returns the outcome a result's or an error's `_meta` reports, or `undefined`. */
export function readDexToolOutcome(meta: unknown): DexToolOutcome | undefined {
  if (!isRecord(meta) || !Object.hasOwn(meta, mcpToolOutcomeMetaKey)) {
    return undefined;
  }
  const outcome = meta[mcpToolOutcomeMetaKey];
  if (outcome === mcpToolOutcomeUnknown || outcome === mcpToolOutcomeRejected) {
    return outcome;
  }
  console.warn(`ignored the unknown MCP tool outcome ${JSON.stringify(outcome)}`);
  return undefined;
}

/** Returns the outcome a JSON-RPC error's `data._meta` reports, or `undefined`. */
export function readDexErrorOutcome(errorData: unknown): DexToolOutcome | undefined {
  return isRecord(errorData) ? readDexToolOutcome(errorData["_meta"]) : undefined;
}

function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
