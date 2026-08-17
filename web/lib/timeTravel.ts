// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent } from './types';

export const TIME_TRAVEL_TYPE = {
  BEGINNING: 1,
  HISTORY_EVENT_TIME: 2,
  STEP_TYPE: 3,
  STEP_EXECUTION_ID: 4,
} as const;

export const TIME_TRAVEL_STEP_METHOD = {
  WAIT_FOR: 1,
  EXECUTE: 2,
} as const;

export interface EventTimeTravelTarget {
  stepExecutionId: string;
  stepMethod: number;
}

export function eventTimeTravelTarget(event: FlowHistoryEvent | null): EventTimeTravelTarget | null {
  if (!event) return null;
  const context = event.payload.context as Record<string, unknown> | undefined;
  const stepExecutionId = context?.stepExecutionId;
  if (typeof stepExecutionId !== 'string' || !stepExecutionId) return null;
  if (event.type.startsWith('StepWaitFor')) {
    return { stepExecutionId, stepMethod: TIME_TRAVEL_STEP_METHOD.WAIT_FOR };
  }
  if (event.type.startsWith('StepExecute')) {
    return { stepExecutionId, stepMethod: TIME_TRAVEL_STEP_METHOD.EXECUTE };
  }
  return null;
}
