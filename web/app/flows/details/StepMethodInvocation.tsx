// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { usePreferences } from '@/app/providers';
import { formatDate, type TimezonePreference } from '@/lib/format';
import { stepMethodInvocationPhaseLabel, stepMethodPendingReasonLabel } from '@/lib/semantic';
import { formatElapsedDuration } from '@/lib/timeline';
import { asData, type Data, Fields, hasData, isPresent } from './StepMethodSections';

const PHASE_PENDING = 1;

export function StepMethodInvocation({ value, label }: { value: unknown; label: string }) {
  const invocation = asData(value);
  if (!hasData(invocation)) return null;
  return <StepMethodInvocationFields invocation={invocation} label={label} />;
}

function StepMethodInvocationFields({ invocation, label }: { invocation: Data; label: string }) {
  const { timezone } = usePreferences();
  const isPending = invocation.phase === PHASE_PENDING || invocation.phase === 'STEP_METHOD_INVOCATION_PHASE_PENDING';
  return <div className="semantic-subsection">
    <h5>{label}</h5>
    <Fields compact values={[
      ['Phase', stepMethodInvocationPhaseLabel(invocation.phase)],
      ['Pending reason', isPending ? stepMethodPendingReasonLabel(invocation.pendingReason) : undefined],
      ['Attempt', invocation.attempt],
      ['Pending since', isPending ? pendingSinceText(invocation.pendingSinceTime, timezone) : undefined],
      ['Attempt started', timeText(invocation.attemptStartedTime, timezone)],
      ['Backoff ends', timeText(invocation.backoffFiresAtTime, timezone)],
    ]} />
  </div>;
}

function pendingSinceText(value: unknown, timezone: TimezonePreference): string | undefined {
  const since = timeText(value, timezone);
  if (since === undefined) return undefined;
  const waited = formatElapsedDuration(Date.now() - new Date(String(value)).getTime());
  return waited ? `${since} (waiting ${waited})` : since;
}

function timeText(value: unknown, timezone: TimezonePreference): string | undefined {
  return isPresent(value) ? formatDate(String(value), timezone) : undefined;
}
