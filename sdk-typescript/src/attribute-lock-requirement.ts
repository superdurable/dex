// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Attribute } from "./persistence.js";

/** A declared write or publish lock: the written key's own lock, or one singleton Attribute's lock. */
export type LockRequirement =
  | { readonly kind: "ownKey" }
  | { readonly kind: "attribute"; readonly lockAttribute: Attribute<unknown> };

export const ownKeyLockRequirement: LockRequirement = Object.freeze({ kind: "ownKey" });

const lockRequirements = new WeakMap<object, LockRequirement>();

export function markLockRequirement<Definition extends object>(
  definition: Definition,
  requirement: LockRequirement,
): Definition {
  lockRequirements.set(definition, requirement);
  return definition;
}

export function lockRequirementOf(definition: object): LockRequirement | undefined {
  return lockRequirements.get(definition);
}

/** Keeps the lock requirement when a definition option returns a new definition. */
export function carryLockRequirement<Definition extends object>(source: object, copy: Definition): Definition {
  const requirement = lockRequirements.get(source);
  return requirement === undefined ? copy : markLockRequirement(copy, requirement);
}
