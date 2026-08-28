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
  StepMovement,
  Wait,
  doubleCodec,
  goTo,
  goToMany,
  gracefulComplete,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
  type StepOptions,
} from "../../src/index.js";

class ImmutableFailingWaitStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ImmutableFailingWaitStep";
  }

  public waitFor(_context: Context, input: number): Wait {
    throw new Error(`expected wait failure ${input}`);
  }

  public execute(context: Context, input: number): StepDecision {
    if (!context.waitForMethodFailed()) {
      throw new Error("wait failure was not reported");
    }
    return input === 1 ? goTo(ImmutableFailingWaitStep, 2) : gracefulComplete(input);
  }

  public getStepOptions(): StepOptions {
    return {
      waitForRetry: { maximumAttempts: 1 },
      waitForFailure: "failFlow",
    };
  }
}

class ImmutableStartStep implements Step<number> {
  public readonly inputCodec = doubleCodec;

  public getStepType(): string {
    return "ImmutableStartStep";
  }

  public execute(_context: Context, _input: number): StepDecision {
    return goToMany(
      StepMovement.of(ImmutableFailingWaitStep, 1, {
        waitForRetry: { maximumAttempts: 1 },
        waitForFailure: "proceed",
      }),
    );
  }
}

export class ImmutableStepOptionsFlow implements Flow<number> {
  private readonly failing = new ImmutableFailingWaitStep();
  private readonly start = new ImmutableStartStep();

  public getFlowType(): string {
    return "ImmutableStepOptionsFlow";
  }

  public getSteps() {
    return StepList.startStep(this.start).otherSteps(this.failing);
  }
}
