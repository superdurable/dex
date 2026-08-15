// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { status } from "@grpc/grpc-js";


/** Indicates that an API is intentionally unavailable in the current SDK phase. */
export class PhaseNotImplementedError extends Error {}

/** Provides stable Dex-specific classifications beyond gRPC status codes. */
export const ErrorSubStatus = Object.freeze({
  /** Dex returned no more specific classification. */
  UNCATEGORIZED: "uncategorized",
  /** A start request conflicted with an existing Flow ID. */
  FLOW_ALREADY_STARTED: "flowAlreadyStarted",
  /** No Flow exists for the requested ID. */
  FLOW_NOT_EXISTS: "flowNotExists",
  /** A nested application Worker invocation failed. */
  WORKER_API_ERROR: "workerApiError",
  /** A retryable long poll ended without observing its condition. */
  LONG_POLL_TIMEOUT: "longPollTimeout",
} as const);

/** Represents a value from {@link ErrorSubStatus}. */
export type ErrorSubStatus = (typeof ErrorSubStatus)[keyof typeof ErrorSubStatus];

/** Provides terminal Flow failure categories returned by Dex. */
export const FlowErrorType = Object.freeze({
  /** A Step decision could not be applied. */
  STEP_DECISION_FAILED: "stepDecisionFailed",
  /** A Client-originated operation failed the Flow. */
  CLIENT_API_FAILED: "clientApiFailed",
  /** Worker dispatch or application handler execution failed. */
  WORKER_API_FAILED: "workerApiFailed",
  /** Application Flow code returned an invalid definition or result. */
  INVALID_USER_FLOW_CODE: "invalidUserFlowCode",
  /** Dex encountered an internal invariant or infrastructure failure. */
  INTERNAL: "internal",
} as const);

/** Represents a value from {@link FlowErrorType}. */
export type FlowErrorType = (typeof FlowErrorType)[keyof typeof FlowErrorType];

/** Exposes structured metadata returned by a failed FlowService operation. */
export class DexServiceError extends Error {
  /**
   * Creates a structured service error.
   * @param code - Outer gRPC status code.
   * @param subStatus - Stable Dex-specific classification.
   * @param detail - Human-readable service detail.
   * @param operation - Client operation that failed.
   * @param flowId - Target Flow ID, or `undefined` for service-wide calls.
   * @param options - Standard Error construction options.
   */
  public constructor(
    public readonly code: status,
    public readonly subStatus: ErrorSubStatus,
    public readonly detail: string,
    public readonly operation: string,
    public readonly flowId: string | undefined,
    options?: ErrorOptions,
  ) {
    super(detail, options);
    this.name = new.target.name;
  }
}

/** Indicates that `startFlow` conflicts with an existing Flow ID. */
export class FlowAlreadyStartedError extends DexServiceError {}

/** Indicates that an operation targeted a Flow ID that does not exist. */
export class FlowNotFoundError extends DexServiceError {}

/** Indicates that an operation requires an active but already closed Flow. */
export class FlowNotActiveError extends DexServiceError {}

/** Exposes both outer FlowService and nested WorkerService failure details. */
export class WorkerInvocationError extends DexServiceError {
  /**
   * Creates an error from outer and nested Worker metadata.
   * @param code - Outer FlowService gRPC status.
   * @param subStatus - Dex-specific classification.
   * @param detail - Outer human-readable detail.
   * @param operation - Client operation that invoked the Worker.
   * @param flowId - Target Flow ID, when available.
   * @param workerCode - Nested Worker gRPC status, when available.
   * @param workerErrorType - Worker-reported application error type.
   * @param workerErrorDetail - Worker-reported human-readable detail.
   * @param options - Standard Error construction options.
   */
  public constructor(
    code: status,
    subStatus: ErrorSubStatus,
    detail: string,
    operation: string,
    flowId: string | undefined,
    public readonly workerCode: status | undefined,
    public readonly workerErrorType: string,
    public readonly workerErrorDetail: string,
    options?: ErrorOptions,
  ) {
    super(code, subStatus, detail, operation, flowId, options);
  }
}

/** Indicates that an RPC could not acquire all requested Attribute locks. */
export class RpcLockConflictError extends DexServiceError {}

/** Indicates that a retryable long poll ended before its condition was observed. */
export class LongPollTimeoutError extends DexServiceError {}

/** Indicates that Registry construction found an invalid Flow definition. */
export class FlowDefinitionError extends Error {
  /**
   * Creates a Flow definition error.
   * @param message - Precise validation failure detail.
   * @param options - Standard Error construction options.
   */
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = new.target.name;
  }
}

/** Identifies a Step or RPC result that violates the SDK contract. */
export class InvalidStepResultError extends FlowDefinitionError {
  /**
   * Creates an invalid-handler-result error.
   * @param flowType - Containing Flow type.
   * @param stepType - Step type, or `undefined` for an RPC.
   * @param method - Handler method that returned the invalid value.
   * @param detail - Precise contract violation.
   * @param options - Standard Error construction options.
   */
  public constructor(
    public readonly flowType: string,
    public readonly stepType: string | undefined,
    public readonly method: "waitFor" | "execute" | "rpc",
    detail: string,
    options?: ErrorOptions,
  ) {
    const target = stepType === undefined ? `RPC in Flow ${flowType}` : `Flow ${flowType} Step ${stepType}`;
    super(`${target} ${method} returned an invalid result: ${detail}`, options);
  }
}

/** Reports an application value encoding, decoding, or hydration failure. */
export class ValueMappingError extends Error {
  /**
   * Creates a value-mapping error.
   * @param operation - Mapping phase that failed.
   * @param detail - Incompatible type, wire kind, or malformed payload detail.
   * @param options - Standard Error construction options.
   */
  public constructor(
    public readonly operation: "encode" | "decode" | "hydrate",
    detail: string,
    options?: ErrorOptions,
  ) {
    super(`Cannot ${operation} Dex Value: ${detail}`, options);
    this.name = new.target.name;
  }
}

/**
 * Creates a consistent error for an API planned in a later implementation phase.
 * @param component - Human-readable unavailable component name.
 * @returns A new PhaseNotImplementedError.
 */
export function laterPhase(component: string): PhaseNotImplementedError {
  return new PhaseNotImplementedError(`${component} belongs to a later phase`);
}
