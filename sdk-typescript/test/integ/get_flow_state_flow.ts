// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import {
  StepList,
  Timer,
  Wait,
  gracefulComplete,
  voidCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

export const GET_FLOW_STATE_FAILURE_DETAIL = "typescript getFlowState charge declined";
export const GET_FLOW_STATE_RETRY_INTERVAL_MS = 60_000;

export class ChargeDeclinedError extends Error {
  public constructor(message: string) {
    super(message);
    this.name = "ChargeDeclinedError";
  }
}

class GetFlowStateFailingStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "GetFlowStateFailingStep";
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: {
        initialIntervalMs: GET_FLOW_STATE_RETRY_INTERVAL_MS,
        maximumAttempts: 100,
      },
    };
  }

  public execute(_context: Context, _input: void): StepDecision {
    throw new ChargeDeclinedError(GET_FLOW_STATE_FAILURE_DETAIL);
  }
}

export class GetFlowStateFailingFlow implements Flow<void> {
  private readonly start = new GetFlowStateFailingStep();

  public getFlowType(): string {
    return "GetFlowStateFailingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}

class GetFlowStateWaitingStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public getStepType(): string {
    return "GetFlowStateWaitingStep";
  }

  public waitFor(_context: Context, _input: void): Wait {
    return Wait.until(Timer.byDuration(3_600_000, "get-flow-state-timer"));
  }

  public execute(_context: Context, _input: void): StepDecision {
    return gracefulComplete("waited");
  }
}

export class GetFlowStateWaitingFlow implements Flow<void> {
  private readonly start = new GetFlowStateWaitingStep();

  public getFlowType(): string {
    return "GetFlowStateWaitingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start);
  }
}
