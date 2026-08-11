// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { AttributeSyncConfig } from "./gen/dex.js";
import type { FlowConfig } from "./options.js";

const syncedDefinitions = new WeakSet<object>();

export function markAttributeStoreSynced<Definition extends object>(
  definition: Definition,
): Definition {
  syncedDefinitions.add(definition);
  return definition;
}

export function mapAttributeStoreSync(
  definition: object,
): AttributeSyncConfig | undefined {
  return syncedDefinitions.has(definition) ? { enabled: true } : undefined;
}

export function mapAttributeStoreName(config: FlowConfig | undefined): string | undefined {
  return config?.attributeStoreName;
}
