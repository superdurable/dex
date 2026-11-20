// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

const plaintextDefinitions = new WeakSet<object>();

export function markPlaintextAttribute<Definition extends object>(definition: Definition): Definition {
  plaintextDefinitions.add(definition);
  return definition;
}

export function isPlaintextAttributeDefinition(definition: object): boolean {
  return plaintextDefinitions.has(definition);
}

/** Keeps the Plaintext declaration when a definition option returns a new definition. */
export function carryPlaintextAttribute<Definition extends object>(source: object, copy: Definition): Definition {
  return isPlaintextAttributeDefinition(source) ? markPlaintextAttribute(copy) : copy;
}
