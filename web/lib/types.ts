// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';

export type {
  FlowDefinitionDiagnostic,
  FlowDefinitionEdge,
  FlowDefinitionGraph,
  FlowDefinitionNode,
  SourceSpan,
} from '@superdurable/flow-definition-renderer';

export const FLOW_STATUS = {
  0: 'Unspecified',
  1: 'Running',
  2: 'Completed',
  3: 'Failed',
  4: 'Timed out',
  5: 'Terminated',
  6: 'Canceled',
} as const;

export type FlowStatus = (typeof FLOW_STATUS)[keyof typeof FLOW_STATUS];

/** The ExecutionStatus value each status needs in a visibility query; display labels are rejected there. */
export const FLOW_STATUS_QUERY_VALUE: Record<Exclude<FlowStatus, 'Unspecified'>, string> = {
  Running: 'Running',
  Completed: 'Completed',
  Failed: 'Failed',
  'Timed out': 'TimedOut',
  Terminated: 'Terminated',
  Canceled: 'Canceled',
};

/** The engine behind the Dex Server, as reported by Dex Web. */
export type ServerEngine = 'unspecified' | 'temporal' | 'cadence' | 'zd';

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
  indexedAttributes: KeyValue[];
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
  /** The current run attempt. Zero or absent on Servers without run attempts. */
  runAttempt?: number;
}

export type HistoryEventType =
  | 'FlowStartedOrContinued'
  | 'FlowClosed'
  | 'StepWaitForCompleted'
  | 'StepWaitForFailed'
  | 'StepWaitForPending'
  | 'StepExecuteCompleted'
  | 'StepExecuteFailed'
  | 'StepExecutePending'
  | 'RpcExecutionCompleted'
  | 'ChannelExternalPublish'
  | 'ChannelExternalDelete'
  | 'TimeTravelFork';

export interface FlowHistoryEvent {
  eventId: number;
  eventTime: string | null;
  type: HistoryEventType;
  payload: Record<string, unknown>;
  /** The run attempt that recorded this event. Zero or absent on Servers without run attempts. */
  runAttempt?: number;
}

export interface HistoryPage {
  events: FlowHistoryEvent[];
  nextPageToken: string;
  nextInternalEventId: number;
}

export interface StreamMessage {
  value: unknown;
  resumeToken: string;
  createdTime: string | null;
  source: string;
}

export interface ActiveStepExecution {
  waitForPullRouting?: Record<string, unknown>;
  executePullRouting?: Record<string, unknown>;
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
  /** The current WaitFor invocation. Absent while Execute runs or on older Servers. */
  waitForInvocation?: Record<string, unknown>;
  /** The current Execute invocation. Absent while WaitFor runs or on older Servers. */
  executeInvocation?: Record<string, unknown>;
}

export interface FlowState {
  stickyAppWorkerId?: string;
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
  kind: 'source' | 'step' | 'subflow';
  status: 'Source' | 'Active' | 'Waiting' | 'Pending' | 'Completed' | 'Failed' | 'Canceled';
  stepType?: string;
  fromStepExecutionId?: string;
  movement?: Record<string, unknown>;
  isPlanned?: boolean;
  waitFor?: FlowHistoryEvent;
  execute?: FlowHistoryEvent;
  pendingWaitFor?: FlowHistoryEvent;
  pendingExecute?: FlowHistoryEvent;
  active?: ActiveStepExecution;
  parentStepId?: string;
  flowId?: string;
  subFlowStatus?: string;
  reusePolicy?: string;
}

export interface StepGraphEdge {
  id: string;
  source: string;
  target: string;
}

/** Where Dex Web read a definition: a directory file, a Worker upload, or dexcli visualize. */
export type FlowDefinitionSource = 'directory' | 'uploaded' | 'visualize';

/** One definition without its graph, as definition listings return it. */
export interface FlowDefinitionSummary {
  id: string;
  file: string;
  /** Empty when Dex Web rejected the definition before reading its version. */
  schemaVersion: '1.0' | '2.0' | '';
  flowName: string;
  sourceLanguage: string;
  sourcePath: string;
  valid: boolean;
  source: FlowDefinitionSource;
  digest?: string;
  generatedAt?: string;
  uploadedAt?: string;
  /** Why Dex Web rejected this definition; a rejected definition has no graph. */
  error?: string;
}

/** A definition Dex Web accepted, with the graph to draw. */
export interface FlowDefinitionEntry extends FlowDefinitionSummary {
  schemaVersion: '1.0' | '2.0';
  graph: FlowDefinitionGraph;
}

/** The Dex Agents session parts of a graph, read by Dex Web's server. */
export interface V2AgentSessionInfo {
  language: string;
  hasQuestions: boolean;
  rpcNames: string[];
}

export interface V2CatalogEntry {
  flowType: string;
  definition: FlowV2Definition;
  /** Sent with every request about this Flow type, so a changed definition is noticed. */
  definitionRevision?: string;
  agentSession?: V2AgentSessionInfo;
}

/** Every Flow type with a definition, without reading any definition. */
export interface FlowDefinitionTypeList {
  configured: boolean;
  operatorAPI: boolean;
  flowTypes: { flowType: string; source: FlowDefinitionSource }[];
  sourceErrors?: string[];
}

/** Every definition of one Flow type, with graphs, and its v2 contract when it has a valid one. */
export interface FlowTypeDefinitions {
  flowType: string;
  definitionRevision: string;
  definitions: (FlowDefinitionSummary & { graph?: FlowDefinitionGraph })[];
  v2: V2CatalogEntry | null;
}

export interface V2Catalog {
  enabled: boolean;
  flows: V2CatalogEntry[];
  definitionRevision: string;
  serverEngine?: ServerEngine;
}

export interface V2Flow {
  flowId: string;
  flowType: string;
  flowStatus: FlowStatus;
  flowStatusCode: number;
  startTime: string | null;
  closeTime: string | null;
  indexedAttributes: Record<string, unknown>;
  summary?: Record<string, unknown>;
  summaryError?: string;
}

export interface V2SearchResult {
  flows: V2Flow[];
  nextPageToken: string;
}

export interface V2Display {
  flowId: string;
  flowType: string;
  flowStatus: FlowStatus;
  flowStatusCode: number;
  isActive: boolean;
  display: Record<string, unknown>;
  attributeSnapshot: Record<string, unknown>;
  eligibleActions: string[];
}

export interface FlowDefinitionCatalog {
  configured: boolean;
  directory?: string;
  source?: string;
  definitionRevision?: string;
  definitionCount: number;
  definitions: FlowDefinitionSummary[];
  /** A source that failed as a whole, such as an unreachable Dex Server. */
  sourceErrors?: string[];
}
