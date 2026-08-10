// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { status } from "@grpc/grpc-js";

import type { Codec } from "./codec.js";
import type { Value } from "./gen/dex.js";
import type { FlowStatus } from "./options.js";
import { decodeValue } from "./value-mapper.js";

export class PhaseNotImplementedError extends Error {}

export const ErrorSubStatus = Object.freeze({
  UNCATEGORIZED: "uncategorized",
  FLOW_ALREADY_STARTED: "flowAlreadyStarted",
  FLOW_NOT_EXISTS: "flowNotExists",
  WORKER_API_ERROR: "workerApiError",
  LONG_POLL_TIMEOUT: "longPollTimeout",
} as const);

export type ErrorSubStatus = (typeof ErrorSubStatus)[keyof typeof ErrorSubStatus];

export const FlowErrorType = Object.freeze({
  STEP_DECISION_FAILED: "stepDecisionFailed",
  CLIENT_API_FAILED: "clientApiFailed",
  WORKER_API_FAILED: "workerApiFailed",
  INVALID_USER_FLOW_CODE: "invalidUserFlowCode",
  INTERNAL: "internal",
} as const);

export type FlowErrorType = (typeof FlowErrorType)[keyof typeof FlowErrorType];

export class DexServiceError extends Error {
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

export class FlowAlreadyStartedError extends DexServiceError {}

export class FlowNotFoundError extends DexServiceError {}

export class FlowNotActiveError extends DexServiceError {}

export class WorkerInvocationError extends DexServiceError {
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

export class RpcLockConflictError extends DexServiceError {}

export class LongPollTimeoutError extends DexServiceError {}

export class FlowDefinitionError extends Error {
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = new.target.name;
  }
}

export class InvalidStepResultError extends FlowDefinitionError {
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

export class ValueMappingError extends Error {
  public constructor(
    public readonly operation: "encode" | "decode" | "hydrate",
    detail: string,
    options?: ErrorOptions,
  ) {
    super(`Cannot ${operation} Dex Value: ${detail}`, options);
    this.name = new.target.name;
  }
}

export class FlowUncompletedError extends Error {
  public constructor(
    public readonly runId: string,
    public readonly status: FlowStatus,
    public readonly errorType: FlowErrorType | undefined,
    message: string | undefined,
    private readonly results: readonly Value[],
  ) {
    super(message);
  }

  public get resultCount(): number {
    return this.results.length;
  }

  public getResult<T>(index: number, codec: Codec<T>): T {
    const value = this.results[index];
    if (value === undefined) {
      throw new RangeError(`result index ${index} is out of range`);
    }
    return decodeValue(codec, value);
  }
}

export function laterPhase(component: string): PhaseNotImplementedError {
  return new PhaseNotImplementedError(`${component} belongs to a later phase`);
}
