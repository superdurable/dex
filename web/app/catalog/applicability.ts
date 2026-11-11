// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionEntry, FlowDefinitionTypeList, FlowTypeDefinitions, V2CatalogEntry } from '@/lib/types';
import { agentSessionSchema, type AgentSessionSchema } from '../agents/sessionSchema';
import { drawableDefinitions } from './flowTypeDefinitionCache';

/**
 * The definition the Design tab draws for a Flow type, or null when no accepted definition matches.
 *
 * A valid file wins over one with blocking diagnostics, and schema 2.0 wins over 1.0.
 */
export function designDefinitionFor(
  definitions: FlowTypeDefinitions | null | undefined,
  flowType: string | undefined,
): FlowDefinitionEntry | null {
  if (!flowType) return null;
  const matches = drawableDefinitions(definitions).filter((definition) => definition.flowName === flowType);
  const rank = (definition: FlowDefinitionEntry) => (definition.valid ? 2 : 0) + (definition.schemaVersion === '2.0' ? 1 : 0);
  return [...matches].sort((left, right) => rank(right) - rank(left))[0] ?? null;
}

/** The operator catalog entry for a Flow type, when the operator API serves that Flow type. */
export function catalogEntryFor(
  definitions: FlowTypeDefinitions | null | undefined,
  canUseOperatorAPI: boolean,
): V2CatalogEntry | null {
  if (!canUseOperatorAPI) return null;
  return definitions?.v2 ?? null;
}

/** The Dex Agents session schema of a Flow type, read by the same rule as the Agent session view. */
export function sessionSchemaFor(entry: V2CatalogEntry | null): AgentSessionSchema | null {
  return agentSessionSchema(entry);
}

/** A run has something to act on when its definition declares Actions or Display fields. */
export function hasRunActions(entry: V2CatalogEntry | null): boolean {
  if (!entry) return false;
  return entry.definition.actions.length > 0 || entry.definition.display.fields.length > 0;
}

/**
 * Work Queue needs the operator API and at least one Flow type with a definition.
 *
 * The Flow type list reads no definition, so the page itself says when none has a valid 2.0 contract.
 */
export function isWorkQueueApplicable(flowTypes: FlowDefinitionTypeList | null | undefined, canUseOperatorAPI: boolean): boolean {
  return canUseOperatorAPI && (flowTypes?.flowTypes.length ?? 0) > 0;
}
