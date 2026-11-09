// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowHistoryEvent, FlowState } from '@/lib/types';
import { asData, Fields } from './StepMethodSections';

export interface FlowRoutingData {
  workerTarget: Record<string, unknown>;
  stickyAppWorkerId?: string;
}

export function flowRoutingData(state: FlowState | null, events: FlowHistoryEvent[]): FlowRoutingData {
  const start = events.find((event) => event.type === 'FlowStartedOrContinued');
  const config = asData(state?.flowConfig ?? start?.payload.flowConfig);
  const closed = events.findLast((event) => event.type === 'FlowClosed');
  const lastServed = events.findLast((event) =>
    event.type.endsWith('Completed') && event.type.startsWith('Step')
    && typeof asData(asData(event.payload.context).pullRouting).servedByAppWorkerId === 'string');
  const stickyWorkerID = state?.stickyAppWorkerId
    ?? closed?.payload.stickyAppWorkerId
    ?? asData(asData(lastServed?.payload.context).pullRouting).servedByAppWorkerId
    ?? asData(start?.payload.continuedStart).stickyAppWorkerId;
  return {
    workerTarget: asData(config.workerTarget),
    stickyAppWorkerId: typeof stickyWorkerID === 'string' ? stickyWorkerID : undefined,
  };
}

export function FlowRouting({ routing, flowType, showFlowType = false }: {
  routing: FlowRoutingData;
  flowType: string;
  showFlowType?: boolean;
}) {
  return <Fields compact values={[
    ...(showFlowType ? [['Flow type', flowType]] as [string, unknown][] : []),
    ['Task queue', flowType],
    ['Sticky Worker ID', routing.stickyAppWorkerId || '—'],
  ]} />;
}

export { PullRouting } from './StepMethodSections';
