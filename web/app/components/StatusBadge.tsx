// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowStatus } from '@/lib/types';

export function StatusBadge({ status }: { status: FlowStatus | string }) {
  const tone = status.toLowerCase().replaceAll(' ', '-');
  return <span className={`status-badge status-${tone}`}>{status || 'Unknown'}</span>;
}
