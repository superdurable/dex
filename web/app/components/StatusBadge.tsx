// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Activity,
  Ban,
  CircleArrowRight,
  CircleCheck,
  CircleHelp,
  CircleX,
  OctagonX,
  TimerOff,
  type LucideIcon,
} from 'lucide-react';
import type { FlowStatus } from '@/lib/types';

const statusIcons: Record<string, LucideIcon> = {
  running: Activity,
  completed: CircleCheck,
  failed: CircleX,
  'timed-out': TimerOff,
  timedout: TimerOff,
  terminated: OctagonX,
  canceled: Ban,
};

/** Shows a Flow status as text with a matching icon, so color is never the only signal. */
export function StatusBadge({ status, size = 'default' }: { status: FlowStatus | string; size?: 'default' | 'large' }) {
  const tone = status.toLowerCase().replaceAll(' ', '-');
  const Icon = statusIcons[tone] ?? CircleHelp;
  return (
    <span className={`status-badge status-${tone}${size === 'large' ? ' status-badge--large' : ''}`}>
      <Icon aria-hidden="true" size={size === 'large' ? 16 : 13} strokeWidth={2.25} />
      {status || 'Unknown'}
    </span>
  );
}
