// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  StepList,
  Wait,
  gracefulComplete,
  retryAfter,
  voidCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export const WAIT_FOR_RETRY_AFTER_DETAIL = "typescript waitFor retry-after failure";
export const EXECUTE_RETRY_AFTER_DETAIL = "typescript execute retry-after failure";
export const RETRY_AFTER_SECONDS = 2;
export const RETRY_POLICY_INTERVAL_SECONDS = 10;

function retryAfterStepOptions(waitFor: boolean): StepOptions {
  const retry = {
    initialIntervalMs: RETRY_POLICY_INTERVAL_SECONDS * 1_000,
    maximumAttempts: 3,
  };
  if (waitFor) {
    return {
      waitForRetry: retry,
      waitForDurability: "sync",
      executeDurability: "sync",
    };
  }
  return {
    executeRetry: retry,
    executeDurability: "sync",
  };
}

class WorkerRetryAfterWaitForStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "WorkerRetryAfterWaitForStep";
  }

  public getStepOptions(): StepOptions {
    return retryAfterStepOptions(true);
  }

  public waitFor(context: Context, _input: void): Wait {
    if (context.attempt === 1) {
      throw retryAfter(RETRY_AFTER_SECONDS, new Error(WAIT_FOR_RETRY_AFTER_DETAIL));
    }
    return Wait.skipImmediately();
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("wait-retry-after");
  }
}

class WorkerRetryAfterExecuteStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "WorkerRetryAfterExecuteStep";
  }

  public getStepOptions(): StepOptions {
    return retryAfterStepOptions(false);
  }

  public execute(context: Context, _input: void): StepDecision {
    if (context.attempt === 1) {
      throw retryAfter(RETRY_AFTER_SECONDS, new Error(EXECUTE_RETRY_AFTER_DETAIL));
    }
    return gracefulComplete("execute-retry-after");
  }
}

export class WorkerRetryAfterWaitForFlow implements Flow<void> {
  private readonly start = new WorkerRetryAfterWaitForStep();

  public getFlowType(): string {
    return "WorkerRetryAfterWaitForFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

export class WorkerRetryAfterExecuteFlow implements Flow<void> {
  private readonly start = new WorkerRetryAfterExecuteStep();

  public getFlowType(): string {
    return "WorkerRetryAfterExecuteFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
