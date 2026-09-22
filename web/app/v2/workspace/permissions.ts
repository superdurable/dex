// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';

/** Permissions declared by a Flow's Actions, sorted for a stable picker. */
export function permissionsOf(definition: FlowV2Definition | undefined): string[] {
  const seen: string[] = [];
  for (const action of definition?.actions ?? []) {
    const permission = action.requiredPermission;
    if (permission === '' || seen.includes(permission)) continue;
    seen.push(permission);
  }
  return seen.sort();
}

/** Action labels associated with one permission. */
export function permissionActionLabels(
  definition: FlowV2Definition | undefined,
  permission: string,
): string[] {
  return (definition?.actions ?? [])
    .filter((action) => action.requiredPermission === permission)
    .map((action) => action.label);
}
