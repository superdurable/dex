// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { V2Catalog } from '@/lib/types';

export interface StartWorkerTargetOptions {
  isDefaultPull: boolean;
  isAddressWorkerTargetSupported: boolean;
}

/** server-zd dispatches only to pull Workers, so the Start dialog offers no address there. */
export function startWorkerTargetOptions(
  catalog: Pick<V2Catalog, 'startFlowWorkerTargetPull' | 'serverEngine'>,
): StartWorkerTargetOptions {
  const isPullOnly = catalog.serverEngine === 'zd';
  return {
    isDefaultPull: catalog.startFlowWorkerTargetPull === true || isPullOnly,
    isAddressWorkerTargetSupported: !isPullOnly,
  };
}
