// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionCatalog, FlowDefinitionEntry, V2Catalog, V2CatalogEntry } from '@/lib/types';
import { agentSessionSchema, type AgentSessionSchema } from '../agents/sessionSchema';

/**
 * The definition the Design tab draws for a Flow type, or null when no loaded file matches.
 *
 * A valid file wins over one with blocking diagnostics, and schema 2.0 wins over 1.0.
 */
export function designDefinitionFor(
  definitions: FlowDefinitionCatalog | null | undefined,
  flowType: string | undefined,
): FlowDefinitionEntry | null {
  if (!definitions?.configured || !flowType) return null;
  const matches = definitions.definitions.filter((definition) => definition.flowName === flowType);
  const rank = (definition: FlowDefinitionEntry) => (definition.valid ? 2 : 0) + (definition.schemaVersion === '2.0' ? 1 : 0);
  return [...matches].sort((left, right) => rank(right) - rank(left))[0] ?? null;
}

/** The operator catalog entry for a Flow type, when the operator API serves that Flow type. */
export function catalogEntryFor(
  catalog: V2Catalog | null | undefined,
  canUseOperatorAPI: boolean,
  flowType: string | undefined,
): V2CatalogEntry | null {
  if (!canUseOperatorAPI || !flowType) return null;
  return catalog?.flows.find((entry) => entry.flowType === flowType) ?? null;
}

/** The Dex Agents session schema of a Flow type, read by the same rule as the Agent session view. */
export function sessionSchemaFor(
  definitions: FlowDefinitionCatalog | null | undefined,
  entry: V2CatalogEntry | null,
): AgentSessionSchema | null {
  if (!entry) return null;
  const graph = definitions?.definitions.find((candidate) => (
    candidate.flowName === entry.flowType && candidate.schemaVersion === '2.0' && candidate.valid
  ))?.graph;
  return agentSessionSchema(graph, entry.definition);
}

/** A run has something to act on when its definition declares Actions or Display fields. */
export function hasRunActions(entry: V2CatalogEntry | null): boolean {
  if (!entry) return false;
  return entry.definition.actions.length > 0 || entry.definition.display.fields.length > 0;
}

/** Work Queue needs the operator API and at least one Flow type with a valid 2.0 definition. */
export function isWorkQueueApplicable(catalog: V2Catalog | null | undefined, canUseOperatorAPI: boolean): boolean {
  return canUseOperatorAPI && (catalog?.flows.length ?? 0) > 0;
}
