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

/** Configures FlowService connectivity and default Flow routing. */
export interface ClientOptions {
  /** Plaintext Dex gRPC target; defaults to `localhost:8801`. */
  readonly serverAddress?: string;
  /** Worker endpoint advertised by `startFlow` unless a Flow overrides it. */
  readonly workerTarget?: WorkerTarget;
}

/** Controls which active Steps are included in Flow search indexing. */
export const ActiveStepSearchMode = Object.freeze({
  /** Uses the Dex server's current default policy. */
  DEFAULT: "default",
  /** Indexes every active Step. */
  ALL: "all",
} as const);

/** Represents a value from {@link ActiveStepSearchMode}. */
export type ActiveStepSearchMode =
  (typeof ActiveStepSearchMode)[keyof typeof ActiveStepSearchMode];

/** Controls whether `startFlow` may reuse a previously used Flow ID. */
export const IdReusePolicy = Object.freeze({
  /** Uses the Dex server's default reuse policy. */
  DEFAULT: "default",
  /** Reuses an ID only after an unsuccessful closed run. */
  ALLOW_IF_PREVIOUS_FAILED: "allowIfPreviousFailed",
  /** Reuses an ID after any closed run. */
  ALLOW_IF_NOT_RUNNING: "allowIfNotRunning",
  /** Terminates an active run before starting the replacement. */
  ALLOW_TERMINATE_IF_RUNNING: "allowTerminateIfRunning",
  /** Rejects every previously used Flow ID. */
  DISALLOW: "disallow",
} as const);

/** Represents a value from {@link IdReusePolicy}. */
export type IdReusePolicy = (typeof IdReusePolicy)[keyof typeof IdReusePolicy];

/** Overrides mutable server behavior for one Flow execution. */
export interface FlowConfig {
  /** Optional active-Step visibility indexing policy. */
  readonly activeStepSearchMode?: ActiveStepSearchMode;
  /** Positive history-event threshold requesting continue-as-new. */
  readonly continueAsNewThreshold?: number;
  /** Positive history page-size budget in bytes for continue-as-new. */
  readonly continueAsNewPageSizeBytes?: number;
  /** Default durability for subsequent Step handler results. */
  readonly stepDurability?: StepDurability;
  /** Worker endpoint used for subsequent handler dispatch. */
  readonly workerTarget?: WorkerTarget;
}

/**
 * Describes one Attribute value written atomically when a Flow starts.
 * @typeParam T - Attribute value type.
 */
export interface InitialAttribute<T> {
  /** Registered singleton Attribute or AttributeMap definition. */
  readonly attribute: Attribute<T> | AttributeMap<T>;
  /** Required non-empty map key; omitted for a singleton Attribute. */
  readonly instance?: string;
  /** Typed initial value. */
  readonly value: T;
}

/** Creates type-safe initial Attribute writes for {@link StartFlowOptions}. */
export const InitialAttribute = Object.freeze({
  /**
   * Creates a singleton Attribute initialization.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered singleton Attribute.
   * @param value - Typed initial value.
   * @returns An immutable initialization descriptor.
   */
  of<T>(attribute: Attribute<T>, value: T): InitialAttribute<T> {
    return { attribute, value };
  },
  /**
   * Creates an AttributeMap instance initialization.
   * @typeParam T - Attribute value type.
   * @param attribute - Registered AttributeMap.
   * @param instance - Non-empty logical map key.
   * @param value - Typed initial value.
   * @returns An immutable initialization descriptor.
   */
  mapValue<T>(
    attribute: AttributeMap<T>,
    instance: string,
    value: T,
  ): InitialAttribute<T> {
    requireName(instance);
    return { attribute, instance, value };
  },
});

/**
 * Configures creation of a new Flow execution. All durations use milliseconds.
 *
 * @example
 * ```ts
 * const status = new Attribute("status", stringCodec);
 * const options: StartFlowOptions = {
 *   timeoutMs: 30 * 60_000,
 *   idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING,
 *   attributes: [InitialAttribute.of(status, "queued")],
 *   configOverride: { activeStepSearchMode: ActiveStepSearchMode.ALL },
 * };
 * ```
 */
export interface StartFlowOptions {
  /** Maximum Flow lifetime in milliseconds; omission uses registered defaults. */
  readonly timeoutMs?: number;
  /** Delay before the starting Step becomes eligible, in milliseconds. */
  readonly startDelayMs?: number;
  /** Flow ID reuse policy; omission uses `DEFAULT`. */
  readonly idReusePolicy?: IdReusePolicy;
  /** Server-supported cron expression for recurring runs. */
  readonly cronSchedule?: string;
  /** Optional Flow-level retry policy. */
  readonly retryPolicy?: RetryPolicy;
  /** Initial Attribute writes applied atomically with Flow creation. */
  readonly attributes?: readonly InitialAttribute<any>[];
  /** Flow configuration applied over registered defaults. */
  readonly configOverride?: FlowConfig;
  /** Returns the existing run instead of raising an already-started error. */
  readonly ignoreAlreadyStarted?: boolean;
  /** Non-empty idempotency key; omission generates a UUID. */
  readonly requestId?: string;
}

/** Describes the lifecycle state of one Flow run. */
export type FlowStatus =
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "terminated"
  | "timedOut"
  | "continuedAsNew";

/** Summarizes the current or latest run for one Flow ID. */
export interface FlowInfo {
  /** Stable application-assigned Flow ID. */
  readonly flowId: string;
  /** Server-assigned run ID. */
  readonly runId: string;
  /** Registered Flow type. */
  readonly flowType: string;
  /** Current lifecycle state. */
  readonly status: FlowStatus;
  /** UTC run start timestamp. */
  readonly startedAt: Date;
}

