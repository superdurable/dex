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

/** Controls how Dex responds when a positive soft Flow timeout expires. */
export const FlowTimeoutPolicy = Object.freeze({
  /** Invokes `Flow.handleTimeout` when present, and fails otherwise. */
  DEFAULT: "default",
  /** Fails with `FlowErrorType.FLOW_TIMEOUT` and permits Flow retries. */
  FAIL: "fail",
  /** Cancels without retrying the Flow. */
  CANCEL: "cancel",
  /** Invokes `Flow.handleTimeout` once after the durable timer fires or is skipped. */
  HANDLER: "handler",
} as const);

/** Represents a value from {@link FlowTimeoutPolicy}. */
export type FlowTimeoutPolicy =
  (typeof FlowTimeoutPolicy)[keyof typeof FlowTimeoutPolicy];

/** Overrides mutable server behavior for one Flow execution. */
export interface FlowConfig {
  /** Optional active-Step visibility indexing policy. */
  readonly activeStepSearchMode?: ActiveStepSearchMode;
  /**
   * Selects the server-configured Attribute Store receiving opted-in Attribute writes.
   *
   * Omit this property to preserve the existing Flow setting. An explicit empty string is sent with
   * presence and disables later synchronization. Projection is asynchronous and never rolls back Flow
   * Attribute writes when the external store update fails.
   */
  readonly attributeStoreName?: string;
  /** Positive history-event threshold requesting continue-as-new. */
  readonly continueAsNewThreshold?: number;
  /** Positive history page-size budget in bytes for continue-as-new. */
  readonly continueAsNewPageSizeBytes?: number;
  /** Default durability for later Step handler results. */
  readonly stepDurability?: StepDurability;
  /** Worker endpoint used for later handler calls. */
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
   * @returns The Attribute initialization value.
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
   * @returns The AttributeMap initialization value.
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
  /** Dex durable soft timeout in milliseconds; omitted or zero disables it. */
  readonly timeoutMs?: number;
  /** Action taken when a positive timeout expires; defaults from the Flow's hook. */
  readonly timeoutPolicy?: FlowTimeoutPolicy;
  /** Delay before the starting Step becomes eligible, in milliseconds. */
  readonly startDelayMs?: number;
  /** Flow ID reuse policy; uses `DEFAULT` when omitted. */
  readonly idReusePolicy?: IdReusePolicy;
  /** Optional Flow-level retry policy. */
  readonly retryPolicy?: RetryPolicy;
  /** Initial Attribute writes applied atomically with Flow creation. */
  readonly attributes?: readonly InitialAttribute<any>[];
  /** Flow configuration applied over registered defaults. */
  readonly configOverride?: FlowConfig;
  /** Returns the existing run instead of raising an already-started error. */
  readonly ignoreAlreadyStarted?: boolean;
  /** Non-empty idempotency key; generates a UUID when omitted. */
  readonly requestId?: string;
}

/**
 * Describes the lifecycle state of one Flow run.
 * `serverSideTimeoutInternalOnly` is reserved for backend hard-timeout reporting.
 */
export type FlowStatus =
  | "running"
  | "completed"
  | "failed"
  | "cancelled"
  | "terminated"
  | "serverSideTimeoutInternalOnly"
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
  /** Positive execution number; defaults to the first execution. */
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

/** Selects the historical point from which time travel creates a new run. */
export const TimeTravelType = Object.freeze({
  /** Restarts from the beginning of the Flow. */
  BEGINNING: "beginning",
  /** Resumes at the first event at or after a timestamp. */
  HISTORY_EVENT_TIME: "historyEventTime",
  /** Resumes at the latest execution of a Step type. */
  STEP_TYPE: "stepType",
  /** Resumes at one exact Step execution ID. */
  STEP_EXECUTION_ID: "stepExecutionId",
} as const);

/** Represents a value from {@link TimeTravelType}. */
export type TimeTravelType = (typeof TimeTravelType)[keyof typeof TimeTravelType];

/** Selects the Step method used as a Step execution time travel boundary. */
export const TimeTravelStepMethod = Object.freeze({
  /** Reruns WaitFor and everything after it. */
  WAIT_FOR: "waitFor",
  /** Keeps the WaitFor result and reruns Execute and everything after it. */
  EXECUTE: "execute",
} as const);

/** Represents a value from {@link TimeTravelStepMethod}. */
export type TimeTravelStepMethod = (typeof TimeTravelStepMethod)[keyof typeof TimeTravelStepMethod];

/** Configures creation of a new run from existing Flow history. */
export interface TimeTravelOptions {
  /** Time travel point selector kind. */
  readonly type: TimeTravelType;
  /** Timestamp used with `HISTORY_EVENT_TIME`. */
  readonly historyEventTime?: Date;
  /** Registered Step type used with `STEP_TYPE`. */
  readonly stepType?: string;
  /** Exact execution ID used with `STEP_EXECUTION_ID`. */
  readonly stepExecutionId?: string;
  /** WaitFor or Execute boundary required with `STEP_EXECUTION_ID`. */
  readonly stepMethod?: TimeTravelStepMethod;
  /** Optional operator-readable time travel reason. */
  readonly reason?: string;
  /** Prevents reapplication of RPCs, Channel publications, and Attribute writes after the selected point. */
  readonly skipWritesReapply?: boolean;
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
  /** Stop behavior; defaults to cooperative cancellation. */
  readonly type?: StopType;
  /** Optional operator-readable reason recorded by Dex. */
  readonly reason?: string;
}

/** Configures Worker networking and startup synchronization. */
export interface WorkerOptions {
  /** Local plaintext WorkerService listener; defaults to `:8803`. */
  readonly bindAddress?: string;
  /** Endpoint advertised to Dex; derived from `bindAddress` when omitted. */
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
