// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ExecuteFailure,
  NonRetryableError,
  StepList,
  Wait,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type RecoveryErrorInfo,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export const NON_RETRYABLE_DETAIL = "typescript card rejected";

// Handler calls and recovery errors per Flow ID; the Worker runs in the test process.
export const attemptsByFlowId = new Map<string, number>();
export const recoveryByFlowId = new Map<string, RecoveryErrorInfo>();

export class CardRejectedError extends Error {
  public constructor() {
    super(NON_RETRYABLE_DETAIL);
    this.name = "CardRejectedError";
  }
}

// A real business retry policy: unlimited attempts an hour apart.
const hourlyRetry = { initialIntervalMs: 3_600_000, maximumIntervalMs: 3_600_000 };

function failPermanently(context: Context): never {
  attemptsByFlowId.set(context.flowId, (attemptsByFlowId.get(context.flowId) ?? 0) + 1);
  throw new NonRetryableError(new CardRejectedError());
}

function recordRecovery(context: Context): StepDecision {
  const recovery = context.recoveryError;
  if (recovery === undefined) {
    throw new NonRetryableError("recovery error is missing");
  }
  recoveryByFlowId.set(context.flowId, recovery);
  return gracefulComplete(recovery.detail);
}

class NonRetryableRecoveryStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "NonRetryableRecoveryStep";
  }

  public waitFor(_context: Context, _input: string): Wait {
    return Wait.skipImmediately();
  }

  public execute(context: Context, _input: string): StepDecision {
    return recordRecovery(context);
  }
}

class NonRetryableChargeStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "NonRetryableChargeStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: hourlyRetry,
      executeFailure: ExecuteFailure.proceedTo(NonRetryableRecoveryStep),
    };
  }

  public waitFor(_context: Context, _input: string): Wait {
    return Wait.skipImmediately();
  }

  public execute(context: Context, _input: string): StepDecision {
    return failPermanently(context);
  }
}

export class NonRetryableExecuteFlow implements Flow<string> {
  private readonly start = new NonRetryableChargeStep();
  private readonly recover = new NonRetryableRecoveryStep();

  public getFlowType(): string {
    return "NonRetryableExecuteFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.recover);
  }
}

class NonRetryableWaitForStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "NonRetryableWaitForStep";
  }

  public getStepOptions(): StepOptions {
    return { waitForRetry: hourlyRetry, waitForFailure: "proceed" };
  }

  public waitFor(context: Context, _input: void): Wait {
    return failPermanently(context);
  }

  public execute(context: Context, _input: void): StepDecision {
    return recordRecovery(context);
  }
}

export class NonRetryableWaitForFlow implements Flow<void> {
  private readonly start = new NonRetryableWaitForStep();

  public getFlowType(): string {
    return "NonRetryableWaitForFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
