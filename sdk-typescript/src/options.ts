// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Attribute, AttributeMap } from "./persistence.js";
import type { RetryPolicy, StepDurability } from "./step.js";
import { requireName } from "./validation.js";

export interface ClientOptions {
  readonly serverAddress?: string;
  readonly workerTarget?: WorkerTarget;
}

export const ActiveStepSearchMode = Object.freeze({
  DEFAULT: "default",
  ALL: "all",
} as const);

export type ActiveStepSearchMode =
  (typeof ActiveStepSearchMode)[keyof typeof ActiveStepSearchMode];

export const IdReusePolicy = Object.freeze({
  DEFAULT: "default",
  ALLOW_IF_PREVIOUS_FAILED: "allowIfPreviousFailed",
  ALLOW_IF_NOT_RUNNING: "allowIfNotRunning",
  ALLOW_TERMINATE_IF_RUNNING: "allowTerminateIfRunning",
  DISALLOW: "disallow",
} as const);

export type IdReusePolicy = (typeof IdReusePolicy)[keyof typeof IdReusePolicy];

export interface FlowConfig {
  readonly activeStepSearchMode?: ActiveStepSearchMode;
  readonly continueAsNewThreshold?: number;
  readonly continueAsNewPageSizeBytes?: number;
  readonly stepDurability?: StepDurability;
  readonly workerTarget?: WorkerTarget;
}

export interface InitialAttribute<T> {
  readonly attribute: Attribute<T> | AttributeMap<T>;
  readonly instance?: string;
  readonly value: T;
}

export const InitialAttribute = Object.freeze({
  of<T>(attribute: Attribute<T>, value: T): InitialAttribute<T> {
    return { attribute, value };
  },
  mapValue<T>(
    attribute: AttributeMap<T>,
    instance: string,
    value: T,
  ): InitialAttribute<T> {
    requireName(instance);
    return { attribute, instance, value };
  },
});

export interface StartFlowOptions {
  readonly timeoutMs?: number;
  readonly startDelayMs?: number;
  readonly idReusePolicy?: IdReusePolicy;
  readonly cronSchedule?: string;
  readonly retryPolicy?: RetryPolicy;
  readonly attributes?: readonly InitialAttribute<any>[];
  readonly configOverride?: FlowConfig;
  readonly ignoreAlreadyStarted?: boolean;
  readonly requestId?: string;
}

export type FlowStatus =
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "terminated"
  | "timedOut"
  | "continuedAsNew";

export interface FlowInfo {
  readonly flowId: string;
  readonly runId: string;
  readonly flowType: string;
  readonly status: FlowStatus;
  readonly startedAt: Date;
}

export interface SearchFlowEntry {
  readonly flowId: string;
  readonly runId: string;
  readonly flowType: string;
  readonly status: FlowStatus;
  readonly startedAt: Date;
  readonly closedAt: Date | undefined;
  readonly indexedAttributes: ReadonlyMap<string, unknown>;
}

export interface SearchFlowsPage {
  readonly flows: readonly SearchFlowEntry[];
  readonly nextPageToken: string;
}

export interface StepExecutionId {
  readonly stepType: string;
  readonly number?: number;
}

export const StepExecutionId = Object.freeze({
  of(stepType: string, number?: number): StepExecutionId {
    return { stepType, number: number ?? 1 };
  },
});

export interface TimerId {
  readonly conditionId?: string;
  readonly conditionIndex?: number;
}

export const TimerId = Object.freeze({
  byConditionId(conditionId: string): TimerId {
    requireName(conditionId);
    return { conditionId };
  },
  byConditionIndex(conditionIndex: number): TimerId {
    return { conditionIndex };
  },
});

export const ResetType = Object.freeze({
  BEGINNING: "beginning",
  HISTORY_EVENT_ID: "historyEventId",
  HISTORY_EVENT_TIME: "historyEventTime",
  STEP_TYPE: "stepType",
  STEP_EXECUTION_ID: "stepExecutionId",
} as const);

export type ResetType = (typeof ResetType)[keyof typeof ResetType];

export interface ResetFlowOptions {
  readonly type: ResetType;
  readonly historyEventId?: bigint;
  readonly historyEventTime?: Date;
  readonly stepType?: string;
  readonly stepExecutionId?: string;
  readonly reason?: string;
  readonly skipChannelMessagesReapply?: boolean;
  readonly skipLockingRpcReapply?: boolean;
}

export const StopType = Object.freeze({
  CANCEL: "cancel",
  TERMINATE: "terminate",
  FAIL: "fail",
} as const);

export type StopType = (typeof StopType)[keyof typeof StopType];

export interface StopFlowOptions {
  readonly type?: StopType;
  readonly reason?: string;
}

export interface WorkerOptions {
  readonly bindAddress?: string;
  readonly workerTarget?: WorkerTarget;
  readonly serverAddress?: string;
  /** Startup Indexed Attribute synchronization deadline in milliseconds. Defaults to 120000. */
  readonly attributeIndexSyncTimeoutMs?: number;
}

export interface WorkerTarget {
  readonly address: string;
  readonly headless?: boolean;
}
