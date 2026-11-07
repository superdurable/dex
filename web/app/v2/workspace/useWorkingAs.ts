// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { permissionsAcross } from './permissions';

/**
 * The "Working as" choice, kept to what the Flow types being searched declare.
 *
 * Derived on render as well as reset, so the search that follows a scope change never sends a
 * permission the control no longer shows.
 */
export function useWorkingAs(definitions: readonly FlowV2Definition[]) {
  const [chosen, setPermission] = useState('');
  const permissionOptions = permissionsAcross(definitions);
  const permission = permissionOptions.includes(chosen) ? chosen : '';

  useEffect(() => {
    if (permission !== chosen) setPermission('');
  }, [chosen, permission]);

  const workQueuePermissions = useMemo(() => (permission === '' ? [] : [permission]), [permission]);
  return { permission, permissionOptions, setPermission, workQueuePermissions };
}
