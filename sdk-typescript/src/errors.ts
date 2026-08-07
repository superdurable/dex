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

export class DexError extends Error {
  public constructor(
    public readonly code: status,
    public readonly subStatus: ErrorSubStatus | undefined,
    public readonly detail: string,
    public readonly workerErrorType: string,
    public readonly workerErrorDetail: string,
    options?: ErrorOptions,
  ) {
    super(detail, options);
  }
}

export class LongPollTimeoutError extends Error {
  public constructor(
    public readonly flowId: string,
    options?: ErrorOptions,
  ) {
    super(`Flow is still running: ${flowId}`, options);
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