/** Represents one indexed Flow run returned by `searchFlows`. */
export interface SearchFlowEntry {
  /** Stable application-assigned Flow ID. */
  readonly flowId: string;
  /** Server-assigned run ID for this entry. */
  readonly runId: string;
  /** Registered Flow type. */
  readonly flowType: string;
  /** Indexed lifecycle state. */
  readonly status: FlowStatus;
  /** UTC start timestamp. */
  readonly startedAt: Date;
  /** UTC close timestamp, or `undefined` for open or unknown runs. */
  readonly closedAt: Date | undefined;
  /** Hydrated values keyed by physical search-index name. */
  readonly indexedAttributes: ReadonlyMap<string, unknown>;
}

/** Contains one server-ordered page of Flow search results. */
export interface SearchFlowsPage {
  /** Result entries in server-defined query order. */
  readonly flows: readonly SearchFlowEntry[];
  /** Opaque next-page token, or an empty string on the final page. */
  readonly nextPageToken: string;
}

/** Identifies one numbered execution of a registered Step type. */
export interface StepExecutionId {
  /** Registered Step type. */
  readonly stepType: string;
  /** Positive execution number; omission means the first execution. */
  readonly number?: number;
}

/** Creates normalized {@link StepExecutionId} values. */
export const StepExecutionId = Object.freeze({
  /**
   * Creates a Step execution identifier.
   * @param stepType - Non-empty registered Step type.
   * @param number - Positive execution number; defaults to one.
   * @returns The normalized identifier.
   */
  of(stepType: string, number?: number): StepExecutionId {
    return { stepType, number: number ?? 1 };
  },
});

/** Selects one Timer condition within a Step execution. */
export interface TimerId {
  /** Stable condition ID, mutually exclusive with `conditionIndex`. */
  readonly conditionId?: string;
  /** Zero-based condition index, mutually exclusive with `conditionId`. */
  readonly conditionIndex?: number;
}

/** Creates Timer selectors by stable ID or Wait-tree position. */
export const TimerId = Object.freeze({
  /**
   * Selects a Timer by stable condition ID.
   * @param conditionId - Non-empty ID assigned by `Timer.byDuration`.
   * @returns An ID-based Timer selector.
   */
  byConditionId(conditionId: string): TimerId {
    requireName(conditionId);
    return { conditionId };
  },
  /**
   * Selects a Timer by zero-based Wait-tree position.
   * @param conditionIndex - Non-negative flattened Timer index.
   * @returns An index-based Timer selector.
   */
  byConditionIndex(conditionIndex: number): TimerId {
    return { conditionIndex };
  },
});

/** Selects the history point from which a Flow reset resumes. */
export const ResetType = Object.freeze({
  /** Restarts from the beginning of the Flow. */
  BEGINNING: "beginning",
  /** Resumes at a specific history event ID. */
  HISTORY_EVENT_ID: "historyEventId",
  /** Resumes at the first event at or after a timestamp. */
  HISTORY_EVENT_TIME: "historyEventTime",
  /** Resumes at the latest execution of a Step type. */
  STEP_TYPE: "stepType",
  /** Resumes at one exact Step execution ID. */
  STEP_EXECUTION_ID: "stepExecutionId",
} as const);

/** Represents a value from {@link ResetType}. */
export type ResetType = (typeof ResetType)[keyof typeof ResetType];

/** Configures creation of a new run from existing Flow history. */
export interface ResetFlowOptions {
  /** Reset-point selector kind. */
  readonly type: ResetType;
  /** Event ID used with `HISTORY_EVENT_ID`. */
  readonly historyEventId?: bigint;
  /** Timestamp used with `HISTORY_EVENT_TIME`. */
  readonly historyEventTime?: Date;
  /** Registered Step type used with `STEP_TYPE`. */
  readonly stepType?: string;
  /** Exact execution ID used with `STEP_EXECUTION_ID`. */
  readonly stepExecutionId?: string;
  /** Optional operator-readable reset reason. */
  readonly reason?: string;
  /** Prevents reapplication of Channel messages after the reset point. */
  readonly skipChannelMessagesReapply?: boolean;
  /** Prevents reapplication of locking RPC effects after the reset point. */
  readonly skipLockingRpcReapply?: boolean;
}

/** Selects how an active Flow should close. */
export const StopType = Object.freeze({
  /** Requests cooperative cancellation. */
  CANCEL: "cancel",
  /** Forces immediate termination. */
  TERMINATE: "terminate",
  /** Closes the Flow as failed. */
  FAIL: "fail",
} as const);

/** Represents a value from {@link StopType}. */
export type StopType = (typeof StopType)[keyof typeof StopType];

/** Configures a `stopFlow` request. */
export interface StopFlowOptions {
  /** Stop behavior; omission requests cooperative cancellation. */
  readonly type?: StopType;
  /** Optional operator-readable reason recorded by Dex. */
  readonly reason?: string;
}

/** Configures Worker networking and startup synchronization. */
export interface WorkerOptions {
  /** Local plaintext WorkerService listener; defaults to `:8803`. */
  readonly bindAddress?: string;
  /** Endpoint advertised to Dex; omission derives it from `bindAddress`. */
  readonly workerTarget?: WorkerTarget;
  /** Dex FlowService target; defaults to `localhost:8801`. */
  readonly serverAddress?: string;
  /** Startup Indexed Attribute synchronization deadline in milliseconds. Defaults to 120000. */
  readonly attributeIndexSyncTimeoutMs?: number;
}

/** Identifies the application Worker endpoint advertised to Dex. */
export interface WorkerTarget {
  /** Plaintext gRPC target; headless targets must use `host:port`. */
  readonly address: string;
  /** Whether Dex connects directly without service discovery. */
  readonly headless?: boolean;
}
