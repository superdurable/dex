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
  ExecuteFailure,
  StepList,
  Wait,
  doubleCodec,
  forceFail,
  goTo,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class RecoverStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly failingStep: () => FailingStep) {}

  public getStepType(): string {
    return "RecoverStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.skipImmediately();
  }

  public execute(_context: Context, input: number): StepDecision {
    if (input === 10) {
      return gracefulComplete(input);
    }
    if (input === 5) {
      return goTo(this.failingStep(), input * 2);
    }
    return forceFail(`unexpected input ${input}`);
  }
}

class FailingStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public constructor(private readonly recover: RecoverStep) {}

  public getStepType(): string {
    return "FailingStep";
  }

  public waitFor(_context: Context, _input: number): Wait {
    return Wait.skipImmediately();
  }

  public execute(_context: Context, _input: number): StepDecision {
    throw new Error("execute failure");
  }

  public getStepOptions(): StepOptions {
    return {
      executeRetry: { maximumAttempts: 1, backoffCoefficient: 2 },
      executeFailure: ExecuteFailure.proceedTo(this.recover),
    };
  }
}

export class StateRecoveryFlow implements Flow<number> {
  private readonly recover: RecoverStep = new RecoverStep((): FailingStep => this.start);
  private readonly start: FailingStep = new FailingStep(this.recover);

  public getFlowType(): string {
    return "StateRecoveryFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.recover);
  }
}
