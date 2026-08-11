// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export const FLOW_STATUS = {
  0: 'Unspecified',
  1: 'Running',
  2: 'Completed',
  3: 'Failed',
  4: 'Timed out',
  5: 'Terminated',
  6: 'Canceled',
  7: 'Continued as new',
} as const;

export type FlowStatus = (typeof FLOW_STATUS)[keyof typeof FLOW_STATUS];

export interface KeyValue {
  key: string;
  value: unknown;
}

export interface FlowExecution {
  flowId: string;
  runId: string;
  flowType: string;
  flowStatus: FlowStatus;
  flowStatusCode: number;
  startTime: string | null;
  closeTime: string | null;
  searchAttributes: KeyValue[];
}

export interface SearchFlowsResult {
  flows: FlowExecution[];
  nextPageToken: string;
}

export interface FlowSummary {
  flowId: string;
  runId: string;
  firstRunId: string;
  requestId: string;
  flowType: string;
  flowStatus: FlowStatus;
  flowStatusCode: number;
  startTime: string | null;
  closeTime: string | null;
}

export type HistoryEventType =
  | 'FlowStartedOrContinued'
  | 'FlowClosed'
  | 'StepWaitForCompleted'
  | 'StepWaitForFailed'
  | 'StepExecuteCompleted'
  | 'StepExecuteFailed'
  | 'RpcExecutionCompleted'
  | 'ChannelExternalPublish';

export interface FlowHistoryEvent {
  eventId: number;
  eventTime: string | null;
  type: HistoryEventType;
  payload: Record<string, unknown>;
}

export interface HistoryPage {
  events: FlowHistoryEvent[];
  nextPageToken: string;
  nextInternalEventId: number;
}

export interface ActiveStepExecution {
  stepExecutionId: string;
  fromStepExecutionId: string;
  stepType: string;
  phase: 'Active' | 'Waiting' | 'Unspecified';
  movement?: Record<string, unknown>;
  waitingCondition?: Record<string, unknown>;
  completedConditions?: Record<string, unknown>;
  stepExecutionLocals: KeyValue[];
  timers: Record<string, unknown>[];
  lastFailureInfo?: Record<string, unknown>;
}

export interface FlowState {
  flowConfig: Record<string, unknown>;
  attributes: KeyValue[];
  activeStepExecutions: ActiveStepExecution[];
  queuedSteps: Record<string, unknown>[];
  pendingChannelMessages: Record<string, unknown>;
  completedSteps: Record<string, unknown>[];
}

export interface StepGraphNode {
  id: string;
  label: string;
  kind: 'source' | 'step' | 'terminal';
  status: 'Source' | 'Active' | 'Waiting' | 'Completed' | 'Failed' | 'Terminal';
  previousRunId?: string;
  stepType?: string;
  fromStepExecutionId?: string;
  waitFor?: FlowHistoryEvent;
  execute?: FlowHistoryEvent;
  active?: ActiveStepExecution;
  transient?: boolean;
}

export interface StepGraphEdge {
  id: string;
  source: string;
  target: string;
}
